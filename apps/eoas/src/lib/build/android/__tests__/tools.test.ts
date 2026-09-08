import fs from 'fs-extra';
import os from 'os';
import path from 'path';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { configureAndroidSdk, resolveAndroidTools } from '../tools';

describe('local Android tools', () => {
  let project: string;
  let sdk: string;
  beforeEach(async () => {
    project = await fs.mkdtemp(path.join(os.tmpdir(), 'eoas-android-tools-'));
    sdk = path.join(project, 'Android SDK');
    await fs.ensureDir(sdk);
  });
  afterEach(async () => {
    vi.restoreAllMocks();
    await fs.remove(project);
  });

  it('exports the configured SDK and JAVA_HOME without changing process.env', async () => {
    const previous = { ...process.env };
    const env = await resolveAndroidTools(project, {}, { ANDROID_HOME: sdk, JAVA_HOME: '/jdk' });
    expect(env).toEqual({ ANDROID_HOME: sdk, ANDROID_SDK_ROOT: sdk, JAVA_HOME: '/jdk' });
    expect(process.env).toEqual(previous);
  });

  it('lets flags override environment paths', async () => {
    const env = await resolveAndroidTools(
      project,
      { androidSdk: sdk, javaHome: '/flag-jdk' },
      { ANDROID_HOME: '/wrong', JAVA_HOME: '/wrong' }
    );
    expect(env.ANDROID_HOME).toBe(sdk);
    expect(env.JAVA_HOME).toBe('/flag-jdk');
  });

  it('falls back to ANDROID_SDK_ROOT and leaves JAVA_HOME to gradlew when unset', async () => {
    const env = await resolveAndroidTools(project, {}, { ANDROID_SDK_ROOT: sdk });
    expect(env.ANDROID_HOME).toBe(sdk);
    expect(env.JAVA_HOME).toBeUndefined();
  });

  it('finds the standard Linux SDK path when no path is configured', async () => {
    vi.spyOn(os, 'platform').mockReturnValue('linux');
    vi.spyOn(os, 'homedir').mockReturnValue(project);
    const linuxSdk = path.join(project, 'Android/Sdk');
    await fs.move(sdk, linuxSdk);
    expect((await resolveAndroidTools(project, {}, {})).ANDROID_HOME).toBe(linuxSdk);
  });

  it('rejects an explicitly configured SDK path instead of trying another installation', async () => {
    await expect(
      resolveAndroidTools(project, { androidSdk: '/missing-sdk' }, { ANDROID_HOME: sdk })
    ).rejects.toThrow(/ANDROID_HOME.*--android-sdk/);
  });

  it('updates only sdk.dir in the build copy using the selected SDK', async () => {
    await fs.outputFile(
      path.join(project, 'android/local.properties'),
      '# retained\nsdk.dir=/old\ncustom=value\n'
    );
    await configureAndroidSdk(project, sdk);
    const contents = await fs.readFile(path.join(project, 'android/local.properties'), 'utf8');
    expect(contents).toContain('# retained');
    expect(contents).toContain(`sdk.dir=${sdk}`);
    expect(contents).toContain('custom=value');
    expect(contents).not.toContain('/old');
  });
});
