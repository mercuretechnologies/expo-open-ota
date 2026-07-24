import { useMemo } from 'react';
import type { ReactNode } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Activity, AlertTriangle, Users } from 'lucide-react';
import { api, UpdateHealthHistoryPoint } from '@/lib/api';
import { useSelectedApp } from '@/lib/SelectedAppContext';
import { Skeleton } from '@/components/ui/skeleton';

export type HealthHistorySeries = {
  key: string;
  label: string;
  updateUUIDs: string[];
  color: string;
};

type AggregatedPoint = Omit<UpdateHealthHistoryPoint, 'role'>;

const chartWidth = 520;
const chartHeight = 84;

const aggregateSeries = (
  updateUUIDs: string[],
  pointsByUpdate: Record<string, UpdateHealthHistoryPoint[]>
) => {
  const byTimestamp = new Map<string, AggregatedPoint>();
  for (const updateUUID of updateUUIDs) {
    for (const point of pointsByUpdate[updateUUID] ?? []) {
      const current = byTimestamp.get(point.timestamp);
      if (current) {
        current.devicesOnUpdate += point.devicesOnUpdate;
        current.successfulDevices += point.successfulDevices;
        current.faultyDevices += point.faultyDevices;
        current.updateIssues += point.updateIssues;
        current.runtimeIssues += point.runtimeIssues;
      } else {
        byTimestamp.set(point.timestamp, {
          ...point,
        });
      }
    }
  }
  return Array.from(byTimestamp.values())
    .sort((a, b) => a.timestamp.localeCompare(b.timestamp))
    .map(point => {
      const attempts = point.successfulDevices + point.faultyDevices;
      return {
        ...point,
        healthPercent: attempts > 0 ? (100 * point.successfulDevices) / attempts : null,
      };
    });
};

const linePath = (
  points: AggregatedPoint[],
  value: (point: AggregatedPoint) => number | null,
  maximum?: number
) => {
  const values = points.map(value);
  const finiteValues = values.filter((item): item is number => item != null);
  if (finiteValues.length === 0) return '';
  const maxValue = maximum ?? Math.max(1, ...finiteValues);
  let plotted = 0;
  return values
    .map((item, index) => {
      if (item == null) return null;
      const x = points.length === 1 ? chartWidth / 2 : (index / (points.length - 1)) * chartWidth;
      const y = chartHeight - Math.min(1, Math.max(0, item / maxValue)) * chartHeight;
      const command = plotted === 0 ? 'M' : 'L';
      plotted += 1;
      return `${command} ${x.toFixed(2)} ${y.toFixed(2)}`;
    })
    .filter(Boolean)
    .join(' ');
};

const latestValue = (
  points: AggregatedPoint[],
  value: (point: AggregatedPoint) => number | null
) => {
  for (let index = points.length - 1; index >= 0; index -= 1) {
    const current = value(points[index]);
    if (current != null) return current;
  }
  return null;
};

