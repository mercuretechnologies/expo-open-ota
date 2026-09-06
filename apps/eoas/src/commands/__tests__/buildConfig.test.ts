import fs from 'fs-extra';
import os from 'os';
import path from 'path';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { confirmAsync, promptAsync, selectAsync } from '../../lib/prompts';
import BuildConfig from '../build/config';
import BuildConfigure from '../build/configure';

vi.mock('../../lib/prompts', () => ({
  confirmAsync: vi.fn(),
  promptAsync: vi.fn(),
  selectAsync: vi.fn(),
}));
vi.mock('../../lib/package', () => ({ isExpoInstalled: () => true }));
vi.mock('../../lib/expoConfig', () => ({
  getPrivateExpoConfigAsync: async () => ({ android: { package: 'com.example.app' } }),
}));
const eoasRoot = path.resolve(__dirname, '../../..');
let directory: string;

beforeEach(async () => {
  directory = await fs.mkdtemp(path.join(os.tmpdir(), 'eoas-config-command-'));
  vi.spyOn(process, 'cwd').mockReturnValue(directory);
  vi.mocked(confirmAsync).mockResolvedValue(true);
  vi.mocked(promptAsync).mockResolvedValue({ applicationId: 'com.example.qa' });
});
afterEach(async () => {
  vi.restoreAllMocks();
  vi.clearAllMocks();
  await fs.remove(directory);
});

describe('build commands', () => {
  it('imports eas.json through the configure wizard and displays the selected profile', async () => {
    const source = { build: { qa: { distribution: 'internal' } } };
    await fs.writeJson(path.join(directory, 'eas.json'), source);
    vi.mocked(selectAsync).mockResolvedValue('import');
    await BuildConfigure.run([], eoasRoot);
    const stdout = vi.spyOn(process.stdout, 'write').mockImplementation(() => true);
    await BuildConfig.run(['--profile=qa', '--json'], eoasRoot);
    expect(JSON.parse(stdout.mock.calls.map(call => call[0]).join('')).android.applicationId).toBe(
      'com.example.qa'
    );
    expect(await fs.readJson(path.join(directory, 'eas.json'))).toEqual(source);
  });

  it('does not write when an imported configuration is declined', async () => {
    await fs.writeJson(path.join(directory, 'eas.json'), { build: { qa: {} } });
    vi.mocked(selectAsync).mockResolvedValue('import');
    vi.mocked(confirmAsync).mockResolvedValue(false);
    await BuildConfigure.run([], eoasRoot);
    expect(await fs.pathExists(path.join(directory, 'xprem.json'))).toBe(false);
  });

  it('fails validation of missing and invalid files', async () => {
    await expect(BuildConfig.run([], eoasRoot)).rejects.toThrow(/EEXIT/);
    await fs.writeJson(path.join(directory, 'xprem.json'), { schemaVersion: 999, profiles: {} });
    await expect(BuildConfig.run([], eoasRoot)).rejects.toThrow(/EEXIT/);
  });

  it('treats inherited object names as missing profiles', async () => {
    await fs.writeJson(path.join(directory, 'xprem.json'), {
      schemaVersion: 1,
      profiles: {
        qa: { android: { applicationId: 'com.example.qa', mode: 'release', artifact: 'apk' } },
      },
    });
    for (const name of ['constructor', '__proto__', 'missing']) {
      await expect(BuildConfig.run(['--profile', name, '--json'], eoasRoot)).rejects.toThrow(
        /EEXIT/
      );
    }
  });
});
