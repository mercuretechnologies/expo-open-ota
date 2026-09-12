import fs from 'fs-extra';
import os from 'os';
import path from 'path';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { LogLine, createBuildLog } from '../../log';
import { BuildPhase, BuildPhaseResult, LogMarker } from '../../phases';
import { formatGradleProfile, logGradleProfile, parseGradleProfile } from '../gradleProfile';

vi.mock('../../../log', () => ({
  default: { log: vi.fn(), warn: vi.fn(), succeed: vi.fn(), fail: vi.fn() },
}));

// Same structure as Gradle's ProfileReportRenderer, including empty result cells.
const report = `<html><body>
<h2>Configuration</h2><table><tr><td>:unrelated</td><td>42.000s</td></tr></table>
<h2>Task Execution</h2><table>
<thead><tr><th>Task</th><th>Duration</th><th>Result</th></tr></thead>
<tr><td>:</td><td>0.250s</td><td>(total)</td></tr>
<tr><td class="indentPath">:prepare</td><td>0.250s</td><td></td></tr>
<tr><td>:app</td><td>1m2.00s</td><td>(total)</td></tr>
<tr><td class="indentPath">:app:compileJava</td><td>1m0.50s</td><td></td></tr>
<tr><td class="indentPath">:app:merge&lt;resources&gt;&amp;assets</td><td>1.500s</td><td>FROM-CACHE</td></tr>
<tr><td>:library</td><td>2.750s</td><td>(total)</td></tr>
<tr><td class="indentPath">:library:compileJava</td><td>2.250s</td><td></td></tr>
<tr><td class="indentPath">:library:check</td><td>0.500s</td><td>UP-TO-DATE</td></tr>
<tr><td class="indentPath">:library:optional</td><td>0s</td><td>SKIPPED</td></tr>
</table></body></html>`;

describe('Gradle task execution profile', () => {
  it('reads only task timings, decodes HTML and preserves cache/skipped results', () => {
    const tasks = parseGradleProfile(report);
    expect(tasks).toHaveLength(6);
    expect(tasks[0]).toEqual({ path: ':prepare', seconds: 0.25, result: 'executed' });
    expect(tasks[1]).toEqual({ path: ':app:compileJava', seconds: 60.5, result: 'executed' });
    expect(tasks[2]).toEqual({
      path: ':app:merge<resources>&assets',
      seconds: 1.5,
      result: 'from-cache',
    });
    expect(tasks[4].result).toBe('up-to-date');
    expect(tasks[5]).toEqual({ path: ':library:optional', seconds: 0, result: 'skipped' });
  });

  it.each([
    ['0s', 0],
    ['0.012s', 0.012],
    ['1m2.34s', 62.34],
    ['2h3m4.50s', 7384.5],
    ['1d2h3m4.50s', 93784.5],
  ])('understands Gradle duration %s', (duration, seconds) => {
    expect(
      parseGradleProfile(report.replace('0.250s</td><td></td>', `${duration}</td><td></td>`))[0]
        .seconds
    ).toBe(seconds);
  });

  it('groups modules and sorts by duration without counting module totals twice', () => {
    const output = formatGradleProfile(parseGradleProfile(report));
    expect(output).toContain('6 tasks, total task time: 65.0s');
    expect(output).toMatch(/:app\s+│\s+62.0s\s+│\s+95.4%/);
    expect(output).toContain('├─ compileJava');
    expect(output).toContain('└─ merge<resources>&assets');
    expect(output.indexOf(':app')).toBeLessThan(output.indexOf(':library'));
    expect(output).not.toContain('optional'); // Keep the report readable, show tasks >= 1s.
    expect(output).toContain('Tasks under 1s are included in totals');
    expect(output).toContain('parallel');
  });

  it('keeps the table aligned for long native task names', () => {
    const output = formatGradleProfile([
      {
        path: ':react-native-long-package-name:buildCMakeRelWithDebInfo[arm64-v8a]'.repeat(3),
        seconds: 8,
        result: 'executed',
      },
      { path: ':app:compile', seconds: 2, result: 'executed' },
    ]);
    const rows = output.split('\n').filter(line => /^[┌│├└]/.test(line));
    expect(rows.length).toBeGreaterThan(4);
    expect(new Set(rows.map(line => line.length)).size).toBe(1);
    expect(rows.every(line => line.length <= 120)).toBe(true);
  });

  it('handles reports with only fast or zero-duration tasks', () => {
    const output = formatGradleProfile([{ path: ':app:check', seconds: 0, result: 'up-to-date' }]);
    expect(output).toContain('1 task, total task time: 0.0s');
    expect(output).toContain('No tasks took at least 1s.');
    expect(output).not.toMatch(/NaN|Infinity/);
  });

  it.each(['<html>not a profile</html>', report.replace('1m0.50s', 'oops')])(
    'rejects missing or unrecognized task timings',
    html => {
      expect(() => parseGradleProfile(html)).toThrow();
    }
  );
});

describe('Gradle profile logs', () => {
  let directory: string;
  beforeEach(async () => {
    directory = await fs.mkdtemp(path.join(os.tmpdir(), 'eoas-profile-'));
  });
  afterEach(async () => {
    await fs.remove(directory);
  });

  async function readProfileEvents(): Promise<LogLine[]> {
    const log = await createBuildLog(directory, 'test');
    const events: LogLine[] = [];
    log.streamTo({
      write: event => {
        events.push(event);
      },
      close: async () => {},
    });
    try {
      await log.runBuildPhase(BuildPhase.GRADLE_BUILD_PROFILE, phase =>
        logGradleProfile(directory, phase)
      );
    } finally {
      await log.close();
    }
    return events;
  }

  it('logs the latest report as individual lines in the EAS profiling phase', async () => {
    await fs.outputFile(
      path.join(directory, 'build/reports/profile/profile-2026-09-09-09-00-00.html'),
      '<old/>'
    );
    await fs.outputFile(
      path.join(directory, 'build/reports/profile/profile-2026-09-09-10-00-00.html'),
      report
    );
    const events = await readProfileEvents();
    expect(events[0]).toMatchObject({
      marker: LogMarker.START_PHASE,
      phase: 'GRADLE_BUILD_PROFILE',
      buildStepDisplayName: 'Gradle build profile',
    });
    expect(events.at(-1)).toMatchObject({
      marker: LogMarker.END_PHASE,
      result: BuildPhaseResult.SUCCESS,
    });
    expect(events.map(event => event.msg).join('\n')).toContain('6 tasks, total task time: 65.0s');
    expect(events.every(event => !event.msg.includes('\n'))).toBe(true);
  });

  it.each(['missing', 'malformed', 'empty'])(
    'skips a %s report without failing the build',
    async kind => {
      if (kind !== 'missing') {
        await fs.outputFile(
          path.join(directory, 'build/reports/profile/profile-2026-09-09.html'),
          kind === 'empty' ? '<h2>Task Execution</h2><table></table>' : '<bad/>'
        );
      }
      const events = await readProfileEvents();
      expect(events.at(-1)).toMatchObject({
        marker: LogMarker.END_PHASE,
        result: BuildPhaseResult.SKIPPED,
      });
    }
  );
});
