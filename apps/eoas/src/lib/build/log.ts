import { randomUUID } from 'crypto';

import { createBuildOutputRedactor } from './errors';
import { openLogFile } from './logFile';
import { BuildPhase, BuildPhaseResult, LogMarker, buildPhaseDisplayName } from './phases';
import Log from '../log';

type OutputSource = 'stdout' | 'stderr';

// One line of the build log, in the JSON shape the server and dashboard read.
export interface LogLine {
  logId: string;
  time: string;
  level: 30 | 40 | 50;
  msg: string;
  phase?: BuildPhase;
  buildStepId?: string;
  buildStepDisplayName?: string;
  source?: OutputSource;
  marker?: LogMarker;
  result?: BuildPhaseResult;
  durationMs?: number;
}

// Receives every line once streaming is on and sends it to the server.
export interface LogUploader {
  write(line: LogLine): void;
  close(): Promise<void>;
}

// What the work of one build phase writes with. Its lines carry that phase.
export interface PhaseLogger {
  // write keeps a line in the log only; info and warn also show it in the terminal.
  write(line: string, source?: OutputSource): void;
  info(line: string): void;
  warn(line: string): void;
  markSkipped(): void;
}

// The log of a whole build: a local file, optionally streamed to the server.
export interface BuildLog {
  path: string;
  write(line: string, source?: OutputSource): void;
  info(line: string): void;
  warn(line: string): void;
  maskSecrets(secrets: string[]): void;
  streamTo(uploader: LogUploader): void;
  runBuildPhase<T>(
    phase: BuildPhase,
    work: (phaseLog: PhaseLogger) => Promise<T>,
    displayName?: string
  ): Promise<T>;
  abort(): void;
  close(): Promise<void>;
}

// Lines written before streamTo is called are held for the uploader, up to this size.
const MAX_HELD_BYTES = 256 * 1024;

export async function createBuildLog(
  project: string,
  profile: string,
  verbose = false
): Promise<BuildLog> {
  const logFile = await openLogFile(project, profile);
  let redact = createBuildOutputRedactor([]);
  let uploader: LogUploader | undefined;
  const heldForUpload: LogLine[] = [];
  let heldBytes = 0;
  let closing: Promise<void> | undefined;
  let failCurrentPhase: (() => void) | undefined;

  const addLine = (
    phaseFields: Partial<LogLine>,
    msg: string,
    extra: Partial<LogLine> = {}
  ): void => {
    if (closing) {
      return;
    }
    const logLine: LogLine = {
      logId: randomUUID(),
      time: new Date().toISOString(),
      level: 30,
      msg: redact(msg),
      ...phaseFields,
      ...extra,
    };
    if (uploader) {
      uploader.write(logLine);
    } else if (heldBytes < MAX_HELD_BYTES) {
      heldForUpload.push(logLine);
      heldBytes += Buffer.byteLength(JSON.stringify(logLine));
    }
    logFile.append(`${phasePrefix(logLine.phase)}${logLine.msg}`);
  };

  const loggingMethods = (
    phaseFields: Partial<LogLine>
  ): Pick<PhaseLogger, 'write' | 'info' | 'warn'> => {
    const terminalLine = (line: string): string =>
      `${phasePrefix(phaseFields.phase)}${redact(line)}`;
    return {
      write(line, source) {
        addLine(phaseFields, line, { source });
        if (verbose) {
          (source === 'stderr' ? process.stderr : process.stdout).write(`${terminalLine(line)}\n`);
        }
      },
      info(line) {
        addLine(phaseFields, line);
        Log.log(terminalLine(line));
      },
      warn(line) {
        addLine(phaseFields, line, { level: 40 });
        Log.warn(terminalLine(line));
      },
    };
  };

  return {
    path: logFile.path,
    ...loggingMethods({}),
    maskSecrets(secrets) {
      redact = createBuildOutputRedactor(secrets);
    },
    streamTo(target) {
      uploader = target;
      for (const line of heldForUpload) {
        uploader.write({ ...line, msg: redact(line.msg) });
      }
      heldForUpload.length = 0;
    },
    async runBuildPhase(phase, work, displayName = buildPhaseDisplayName[phase]) {
      const phaseFields: Partial<LogLine> = {
        phase,
        buildStepId: randomUUID(),
        buildStepDisplayName: displayName,
      };
      const methods = loggingMethods(phaseFields);
      let warned = false;
      let skipped = false;
      const phaseLog: PhaseLogger = {
        ...methods,
        warn(line) {
          warned = true;
          methods.warn(line);
        },
        markSkipped() {
          skipped = true;
        },
      };

      const startedAt = Date.now();
      let finished = false;
      const finishPhase = (result: BuildPhaseResult): void => {
        if (finished) {
          return;
        }
        finished = true;
        failCurrentPhase = undefined;
        addLine(phaseFields, `End phase: ${phase}`, {
          marker: LogMarker.END_PHASE,
          result,
          durationMs: Date.now() - startedAt,
        });
        const summary = `${displayName} — ${result}`;
        if (result === BuildPhaseResult.FAIL) {
          Log.fail(summary);
        } else {
          Log.succeed(summary);
        }
      };

      failCurrentPhase = (): void => {
        finishPhase(BuildPhaseResult.FAIL);
      };
      addLine(phaseFields, `Start phase: ${phase}`, { marker: LogMarker.START_PHASE });
      Log.log(displayName);
      try {
        const value = await work(phaseLog);
        finishPhase(
          skipped
            ? BuildPhaseResult.SKIPPED
            : warned
              ? BuildPhaseResult.WARNING
              : BuildPhaseResult.SUCCESS
        );
        return value;
      } catch (error) {
        addLine(phaseFields, error instanceof Error ? error.message : 'Build phase failed.', {
          level: 50,
        });
        finishPhase(BuildPhaseResult.FAIL);
        throw error;
      }
    },
    abort() {
      failCurrentPhase?.();
    },
    close() {
      closing ??= (async () => {
        try {
          await logFile.close();
        } finally {
          await uploader?.close();
        }
      })();
      return closing;
    },
  };
}

// Opens the log for one build, records a failure in it and points the error at
// the file, and always closes it.
export async function withBuildLog<T>(
  project: string,
  profile: string,
  title: string,
  work: (buildLog: BuildLog) => Promise<T>,
  verbose = false
): Promise<T> {
  const buildLog = await createBuildLog(project, profile, verbose);
  buildLog.write(`${title} — profile ${profile} — ${new Date().toISOString()}`);
  Log.log(`Build log: ${buildLog.path}`);
  let result: T;
  try {
    result = await work(buildLog);
  } catch (error) {
    const message = error instanceof Error ? error.message : 'Build failed.';
    buildLog.write(message);
    await buildLog.close().catch(closeError => {
      Log.warn(`Build log may be incomplete: ${(closeError as Error).message}`);
    });
    throw new Error(`${message}\n\nFull build log: ${buildLog.path}`, { cause: error });
  }
  await buildLog.close();
  return result;
}

function phasePrefix(phase?: BuildPhase): string {
  return phase ? `[${phase}] ` : '';
}
