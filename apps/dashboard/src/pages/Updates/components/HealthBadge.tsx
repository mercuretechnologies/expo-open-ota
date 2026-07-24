import { UpdateHealthRecord } from '@/lib/api.ts';

// The health score of an update: successful launches over launch attempts,
// from the device registry. Rendered as a quiet status dot + percentage (the
// column header already says "Health"); the tooltip carries the raw counts.
// Only meaningful for the update currently being adopted (the newest, or the
// one mid-rollout): older updates bleed their successes to their successor
// while failures stay behind.
export const HealthBadge = ({ health }: { health?: UpdateHealthRecord }) => {
  if (!health || health.healthPercent == null) {
    return <span className="whitespace-nowrap text-xs text-muted-foreground/60">No data</span>;
  }
  const percent = health.healthPercent;
  const dot =
    percent >= 98 ? 'bg-emerald-500' : percent >= 90 ? 'bg-amber-500' : 'bg-red-500';
  // Floor, never round up: "100%" must mean zero failures.
  const label =
    percent === 100 ? '100%' : `${(Math.floor(percent * 10) / 10).toFixed(1)}%`;
  const detail = `${health.devicesOnUpdate.toLocaleString()} devices on it · ${health.launchFailures.toLocaleString()} launch failures`;
  return (
    <span
      className={`inline-flex items-center gap-1.5 whitespace-nowrap text-sm tabular-nums ${
        percent < 90 ? 'font-medium text-red-600' : ''
      }`}
      title={detail}>
      <span className={`h-2 w-2 shrink-0 rounded-full ${dot}`} />
      {label}
    </span>
  );
};
