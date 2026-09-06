import fs from 'fs-extra';
import path from 'path';

import Log from '../log';
import { confirmAsync, promptAsync, selectAsync } from '../prompts';
import { CONFIG_FILENAME, parseConfig, readConfig } from './config';
import { readEasConfig } from './eas';
import { convertEasConfig } from './importEas';
import { ApplicationIdSchema, ProfileNameSchema, ResourceNameSchema } from './schema';
import { BuildProfile, XpremConfig } from './types';

export type PreparedConfig =
  | { kind: 'write'; config: XpremConfig }
  | { kind: 'continue' | 'cancelled' };

async function askApplicationId(name: string, initial = ''): Promise<string> {
  const { applicationId } = await promptAsync({
    name: 'applicationId',
    type: 'text',
    initial,
    message: `Android application ID for profile "${name}" (confirm any variant-specific package)`,
    validate: value =>
      !ApplicationIdSchema.required().validate(value).error ||
      'Enter an Android package such as com.example.app',
  });
  return applicationId;
}

async function confirmConfig(config: XpremConfig, hasWarnings = false): Promise<boolean> {
  parseConfig(config);
  Log.log(JSON.stringify(config, null, 2));
  return await confirmAsync({
    name: 'save',
    type: 'confirm',
    message: hasWarnings
      ? 'Accept the reported omissions and save this xprem.json?'
      : 'Save this xprem.json?',
    initial: false,
  });
}

async function reviewEasImport(
  source: string,
  applicationId?: string
): Promise<XpremConfig | undefined> {
  const converted = convertEasConfig(await readEasConfig(source), applicationId);
  for (const issue of converted.issues) {
    Log.warn(`${issue.path}: ${issue.message}`);
  }
  if (converted.issues.some(issue => issue.level === 'error')) {
    throw new Error('EAS import needs manual changes. No xprem.json was written.');
  }
  const hasWarnings = converted.issues.length > 0;
  for (const [name, profile] of Object.entries(converted.config.profiles)) {
    profile.android.applicationId = await askApplicationId(name, profile.android.applicationId);
  }
  return (await confirmConfig(converted.config, hasWarnings)) ? converted.config : undefined;
}

export async function prepareBuildConfig(
  projectDir: string,
  applicationId?: string
): Promise<PreparedConfig> {
  const target = path.join(projectDir, CONFIG_FILENAME);
  if (await fs.pathExists(target)) {
    await readConfig(target);
    Log.succeed('Existing xprem.json is valid and will be kept.');
    return { kind: 'continue' };
  }
  const source = path.join(projectDir, 'eas.json');
  const hasEas = await fs.pathExists(source);
  const choice = await selectAsync('How would you like to configure build profiles?', [
    ...(hasEas ? [{ title: 'Import profiles from eas.json', value: 'import' }] : []),
    { title: 'Create a new profile', value: 'new' },
    { title: 'Cancel', value: 'cancel' },
  ]);
  if (!choice) {
    return { kind: 'cancelled' };
  }
  if (choice === 'cancel') {
    return { kind: 'cancelled' };
  }
  if (choice === 'import') {
    const config = await reviewEasImport(source, applicationId);
    return config ? { kind: 'write', config } : { kind: 'cancelled' };
  }
  const preset = await selectAsync('What is this profile for?', [
    { title: 'Development client (debug APK)', value: 'dev' },
    { title: 'Standalone testing (release APK)', value: 'qa' },
    { title: 'Google Play (release AAB)', value: 'play' },
  ]);
  if (!preset) {
    return { kind: 'cancelled' };
  }
  const { name } = await promptAsync({
    name: 'name',
    type: 'text',
    message: 'Profile name',
    initial: preset,
    validate: value =>
      !ProfileNameSchema.validate(value).error ||
      'Use 1–64 letters, digits, hyphens or underscores, starting with a letter or digit',
  });
  const android: BuildProfile['android'] = {
    applicationId: await askApplicationId(name, applicationId),
    mode: preset === 'dev' ? 'debug' : 'release',
    artifact: preset === 'play' ? 'aab' : 'apk',
    ...(preset === 'dev' ? { developmentClient: true } : {}),
  };
  const { channel } = await promptAsync({
    name: 'channel',
    type: 'text',
    message: 'xprem OTA channel (leave empty for no channel)',
    validate: value =>
      !value ||
      !ResourceNameSchema.validate(value).error ||
      'Enter a valid xprem channel name (128 bytes max; no path separators, controls, or *)',
  });
  const config: XpremConfig = {
    schemaVersion: 1,
    profiles: { [name]: { android, ...(channel ? { channel } : {}) } },
  };
  return (await confirmConfig(config)) ? { kind: 'write', config } : { kind: 'cancelled' };
}
