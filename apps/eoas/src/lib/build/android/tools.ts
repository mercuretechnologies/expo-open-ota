import { AndroidConfig } from '@expo/config-plugins';
import fs from 'fs-extra';
import os from 'os';
import path from 'path';

export interface AndroidToolsOptions {
  javaHome?: string;
  androidSdk?: string;
}

// Same SDK lookup as Expo CLI: ANDROID_HOME, then ANDROID_SDK_ROOT, then the
// platform's default install location. Gradle validates the JDK and the SDK contents.
export async function resolveAndroidTools(
  project: string,
  options: AndroidToolsOptions = {},
  environment: NodeJS.ProcessEnv = process.env
): Promise<Record<string, string>> {
  if (!['darwin', 'linux'].includes(os.platform())) {
    throw new Error(
      'Local Android builds require macOS or Linux. On Windows, run the CLI inside WSL.'
    );
  }
  const sdk = await locateAndroidSdk(project, options, environment);
  const env: Record<string, string> = { ANDROID_HOME: sdk, ANDROID_SDK_ROOT: sdk };
  const javaHome = options.javaHome || environment.JAVA_HOME;
  if (javaHome) {
    env.JAVA_HOME = path.resolve(project, javaHome);
  }
  return env;
}

async function locateAndroidSdk(
  project: string,
  options: AndroidToolsOptions,
  environment: NodeJS.ProcessEnv
): Promise<string> {
  const configured = options.androidSdk || environment.ANDROID_HOME || environment.ANDROID_SDK_ROOT;
  if (configured) {
    const sdk = path.resolve(project, configured);
    if (!(await fs.pathExists(sdk))) {
      throw new Error(
        `Android SDK not found at ${sdk}. Fix ANDROID_HOME or pass --android-sdk /path/to/sdk.`
      );
    }
    return sdk;
  }
  const defaults =
    os.platform() === 'darwin'
      ? [path.join(os.homedir(), 'Library/Android/sdk')]
      : [path.join(os.homedir(), 'Android/Sdk'), path.join(os.homedir(), 'Android/sdk')];
  for (const candidate of defaults) {
    if (await fs.pathExists(candidate)) {
      return candidate;
    }
  }
  throw new Error(
    `Android SDK not found at ${defaults[0]}. Install it with Android Studio and set ANDROID_HOME or pass --android-sdk /path/to/sdk.`
  );
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
