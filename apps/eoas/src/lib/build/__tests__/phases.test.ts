import fs from 'fs-extra';
import os from 'os';
import path from 'path';
import { afterEach, expect, it, vi } from 'vitest';

import { BuildLogEvent, createBuildLog } from '../log';
import { BuildPhase, BuildPhaseResult, LogMarker } from '../phases';

vi.mock('../../log', () => ({
  default: { log: vi.fn(), warn: vi.fn(), fail: vi.fn(), succeed: vi.fn() },
}));
afterEach(() => vi.useRealTimers());

it('uses EAS markers, phase IDs, results and durations, including buffered preparation logs', async () => {
  const directory = await fs.mkdtemp(path.join(os.tmpdir(), 'eoas-phases-'));
  const log = await createBuildLog(directory, 'test');
  try {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-09-09T10:00:00Z'));
    await log.runBuildPhase(BuildPhase.PREPARE_PROJECT, async phase => {
      phase.info('Preparing project');
      vi.advanceTimersByTime(1234);
    });
    const events: BuildLogEvent[] = [];
    log.streamTo({
      write: event => {
        events.push(event);
      },
      close: async () => {},
    });
    await log.runBuildPhase(BuildPhase.PREBUILD, async phase => {
      phase.markSkipped();
    });
    await log.runBuildPhase(BuildPhase.RUN_EXPO_DOCTOR, async phase => {
      phase.warn('warning');
    });
    expect(events[0]).toMatchObject({ phase: 'PREPARE_PROJECT', marker: 'START_PHASE' });
    expect(events[1].buildStepId).toBe(events[0].buildStepId);
    expect(events[2]).toMatchObject({ marker: 'END_PHASE', result: 'success', durationMs: 1234 });
    expect(
      events.filter(event => event.marker === LogMarker.END_PHASE).map(event => event.result)
    ).toEqual(['success', 'skipped', 'warning']);
    expect(new Set(events.map(event => event.logId)).size).toBe(events.length);
  } finally {
    await log.close();
    await fs.remove(directory);
  }
});

it('keeps errors in their phase, masks secrets, and closes an interrupted phase once', async () => {
  const directory = await fs.mkdtemp(path.join(os.tmpdir(), 'eoas-phases-'));
  const log = await createBuildLog(directory, 'test');
  const events: BuildLogEvent[] = [];
  log.setSecrets(['password']);
  log.streamTo({
    write: event => {
      events.push(event);
    },
    close: async () => {},
  });
  try {
    await expect(
      log.runBuildPhase(BuildPhase.RUN_GRADLEW, async phase => {
        phase.write('password', 'stderr');
        throw new Error('tool failed: password');
      })
    ).rejects.toThrow('tool failed');
    expect(events.find(event => event.source === 'stderr')?.msg).toBe('[REDACTED]');
    expect(events.at(-1)).toMatchObject({ marker: 'END_PHASE', result: BuildPhaseResult.FAIL });
    expect(JSON.stringify(events)).not.toContain('password');
    await log.runBuildPhase(BuildPhase.RUN_GRADLEW, async () => {
      log.abort();
    });
    const ends = events.filter(event => event.marker === LogMarker.END_PHASE);
    expect(ends).toHaveLength(2);
    expect(ends[1].result).toBe('failed');
    expect(ends[1].buildStepId).not.toBe(ends[0].buildStepId);
  } finally {
    await log.close();
    await fs.remove(directory);
  }
});
