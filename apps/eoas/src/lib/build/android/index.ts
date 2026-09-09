import { AndroidConfig } from '@expo/config-plugins';
import fg from 'fast-glob';
import fs from 'fs-extra';
import path from 'path';

import { AndroidToolsOptions, configureAndroidSdk, resolveAndroidTools } from './tools';
import Log from '../../log';
import { resolvePackageRunner, splitPackageRunner } from '../../packageRunner';
import { secretsToRedact } from '../errors';
import { BuildLog, withBuildLog } from '../log';
import {
  BuildInputs,
  BuildOptions,
  configEnvironment,
  fetchBuildEnvironment,
  nodeEnvFor,
  readEnvFile,
  resolveBuildEndpoint,
  resolveOutputPath,
  selectProfile,
  spawnEnvironment,
} from '../prepare';
import { allocateBuildNumber, fetchCredentials } from '../server';
import { StageCommand, StageRunner, createStageRunner } from '../stage';
import {
  copyProject,
  copyTemplate,
  evaluateExpoConfig,
  expoStage,
  validateBundle,
  withTemporaryDirectory,
  writeAppJson,
} from '../workspace';

export type AndroidBuildOptions = BuildOptions & AndroidToolsOptions;

export interface AndroidCredentials {
  keystore: string;
  keystorePassword: string;
  keyAlias: string;
  keyPassword: string;
}

interface AndroidBuild extends BuildInputs {
  options: AndroidBuildOptions;
  credentials: AndroidCredentials;
}

const MAX_VERSION_CODE = 2100000000;

export async function buildAndroid(project: string, options: AndroidBuildOptions): Promise<string> {
  return await withBuildLog(project, options.profile, 'Android build', async buildLog => {
    const build = await prepareBuild(project, options, buildLog);
    const secrets = secretsToRedact(build.variables, [
      build.credentials.keystore,
      build.credentials.keystorePassword,
      build.credentials.keyPassword,
    ]);
    const stages = createStageRunner(buildLog, secrets, options.verbose || Log.isDebug);
    return await withTemporaryDirectory(buildLog, stages, temporary =>
      buildInWorkspace(build, temporary, stages, buildLog)
    );
  });
}

async function prepareBuild(
  project: string,
  options: AndroidBuildOptions,
  buildLog: BuildLog
): Promise<AndroidBuild> {
  const profile = await selectProfile(project, options);
  const local = await readEnvFile(project, options.envFile);
  buildLog.info('Locating local Android tools');
  const toolEnv = await resolveAndroidTools(
    project,
    options,
    { ...process.env, ...local },
    buildLog.info
  );
  buildLog.info(`Android SDK: ${toolEnv.ANDROID_HOME}`);
  buildLog.info(`JAVA_HOME: ${toolEnv.JAVA_HOME}`);
  const packageRunner = splitPackageRunner(resolvePackageRunner(options.packageRunner, project));
  const nodeEnv = nodeEnvFor(profile.android.mode);
  const endpoint = await resolveBuildEndpoint(
    project,
    options,
    'android',
    profile.android.applicationId,
    configEnvironment(local, toolEnv, nodeEnv)
  );
  const variables = await fetchBuildEnvironment(endpoint, profile, local, buildLog);
  const credentials = await fetchCredentials<AndroidCredentials>(endpoint, 'android', [
    'keystore',
    'keystorePassword',
    'keyAlias',
    'keyPassword',
  ]);
  const output = await resolveOutputPath(project, options, profile.android.artifact, buildLog);
  return {
    project,
    options,
    profile,
    credentials,
    packageRunner,
    endpoint,
    variables,
    output,
    nodeEnv,
    toolEnv,
    env: spawnEnvironment(variables, toolEnv, nodeEnv),
  };
}

