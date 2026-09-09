import { BuildPhase, BuildPhaseResult, LogMarker } from '@expo/eas-build-job';
import { randomUUID } from 'crypto';
import { afterEach, expect, it, vi } from 'vitest';

import { LogLine } from '../log';
import { request } from '../server';
import { createLogUploader } from '../upload';

vi.mock('../server', () => ({ request: vi.fn() }));

const event = (msg: string): LogLine => ({
  logId: randomUUID(),
  time: new Date().toISOString(),
  level: 30,
  msg,
  phase: BuildPhase.RUN_GRADLEW,
  buildStepId: 'gradle-1',
  buildStepDisplayName: 'Run Gradle',
});

afterEach(() => {
  vi.resetAllMocks();
  vi.useRealTimers();
});

it('streams ordered UTF-8 batches, redacts before splitting, and flushes on close', async () => {
  const received: { offset: number; content: string; format: string }[] = [];
  vi.mocked(request).mockImplementation(async (_url, options) => {
    const batch = options!.body as (typeof received)[number];
    received.push(batch);
    return { nextOffset: batch.offset + Buffer.byteLength(batch.content) };
  });
  const warn = vi.fn();
  const stream = createLogUploader('/build/id', 'build-id', ['secret-value'], warn);
  const line = 'é🙂'.repeat(10000) + 'secret-value';
  stream.write(event(line));
  stream.write(event('last line'));
  await stream.close();
  const records: LogLine[] = received.flatMap(batch =>
    batch.content
      .trimEnd()
      .split('\n')
      .map(line => JSON.parse(line))
  );
  expect(records.map(record => record.msg).join('')).toBe(
    line.replace('secret-value', '[REDACTED]') + 'last line'
  );
  expect(new Set(records.map(record => record.logId)).size).toBe(records.length);
  expect(
    records.every(
      record => record.phase === BuildPhase.RUN_GRADLEW && record.buildStepId === 'gradle-1'
    )
  ).toBe(true);
  let offset = 0;
  for (const batch of received) {
    expect(batch.offset).toBe(offset);
    expect(batch.format).toBe('ndjson');
    expect(batch.content.endsWith('\n')).toBe(true);
    expect(Buffer.byteLength(batch.content)).toBeLessThanOrEqual(32768);
    offset += Buffer.byteLength(batch.content);
  }
  expect(request).toHaveBeenCalledWith(
    '/build/id/artifacts/build-id/logs',
    expect.objectContaining({ method: 'POST', retry: false })
  );
  expect(warn).not.toHaveBeenCalled();
});

it('retries an uncertain batch at the same offset while the build continues', async () => {
  vi.useFakeTimers();
  vi.mocked(request)
    .mockRejectedValueOnce(new Error('network timeout'))
    .mockImplementation(async (_url, options) => {
      const batch = options!.body as { offset: number; content: string };
      return { nextOffset: batch.offset + Buffer.byteLength(batch.content) };
    });
  const stream = createLogUploader('/build/id', 'build-id', [], vi.fn());
  stream.write(event('first'));
  await vi.advanceTimersByTimeAsync(2000);
  stream.write(event('second'));
  await vi.advanceTimersByTimeAsync(2000);
  await stream.close();
  expect(vi.mocked(request).mock.calls[0][1]?.body).toEqual(
    vi.mocked(request).mock.calls[1][1]?.body
  );
});

it('stops after repeated failures without rejecting close or leaking errors into warnings', async () => {
  vi.useFakeTimers();
  vi.mocked(request).mockRejectedValue(new Error('a server error containing secrets'));
  const warn = vi.fn();
  const stream = createLogUploader('/build/id', 'build-id', [], warn);
  stream.write(event('hello'));
  await vi.advanceTimersByTimeAsync(6000);
  await expect(stream.close()).resolves.toBeUndefined();
  expect(request).toHaveBeenCalledTimes(3);
  expect(warn).toHaveBeenCalledTimes(1);
  expect(warn.mock.calls[0][0]).not.toContain('secrets');
  expect(vi.getTimerCount()).toBe(0);
});

it('bounds queued output when the server cannot keep up', async () => {
  const warn = vi.fn();
  const stream = createLogUploader('/build/id', 'build-id', [], warn);
  for (let i = 0; i < 40; i++) {
    stream.write(event('x'.repeat(32768)));
  }
  await expect(stream.close()).resolves.toBeUndefined();
  expect(warn).toHaveBeenCalledTimes(1);
  expect(request).not.toHaveBeenCalled();
});

it('caps remote log storage while leaving the local logger free to continue', async () => {
  vi.useFakeTimers();
  let bytes = 0;
  vi.mocked(request).mockImplementation(async (_url, options) => {
    const batch = options!.body as { offset: number; content: string };
    bytes += Buffer.byteLength(batch.content);
    return { nextOffset: batch.offset + Buffer.byteLength(batch.content) };
  });
  const warn = vi.fn();
  const stream = createLogUploader('/build/id', 'build-id', [], warn);
  for (let i = 0; i < 180; i++) {
    stream.write(event('x'.repeat(65535)));
    await vi.advanceTimersByTimeAsync(2000);
  }
  await stream.close();
  expect(bytes).toBeLessThanOrEqual(10 * 1024 * 1024);
  expect(bytes).toBeGreaterThan(10 * 1024 * 1024 - 32768);
  expect(warn).toHaveBeenCalledTimes(1);
});

it('preserves phase markers and strips terminal escapes before sending JSON', async () => {
  const records: LogLine[] = [];
  vi.mocked(request).mockImplementation(async (_url, options) => {
    const batch = options!.body as { offset: number; content: string };
    records.push(
      ...batch.content
        .trimEnd()
        .split('\n')
        .map(line => JSON.parse(line))
    );
    return { nextOffset: batch.offset + Buffer.byteLength(batch.content) };
  });
  const stream = createLogUploader('/build/id', 'build-id', [], vi.fn());
  stream.write({ ...event('Start phase'), marker: LogMarker.START_PHASE });
  stream.write(event('\x1b[33mApplying plugin\x1b[0m'));
  stream.write({
    ...event('End phase'),
    marker: LogMarker.END_PHASE,
    result: BuildPhaseResult.SUCCESS,
    durationMs: 3500,
  });
  await stream.close();
  expect(records[0].marker).toBe('START_PHASE');
  expect(records[1].msg).toBe('Applying plugin');
  expect(records[2]).toMatchObject({ marker: 'END_PHASE', result: 'success', durationMs: 3500 });
});
