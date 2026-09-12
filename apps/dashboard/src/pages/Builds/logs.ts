export interface BuildLogEvent {
  logId: string;
  time: string;
  level: number;
  msg: string;
  phase?: string;
  buildStepId?: string;
  buildStepDisplayName?: string;
  marker?: 'START_PHASE' | 'END_PHASE';
  result?: 'success' | 'failed' | 'warning' | 'skipped' | 'unknown';
  durationMs?: number;
}

export interface BuildLogGroup {
  id: string;
  label: string;
  output: string;
  startedAt?: number;
  result?: BuildLogEvent['result'];
  durationMs?: number;
}

export interface BuildLogChunk {
  offset: number;
  content: string;
  format: 'text' | 'ndjson';
  createdAt: string;
}

export function appendBuildLogs(groups: Map<string, BuildLogGroup>, chunks: BuildLogChunk[]): void {
  for (const chunk of chunks) {
    if (chunk.format !== 'ndjson') {
      const group = groups.get('legacy') ?? { id: 'legacy', label: 'Build output', output: '' };
      groups.set(group.id, { ...group, output: group.output + chunk.content });
      continue;
    }
    for (const line of chunk.content.trimEnd().split('\n')) {
      const event: BuildLogEvent = JSON.parse(line);
      const id = event.buildStepId ?? event.buildStepDisplayName ?? event.phase ?? 'general';
      const group = { ...(groups.get(id) ?? { id, label: 'Build', output: '' }) };
      group.label = event.buildStepDisplayName ?? event.phase ?? group.label;
      if (event.marker === 'START_PHASE') group.startedAt = Date.parse(event.time);
      else if (event.marker === 'END_PHASE') {
        group.result = event.result;
        group.durationMs = event.durationMs;
      } else group.output += `${event.msg}\n`;
      groups.set(id, group);
    }
  }
}

export function buildLogsText(groups: Iterable<BuildLogGroup>): string {
  return [...groups].map(group => `${group.label}\n${stripAnsi(group.output)}`).join('\n');
}
import stripAnsi from 'strip-ansi';
