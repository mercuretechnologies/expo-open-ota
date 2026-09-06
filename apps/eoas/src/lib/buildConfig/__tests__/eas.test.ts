import fs from 'fs-extra';
import os from 'os';
import path from 'path';
import { afterEach, describe, expect, it } from 'vitest';

import { parseEasConfig, readEasConfig, resolveEasProfile } from '../eas';

const directories: string[] = [];
afterEach(async () => {
  await Promise.all(directories.splice(0).map(directory => fs.remove(directory)));
});

describe('EAS compatibility', () => {
  it('reads comments and trailing commas using the EAS parser', () => {
    expect(parseEasConfig('{ /* comment */ "build": { "qa": {}, }, }').build.qa).toEqual({});
  });

  it('limits inheritance to five profiles like EAS', () => {
    const build = {
      a: {},
      b: { extends: 'a' },
      c: { extends: 'b' },
      d: { extends: 'c' },
      e: { extends: 'd' },
      f: { extends: 'e' },
    };
    expect(() => resolveEasProfile(build, 'e')).not.toThrow();
    expect(() => resolveEasProfile(build, 'f')).toThrow(/deep/);
  });

  it('does not expose invalid source values in syntax errors', () => {
    expect(() => parseEasConfig('{"secret":"never-print-this", broken')).toThrow('Invalid JSON');
    expect(() => parseEasConfig('{"secret":"never-print-this", broken')).not.toThrow(
      'never-print-this'
    );
  });

  it('rejects prototype-bearing JSON5 objects', () => {
    expect(() => parseEasConfig('{__proto__: {evil: true}, build: {qa: {}}}')).toThrow(
      /Unsafe key/
    );
  });

  it('rejects oversized and non-regular EAS sources', async () => {
    const directory = await fs.mkdtemp(path.join(os.tmpdir(), 'xprem-eas-source-'));
    directories.push(directory);
    const oversized = path.join(directory, 'oversized.json');
    await fs.writeFile(oversized, ' '.repeat(1024 * 1024 + 1));
    await expect(readEasConfig(oversized)).rejects.toThrow(/smaller than 1 MiB/);
    await expect(readEasConfig(directory)).rejects.toThrow(/JSON file/);
  });
});