async function buildInWorkspace(
  build: AndroidBuild,
  temporary: string,
  stages: StageRunner,
  buildLog: BuildLog
): Promise<string> {
  const { applicationId, developmentClient, mode } = build.profile.android;
  const working = await copyProject(build.project, temporary);
  const keystore = await writeKeystore(build.credentials, temporary);
  if (
    developmentClient &&
    !(await fs.pathExists(path.join(working, 'node_modules/expo-dev-client')))
  ) {
    throw new Error('This profile requires expo-dev-client to be installed.');
  }
  const expo = await evaluateExpoConfig(build, working);
  await writeAppJson(working, expo);
  await validateBundle(build, 'android', mode, working, temporary, stages, buildLog);
  const versionCode = await allocateVersionCode(build.endpoint);
  await writeAppJson(working, {
    ...expo,
    android: { ...expo.android, package: applicationId, versionCode },
  });
  if (await fs.pathExists(path.join(working, 'android'))) {
    buildLog.info(
      'Using maintained Android project. Native settings are retained; package, versionCode, signing and the expo-updates configuration are overridden in the temporary copy.'
    );
    // Same as EAS for bare projects: the manifest keeps whatever environment
    // the last prebuild saw, so channel, URL and runtime are re-synced here.
    const [command, prefix] = build.packageRunner;
    await stages.run({
      title: 'Syncing expo-updates configuration',
      command,
      args: [
        ...prefix,
        'expo-updates',
        'configuration:syncnative',
        '--platform',
        'android',
        '--workflow',
        'generic',
      ],
      cwd: working,
      env: build.env,
    });
  } else {
    await stages.run(
      expoStage(build, working, 'Generating Android project', [
        'prebuild',
        '--platform',
        'android',
        '--no-install',
      ])
    );
  }
  await configureAndroidSdk(working, build.toolEnv.ANDROID_HOME);
  const signing = await configureSigning(build, working, temporary, keystore, versionCode);
  await prepareGradlew(working);
  await stages.run(gradleStage(build, working, signing));
  return await collectArtifact(build, working, versionCode, buildLog);
}

// The server validated the keystore, alias and passwords when they were stored.
async function writeKeystore(credentials: AndroidCredentials, temporary: string): Promise<string> {
  const keystore = path.join(temporary, 'keystore');
  await fs.writeFile(keystore, Buffer.from(credentials.keystore, 'base64'), { mode: 0o600 });
  return keystore;
}

async function allocateVersionCode(endpoint: string): Promise<number> {
  const versionCode = Number(await allocateBuildNumber(endpoint));
  if (versionCode > MAX_VERSION_CODE) {
    throw new Error('Allocated build number exceeds the Android versionCode limit.');
  }
  return versionCode;
}

// Gradle reads signing.json through EOAS_SIGNING_FILE; the script itself holds no secrets.
async function configureSigning(
  build: AndroidBuild,
  working: string,
  temporary: string,
  keystore: string,
  versionCode: number
): Promise<string> {
  const signing = path.join(temporary, 'signing.json');
  await fs.writeJson(
    signing,
    {
      ...build.credentials,
      keystore,
      applicationId: build.profile.android.applicationId,
      versionCode,
    },
    { mode: 0o600 }
  );
  const buildGradle = AndroidConfig.Paths.getAppBuildGradleFilePath(working);
  await copyTemplate('gradle/eoas.gradle', path.join(path.dirname(buildGradle), 'eoas.gradle'));
  await fs.appendFile(
    buildGradle,
    buildGradle.endsWith('.kts')
      ? '\napply(from = "eoas.gradle")\n'
      : '\napply from: "eoas.gradle"\n'
  );
  return signing;
}

// Checkouts made on Windows or unpacked from archives lose the executable bit
// and may carry CRLF line endings. A missing wrapper is left for spawn to report.
async function prepareGradlew(working: string): Promise<void> {
  const gradlew = path.join(working, 'android/gradlew');
  if (!(await fs.pathExists(gradlew))) {
    return;
  }
  const script = await fs.readFile(gradlew, 'utf8');
  if (script.includes('\r')) {
    await fs.writeFile(gradlew, script.replace(/\r\n/g, '\n'));
  }
  await fs.chmod(gradlew, 0o755);
}

function gradleStage(build: AndroidBuild, working: string, signing: string): StageCommand {
  const { artifact, mode } = build.profile.android;
  const task = `${artifact === 'aab' ? 'bundle' : 'assemble'}${
    mode === 'release' ? 'Release' : 'Debug'
  }`;
  return {
    title: `Building signed ${artifact.toUpperCase()}`,
    command: path.join(working, 'android/gradlew'),
    args: [`:app:${task}`, '--no-daemon', '--console=plain'],
    cwd: path.join(working, 'android'),
    env: { ...build.env, EOAS_SIGNING_FILE: signing, LC_ALL: 'C.UTF-8' },
  };
}

async function collectArtifact(
  build: AndroidBuild,
  working: string,
  versionCode: number,
  buildLog: BuildLog
): Promise<string> {
  const { artifact, mode } = build.profile.android;
  const candidates = await fg(
    `android/app/build/outputs/${artifact === 'aab' ? 'bundle' : 'apk'}/**/*.${artifact}`,
    { cwd: working, absolute: true }
  );
  const matching = candidates.filter(file => file.toLowerCase().includes(mode));
  if (matching.length !== 1) {
    throw new Error('Expected one build artifact; split APKs/product flavors are not supported.');
  }
  await fs.ensureDir(path.dirname(build.output));
  await fs.copyFile(matching[0], build.output, fs.constants.COPYFILE_EXCL);
  const summary = `Built ${build.output} (versionCode ${versionCode}).`;
  buildLog.write(summary);
  Log.succeed(summary);
  return build.output;
}
