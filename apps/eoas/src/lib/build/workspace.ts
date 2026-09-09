import { ExpoConfig } from '@expo/config';
import fg from 'fast-glob';
import fs from 'fs-extra';
import os from 'os';
import path from 'path';

import { checkEnvironment } from './environment';
import { BuildLog } from './log';
import { BuildInputs, configEnvironment } from './prepare';
import { BuildCommand, runBuildCommand } from './run';
import { BuildPlatform } from './server';
import { getPrivateExpoConfigAsync } from '../expoConfig';
import GitClient from '../vcs/clients/git';

const TEMPLATES = path.resolve(__dirname, '../../../templates');

// Runs one build inside a temporary directory that is removed afterwards, also
// when the user interrupts the process.
export async function withTemporaryDirectory<T>(
  buildLog: BuildLog,
  work: (temporary: string) => Promise<T>
): Promise<T> {
  const temporary = await fs.mkdtemp(path.join(os.tmpdir(), 'eoas-build-'));
  const interrupt = (): void => {
    buildLog.abort();
    buildLog.write('Build interrupted.');
    void buildLog.close().finally(() => fs.remove(temporary).finally(() => process.exit(130)));
  };
  process.once('SIGINT', interrupt);
  process.once('SIGTERM', interrupt);
  try {
    return await work(temporary);
  } finally {
    process.removeListener('SIGINT', interrupt);
    process.removeListener('SIGTERM', interrupt);
    await fs.remove(temporary);
  }
}

export async function copyTemplate(name: string, destination: string): Promise<void> {
  await fs.copy(path.join(TEMPLATES, name), destination);
}

// EAS likewise archives the project before prebuild and injects signing/version
// Gradle scripts afterwards (eas-cli build-tools/steps/utils/android/gradleConfig).
// Installed dependencies are shared through links; generated native files stay in the copy.
export async function copyProject(project: string, temporary: string): Promise<string> {
  let root = project;
  try {
    root = await new GitClient(project).getRootPathAsync();
  } catch {
    /* Non-git project. */
  }
  const target = path.join(temporary, 'workspace');
  await fs.copy(root, target, {
    filter: async source => {
      const name = path.basename(source);
      if (
        ['.git', '.expo', '.gradle'].includes(name) ||
        source === path.join(project, 'build-artifacts')
      ) {
        return false;
      }
      if (name === 'node_modules') {
        const destination = path.join(target, path.relative(root, source));
        await fs.ensureDir(path.dirname(destination));
        await fs.ensureSymlink(source, destination, 'dir');
        return false;
      }
      return !/[/\\](android|ios)[/\\](app[/\\])?build([/\\]|$)/.test(source);
    },
  });
  return path.join(target, path.relative(root, project));
}

// Evaluates app.config.* with the build environment and removes it from the
// copy; the platform then freezes the result into app.json with writeAppJson.
export async function evaluateExpoConfig(build: BuildInputs, working: string): Promise<ExpoConfig> {
  const expo = await getPrivateExpoConfigAsync(working, {
    env: configEnvironment(build.variables, build.toolEnv, build.nodeEnv),
    packageRunner: build.options.packageRunner,
  });
  const dynamicConfigs = await fg('app.config.{js,cjs,mjs,ts}', { cwd: working });
  // Config source executes in Node, outside Metro's graph, so it is checked here.
  if (!build.options.ignoreEnvCheck) {
    for (const file of dynamicConfigs) {
      checkEnvironment(
        await fs.readFile(path.join(working, file), 'utf8'),
        file,
        Object.keys(build.variables)
      );
    }
  }
  for (const file of dynamicConfigs) {
    await fs.remove(path.join(working, file));
  }
  return expo;
}

export async function writeAppJson(working: string, expo: ExpoConfig): Promise<void> {
  const { _internal, ...config } = expo;
  await fs.writeJson(path.join(working, 'app.json'), { expo: config }, { mode: 0o600 });
}

export function expoCommand(
  build: BuildInputs,
  working: string,
  title: string,
  args: string[]
): BuildCommand {
  const [command, prefix] = build.packageRunner;
  return { title, command, args: [...prefix, 'expo', ...args], cwd: working, env: build.env };
}

// Export walks the real native graph even for debug builds whose binary
// normally loads JS from a development server rather than embedding it.
export async function validateBundle(
  build: BuildInputs,
  platform: BuildPlatform,
  mode: 'debug' | 'release',
  working: string,
  temporary: string,
  buildLog: BuildLog,
  secrets: string[]
): Promise<void> {
  let report: string | undefined;
  if (build.options.ignoreEnvCheck) {
    buildLog.warn(
      'Environment check explicitly disabled; missing/dynamic references are not verified.'
    );
  } else {
    report = await installMetroCheck(working, temporary, Object.keys(build.variables));
  }
  const args = ['export', '--platform', platform, '--output-dir', '.eoas-export', '--clear'];
  if (mode === 'debug') {
    args.push('--dev');
  }
  try {
    await runBuildCommand(
      expoCommand(build, working, `Validating ${platform} bundle`, args),
      buildLog,
      secrets
    );
  } catch (error) {
    // The transformer writes the environment failure to the report; Metro's
    // own output only says that a transform failed.
    if (report && (await fs.pathExists(report))) {
      throw new Error((await fs.readFile(report, 'utf8')).trim());
    }
    throw error;
  }
}

// Replaces the project's Metro config with one whose transformer runs
// checkEnvironment on every project module. Failures land in the returned
// report file; the original config is kept for the wrapper to load.
export async function installMetroCheck(
  project: string,
  temporary: string,
  known: string[]
): Promise<string> {
  const files = await fg('metro.config.{js,cjs,mjs,ts,json}', { cwd: project });
  if (files.length > 1) {
    throw new Error('Multiple Metro configurations found; keep a single config before building.');
  }
  let original: string | undefined;
  if (files[0]) {
    original = path.join(project, `eoas-original-${files[0]}`);
    await fs.move(path.join(project, files[0]), original);
  }
  const report = path.join(temporary, 'environment-errors');
  await fs.writeJson(path.join(project, '.eoas-metro-check.json'), {
    original,
    known,
    report,
    checker: path.resolve(__dirname, '../../../dist/lib/build/environment.js'),
  });
  await copyTemplate('metro/metro.config.cjs', path.join(project, 'metro.config.cjs'));
  await copyTemplate(
    'metro/metro-transformer.cjs',
    path.join(project, 'eoas-metro-transformer.cjs')
  );
  return report;
}

export async function restoreMetroConfig(project: string): Promise<void> {
  const settingsPath = path.join(project, '.eoas-metro-check.json');
  if (!(await fs.pathExists(settingsPath))) {
    return;
  }
  const settings = await fs.readJson(settingsPath);
  await fs.remove(path.join(project, 'metro.config.cjs'));
  await fs.remove(path.join(project, 'eoas-metro-transformer.cjs'));
  if (settings.original) {
    const originalName = path.basename(settings.original).replace(/^eoas-original-/, '');
    await fs.move(settings.original, path.join(project, originalName));
  }
  await fs.remove(settingsPath);
}
