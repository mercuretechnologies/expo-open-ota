import { randomUUID } from 'crypto';
import { mkdir, open } from 'fs/promises';
import path from 'path';

export interface LogFile {
  path: string;
  append(line: string): void;
  close(): Promise<void>;
}

// Appends lines in order; a write failure surfaces when the file is closed.
export async function openLogFile(project: string, profile: string): Promise<LogFile> {
  const directory = path.join(project, 'build-artifacts/logs');
  await mkdir(directory, { recursive: true });
  const stamp = new Date().toISOString().replace(/[:.]/g, '-');
  const name = `${profile.replace(/[^a-zA-Z0-9_-]/g, '_')}-${stamp}-${randomUUID()}.log`;
  const logPath = path.join(directory, name);
  const handle = await open(logPath, 'wx', 0o600);
  let pending = Promise.resolve();
  let failure: unknown;
  return {
    path: logPath,
    append(line) {
      pending = pending
        .then(async () => {
          if (!failure) {
            await handle.appendFile(`${line}\n`);
          }
        })
        .catch(error => {
          failure = error;
        });
    },
    async close() {
      await pending;
      await handle.close();
      if (failure) {
        throw failure;
      }
    },
  };
}
