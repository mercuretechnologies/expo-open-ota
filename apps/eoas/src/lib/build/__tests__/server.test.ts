import fetch from 'node-fetch';
import { afterEach, expect, it, vi } from 'vitest';

import { allocateBuildNumber } from '../server';

vi.mock('node-fetch', () => ({ default: vi.fn() }));

afterEach(() => {
  vi.unstubAllEnvs();
  vi.clearAllMocks();
});

// A lost response may already have reserved the number on the server.
it('never retries an uncertain build number reservation', async () => {
  vi.stubEnv('EOO_TOKEN', 'test-token');
  vi.mocked(fetch).mockRejectedValue(new Error('socket hang up'));
  await expect(allocateBuildNumber('https://example.com/app/build/id')).rejects.toThrow(
    'no automatic retry'
  );
  expect(fetch).toHaveBeenCalledTimes(1);
});
