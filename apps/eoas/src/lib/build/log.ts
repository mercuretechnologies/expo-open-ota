import { randomUUID } from 'crypto';
import { mkdir, open } from 'fs/promises';
import path from 'path';

import Log from '../log';

export interface BuildLog {
  path: string;
  // write keeps a line in the file only; info and warn also show it in the terminal.
  write(line: string): void;
  info(line: string): void;
  warn(line: string): void;
  close(): Promise<void>;
}

export async function createBuildLog(project: string, profile: string): Promise<BuildLog> {
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
  const write = (line: string): void => {
    if (closing) {
      return;
    }
    pending = pending
      .then(async () => {
        if (!failure) {
          await file.appendFile(`${line}\n`);
        }
      })
      .catch(error => {
        failure = error;
      });
  };
  return {
    path: logPath,
    write,
    info(line: string): void {
      write(line);
      Log.log(line);
    },
    warn(line: string): void {
      write(`Warning: ${line}`);
      Log.warn(line);
    },
    close(): Promise<void> {
      closing ??= (async () => {
        await pending;
        await file.close();
        if (failure) {
          throw failure;
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
  work: (buildLog: BuildLog) => Promise<T>
): Promise<T> {
  const buildLog = await createBuildLog(project, profile);
  buildLog.write(`${title} — profile ${profile} — ${new Date().toISOString()}`);
  Log.log(`Build log: ${buildLog.path}`);
  try {
    return await work(buildLog);
  } catch (error) {
    const message = error instanceof Error ? error.message : 'Build failed.';
    buildLog.write(message);
    throw new Error(`${message}\n\nFull build log: ${buildLog.path}`);
  } finally {
    await buildLog.close();
  }
}
