import { PassThrough } from 'stream';
import { describe, expect, it, vi } from 'vitest';

import { streamBuildOutput } from '../output';

describe('live build output', () => {
  it('prints complete lines before the tool exits and masks secrets across chunks', async () => {
    const stream = new PassThrough();
    const write = vi.fn();
    const lines = streamBuildOutput(stream, ['signing-secret'], write);
    stream.write('> Task :app:compileReleaseKotlin\npassword=sign');
    expect(write).toHaveBeenCalledWith('> Task :app:compileReleaseKotlin');
    expect(write).toHaveBeenCalledTimes(1);
    stream.write('ing-secret\n');
    expect(write).toHaveBeenLastCalledWith('password=[REDACTED]');
    const closed = new Promise<void>(resolve => lines.once('close', resolve));
    stream.end('BUILD SUCCESSFUL');
    await closed;
    expect(write).toHaveBeenLastCalledWith('BUILD SUCCESSFUL');
  });

  it('masks multiline secrets without holding back unrelated progress', () => {
    const stream = new PassThrough();
    const write = vi.fn();
    const lines = streamBuildOutput(stream, ['private-line-one\nprivate-line-two'], write);
    stream.write('private-line-one\nprivate-line-two\n> Task :app:bundleRelease\n');
    expect(write.mock.calls).toEqual([
      ['[REDACTED]'],
      ['[REDACTED]'],
      ['> Task :app:bundleRelease'],
    ]);
    lines.close();
    stream.destroy();
  });
});
