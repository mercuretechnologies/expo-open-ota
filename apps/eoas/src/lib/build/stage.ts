import spawnAsync from '@expo/spawn-async';
import chalk from 'chalk';
import cliTruncate from 'cli-truncate';
import logUpdate from 'log-update';
import { stripVTControlCharacters } from 'util';

import { formatBuildError } from './errors';
import { BuildLog } from './log';
import { streamBuildOutput } from './output';
import Log from '../log';

export interface BuildStage {
  write(line: string): void;
  finish(success: boolean): void;
}

export interface StageCommand {
  title: string;
  command: string;
  args: string[];
  cwd: string;
  env: NodeJS.ProcessEnv;
}

export interface StageRunner {
  run(stage: StageCommand): Promise<void>;
  abort(): void;
}

// Interactive terminals get a spinner with the last few output lines; CI and
// verbose mode print every line. The build log always receives everything.
export function createBuildStage(
  title: string,
  log: Pick<BuildLog, 'write'>,
  verbose: boolean
): BuildStage {
  const interactive = process.stdout.isTTY && !process.env.CI && !verbose;
  const preview: string[] = [];
  const render = interactive ? logUpdate.create(process.stdout) : undefined;
  let frame = 0;
  let finished = false;
  const display = (heading: string): string => {
    const width = Math.max(1, (process.stdout.columns || 80) - 1);
    const maxLines = Math.max(0, Math.min(4, (process.stdout.rows || 24) - 3));
    const visible = maxLines ? preview.slice(-maxLines) : [];
    return [heading, ...visible.map(line => `│  ${line}`)]
      .map(line => cliTruncate(line, width))
      .join('\n');
  };
  log.write(title);
  const refresh = (): void => {
    render?.(display(`${['◒', '◐', '◓', '◑'][frame++ % 4]}  ${title}`));
  };
  if (render) {
    refresh();
  } else {
    Log.log(title);
  }
  const timer = render ? setInterval(refresh, 100) : undefined;
  return {
    write(line: string): void {
      log.write(line);
      if (finished) {
        return;
      }
      if (!render) {
        process.stdout.write(`${line}\n`);
        return;
      }
      const plain = stripVTControlCharacters(line).replace(/\r/g, '');
      if (plain.trim()) {
        preview.push(plain);
        if (preview.length > 4) {
          preview.shift();
        }
      }
    },
    finish(success: boolean): void {
      if (finished) {
        return;
      }
      finished = true;
      clearInterval(timer);
      const summary = `${title} — ${success ? 'done' : 'failed'}`;
      log.write(summary);
      if (render) {
        if (success) {
          render.clear();
        } else {
          render(display(chalk.red(`▲  ${summary}`)));
        }
        render.done();
      }
      if (success) {
        Log.succeed(summary);
      } else if (!render) {
        Log.fail(summary);
      }
    },
  };
}

// Runs one build tool as a stage: output is redacted and streamed to the log
// and terminal, and a failure becomes a readable error. abort() closes the
// spinner of the running stage when the build is interrupted.
export function createStageRunner(
  buildLog: BuildLog,
  secrets: string[],
  verbose: boolean
): StageRunner {
  let active: BuildStage | undefined;
  return {
    async run({ title, command, args, cwd, env }: StageCommand): Promise<void> {
      const stage = createBuildStage(title, buildLog, verbose);
      active = stage;
      try {
        const running = spawnAsync(command, args, { cwd, env });
        const streams = [running.child?.stdout, running.child?.stderr].flatMap(stream =>
          stream ? [streamBuildOutput(stream, secrets, stage.write)] : []
        );
        try {
          await running;
        } finally {
          streams.forEach(stream => {
            stream.close();
          });
        }
        stage.finish(true);
      } catch (error) {
        stage.finish(false);
        throw new Error(formatBuildError(title, error, secrets));
      } finally {
        active = undefined;
      }
    },
    abort(): void {
      active?.finish(false);
    },
  };
}
