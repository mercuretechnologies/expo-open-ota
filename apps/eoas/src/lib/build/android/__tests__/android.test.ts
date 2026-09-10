import spawnAsync, { SpawnOptions } from '@expo/spawn-async';
import fs from 'fs-extra';
import os from 'os';
import path from 'path';
import { PassThrough } from 'stream';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import {
  allocateBuildNumber,
  fetchCredentials,
  fetchEnvironment,
  resolveIdentifier,
} from '../../server';
import { buildAndroid } from '../index';
import { resolveAndroidTools } from '../tools';

vi.mock('@expo/spawn-async', () => ({ default: vi.fn() }));
vi.mock('../../server', async importOriginal => ({
  ...(await importOriginal<typeof import('../../server')>()),
  resolveIdentifier: vi.fn(),
  fetchEnvironment: vi.fn(),
  fetchCredentials: vi.fn(),
  allocateBuildNumber: vi.fn(),
}));
vi.mock('../tools', async importOriginal => ({
  ...(await importOriginal<typeof import('../tools')>()),
  resolveAndroidTools: vi.fn(),
}));
vi.mock('../../../log', () => ({
  default: { log: vi.fn(), warn: vi.fn(), succeed: vi.fn(), fail: vi.fn() },
  link: (url: string) => url,
}));

