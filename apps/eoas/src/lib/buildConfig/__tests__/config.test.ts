import fs from 'fs-extra';
import os from 'os';
import path from 'path';
import { afterEach, describe, expect, it } from 'vitest';

import { readConfig, validateConfig, writeNewConfig } from '../config';
import { convertEasConfig } from '../importEas';

const profile = {
  channel: 'staging',
  android: { applicationId: 'com.example.qa', mode: 'release', artifact: 'apk' },
};
const config = (): { schemaVersion: number; profiles: { qa: typeof profile } } => ({
  schemaVersion: 1,
  profiles: { qa: structuredClone(profile) },
});
const directories: string[] = [];
afterEach(async () => {
  await Promise.all(directories.splice(0).map(dir => fs.remove(dir)));
});

describe('xprem.json contract', () => {
  it('accepts explicit profiles with or without a resource selection', () => {
    expect(validateConfig(config())).toEqual([]);
    const offline = config();
    delete (offline.profiles.qa as { channel?: string }).channel;
    expect(validateConfig(offline)).toEqual([]);
  });

  it.each([
    ['empty profiles', { schemaVersion: 1, profiles: {} }, 'profiles'],
    ['unknown field', { ...config(), env: { SECRET: 'never-print-this' } }, 'env'],
    [
      'bad format',
      {
        schemaVersion: 1,
        profiles: { qa: { android: { ...profile.android, artifact: 'bundle' } } },
      },
      'profiles.qa.android.artifact',
    ],
    [
      'invalid package',
      {
        schemaVersion: 1,
        profiles: { qa: { android: { ...profile.android, applicationId: 'not-a-package' } } },
      },
      'profiles.qa.android.applicationId',
    ],
    [
      'debug bundle',
      {
        schemaVersion: 1,
        profiles: { qa: { android: { ...profile.android, mode: 'debug', artifact: 'aab' } } },
      },
      'profiles.qa.android',
    ],
    [
      'path separator in channel',
      { ...config(), profiles: { qa: { ...profile, channel: 'a/b' } } },
      'profiles.qa.channel',
    ],
    [
      'oversized channel',
      { ...config(), profiles: { qa: { ...profile, channel: 'é'.repeat(65) } } },
      'profiles.qa.channel',
    ],
  ])('rejects %s with a useful path, without echoing values', (_name, input, location) => {
    const errors = validateConfig(input);
    expect(errors.some(error => error.path.startsWith(location))).toBe(true);
    expect(JSON.stringify(errors)).not.toContain('never-print-this');
  });

  it('does not insert defaults or coerce values', () => {
    const input = config();
    const before = structuredClone(input);
    validateConfig(input);
    expect(input).toEqual(before);
    expect(validateConfig({ ...input, schemaVersion: '1' })).not.toEqual([]);
  });

  it('writes a validated file once, refuses replacement, and preserves invalid files', async () => {
    const dir = await fs.mkdtemp(path.join(os.tmpdir(), 'xprem-config-'));
    directories.push(dir);
    const file = path.join(dir, 'xprem.json');
    await writeNewConfig(file, config());
    expect(await readConfig(file)).toEqual(config());
    await expect(writeNewConfig(file, config())).rejects.toThrow(/exist/i);
    await fs.writeFile(file, '{"token":"never-print-this", broken}');
    await expect(readConfig(file)).rejects.toThrow(/JSON/);
    await expect(readConfig(file)).rejects.not.toThrow('never-print-this');
    await expect(writeNewConfig(file, config())).rejects.toThrow(/exist/i);
    expect(await fs.readFile(file, 'utf8')).toContain('never-print-this');
  });
});

describe('EAS import', () => {
  it('resolves inheritance and Android precedence without changing the input', () => {
    const input = {
      build: {
        base: { channel: 'staging', android: { buildType: 'apk' } },
        qa: { extends: 'base', android: { buildType: 'app-bundle' } },
        dev: { extends: 'base', developmentClient: true },
      },
    };
    const before = structuredClone(input);
    const converted = convertEasConfig(input, 'com.example.app');
    expect(converted.config.profiles.qa.android).toMatchObject({
      artifact: 'aab',
      mode: 'release',
    });
    expect(converted.config.profiles.dev.android).toMatchObject({
      artifact: 'apk',
      mode: 'debug',
      developmentClient: true,
    });
    expect(converted.config.profiles.dev.channel).toBeUndefined();
    expect(converted.config.profiles.qa.channel).toBe('staging');
    expect(validateConfig(converted.config)).toEqual([]);
    expect(input).toEqual(before);
  });

  it('reports omitted options at their declaration without exposing secrets', () => {
    const converted = convertEasConfig(
      {
        cli: { appVersionSource: 'remote' },
        build: {
          base: {
            env: { TOKEN: 'never-print-this' },
            android: { autoIncrement: true, image: 'latest' },
          },
          qa: { extends: 'base' },
        },
      },
      'com.example.app'
    );
    expect(converted.issues.map(issue => issue.path)).toEqual(
      expect.arrayContaining([
        'cli',
        'build.base.env',
        'build.base.android.autoIncrement',
        'build.base.android.image',
      ])
    );
    expect(converted.issues.map(issue => issue.path)).not.toContain('build.qa.env');
    expect(JSON.stringify(converted)).not.toContain('never-print-this');
  });

  it('blocks custom Gradle tasks instead of guessing a release recipe', () => {
    const converted = convertEasConfig(
      { build: { qa: { android: { gradleCommand: ':app:assembleSpecialRelease' } } } },
      'com.example.app'
    );
    expect(converted.issues).toContainEqual(
      expect.objectContaining({ level: 'error', path: 'build.qa.android.gradleCommand' })
    );
  });

  it('reports invalid profile names during conversion', () => {
    const converted = convertEasConfig({ build: { 'prod.eu': {} } }, 'com.example.app');
    expect(converted.issues).toContainEqual(
      expect.objectContaining({ level: 'error', path: 'build.prod.eu' })
    );
  });

  it.each([
    { build: { a: { extends: 'b' }, b: { extends: 'a' } } },
    { build: { a: { android: [] } } },
  ])('rejects malformed input or unresolved inheritance', input => {
    expect(() => convertEasConfig(input, 'com.example.app')).toThrow();
  });
});
