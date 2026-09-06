import fs from 'fs-extra';
import os from 'os';
import path from 'path';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { createOrModifyExpoConfigAsync } from '../../lib/expoConfig';
import { promptAsync } from '../../lib/prompts';
import { ensurePrivateKeyIgnored } from '../../lib/utils';
import UpdateConfigure from '../update/configure';

vi.mock('../../lib/expoConfig', () => ({
  getPrivateExpoConfigAsync: async () => ({
    android: { package: 'com.example.app' },
    extra: { eas: { projectId: 'must-not-be-used-as-xprem-app-id' } },
  }),
  getExpoConfigUpdateUrl: () => undefined,
  createOrModifyExpoConfigAsync: vi.fn(),
}));
vi.mock('../../lib/package', () => ({ isExpoInstalled: () => true }));
vi.mock('../../lib/prompts', () => ({
  confirmAsync: vi.fn(async () => true),
  promptAsync: vi.fn(async () => ({
    appId: 'app-id',
    updateUrl: 'https://ota.example.com',
    codeSigningCertificatePath: 'cert.pem',
  })),
}));
vi.mock('../../lib/utils', () => ({
  ensurePrivateKeyIgnored: vi.fn(),
  isValidUpdateUrl: () => true,
}));
const eoasRoot = path.resolve(__dirname, '../../..');
let directory: string;

beforeEach(async () => {
  directory = await fs.mkdtemp(path.join(os.tmpdir(), 'eoas-init-'));
  vi.spyOn(process, 'cwd').mockReturnValue(directory);
  vi.mocked(createOrModifyExpoConfigAsync).mockResolvedValue(undefined);
});
afterEach(async () => {
  vi.restoreAllMocks();
  vi.clearAllMocks();
  await fs.remove(directory);
});

describe('update:configure', () => {
  it('configures updates without using the EAS project ID as the xprem app ID', async () => {
    await UpdateConfigure.run([], eoasRoot);
    expect(vi.mocked(promptAsync).mock.calls[0][0]).not.toHaveProperty('initial');
    expect(ensurePrivateKeyIgnored).toHaveBeenCalled();
    expect(await fs.pathExists(path.join(directory, 'xprem.json'))).toBe(false);
  });

  it('keeps the private key ignored when Expo configuration fails', async () => {
    vi.mocked(createOrModifyExpoConfigAsync).mockRejectedValue(new Error('Expo write failed'));
    await expect(UpdateConfigure.run([], eoasRoot)).rejects.toThrow('Expo write failed');
    expect(ensurePrivateKeyIgnored).toHaveBeenCalled();
  });
});
