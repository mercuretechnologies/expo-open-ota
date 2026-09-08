import { PassThrough } from 'stream';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { streamBuildOutput } from '../output';
import { createBuildStage } from '../stage';

const screen = vi.hoisted(() => Object.assign(vi.fn(), { clear: vi.fn(), done: vi.fn() }));
vi.mock('log-update', () => ({ default: { create: () => screen } }));
vi.mock('../../log', () => ({
  default: { log: vi.fn(), succeed: vi.fn(), fail: vi.fn() },
}));

afterEach(() => {
  vi.restoreAllMocks();
  vi.clearAllMocks();
  vi.useRealTimers();
  vi.unstubAllEnvs();
});

describe('grouped build output', () => {
  it('keeps all redacted lines in the log while limiting the live preview, then collapses it', () => {
    vi.useFakeTimers();
    vi.stubEnv('CI', '');
    vi.spyOn(process, 'stdout', 'get').mockReturnValue(
      Object.assign(new PassThrough(), {
        isTTY: true,
        columns: 80,
        rows: 24,
      }) as unknown as typeof process.stdout
    );
    const write = vi.fn();
    const stage = createBuildStage('Building signed AAB', { write }, false);
    const output = new PassThrough();
    const lines = streamBuildOutput(output, ['secret-value'], stage.write);
    for (let i = 0; i < 100; i++) {
      output.write(`Task ${i}: secret-value\n`);
    }
    vi.advanceTimersByTime(100);
    const preview = screen.mock.calls.at(-1)?.[0] as string;
    expect(preview).toContain('Building signed AAB');
    expect(preview).toContain('Task 99: [REDACTED]');
    expect(preview).not.toContain('Task 0:');
    expect(preview.split('\n').length).toBeLessThanOrEqual(6);
    expect(write).toHaveBeenCalledWith('Task 0: [REDACTED]');
    expect(write).toHaveBeenCalledWith('Task 99: [REDACTED]');
    expect(write.mock.calls.flat().join('\n')).not.toContain('secret-value');
    stage.finish(true);
    expect(screen.clear).toHaveBeenCalled();
    expect(screen.done).toHaveBeenCalled();
    expect(vi.getTimerCount()).toBe(0);
    lines.close();
    output.destroy();
  });

  it.each([
    [true, true],
    [true, false],
    [false, false],
  ])('streams every line without cursor updates when verbose=%s and TTY=%s', (verbose, isTTY) => {
    vi.spyOn(process, 'stdout', 'get').mockReturnValue(
      Object.assign(new PassThrough(), { isTTY }) as unknown as typeof process.stdout
    );
    const stdout = vi.spyOn(process.stdout, 'write').mockReturnValue(true);
    const write = vi.fn();
    const stage = createBuildStage('Gradle', { write }, verbose);
    stage.write('first');
    stage.write('last');
    stage.finish(true);
    expect(stdout.mock.calls.map(([line]) => line).join('')).toBe('first\nlast\n');
    expect(screen).not.toHaveBeenCalled();
  });

  it('keeps failure details visible and stops refreshing', () => {
    vi.useFakeTimers();
    vi.stubEnv('CI', '');
    vi.spyOn(process, 'stdout', 'get').mockReturnValue(
      Object.assign(new PassThrough(), {
        isTTY: true,
        columns: 80,
        rows: 24,
      }) as unknown as typeof process.stdout
    );
    const stage = createBuildStage('Gradle', { write: vi.fn() }, false);
    stage.write('Compilation failed: missing dependency');
    stage.finish(false);
    expect(screen.mock.calls.at(-1)?.[0]).toContain('Compilation failed: missing dependency');
    expect(screen.done).toHaveBeenCalled();
    expect(vi.getTimerCount()).toBe(0);
  });
});