describe('Android orchestration', () => {
  let project: string;
  let sdk: string;
  const events: string[] = [];
  let temporaryProject: string | undefined;
  let syncArgs: string[] | undefined;
  let failExport = false;
  beforeEach(async () => {
    vi.clearAllMocks();
    events.length = 0;
    failExport = false;
    temporaryProject = undefined;
    syncArgs = undefined;
    project = await fs.mkdtemp(path.join(os.tmpdir(), 'eoas-test-project-'));
    sdk = path.join(project, 'sdk');
    vi.mocked(resolveAndroidTools).mockResolvedValue({
      JAVA_HOME: '/checked/jdk',
      ANDROID_HOME: sdk,
      ANDROID_SDK_ROOT: sdk,
    });
    await fs.writeJson(path.join(project, 'xprem.json'), {
      schemaVersion: 1,
      profiles: {
        production: {
          environment: 'staging',
          android: { applicationId: 'com.example.app', mode: 'release', artifact: 'aab' },
        },
      },
    });
    await fs.writeJson(path.join(project, 'package.json'), {
      name: 'fixture',
      dependencies: { expo: '*' },
    });
    await fs.writeJson(path.join(project, 'app.json'), {
      expo: { name: 'fixture', slug: 'fixture' },
    });
    await fs.writeFile(
      path.join(project, 'override.env'),
      'OVERRIDE=file-secret-value\nRELEASE_CHANNEL=\n'
    );
    vi.mocked(resolveIdentifier).mockImplementation(
      async root => `${root}/aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa`
    );
    vi.mocked(fetchEnvironment).mockImplementation(async (_endpoint, selection) => {
      events.push(`environment ${new URLSearchParams(selection)}`);
      return {
        REMOTE: 'remote-secret',
        OVERRIDE: 'server-secret',
        EXPO_PUBLIC_NAV: 'react-navigation',
      };
    });
    vi.mocked(fetchCredentials).mockResolvedValue({
      keystore: 'YWJj',
      keystorePassword: 'store-secret',
      keyAlias: 'upload',
      keyPassword: 'key-secret',
    } as never);
    vi.mocked(allocateBuildNumber).mockImplementation(async () => {
      events.push('allocate');
      return '42';
    });
    vi.mocked(spawnAsync).mockImplementation((async (
      command: string,
      args?: readonly string[],
      options?: SpawnOptions
    ) => {
      const cwd = options?.cwd as string;
      if (command === 'git') {
        throw new Error('not git');
      }
      if (args?.includes('config')) {
        return {
          stdout: JSON.stringify({
            name: 'fixture',
            slug: 'fixture',
            updates: {
              url: 'https://example.com/manifest',
              requestHeaders: { 'expo-app-id': 'app' },
            },
          }),
        } as never;
      }
      if (args?.includes('export')) {
        events.push('export');
        temporaryProject = cwd;
        expect(options?.env?.OVERRIDE).toBe('file-secret-value');
        expect(options?.env?.RELEASE_CHANNEL).toBe('');
        expect(await fs.pathExists(path.join(cwd, 'build-artifacts'))).toBe(false);
        expect(await fs.readFile(path.join(cwd, '.eoas-metro-check.json'), 'utf8')).not.toContain(
          'remote-secret'
        );
        if (failExport) {
          throw Object.assign(new Error('export failed'), {
            status: 1,
            stderr:
              'expo-router is incompatible with react-navigation. store-secret file-secret-value',
          });
        }
      }
      if (args?.includes('configuration:syncnative')) {
        events.push('sync-updates');
        syncArgs = [...(args ?? [])];
      }
      if (args?.includes('prebuild')) {
        events.push('prebuild');
        const app = await fs.readJson(path.join(cwd, 'app.json'));
        expect(app.expo.android).toEqual({ package: 'com.example.app', versionCode: 42 });
        await fs.outputFile(path.join(cwd, 'android/app/build.gradle'), 'android {}');
        await fs.outputFile(path.join(cwd, 'android/gradlew'), '#!/bin/sh\r\nexit 0\r\n');
      }
      if (command.endsWith('gradlew')) {
        events.push('gradle');
        expect(args).toContain(':app:bundleRelease');
        expect(options?.env?.JAVA_HOME).toBe('/checked/jdk');
        expect(await fs.readFile(path.join(cwd, 'gradlew'), 'utf8')).not.toContain('\r');
        expect((await fs.stat(path.join(cwd, 'gradlew'))).mode & 0o111).toBe(0o111);
        expect(await fs.readFile(path.join(cwd, 'local.properties'), 'utf8')).toContain(
          `sdk.dir=${sdk}`
        );
        const signing = await fs.readJson(options?.env?.EOAS_SIGNING_FILE as string);
        expect(signing.keyPassword).toBe('key-secret');
        await fs.outputFile(
          path.join(cwd, 'app/build/outputs/bundle/release/app-release.aab'),
          'artifact'
        );
      }
      return { stdout: '', stderr: '' } as never;
    }) as unknown as typeof spawnAsync);
  });
  afterEach(async () => {
    vi.restoreAllMocks();
    await fs.remove(project);
  });
  it('explains how to configure a missing xprem.json before contacting the server', async () => {
    await fs.remove(path.join(project, 'xprem.json'));
    await expect(buildAndroid(project, { profile: 'production' })).rejects.toThrow(
      'xprem.json was not found. Run "eoas build:configure" to create it.'
    );
    expect(resolveIdentifier).not.toHaveBeenCalled();
    expect(spawnAsync).not.toHaveBeenCalled();
    expect(resolveAndroidTools).not.toHaveBeenCalled();
  });
  it('stops before any server request when local tools are missing', async () => {
    vi.mocked(resolveAndroidTools).mockRejectedValue(new Error('Android SDK not found at /x.'));
    await expect(buildAndroid(project, { profile: 'production' })).rejects.toThrow(
      'Android SDK not found'
    );
    expect(resolveIdentifier).not.toHaveBeenCalled();
    expect(spawnAsync).not.toHaveBeenCalled();
  });
  it('resolves Expo config with the selected runner and environments without leaking them into the CLI', async () => {
    const previousEnv = process.env;
    await buildAndroid(project, {
      profile: 'production',
      envFile: 'override.env',
      packageRunner: 'npm exec --',
    });
    const calls = vi.mocked(spawnAsync).mock.calls.filter(([, args]) => args?.includes('config'));
    expect(calls).toHaveLength(2);
    for (const [command, args, options] of calls) {
      expect(command).toBe('npm');
      expect(args).toEqual(['exec', '--', 'expo', 'config', '--json']);
      expect(options?.env).toMatchObject({
        NODE_ENV: 'production',
        CI: '1',
        EXPO_NO_DOTENV: '1',
        OVERRIDE: 'file-secret-value',
      });
    }
    expect(calls[0][2]?.env?.REMOTE).toBeUndefined();
    expect(calls[1][2]?.env?.REMOTE).toBe('remote-secret');
    expect(process.env).toBe(previousEnv);
  });
  it('merges overrides, validates before one allocation, configures before prebuild and cleans secrets', async () => {
    const output = await buildAndroid(project, {
      profile: 'production',
      channel: 'production',
      envFile: 'override.env',
      serverUrl: 'https://example.com',
      appId: 'app',
    });
    expect(events[0]).toBe('environment channel=production');
    expect(events.slice(1)).toEqual(['export', 'allocate', 'prebuild', 'gradle']);
    expect(await fs.readFile(output, 'utf8')).toBe('artifact');
    expect(temporaryProject).toBeDefined();
    expect(await fs.pathExists(temporaryProject!)).toBe(false);
    expect(await fs.pathExists(path.join(project, 'android'))).toBe(false);
    expect(await fs.pathExists(path.join(project, '.eoas-metro-check.json'))).toBe(false);
    const logs = await fs.readdir(path.join(project, 'build-artifacts/logs'));
    expect(logs).toHaveLength(1);
    const log = await fs.readFile(path.join(project, 'build-artifacts/logs', logs[0]), 'utf8');
    expect(log).toContain('Building signed AAB');
    expect(log).toContain('versionCode 42');
  });
  it('syncs the expo-updates configuration into a maintained Android project instead of prebuilding', async () => {
    await fs.outputFile(path.join(project, 'android/app/build.gradle'), 'android {}');
    await fs.outputFile(
      path.join(project, 'android/app/src/main/AndroidManifest.xml'),
      '<manifest />'
    );
    await fs.outputFile(path.join(project, 'android/gradlew'), '#!/bin/sh\nexit 0\n');
    await fs.outputFile(path.join(project, 'node_modules/expo-updates/bin/cli.js'), '');
    await buildAndroid(project, {
      profile: 'production',
      envFile: 'override.env',
      serverUrl: 'https://example.com',
      appId: 'app',
    });
    expect(events).not.toContain('prebuild');
    expect(events.indexOf('sync-updates')).toBeGreaterThan(events.indexOf('allocate'));
    expect(events.indexOf('sync-updates')).toBeLessThan(events.indexOf('gradle'));
    expect(syncArgs?.[0]).toMatch(/node_modules\/expo-updates\/bin\/cli\.js$/);
    expect(syncArgs).toEqual(
      expect.arrayContaining([
        'configuration:syncnative',
        '--platform',
        'android',
        '--workflow',
        'generic',
      ])
    );
  });
  it('refuses a maintained Android project without expo-updates instead of downloading it', async () => {
    await fs.outputFile(path.join(project, 'android/app/build.gradle'), 'android {}');
    await expect(
      buildAndroid(project, {
        profile: 'production',
        envFile: 'override.env',
        serverUrl: 'https://example.com',
        appId: 'app',
      })
    ).rejects.toThrow('`expo-updates` package was not found');
    expect(events).not.toContain('sync-updates');
  });
  it('does not fetch an environment when the profile selects neither channel nor environment', async () => {
    const file = path.join(project, 'xprem.json');
    const config = await fs.readJson(file);
    delete config.profiles.production.environment;
    await fs.writeJson(file, config);
    await buildAndroid(project, {
      profile: 'production',
      envFile: 'override.env',
      serverUrl: 'https://example.com',
      appId: 'app',
    });
    expect(fetchEnvironment).not.toHaveBeenCalled();
    expect(events).toEqual(['export', 'allocate', 'prebuild', 'gradle']);
  });
  it('keeps repeated builds and pairs each artifact with its full log', async () => {
    const options = {
      profile: 'production',
      envFile: 'override.env',
      serverUrl: 'https://example.com',
      appId: 'app',
    };
    const legacy = path.join(project, 'build-artifacts/production.aab');
    await fs.outputFile(legacy, 'previous artifact');
    const first = await buildAndroid(project, options);
    await fs.writeFile(first, 'first artifact');
    const second = await buildAndroid(project, options);
    expect(second).not.toBe(first);
    expect(await fs.readFile(first, 'utf8')).toBe('first artifact');
    expect(await fs.readFile(second, 'utf8')).toBe('artifact');
    expect(await fs.readFile(legacy, 'utf8')).toBe('previous artifact');
    for (const artifact of [first, second]) {
      expect(path.dirname(artifact)).toBe(path.join(project, 'build-artifacts'));
      expect(path.basename(artifact)).toMatch(/^production-.+\.aab$/);
      const logPath = path.join(
        project,
        'build-artifacts/logs',
        `${path.basename(artifact, '.aab')}.log`
      );
      expect(await fs.readFile(logPath, 'utf8')).toContain(`Built ${artifact}`);
    }
  });
  it.each([false, true])('respects an explicit output path (already exists: %s)', async exists => {
    const output = path.join(project, 'custom.aab');
    if (exists) {
      await fs.writeFile(output, 'keep me');
    }
    const build = buildAndroid(project, {
      profile: 'production',
      envFile: 'override.env',
      serverUrl: 'https://example.com',
      appId: 'app',
      output: 'custom.aab',
    });
    if (exists) {
      await expect(build).rejects.toThrow('Output already exists');
      expect(await fs.readFile(output, 'utf8')).toBe('keep me');
      expect(events).not.toContain('allocate');
      expect(events).not.toContain('export');
    } else {
      await expect(build).resolves.toBe(output);
      expect(await fs.readFile(output, 'utf8')).toBe('artifact');
    }
  });
  it('saves every stdout/stderr line and forwards redacted output in verbose mode', async () => {
    const simulate = vi.mocked(spawnAsync).getMockImplementation()!;
    vi.mocked(spawnAsync).mockImplementation((command, args, options) => {
      if (!command.endsWith('gradlew')) {
        return simulate(command, args, options);
      }
      const stdout = new PassThrough();
      const stderr = new PassThrough();
      const running = (async () => {
        // Let the runner attach both streams before the tool produces output.
        await new Promise(resolve => setImmediate(resolve));
        for (let i = 0; i < 100; i++) {
          stdout.write(`Compilation ${i}\n`);
        }
        stderr.end('Warning: store-secret\n');
        stdout.end('Last line without newline');
        return await simulate(command, args, options);
      })();
      return Object.assign(running, { child: { stdout, stderr } }) as unknown as ReturnType<
        typeof spawnAsync
      >;
    });
    const terminal = vi.spyOn(process.stdout, 'write').mockReturnValue(true);
    await buildAndroid(project, {
      profile: 'production',
      serverUrl: 'https://example.com',
      appId: 'app',
      verbose: true,
      envFile: 'override.env',
    });
    const logs = await fs.readdir(path.join(project, 'build-artifacts/logs'));
    const log = await fs.readFile(path.join(project, 'build-artifacts/logs', logs[0]), 'utf8');
    const displayed = terminal.mock.calls.map(([line]) => String(line)).join('');
    for (const text of [log, displayed]) {
      expect(text).toContain('Compilation 0\n');
      expect(text).toContain('Compilation 99\n');
      expect(text).toContain('Warning: [REDACTED]');
      expect(text).toContain('Last line without newline');
      expect(text).not.toContain('store-secret');
    }
  });
  it('shows the tool failure without secrets and does not reserve a number', async () => {
    failExport = true;
    await expect(
      buildAndroid(project, {
        profile: 'production',
        envFile: 'override.env',
        serverUrl: 'https://example.com',
        appId: 'app',
      })
    ).rejects.toThrow('expo-router is incompatible with react-navigation. [REDACTED] [REDACTED]');
    expect(events).not.toContain('allocate');
    expect(temporaryProject).toBeDefined();
    expect(await fs.pathExists(temporaryProject!)).toBe(false);
    const logs = await fs.readdir(path.join(project, 'build-artifacts/logs'));
    const log = await fs.readFile(path.join(project, 'build-artifacts/logs', logs[0]), 'utf8');
    expect(log).toContain('expo-router is incompatible with react-navigation.');
    expect(log).not.toContain('store-secret');
    expect(log).not.toContain('file-secret-value');
  });
});
