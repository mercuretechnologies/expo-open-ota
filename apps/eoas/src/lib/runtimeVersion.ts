import { ExpoConfig } from '@expo/config';
import { Updates } from '@expo/config-plugins';
import spawnAsync from '@expo/spawn-async';
import fs from 'fs-extra';
import resolveFrom, { silent as silentResolveFrom } from 'resolve-from';
import semver from 'semver';

import { Env } from './expoConfig';
import Log, { link } from './log';
import { Workflow } from './workflow';

export class ExpoUpdatesCLIModuleNotFoundError extends Error {}
export class ExpoUpdatesCLIInvalidCommandError extends Error {}
export class ExpoUpdatesCLICommandFailedError extends Error {}

export async function expoUpdatesCommandAsync(
  projectDir: string,
  args: string[],
  options: { env: Env | undefined; cwd?: string }
): Promise<string> {
  let expoUpdatesCli;
  try {
    expoUpdatesCli =
      silentResolveFrom(projectDir, 'expo-updates/bin/cli') ??
      resolveFrom(projectDir, 'expo-updates/bin/cli.js');
  } catch (e: any) {
    if (e.code === 'MODULE_NOT_FOUND') {
      throw new ExpoUpdatesCLIModuleNotFoundError(
        `The \`expo-updates\` package was not found. Follow the installation directions at ${link(
          'https://docs.expo.dev/bare/installing-expo-modules/'
        )}`
      );
    }
    throw e;
  }

  try {
    return (
      await spawnAsync(expoUpdatesCli, args, {
        stdio: 'pipe',
        env: { ...process.env, ...options.env },
        cwd: options.cwd,
      })
    ).stdout;
  } catch (e: any) {
    if (e.stderr && typeof e.stderr === 'string') {
      if (e.stderr.includes('Invalid command')) {
        throw new ExpoUpdatesCLIInvalidCommandError(
          `The command specified by ${args} was not valid in the \`expo-updates\` CLI.`
        );
      } else {
        throw new ExpoUpdatesCLICommandFailedError(e.stderr);
      }
    }

    throw e;
  }
}

async function getExpoUpdatesPackageVersionIfInstalledAsync(
  projectDir: string
): Promise<string | null> {
  const maybePackageJson = resolveFrom.silent(projectDir, 'expo-updates/package.json');
  if (!maybePackageJson) {
    return null;
  }
  const { version } = await fs.readJson(maybePackageJson);
  return version ?? null;
}

export async function isModernExpoUpdatesCLIWithRuntimeVersionCommandSupportedAsync(
  projectDir: string
): Promise<boolean> {
  const expoUpdatesPackageVersion = await getExpoUpdatesPackageVersionIfInstalledAsync(projectDir);
  if (expoUpdatesPackageVersion === null) {
    return false;
  }

  if (expoUpdatesPackageVersion.includes('canary')) {
    return true;
  }

  // Anything SDK 51 or greater uses the expo-updates CLI
  return semver.gte(expoUpdatesPackageVersion, '0.25.4');
}

export async function resolveRuntimeVersionUsingCLIAsync({
  platform,
  workflow,
  projectDir,
  env,
  cwd,
}: {
  platform: 'ios' | 'android';
  workflow: Workflow;
  projectDir: string;
  env: Env | undefined;
  cwd?: string;
}): Promise<{
  runtimeVersion: string | null;
  expoUpdatesRuntimeFingerprint: {
    fingerprintSources: object[];
    isDebugFingerprintSource: boolean;
  } | null;
  expoUpdatesRuntimeFingerprintHash: string | null;
}> {
  Log.debug('Using expo-updates runtimeversion:resolve CLI for runtime version resolution');

  const useDebugFingerprintSource = Log.isDebug;

  const extraArgs = useDebugFingerprintSource ? ['--debug'] : [];

  const resolvedRuntimeVersionJSONResult = await expoUpdatesCommandAsync(
    projectDir,
    ['runtimeversion:resolve', '--platform', platform, '--workflow', workflow, ...extraArgs],
    { env, cwd }
  );
  const runtimeVersionResult = JSON.parse(resolvedRuntimeVersionJSONResult);

  Log.debug('runtimeversion:resolve output:');
  Log.debug(resolvedRuntimeVersionJSONResult);

  return {
    runtimeVersion: runtimeVersionResult.runtimeVersion ?? null,
    expoUpdatesRuntimeFingerprint: runtimeVersionResult.fingerprintSources
      ? {
          fingerprintSources: runtimeVersionResult.fingerprintSources,
          isDebugFingerprintSource: useDebugFingerprintSource,
        }
      : null,
    expoUpdatesRuntimeFingerprintHash: runtimeVersionResult.fingerprintSources
      ? runtimeVersionResult.runtimeVersion
      : null,
  };
}

export async function resolveRuntimeVersionAsync({
  exp,
  platform,
  workflow,
  projectDir,
  env,
  cwd,
}: {
  exp: ExpoConfig;
  platform: 'ios' | 'android';
  workflow: Workflow;
  projectDir: string;
  env: Env | undefined;
  cwd?: string;
}): Promise<{
  runtimeVersion: string | null;
  expoUpdatesRuntimeFingerprint: {
    fingerprintSources: object[];
    isDebugFingerprintSource: boolean;
  } | null;
  expoUpdatesRuntimeFingerprintHash: string | null;
} | null> {
  if (!(await isModernExpoUpdatesCLIWithRuntimeVersionCommandSupportedAsync(projectDir))) {
    // fall back to the previous behavior (using the @expo/config-plugins eas-cli dependency rather
    // than the versioned @expo/config-plugins dependency in the project)
    return {
      runtimeVersion: await Updates.getRuntimeVersionNullableAsync(projectDir, exp, platform),
      expoUpdatesRuntimeFingerprint: null,
      expoUpdatesRuntimeFingerprintHash: null,
    };
  }

  try {
    return await resolveRuntimeVersionUsingCLIAsync({ platform, workflow, projectDir, env, cwd });
  } catch (e: any) {
    // if expo-updates is not installed, there's no need for a runtime version in the build
    if (e instanceof ExpoUpdatesCLIModuleNotFoundError) {
      return null;
    }
    throw e;
  }
}
