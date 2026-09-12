import fg from 'fast-glob';
import { XMLParser } from 'fast-xml-parser';
import { readFile } from 'fs/promises';

import { PhaseLogger } from '../log';

interface GradleTaskProfile {
  path: string;
  seconds: number;
  result: string;
}

export async function logGradleProfile(androidDirectory: string, log: PhaseLogger): Promise<void> {
  try {
    // copyProject excludes android/build, so reports belong to this temporary build.
    const reports = await fg('build/reports/profile/profile-*.html', {
      cwd: androidDirectory,
      absolute: true,
    });
    const latest = reports.sort().at(-1);
    const tasks = latest ? parseGradleProfile(await readFile(latest, 'utf8')) : [];
    if (!tasks.length) {
      log.write('No Gradle task execution profile was generated.');
      log.markSkipped();
      return;
    }
    // Keep rows intact when the stream batches/chunks messages.
    for (const line of formatGradleProfile(tasks).split('\n')) {
      log.write(line);
    }
  } catch (error) {
    log.write(`Could not read the Gradle profile: ${(error as Error).message}`);
    log.markSkipped();
  }
}

export function parseGradleProfile(html: string): GradleTaskProfile[] {
  // Gradle's ProfileReportRenderer writes a separate table for each section.
  const table = html.match(
    /<h2>\s*Task Execution\s*<\/h2>\s*(<table\b[^>]*>[\s\S]*?<\/table>)/
  )?.[1];
  if (!table) {
    throw new Error('Task Execution table is missing.');
  }
  const parser = new XMLParser({
    ignoreAttributes: true,
    parseTagValue: false,
    isArray: name => name === 'tr' || name === 'td',
  });
  const parsed = parser.parse(table, true) as { table: { tr?: { td?: string[] }[] } };
  const tasks: GradleTaskProfile[] = [];
  for (const { td = [] } of parsed.table.tr ?? []) {
    const [taskPath, duration, result] = td;
    if (!taskPath?.startsWith(':') || result === '(total)') {
      continue;
    }
    // Gradle's formatDurationVeryTerse uses days/hours/minutes for long tasks.
    const parts = /^(?:(\d+)d)?(?:(\d+)h)?(?:(\d+)m)?(\d+(?:\.\d+)?)s$/.exec(duration);
    if (!parts) {
      throw new Error(`Unrecognized task duration: ${duration}`);
    }
    const [, days = '0', hours = '0', minutes = '0', seconds] = parts;
    tasks.push({
      path: taskPath,
      seconds: Number(days) * 86400 + Number(hours) * 3600 + Number(minutes) * 60 + Number(seconds),
      result: result?.toLowerCase() || 'executed',
    });
  }
  return tasks;
}

export function formatGradleProfile(tasks: GradleTaskProfile[]): string {
  const modules = new Map<string, { seconds: number; tasks: GradleTaskProfile[] }>();
  let total = 0;
  for (const task of tasks) {
    const module = task.path.slice(0, task.path.lastIndexOf(':')) || ':';
    const group = modules.get(module) ?? { seconds: 0, tasks: [] };
    group.seconds += task.seconds;
    group.tasks.push(task);
    modules.set(module, group);
    total += task.seconds;
  }
  const lines = [
    'Gradle Build — Task Execution Profile',
    `${tasks.length} ${tasks.length === 1 ? 'task' : 'tasks'}, total task time: ${total.toFixed(
      1
    )}s`,
    '% Time = share of total task time; parallel tasks can exceed elapsed build time.',
    'Tasks under 1s are included in totals but hidden below.',
    '',
  ];
  const widths = [48, 10, 8, 10, 20];
  const border = (left: string, join: string, right: string): string =>
    left + widths.map(width => '─'.repeat(width + 2)).join(join) + right;
  const row = (cells: string[]): string =>
    '│ ' +
    cells
      .map((cell, index) => {
        const width = widths[index];
        const text =
          cell.length > width
            ? `${cell.slice(0, Math.ceil((width - 1) / 2))}…${cell.slice(
                -Math.floor((width - 1) / 2)
              )}`
            : cell;
        return index === 1 || index === 2 ? text.padStart(width) : text.padEnd(width);
      })
      .join(' │ ') +
    ' │';
  const timing = (name: string, seconds: number, result: string): string => {
    const share = total > 0 ? seconds / total : 0;
    const filled = Math.round(share * 20);
    return row([
      name,
      `${seconds.toFixed(1)}s`,
      `${(share * 100).toFixed(1)}%`,
      result,
      '█'.repeat(filled) + '░'.repeat(20 - filled),
    ]);
  };
  const groups = [...modules].sort((a, b) => b[1].seconds - a[1].seconds);
  if (!groups.some(([, group]) => group.seconds >= 1)) {
    return [...lines, 'No tasks took at least 1s.'].join('\n');
  }
  lines.push(
    border('┌', '┬', '┐'),
    row(['Task', 'Duration', '% Time', 'Result', '']),
    border('├', '┼', '┤')
  );
  for (const [name, group] of groups) {
    if (group.seconds < 1) {
      continue;
    }
    lines.push(timing(name, group.seconds, 'total'));
    const visible = group.tasks
      .filter(task => task.seconds >= 1)
      .sort((a, b) => b.seconds - a.seconds);
    visible.forEach((task, index) => {
      const prefix = index === visible.length - 1 ? '└─' : '├─';
      const taskName = task.path.slice(task.path.lastIndexOf(':') + 1);
      lines.push(timing(`  ${prefix} ${taskName}`, task.seconds, task.result));
    });
  }
  lines.push(border('└', '┴', '┘'));
  return lines.join('\n');
}
