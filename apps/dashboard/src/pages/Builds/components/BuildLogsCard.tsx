import { useEffect, useMemo, useRef, useState } from 'react';
import stripAnsi from 'strip-ansi';
import {
  Check,
  CheckCircle2,
  ChevronRight,
  CircleAlert,
  Clipboard,
  Download,
  Loader2,
  MinusCircle,
  Terminal,
  XCircle,
} from 'lucide-react';
import { api, BuildRecord } from '@/lib/api';
import { Button } from '@/components/ui/button';
import { Card } from '@/components/ui/card';
import { appendBuildLogs, buildLogsText, BuildLogGroup } from '../logs';

const PREVIEW_CHARACTERS = 200000;

const durationLabel = (duration: number) => {
  if (duration < 1000) return '< 1s';
  const seconds = Math.floor(duration / 1000);
  return seconds < 60 ? `${seconds}s` : `${Math.floor(seconds / 60)}m ${seconds % 60}s`;
};

const LogPhase = ({
  group,
  running,
  now,
}: {
  group: BuildLogGroup;
  running: boolean;
  now: number;
}) => {
  const [expanded, setExpanded] = useState<boolean>();
  const [copied, setCopied] = useState(false);
  const [copyFailed, setCopyFailed] = useState(false);
  const viewport = useRef<HTMLPreElement>(null);
  const follow = useRef(true);
  const output = useMemo(() => stripAnsi(group.output), [group.output]);
  const active = group.startedAt !== undefined && !group.result && running;
  const open = expanded ?? (active || group.result === 'failed' || group.id === 'legacy');
  const duration = group.durationMs ?? (active ? Math.max(0, now - group.startedAt!) : undefined);
  const status = active
    ? 'Running'
    : (group.result ?? (group.startedAt === undefined ? 'Output' : 'Incomplete'));
  const Icon = active
    ? Loader2
    : group.result === 'success'
      ? CheckCircle2
      : group.result === 'failed'
        ? XCircle
        : group.result === 'warning'
          ? CircleAlert
          : group.result === 'skipped'
            ? MinusCircle
            : Terminal;
  const color = active
    ? 'animate-spin text-primary'
    : group.result === 'success'
      ? 'text-emerald-600'
      : group.result === 'failed'
        ? 'text-destructive'
        : group.result === 'warning'
          ? 'text-amber-600'
          : 'text-muted-foreground';

  useEffect(() => {
    if (open && follow.current && viewport.current)
      viewport.current.scrollTop = viewport.current.scrollHeight;
  }, [group.output, open]);
  useEffect(() => {
    if (!copied) return;
    const timer = setTimeout(() => setCopied(false), 1500);
    return () => clearTimeout(timer);
  }, [copied]);

  return (
    <section>
      <div className="flex items-center gap-2 px-3">
        <button
          type="button"
          className="flex min-w-0 flex-1 items-center gap-3 py-4 text-left text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          aria-expanded={open}
          aria-controls={`phase-${group.id}`}
          onClick={() => setExpanded(!open)}>
          <ChevronRight
            className={`h-4 w-4 shrink-0 text-muted-foreground transition-transform ${open ? 'rotate-90' : ''}`}
          />
          <Icon className={`h-4 w-4 shrink-0 ${color}`} aria-label={status} />
          <span className="min-w-0 flex-1 font-medium">{group.label}</span>
          {group.result === 'skipped' && (
            <span className="text-xs text-muted-foreground">Skipped</span>
          )}
          {duration !== undefined && (
            <span className="shrink-0 tabular-nums text-muted-foreground">
              {durationLabel(duration)}
            </span>
          )}
        </button>
        <Button
          variant="ghost"
          size="icon"
          className="h-8 w-8 shrink-0 text-muted-foreground"
          disabled={!output}
          title={copyFailed ? 'Could not copy logs' : copied ? 'Copied' : 'Copy logs'}
          aria-label={`Copy ${group.label} logs`}
          onClick={async () => {
            try {
              await navigator.clipboard.writeText(output);
              setCopied(true);
              setCopyFailed(false);
            } catch {
              setCopyFailed(true);
            }
          }}>
          {copied ? <Check className="h-4 w-4" /> : <Clipboard className="h-4 w-4" />}
        </Button>
      </div>
      {open && (
        <div id={`phase-${group.id}`} className="border-t">
          {output.length > PREVIEW_CHARACTERS && (
            <p className="px-4 py-2 text-xs text-muted-foreground">
              Showing the latest output. Copy or download to read the full log.
            </p>
          )}
          {output ? (
            <pre
              ref={viewport}
              tabIndex={0}
              aria-label={`${group.label} output`}
              onScroll={() => {
                const node = viewport.current;
                if (node)
                  follow.current = node.scrollHeight - node.scrollTop - node.clientHeight < 40;
              }}
              className="max-h-96 overflow-auto bg-zinc-950 p-4 font-mono text-xs leading-5 text-zinc-200">
              {output.slice(-PREVIEW_CHARACTERS)}
            </pre>
          ) : (
            <p className="px-4 py-4 text-sm text-muted-foreground">
              {active ? 'Waiting for output…' : 'No output for this step.'}
            </p>
          )}
        </div>
      )}
    </section>
  );
};

