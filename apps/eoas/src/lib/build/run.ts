import spawnAsync from '@expo/spawn-async';
import { ChildProcess } from 'child_process';

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

let active: ChildProcess | undefined;

// Stops the command in progress, if any, and resolves once it has exited.
export async function terminateBuildCommand(): Promise<void> {
  const child = active;
  if (!child || child.exitCode !== null || child.signalCode !== null) {
    return;
  }
  await new Promise<void>(resolve => {
    const forceKill = setTimeout(() => child.kill('SIGKILL'), 5000);
    child.once('exit', () => {
      clearTimeout(forceKill);
      resolve();
    });
    child.kill('SIGTERM');
  });
}

export async function runBuildCommand(
  { title, command, args, cwd, env }: BuildCommand,
  log: PhaseLogger,
  secrets: string[]
): Promise<void> {
  try {
    const running = spawnAsync(command, args, { cwd, env });
    active = running.child;
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
      active = undefined;
      streams.forEach(stream => {
        stream.close();
      });
    }
  } catch (error) {
    throw new Error(formatBuildError(title, error, secrets));
  }
}
