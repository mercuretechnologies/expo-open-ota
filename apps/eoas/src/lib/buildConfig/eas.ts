import fs from 'fs-extra';
import Joi from 'joi';
import JSON5 from 'json5';

export interface EasProfile {
  extends?: string;
  channel?: string;
  developmentClient?: boolean;
  distribution?: 'internal' | 'store';
  buildType?: 'apk' | 'app-bundle';
  gradleCommand?: string;
  env?: Record<string, string>;
  android?: EasProfile;
  ios?: EasProfile;
  [key: string]: unknown;
}

export interface EasConfig {
  build: Record<string, EasProfile>;
  [key: string]: unknown;
}

const MAX_CONFIG_SIZE = 1024 * 1024;
const UNSAFE_KEYS = new Set(['__proto__', 'constructor', 'prototype']);

function assertSafeKeys(value: unknown, location = '$'): void {
  if (Array.isArray(value)) {
    value.forEach((item, index) => {
      assertSafeKeys(item, `${location}.${index}`);
    });
    return;
  }
  if (!value || typeof value !== 'object') {
    return;
  }
  for (const [key, child] of Object.entries(value)) {
    if (UNSAFE_KEYS.has(key)) {
      throw new Error(`Unsafe key in eas.json at ${location}.${key}.`);
    }
    assertSafeKeys(child, `${location}.${key}`);
  }
}

const commonFields = {
  channel: Joi.string(),
  developmentClient: Joi.boolean(),
  distribution: Joi.string().valid('internal', 'store'),
  env: Joi.object().pattern(/.*/, Joi.string().allow('')),
};
const EasProfileSchema = Joi.object({
  ...commonFields,
  extends: Joi.string(),
  android: Joi.object({
    ...commonFields,
    buildType: Joi.string().valid('apk', 'app-bundle'),
    gradleCommand: Joi.string(),
  }).unknown(),
  ios: Joi.object().unknown(),
}).unknown();

// Validate the fields we interpret; retain other fields to report import omissions.
export function validateEasConfig(input: unknown): EasConfig {
  const { error, value } = Joi.object({
    build: Joi.object().pattern(/.*/, EasProfileSchema).min(1).required(),
  })
    .unknown()
    .required()
    .validate(input, { abortEarly: false, convert: false });
  if (error) {
    throw new Error(
      error.details
        .map(detail => `${detail.path.join('.')}: invalid EAS option (${detail.type})`)
        .join('\n')
    );
  }
  return value as EasConfig;
}

export function parseEasConfig(content: string): EasConfig {
  let input: unknown;
  try {
    try {
      input = JSON.parse(content);
    } catch {
      input = JSON5.parse(content);
    }
  } catch {
    throw new Error('Invalid JSON in eas.json. Check the file syntax.');
  }
  assertSafeKeys(input);
  return validateEasConfig(input);
}

export async function readEasConfig(file: string): Promise<EasConfig> {
  let stat;
  try {
    stat = await fs.stat(file);
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code === 'ENOENT') {
      throw new Error('eas.json does not exist. Pass the path to an EAS configuration file.');
    }
    throw error;
  }
  if (!stat.isFile() || stat.size > MAX_CONFIG_SIZE) {
    throw new Error('eas.json must be a JSON file smaller than 1 MiB.');
  }
  return parseEasConfig(await fs.readFile(file, 'utf8'));
}

function mergeProfiles(parent: EasProfile, child: EasProfile): EasProfile {
  const merged = { ...parent, ...child };
  for (const platform of ['android', 'ios'] as const) {
    if (parent[platform] || child[platform]) {
      merged[platform] = mergeProfiles(parent[platform] ?? {}, child[platform] ?? {});
    }
  }
  if (parent.env || child.env) {
    merged.env = { ...parent.env, ...child.env };
  }
  return merged;
}

export function resolveEasProfile(
  profiles: Record<string, EasProfile>,
  name: string,
  ancestors: string[] = []
): EasProfile {
  if (ancestors.includes(name)) {
    throw new Error(`build.${name}.extends: inheritance cycle`);
  }
  if (ancestors.length >= 5) {
    throw new Error(`build.${name}.extends: inheritance is too deep (maximum five profiles)`);
  }
  if (!Object.prototype.hasOwnProperty.call(profiles, name)) {
    throw new Error(`build.${name}: inherited profile does not exist`);
  }
  const profile = profiles[name];
  return profile.extends
    ? mergeProfiles(resolveEasProfile(profiles, profile.extends, [...ancestors, name]), profile)
    : mergeProfiles({}, profile);
}