export const BuildLogsCard = ({ build }: { build: BuildRecord }) => {
  const [groups, setGroups] = useState<BuildLogGroup[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [retry, setRetry] = useState(0);
  const [downloading, setDownloading] = useState(false);
  const [now, setNow] = useState(Date.now());
  const running = ['building', 'uploading'].includes(build.status);

  useEffect(() => {
    if (!running) return;
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, [running]);

  useEffect(() => {
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    let cursor = 0;
    const phases = new Map<string, BuildLogGroup>();
    const deadline = running ? Infinity : Date.now() + 15000;
    setGroups([]);
    setLoading(true);
    const poll = async () => {
      let delay = 2000;
      try {
        const page = await api.getBuildLogs(build.id, cursor, controller.signal);
        if (controller.signal.aborted) return;
        appendBuildLogs(phases, page.chunks);
        cursor = page.nextOffset;
        if (page.chunks.length) setGroups([...phases.values()]);
        setError('');
        if (page.chunks.length === 32) delay = 0;
      } catch {
        if (controller.signal.aborted) return;
        setError('Could not refresh build logs.');
        delay = 5000;
      }
      setLoading(false);
      if (!controller.signal.aborted && (delay === 0 || Date.now() < deadline))
        timer = setTimeout(() => void poll(), delay);
    };
    void poll();
    return () => {
      controller.abort();
      clearTimeout(timer);
    };
  }, [build.id, running, retry]);

  const download = async () => {
    setDownloading(true);
    try {
      const phases = new Map<string, BuildLogGroup>();
      let cursor = 0;
      for (;;) {
        const page = await api.getBuildLogs(build.id, cursor);
        appendBuildLogs(phases, page.chunks);
        cursor = page.nextOffset;
        if (page.chunks.length < 32) break;
      }
      const url = URL.createObjectURL(
        new Blob([buildLogsText(phases.values())], { type: 'text/plain;charset=utf-8' })
      );
      const link = document.createElement('a');
      link.href = url;
      link.download = `${build.id}.log`;
      link.click();
      URL.revokeObjectURL(url);
    } catch {
      setError('Could not download build logs.');
    } finally {
      setDownloading(false);
    }
  };

  return (
    <Card className="overflow-hidden">
      <div className="flex items-center justify-between gap-3 border-b px-4 py-3">
        <div className="flex items-center gap-2 text-sm font-medium">
          <Terminal className="h-4 w-4 text-muted-foreground" />
          Build logs
          {running && <span className="ml-2 text-xs text-emerald-600">● Live</span>}
        </div>
        {groups.length > 0 && (
          <Button variant="ghost" size="sm" disabled={downloading} onClick={() => void download()}>
            <Download className="h-3.5 w-3.5" />
            {downloading ? 'Downloading…' : 'Download'}
          </Button>
        )}
      </div>
      {error && (
        <div
          className="flex items-center justify-between px-4 py-2 text-sm text-destructive"
          role="status">
          {error}
          <Button variant="ghost" size="sm" onClick={() => setRetry(value => value + 1)}>
            Retry
          </Button>
        </div>
      )}
      {groups.length ? (
        <div className="divide-y">
          {groups.map(group => (
            <LogPhase key={group.id} group={group} running={running} now={now} />
          ))}
        </div>
      ) : (
        <p className="px-4 py-6 text-sm text-muted-foreground">
          {loading ? (
            'Loading logs…'
          ) : (
            <>
              No logs received. Run your build with{' '}
              <code className="font-mono text-xs">--stream</code> to view its output here.
            </>
          )}
        </p>
      )}
    </Card>
  );
};
