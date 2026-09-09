import {
  BuildPhase,
  BuildPhaseResult,
  LogMarker,
  buildPhaseDisplayName,
} from '@expo/eas-build-job';
import { randomUUID } from 'crypto';
import { mkdir, open } from 'fs/promises';
import path from 'path';

import { createBuildOutputRedactor } from './errors';
import Log from '../log';

// EAS added this phase after the version of eas-build-job used by this CLI.
type BuildLogPhase = BuildPhase | 'GRADLE_BUILD_PROFILE';

// The fields used by EAS build-tools and its grouped log reader.
export interface BuildLogEvent {
  logId: string;
  time: string;
  level: 30 | 40 | 50;
  msg: string;
  phase?: BuildLogPhase;
  buildStepId?: string;
  buildStepDisplayName?: string;
  source?: 'stdout' | 'stderr';
  marker?: LogMarker;
  result?: BuildPhaseResult;
  durationMs?: number;
}

export interface BuildLogSink {
  write(event: BuildLogEvent): void;
  close(): Promise<void>;
}

export interface BuildLog {
  path: string;
  write(line: string, source?: 'stdout' | 'stderr'): void;
  info(line: string): void;
  warn(line: string): void;
  setSecrets(secrets: string[]): void;
  streamTo(sink: BuildLogSink): void;
  runBuildPhase<T>(
    phase: BuildLogPhase,
    work: (logger: BuildLog) => Promise<T>,
    displayName?: string
  ): Promise<T>;
  markSkipped(): void;
  abort(): void;
  close(): Promise<void>;
}

export async function createBuildLog(
  project: string,
  profile: string,
  verbose = false
): Promise<BuildLog> {
  const directory = path.join(project, 'build-artifacts/logs');
  await mkdir(directory, { recursive: true });
  const name = `${profile.replace(/[^a-zA-Z0-9_-]/g, '_')}-${new Date()
    .toISOString()
    .replace(/[:.]/g, '-')}-${randomUUID()}.log`;
  const logPath = path.join(directory, name);
  const file = await open(logPath, 'wx', 0o600);
  let pending = Promise.resolve();
  let failure: unknown;
  let closing: Promise<void> | undefined;
  let stream: BuildLogSink | undefined;
  let redact = createBuildOutputRedactor([]);
  let bufferedBytes = 0;
  const preparation: BuildLogEvent[] = [];
  const active = new Set<() => void>();

  const emit = (event: BuildLogEvent): void => {
    if (closing) {
      return;
    }
    event = { ...event, msg: redact(event.msg) };
    if (stream) {
      stream.write(event);
    } else if (bufferedBytes < 256 * 1024) {
      preparation.push(event);
      bufferedBytes += Buffer.byteLength(JSON.stringify(event));
    }
    const prefix = event.phase ? `[${event.phase}] ` : '';
    pending = pending
      .then(async () => {
        if (!failure) {
          await file.appendFile(`${prefix}${event.msg}\n`);
        }
      })
      .catch(error => {
        failure = error;
      });
  };

  type PhaseLogger = BuildLog & { run<T>(work: (logger: BuildLog) => Promise<T>): Promise<T> };
  const logger = (scope: Partial<BuildLogEvent> = {}): PhaseLogger => {
    let warned = false;
    let skipped = false;
    const event = (msg: string, fields: Partial<BuildLogEvent> = {}): void => {
      emit({
        logId: randomUUID(),
        time: new Date().toISOString(),
        level: 30,
        msg,
        ...scope,
        ...fields,
      });
    };
    const display = (line: string): string =>
      `${scope.phase ? `[${scope.phase}] ` : ''}${redact(line)}`;
    const loggerForWork: PhaseLogger = {
      path: logPath,
      write(line, source) {
        event(line, { source });
        if (verbose) {
          (source === 'stderr' ? process.stderr : process.stdout).write(`${display(line)}\n`);
        }
      },
      info(line) {
        event(line);
        Log.log(display(line));
      },
      warn(line) {
        warned = true;
        event(line, { level: 40 });
        Log.warn(display(line));
      },
      setSecrets(secrets) {
        redact = createBuildOutputRedactor(secrets);
      },
      streamTo(sink) {
        stream = sink;
        for (const entry of preparation) {
          stream.write({ ...entry, msg: redact(entry.msg) });
        }
        preparation.length = 0;
      },
      markSkipped() {
        skipped = true;
      },
      async runBuildPhase(
        phase,
        work,
        displayName = phase === 'GRADLE_BUILD_PROFILE'
          ? 'Gradle build profile'
          : buildPhaseDisplayName[phase]
      ) {
        const child = logger({
          phase,
          buildStepId: randomUUID(),
          buildStepDisplayName: displayName,
        });
        // The child emits its own scope, so concurrent/repeated phases cannot mix output.
        return await child.run(work);
      },
      abort() {
        for (const finish of active) {
          finish();
        }
      },
      close() {
        closing ??= (async () => {
          try {
            await pending;
            await file.close();
          } finally {
            await stream?.close();
          }
          if (failure) {
            throw failure;
          }
        })();
        return closing;
      },
      async run<T>(work: (logger: BuildLog) => Promise<T>): Promise<T> {
        const started = Date.now();
        let finished = false;
        const finish = (result: BuildPhaseResult): void => {
          if (finished) {
            return;
          }
          finished = true;
          event(`End phase: ${scope.phase}`, {
            marker: LogMarker.END_PHASE,
            result,
            durationMs: Date.now() - started,
          });
          const summary = `${scope.buildStepDisplayName} — ${result}`;
          if (result === BuildPhaseResult.FAIL) {
            Log.fail(summary);
          } else {
            Log.succeed(summary);
          }
        };
        const abort = (): void => {
          finish(BuildPhaseResult.FAIL);
        };
        active.add(abort);
        event(`Start phase: ${scope.phase}`, { marker: LogMarker.START_PHASE });
        Log.log(scope.buildStepDisplayName!);
        try {
          const result = await work(loggerForWork);
          finish(
            skipped
              ? BuildPhaseResult.SKIPPED
              : warned
                ? BuildPhaseResult.WARNING
                : BuildPhaseResult.SUCCESS
          );
          return result;
        } catch (error) {
          event(error instanceof Error ? error.message : 'Build phase failed.', { level: 50 });
          finish(BuildPhaseResult.FAIL);
          throw error;
        } finally {
          active.delete(abort);
        }
      },
    };
    return loggerForWork;
  };
  return logger();
}

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
