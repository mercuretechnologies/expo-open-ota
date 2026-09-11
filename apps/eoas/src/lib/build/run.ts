import spawnAsync from '@expo/spawn-async';

import { formatBuildError } from './errors';
import { PhaseLogger } from './log';
import { streamBuildOutput } from './output';

export interface BuildCommand {
  title: string;
  command: string;
  args: string[];
  cwd: string;
  env: NodeJS.ProcessEnv;
}

export async function runBuildCommand(
  { title, command, args, cwd, env }: BuildCommand,
  log: PhaseLogger,
  secrets: string[]
): Promise<void> {
  try {
    const running = spawnAsync(command, args, { cwd, env });
    const streams = (['stdout', 'stderr'] as const).flatMap(source => {
      const stream = running.child?.[source];
      return stream
        ? [
            streamBuildOutput(stream, secrets, line => {
              log.write(line, source);
            }),
          ]
        : [];
    });
    try {
      await running;
    } finally {
      streams.forEach(stream => {
        stream.close();
      });
    }
  } catch (error) {
    throw new Error(formatBuildError(title, error, secrets));
  }
}
