import fs from 'fs-extra';
import os from 'os';
import path from 'path';
import { expect, it } from 'vitest';

import { createBuildLog } from '../log';

it('keeps complete ordered logs in separate files and flushes before closing', async () => {
  const project = await fs.mkdtemp(path.join(os.tmpdir(), 'eoas-log-test-'));
  try {
    const first = await createBuildLog(project, 'test');
    const second = await createBuildLog(project, 'test');
    expect(first.path).not.toBe(second.path);
    const lines = Array.from({ length: 3000 }, (_, index) => `Task ${index}`);
    for (const line of lines) {
      first.write(line);
    }
    second.write('another build');
    await Promise.all([first.close(), second.close()]);
    expect(await fs.readFile(first.path, 'utf8')).toBe(`${lines.join('\n')}\n`);
    expect(await fs.readFile(second.path, 'utf8')).toBe('another build\n');
    expect((await fs.stat(first.path)).mode & 0o777).toBe(0o600);
  } finally {
    await fs.remove(project);
  }
});