const HistoryChart = ({
  title,
  icon,
  series,
  value,
  format,
  maximum,
}: {
  title: string;
  icon: ReactNode;
  series: Array<HealthHistorySeries & { points: AggregatedPoint[] }>;
  value: (point: AggregatedPoint) => number | null;
  format: (value: number) => string;
  maximum?: number;
}) => {
  const sharedMaximum =
    maximum ??
    Math.max(
      1,
      ...series.flatMap(item =>
        item.points.map(value).filter((current): current is number => current != null)
      )
    );
  return (
    <div className="space-y-2 rounded-lg border bg-background/60 p-3">
      <div className="flex items-center justify-between gap-3">
        <span className="flex items-center gap-1.5 text-xs font-medium text-muted-foreground">
          {icon}
          {title}
        </span>
        <div className="flex flex-wrap justify-end gap-x-3 gap-y-1">
          {series.map(item => {
            const latest = latestValue(item.points, value);
            return (
              <span key={item.key} className="flex items-center gap-1 text-xs">
                <span className="h-2 w-2 rounded-full" style={{ backgroundColor: item.color }} />
                <span className="text-muted-foreground">{item.label}</span>
                <span className="font-medium tabular-nums">
                  {latest == null ? '—' : format(latest)}
                </span>
              </span>
            );
          })}
        </div>
      </div>
      <svg
        viewBox={`0 0 ${chartWidth} ${chartHeight}`}
        className="h-20 w-full overflow-visible"
        role="img"
        aria-label={`${title} over time`}
        preserveAspectRatio="none">
        <line
          x1="0"
          y1={chartHeight}
          x2={chartWidth}
          y2={chartHeight}
          className="stroke-border"
          strokeWidth="1"
        />
        <line
          x1="0"
          y1={chartHeight / 2}
          x2={chartWidth}
          y2={chartHeight / 2}
          className="stroke-border/50"
          strokeWidth="1"
          strokeDasharray="3 4"
        />
        {series.map(item => {
          const path = linePath(item.points, value, sharedMaximum);
          return path ? (
            <path
              key={item.key}
              d={path}
              fill="none"
              stroke={item.color}
              strokeWidth="2.5"
              vectorEffect="non-scaling-stroke"
              strokeLinecap="round"
              strokeLinejoin="round"
            />
          ) : null;
        })}
      </svg>
    </div>
  );
};

export const UpdateHealthHistory = ({
  series,
  from,
  live = false,
}: {
  series: HealthHistorySeries[];
  from?: string;
  live?: boolean;
}) => {
  const { selectedAppId } = useSelectedApp();
  const updateUUIDs = useMemo(
    () => Array.from(new Set(series.flatMap(item => item.updateUUIDs))),
    [series]
  );
  const query = useQuery({
    queryKey: ['update-health-history', selectedAppId, updateUUIDs.join(','), from],
    queryFn: () => api.getUpdateHealthHistory(updateUUIDs, from),
    enabled: !!selectedAppId && updateUUIDs.length > 0,
    refetchInterval: live ? 60_000 : false,
  });
  const aggregated = useMemo(
    () =>
      series.map(item => ({
        ...item,
        points: aggregateSeries(item.updateUUIDs, query.data?.updates ?? {}),
      })),
    [query.data?.updates, series]
  );
  const hasPoints = aggregated.some(item => item.points.length > 0);
  const start = aggregated
    .flatMap(item => item.points)
    .map(point => point.timestamp)
    .sort()[0];
  const end = aggregated
    .flatMap(item => item.points)
    .map(point => point.timestamp)
    .sort()
    .at(-1);

  if (query.isLoading) {
    return <Skeleton className="h-80 w-full rounded-xl" />;
  }
  if (query.error || query.data?.available === false) return null;
  if (!hasPoints) {
    return (
      <div className="rounded-lg border border-dashed px-4 py-6 text-center text-xs text-muted-foreground">
        Health history will appear after the first one-minute snapshot.
      </div>
    );
  }

  return (
    <section className="space-y-3">
      <div className="flex items-end justify-between gap-3">
        <div>
          <h3 className="text-sm font-medium">Health over time</h3>
          <p className="text-xs text-muted-foreground">
            One-minute snapshots from the device registry.
          </p>
        </div>
        {start && end && (
          <span className="text-right text-[11px] text-muted-foreground">
            {new Date(start).toLocaleString()} – {new Date(end).toLocaleString()}
          </span>
        )}
      </div>
      <HistoryChart
        title="Health"
        icon={<Activity className="h-3.5 w-3.5" />}
        series={aggregated}
        value={point => point.healthPercent}
        format={value => `${value.toFixed(1)}%`}
        maximum={100}
      />
      <HistoryChart
        title="Devices on update"
        icon={<Users className="h-3.5 w-3.5" />}
        series={aggregated}
        value={point => point.devicesOnUpdate}
        format={value => Math.round(value).toLocaleString()}
      />
      <HistoryChart
        title="Faulty devices"
        icon={<AlertTriangle className="h-3.5 w-3.5" />}
        series={aggregated}
        value={point => point.faultyDevices}
        format={value => Math.round(value).toLocaleString()}
      />
    </section>
  );
};
