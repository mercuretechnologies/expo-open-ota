import fs from 'fs-extra';
import os from 'os';
import path from 'path';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { confirmAsync, promptAsync, selectAsync } from '../../prompts';
import { prepareBuildConfig } from '../setup';

vi.mock('../../prompts', () => ({
  confirmAsync: vi.fn(),
  promptAsync: vi.fn(),
  selectAsync: vi.fn(),
}));
vi.mock('../../log', () => ({ default: { log: vi.fn(), warn: vi.fn(), succeed: vi.fn() } }));
let directory: string;
const existing = {
  schemaVersion: 1,
  profiles: {
    qa: { android: { applicationId: 'com.example.app', mode: 'release', artifact: 'apk' } },
  },
};

beforeEach(async () => {
  directory = await fs.mkdtemp(path.join(os.tmpdir(), 'xprem-setup-'));
  vi.mocked(selectAsync).mockResolvedValue('import');
  vi.mocked(promptAsync).mockResolvedValue({ applicationId: 'com.example.app' });
  vi.mocked(confirmAsync).mockResolvedValue(true);
});
afterEach(async () => {
  vi.clearAllMocks();
  await fs.remove(directory);
});

describe('build configuration preparation', () => {
  it('validates and preserves an existing file without offering import', async () => {
    await fs.writeJson(path.join(directory, 'xprem.json'), existing);
    await fs.writeJson(path.join(directory, 'eas.json'), { build: { production: {} } });
    expect(await prepareBuildConfig(directory, 'com.example.app')).toEqual({ kind: 'continue' });
    expect(selectAsync).not.toHaveBeenCalled();
    expect(await fs.readJson(path.join(directory, 'xprem.json'))).toEqual(existing);
  });

  it('refuses an invalid existing config without changing it', async () => {
    await fs.writeFile(path.join(directory, 'xprem.json'), '{');
    await expect(prepareBuildConfig(directory)).rejects.toThrow(/JSON/);
    expect(await fs.readFile(path.join(directory, 'xprem.json'), 'utf8')).toBe('{');
  });

  it('offers import but only prepares a reviewed candidate', async () => {
    const eas = { build: { qa: { distribution: 'internal' } } };
    await fs.writeJson(path.join(directory, 'eas.json'), eas);
    expect(await prepareBuildConfig(directory, 'com.example.app')).toEqual({
      kind: 'write',
      config: existing,
    });
    expect(confirmAsync).toHaveBeenCalled();
    expect(await fs.pathExists(path.join(directory, 'xprem.json'))).toBe(false);
    expect(await fs.readJson(path.join(directory, 'eas.json'))).toEqual(eas);
  });

  it('rejects incompatible EAS channels before asking for application IDs', async () => {
    await fs.writeJson(path.join(directory, 'eas.json'), {
      build: { qa: { channel: 'invalid/channel' } },
    });
    await expect(prepareBuildConfig(directory)).rejects.toThrow(/manual changes/);
    expect(promptAsync).not.toHaveBeenCalled();
  });

  it('cancels without writing', async () => {
    await fs.writeJson(path.join(directory, 'eas.json'), { build: { qa: {} } });
    vi.mocked(confirmAsync).mockResolvedValue(false);
    expect(await prepareBuildConfig(directory)).toEqual({ kind: 'cancelled' });
    expect(await fs.pathExists(path.join(directory, 'xprem.json'))).toBe(false);
  });

  it('can cancel build profile configuration', async () => {
    vi.mocked(selectAsync).mockResolvedValue('cancel');
    expect(await prepareBuildConfig(directory)).toEqual({ kind: 'cancelled' });
    expect(promptAsync).not.toHaveBeenCalled();
  });

  it('can prepare a fresh profile without eas.json', async () => {
    vi.mocked(selectAsync).mockResolvedValueOnce('new').mockResolvedValueOnce('qa');
    vi.mocked(promptAsync)
      .mockResolvedValueOnce({ name: 'qa' })
      .mockResolvedValueOnce({ applicationId: 'com.example.app' })
      .mockResolvedValueOnce({ channel: '' });
    expect(await prepareBuildConfig(directory)).toEqual({ kind: 'write', config: existing });
  });
});
