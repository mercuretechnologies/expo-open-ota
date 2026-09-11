import { AndroidConfig } from '@expo/config-plugins';
import fg from 'fast-glob';
import fs from 'fs-extra';
import path from 'path';

import { AndroidToolsOptions, configureAndroidSdk, resolveAndroidTools } from './tools';
import Log from '../../log';
import { resolvePackageRunner, splitPackageRunner } from '../../packageRunner';
import { resolveExpoUpdatesCli } from '../../runtimeVersion';
import { secretsToRedact } from '../errors';
import { BuildLog, PhaseLogger, withBuildLog } from '../log';
import { BuildPhase } from '../phases';
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
import { BuildCommand, runBuildCommand } from '../run';
import { allocateBuildNumber, fetchCredentials } from '../server';
import {
  copyProject,
  copyTemplate,
  evaluateExpoConfig,
  expoCommand,
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
  return await withBuildLog(
    project,
    options.profile,
    'Android build',
    async buildLog => {
      const build = await prepareBuild(project, options, buildLog);
      const secrets = secretsToRedact(build.variables, [
        build.credentials.keystore,
        build.credentials.keystorePassword,
        build.credentials.keyPassword,
      ]);
      buildLog.maskSecrets(secrets);
      return await withTemporaryDirectory(buildLog, temporary =>
        buildInWorkspace(build, temporary, buildLog, secrets)
      );
    },
    options.verbose || Log.isDebug
  );
}

async function prepareBuild(
  project: string,
  options: AndroidBuildOptions,
  buildLog: BuildLog
): Promise<AndroidBuild> {
  const profile = await buildLog.runBuildPhase(
    BuildPhase.CUSTOM,
    () => selectProfile(project, options),
    'Read xprem.json'
  );
  const local = await readEnvFile(project, options.envFile);
  buildLog.maskSecrets(secretsToRedact(local, []));
  const toolEnv = await buildLog.runBuildPhase(
    BuildPhase.BUILDER_INFO,
    async phaseLog => {
      const tools = await resolveAndroidTools(
        project,
        options,
        { ...process.env, ...local },
        message => {
          phaseLog.info(message);
        }
      );
      phaseLog.info(`Android SDK: ${tools.ANDROID_HOME}`);
      phaseLog.info(`JAVA_HOME: ${tools.JAVA_HOME}`);
      return tools;
    },
    'Check local Android tools'
  );
  const packageRunner = splitPackageRunner(resolvePackageRunner(options.packageRunner, project));
  const nodeEnv = nodeEnvFor(profile.android.mode);
  const endpoint = await resolveBuildEndpoint(
    project,
    options,
    'android',
    profile.android.applicationId,
    configEnvironment(local, toolEnv, nodeEnv)
  );
  const variables = await buildLog.runBuildPhase(BuildPhase.SET_UP_BUILD_ENVIRONMENT, phaseLog =>
    fetchBuildEnvironment(endpoint, profile, local, phaseLog)
  );
  buildLog.maskSecrets(secretsToRedact(variables, []));
  const credentials = await buildLog.runBuildPhase(
    BuildPhase.PREPARE_CREDENTIALS,
    () =>
      fetchCredentials<AndroidCredentials>(endpoint, 'android', [
        'keystore',
        'keystorePassword',
        'keyAlias',
        'keyPassword',
      ]),
    'Fetch Android signing credentials'
  );
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
  buildLog: BuildLog,
  secrets: string[]
): Promise<string> {
  const { applicationId, developmentClient, mode } = build.profile.android;
  const working = await buildLog.runBuildPhase(BuildPhase.PREPARE_PROJECT, async () => {
    const working = await copyProject(build.project, temporary);
    if (
      developmentClient &&
      !(await fs.pathExists(path.join(working, 'node_modules/expo-dev-client')))
    ) {
      throw new Error('This profile requires expo-dev-client to be installed.');
    }
    return working;
  });
  const expo = await buildLog.runBuildPhase(BuildPhase.READ_APP_CONFIG, async () => {
    const config = await evaluateExpoConfig(build, working);
    await writeAppJson(working, config);
    return config;
  });
  await buildLog.runBuildPhase(
    BuildPhase.EAGER_BUNDLE,
    async phaseLog => {
      await validateBundle(build, 'android', mode, working, temporary, phaseLog, secrets);
    },
    'Validate Android bundle'
  );
  const versionCode = await buildLog.runBuildPhase(
    BuildPhase.CUSTOM,
    () => allocateVersionCode(build.endpoint),
    'Allocate build number'
  );
  const effectiveExpo = {
    ...expo,
    android: { ...expo.android, package: applicationId, versionCode },
  };
  await writeAppJson(working, effectiveExpo);
  await buildLog.runBuildPhase(BuildPhase.PREBUILD, async phaseLog => {
    if (await fs.pathExists(path.join(working, 'android'))) {
      phaseLog.markSkipped();
      phaseLog.info(
        'Using maintained Android project. Native settings are retained; package, versionCode, signing and the expo-updates configuration are overridden in the temporary copy.'
      );
      // Same as EAS for bare projects: the manifest keeps whatever environment
      // the last prebuild saw, so channel, URL and runtime are re-synced here.
      await runBuildCommand(
        {
          title: 'Syncing expo-updates configuration',
          command: process.execPath,
          args: [
            resolveExpoUpdatesCli(working),
            'configuration:syncnative',
            '--platform',
            'android',
            '--workflow',
            'generic',
          ],
          cwd: working,
          env: build.env,
        },
        phaseLog,
        secrets
      );
    } else {
      await runBuildCommand(
        expoCommand(build, working, 'Generating Android project', [
          'prebuild',
          '--platform',
          'android',
          '--no-install',
        ]),
        phaseLog,
        secrets
      );
    }
  });
  const signing = await buildLog.runBuildPhase(
    BuildPhase.PREPARE_CREDENTIALS,
    async () => {
      const keystore = await writeKeystore(build.credentials, temporary);
      await configureAndroidSdk(working, build.toolEnv.ANDROID_HOME);
      const signingFile = await configureSigning(build, working, temporary, keystore, versionCode);
      await prepareGradlew(working);
      return signingFile;
    },
    'Configure Android signing'
  );
  await buildLog.runBuildPhase(
    BuildPhase.RUN_GRADLEW,
    phaseLog => runBuildCommand(gradleCommand(build, working, signing), phaseLog, secrets),
    `Building signed ${build.profile.android.artifact.toUpperCase()}`
  );
  return await buildLog.runBuildPhase(BuildPhase.PREPARE_ARTIFACTS, phaseLog =>
    collectArtifact(build, working, versionCode, phaseLog)
  );
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

function gradleCommand(build: AndroidBuild, working: string, signing: string): BuildCommand {
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
  buildLog: PhaseLogger
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
