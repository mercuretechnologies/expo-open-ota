import spawnAsync from '@expo/spawn-async';
import fs from 'fs-extra';
import os from 'os';
import path from 'path';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { configureAndroidSdk, resolveAndroidTools } from '../tools';

vi.mock('@expo/spawn-async', () => ({ default: vi.fn() }));

describe('local Android tools', () => {
  let project: string;
  let sdk: string;
  beforeEach(async () => {
    vi.mocked(spawnAsync).mockReset();
    project = await fs.mkdtemp(path.join(os.tmpdir(), 'eoas-android-tools-'));
    sdk = path.join(project, 'Android SDK');
    await fs.outputFile(path.join(sdk, 'platforms/android-35/android.jar'), '');
    await fs.outputFile(path.join(sdk, 'build-tools/35.0.0/aapt2'), '');
    vi.mocked(spawnAsync).mockImplementation(
      async command =>
        ({
          stdout: command.endsWith('javac') ? 'javac 17.0.12' : '',
          stderr: command.endsWith('javac')
            ? ''
            : '    java.home = /detected-jdk\n    java.version = 17.0.12\n',
        }) as never
    );
  });
  afterEach(async () => {
    vi.restoreAllMocks();
    await fs.remove(project);
  });

  it('exports the configured SDK and JAVA_HOME without changing process.env', async () => {
    const previous = { ...process.env };
    const env = await resolveAndroidTools(project, {}, { ANDROID_HOME: sdk, JAVA_HOME: '/jdk' });
    expect(env).toMatchObject({ ANDROID_HOME: sdk, ANDROID_SDK_ROOT: sdk, JAVA_HOME: '/jdk' });
    expect(vi.mocked(spawnAsync).mock.calls.map(([command]) => command)).toEqual([
      '/jdk/bin/java',
      '/jdk/bin/javac',
    ]);
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

  it('falls back to ANDROID_SDK_ROOT and pins the JDK discovered through PATH', async () => {
    const env = await resolveAndroidTools(project, {}, { ANDROID_SDK_ROOT: sdk });
    expect(env.ANDROID_HOME).toBe(sdk);
    expect(env.JAVA_HOME).toBe('/detected-jdk');
    expect(vi.mocked(spawnAsync).mock.calls[1][0]).toBe('/detected-jdk/bin/javac');
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
    ).rejects.toThrow(/ANDROID_HOME.*--androidSdk/);
  });

  it('rejects a directory that is not an installed SDK', async () => {
    await fs.remove(path.join(sdk, 'platforms'));
    await expect(resolveAndroidTools(project, { androidSdk: sdk })).rejects.toThrow(
      /SDK Platforms.*Android Studio/
    );
  });

  it('rejects a file used as an SDK directory', async () => {
    const file = path.join(project, 'not-a-directory');
    await fs.writeFile(file, '');
    await expect(resolveAndroidTools(project, { androidSdk: file })).rejects.toThrow(
      /Android SDK.*--androidSdk/
    );
  });

  it('explains how to fix an unavailable Java executable', async () => {
    vi.mocked(spawnAsync).mockRejectedValue(
      Object.assign(new Error('spawn ENOENT'), { code: 'ENOENT' })
    );
    await expect(
      resolveAndroidTools(project, { androidSdk: sdk, javaHome: '/bad-jdk' })
    ).rejects.toThrow(/Java.*bad-jdk.*JAVA_HOME.*--javaHome/);
  });

  it('requires a compiler from the selected JDK, not an unrelated javac from PATH', async () => {
    vi.mocked(spawnAsync).mockReset();
    vi.mocked(spawnAsync).mockResolvedValueOnce({
      stderr: 'java.home = /jre\njava.version = 17.0.12',
      stdout: '',
    } as never);
    vi.mocked(spawnAsync).mockRejectedValueOnce(new Error('spawn ENOENT'));
    await expect(resolveAndroidTools(project, { androidSdk: sdk }, {})).rejects.toThrow(
      /compiler.*JDK.*--javaHome/
    );
    expect(vi.mocked(spawnAsync).mock.calls[1][0]).toBe('/jre/bin/javac');
  });

  it('rejects a mismatched compiler version', async () => {
    vi.mocked(spawnAsync).mockResolvedValueOnce({
      stderr: 'java.home = /jdk\njava.version = 17.0.12',
      stdout: '',
    } as never);
    vi.mocked(spawnAsync).mockResolvedValueOnce({ stderr: '', stdout: 'javac 21.0.1' } as never);
    await expect(resolveAndroidTools(project, { androidSdk: sdk }, {})).rejects.toThrow(
      /Java 17.*javac 21/
    );
  });

  it('accepts legacy Java version syntax without imposing an Expo-to-Java table', async () => {
    vi.mocked(spawnAsync).mockResolvedValueOnce({
      stderr: 'java.home = /jdk/jre\njava.version = 1.8.0_412',
      stdout: '',
    } as never);
    vi.mocked(spawnAsync).mockResolvedValueOnce({ stderr: '', stdout: 'javac 1.8.0_412' } as never);
    expect((await resolveAndroidTools(project, { androidSdk: sdk }, {})).JAVA_HOME).toBe('/jdk');
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
