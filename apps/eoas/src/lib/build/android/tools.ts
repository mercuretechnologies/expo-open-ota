import { AndroidConfig } from '@expo/config-plugins';
import spawnAsync from '@expo/spawn-async';
import fg from 'fast-glob';
import fs from 'fs-extra';
import os from 'os';
import path from 'path';

export interface AndroidToolsOptions {
  javaHome?: string;
  androidSdk?: string;
}

// Same SDK lookup as Expo CLI: ANDROID_HOME, then ANDROID_SDK_ROOT, then the
// platform's default install location. Gradle checks project-specific versions.
export async function resolveAndroidTools(
  project: string,
  options: AndroidToolsOptions = {},
  environment: NodeJS.ProcessEnv = process.env,
  report: (message: string) => void = () => {}
): Promise<Record<string, string>> {
  if (!['darwin', 'linux'].includes(os.platform())) {
    throw new Error(
      'Local Android builds require macOS or Linux. On Windows, run the CLI inside WSL.'
    );
  }
  const sdk = await locateAndroidSdk(project, options, environment);
  for (const [pattern, component] of [
    ['platforms/*/android.jar', 'SDK Platforms'],
    ['build-tools/*/aapt2', 'SDK Build-Tools'],
  ]) {
    if (!(await fg(pattern, { cwd: sdk, onlyFiles: true })).length) {
      throw new Error(
        `Android ${component} missing in ${sdk}. Install them using Android Studio's SDK Manager, or select another SDK with --androidSdk.`
      );
    }
  }
  const javaHome = await checkJava(
    project,
    options.javaHome || environment.JAVA_HOME,
    environment,
    report
  );
  return {
    ANDROID_HOME: sdk,
    ANDROID_SDK_ROOT: sdk,
    JAVA_HOME: javaHome,
    PATH: [path.join(javaHome, 'bin'), environment.PATH ?? process.env.PATH ?? ''].join(
      path.delimiter
    ),
  };
}

async function locateAndroidSdk(
  project: string,
  options: AndroidToolsOptions,
  environment: NodeJS.ProcessEnv
): Promise<string> {
  const configured = options.androidSdk || environment.ANDROID_HOME || environment.ANDROID_SDK_ROOT;
  if (configured) {
    const sdk = path.resolve(project, configured);
    if (!(await isDirectory(sdk))) {
      throw new Error(
        `Android SDK not found at ${sdk}. Fix ANDROID_HOME or pass --androidSdk /path/to/sdk.`
      );
    }
    return sdk;
  }
  const defaults =
    os.platform() === 'darwin'
      ? [path.join(os.homedir(), 'Library/Android/sdk')]
      : [path.join(os.homedir(), 'Android/Sdk'), path.join(os.homedir(), 'Android/sdk')];
  for (const candidate of defaults) {
    if (await isDirectory(candidate)) {
      return candidate;
    }
  }
  throw new Error(
    `Android SDK not found at ${defaults[0]}. Install it with Android Studio and set ANDROID_HOME or pass --androidSdk /path/to/sdk.`
  );
}

async function isDirectory(directory: string): Promise<boolean> {
  try {
    return (await fs.stat(directory)).isDirectory();
  } catch (error) {
    if (['ENOENT', 'ENOTDIR'].includes((error as NodeJS.ErrnoException).code ?? '')) {
      return false;
    }
    throw error;
  }
}

async function checkJava(
  project: string,
  configuredHome: string | undefined,
  environment: NodeJS.ProcessEnv,
  report: (message: string) => void
): Promise<string> {
  // is configuredHome is an absolute path path.resolve will just resolve "configureHome"
  let javaHome = configuredHome ? path.resolve(project, configuredHome) : undefined;
  const java = javaHome ? path.join(javaHome, 'bin/java') : 'java';
  const env = { ...environment, ...(javaHome ? { JAVA_HOME: javaHome } : {}) };
  let settings: string;
  try {
    const output = await spawnAsync(java, ['-XshowSettings:properties', '-version'], {
      cwd: project,
      env,
      timeout: 10000,
    });
    settings = `${output.stdout}\n${output.stderr}`;
  } catch {
    throw new Error(
      `Java could not run (${java}). Install a JDK and set JAVA_HOME or pass --javaHome /path/to/jdk.`
    );
  }
  const version = settings.match(/^\s*java\.version\s*=\s*(\S+)/m)?.[1];
  const major = javaMajor(version);
  if (!javaHome) {
    javaHome = settings.match(/^\s*java\.home\s*=\s*(.+)$/m)?.[1].trim();
    // Java 8 reports the embedded JRE rather than its enclosing JDK.
    if (major === 8 && javaHome && path.basename(javaHome) === 'jre') {
      javaHome = path.dirname(javaHome);
    }
  }
  if (!major || !javaHome || !path.isAbsolute(javaHome)) {
    throw new Error(
      'Could not determine the Java version or JDK directory. Set JAVA_HOME or pass --javaHome /path/to/jdk.'
    );
  }
  let compiler: string;
  try {
    const output = await spawnAsync(path.join(javaHome, 'bin/javac'), ['-version'], {
      cwd: project,
      env: { ...env, JAVA_HOME: javaHome },
      timeout: 10000,
    });
    compiler = `${output.stdout}\n${output.stderr}`;
  } catch {
    throw new Error(
      `Java compiler unavailable in ${javaHome}. A full JDK is required; set JAVA_HOME or pass --javaHome /path/to/jdk.`
    );
  }
  const compilerMajor = javaMajor(compiler.match(/\bjavac\s+(\S+)/)?.[1]);
  if (compilerMajor !== major) {
    throw new Error(
      `Java ${major} and javac ${
        compilerMajor ?? '(unknown)'
      } do not match in ${javaHome}. Select a complete JDK with JAVA_HOME or --javaHome.`
    );
  }
  report(`Java ${version} (JDK: ${javaHome}); project compatibility is checked by Gradle.`);
  return javaHome;
}

function javaMajor(version: string | undefined): number | undefined {
  const match = version?.match(/^(?:1\.)?(\d+)/);
  return match ? Number(match[1]) : undefined;
}

// Only call on the temporary project so sdk.dir agrees with the selected SDK.
export async function configureAndroidSdk(project: string, sdk: string): Promise<void> {
  const file = path.join(project, 'android/local.properties');
  const properties = (await readProperties(file)).filter(
    item => item.type !== 'property' || item.key.trim() !== 'sdk.dir'
  );
  properties.push({
    type: 'property',
    key: 'sdk.dir',
    value: sdk.replace(/\\/g, '\\\\').replace(/:/g, '\\:'),
  });
  await fs.outputFile(file, AndroidConfig.Properties.propertiesListToString(properties));
}

async function readProperties(file: string): Promise<AndroidConfig.Properties.PropertiesItem[]> {
  try {
    return AndroidConfig.Properties.parsePropertiesFile(await fs.readFile(file, 'utf8'));
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code === 'ENOENT') {
      return [];
    }
    throw error;
  }
}
