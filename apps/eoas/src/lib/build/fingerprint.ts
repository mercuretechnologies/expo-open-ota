import { ExpoConfig } from '@expo/config';
import fs from 'fs-extra';
import path from 'path';
import resolveFrom from 'resolve-from';

import { Workflow } from '../workflow';
import { PhaseLogger } from './log';
import { BuildInputs } from './prepare';
import { runBuildCommand } from './run';
import { resolveRuntimeVersionAsync } from '../runtimeVersion';

export async function fingerprintAndroidBuild(
  build: BuildInputs,
  working: string,
  temporary: string,
  expo: ExpoConfig,
  buildLog: PhaseLogger,
  secrets: string[]
): Promise<{ fingerprint: string; expoSdk: string; runtimeVersion?: string }> {
  const fingerprintModule =
    resolveFrom.silent(working, 'expo/fingerprint') ?? require.resolve('@expo/fingerprint');
  const output = path.join(temporary, 'fingerprint.json');
  await runBuildCommand(
    {
      title: 'Computing Expo fingerprint',
      command: process.execPath,
      args: [
        path.resolve(__dirname, '../../../templates/fingerprint.cjs'),
        working,
        fingerprintModule,
        output,
      ],
      cwd: working,
      env: build.env,
    },
    buildLog,
    secrets
  );
  const fingerprint = await fs.readJson(output);
  if (
    !/^(?:[a-f0-9]{40}|[a-f0-9]{64})$/.test(fingerprint.fingerprint) ||
    typeof fingerprint.expoSdk !== 'string'
  ) {
    throw new Error('Invalid Expo fingerprint result.');
  }
  const runtime = await resolveRuntimeVersionAsync({
    exp: expo,
    platform: 'android',
    workflow: (await fs.pathExists(path.join(working, 'android')))
      ? Workflow.GENERIC
      : Workflow.MANAGED,
    projectDir: working,
    cwd: working,
    env: Object.fromEntries(
      Object.entries(build.env).filter((entry): entry is [string, string] => entry[1] !== undefined)
    ),
  });
  return {
    fingerprint: fingerprint.fingerprint,
    expoSdk: fingerprint.expoSdk,
    runtimeVersion: runtime?.runtimeVersion ?? undefined,
  };
}
