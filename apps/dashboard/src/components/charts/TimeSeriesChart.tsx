import { useId, useMemo } from 'react';
import { ParentSize } from '@visx/responsive';
import {
  AreaSeries,
  AreaStack,
  Axis,
  buildChartTheme,
  GlyphSeries,
  Grid,
  Tooltip,
  XYChart,
} from '@visx/xychart';

export type TimeSeriesPoint = {
  timestamp: Date;
  value: number;
};

export type TimeSeriesDefinition = {
  key: string;
  label: string;
  color: string;
  points: TimeSeriesPoint[];
};

type TimeSeriesChartProps = {
  series: TimeSeriesDefinition[];
  formatValue: (value: number) => string;
  formatAxisValue?: (value: number) => string;
  mode?: 'line' | 'stacked';
  maximum?: number;
  ariaLabel: string;
  height?: number;
  className?: string;
};

const xAccessor = (point: TimeSeriesPoint) => point.timestamp;
const yAccessor = (point: TimeSeriesPoint) => point.value;

const chartTheme = buildChartTheme({
  backgroundColor: 'hsl(var(--popover))',
  colors: ['hsl(var(--primary))'],
  gridColor: 'hsl(var(--border))',
  gridColorDark: 'hsl(var(--border))',
  gridStyles: {
    strokeOpacity: 0.55,
    strokeDasharray: '2 5',
  },
  svgLabelBig: {
    fill: 'hsl(var(--foreground))',
    fontFamily: 'Inter, system-ui, sans-serif',
    fontSize: 11,
  },
  svgLabelSmall: {
    fill: 'hsl(var(--muted-foreground))',
    fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
    fontSize: 10,
  },
  htmlLabel: {
    color: 'hsl(var(--foreground))',
    fontFamily: 'Inter, system-ui, sans-serif',
    fontSize: 12,
  },
  xAxisLineStyles: {
    stroke: 'hsl(var(--border))',
  },
  yAxisLineStyles: {
    stroke: 'transparent',
  },
  xTickLineStyles: {
    stroke: 'transparent',
  },
  yTickLineStyles: {
    stroke: 'transparent',
  },
  tickLength: 0,
});

const formatCompactNumber = (value: number) =>
  Intl.NumberFormat(undefined, {
    notation: Math.abs(value) >= 1_000 ? 'compact' : 'standard',
    maximumFractionDigits: Math.abs(value) >= 1_000 ? 1 : 0,
  }).format(value);

const timeFormatter = (start: number, end: number) => {
  const spansMultipleDays = end - start >= 24 * 60 * 60 * 1_000;
  return new Intl.DateTimeFormat(undefined, {
    ...(spansMultipleDays ? { month: 'short', day: 'numeric' } : {}),
    hour: '2-digit',
    minute: '2-digit',
  });
};

const timestampFormatter = new Intl.DateTimeFormat(undefined, {
  month: 'short',
  day: 'numeric',
  hour: '2-digit',
  minute: '2-digit',
  second: '2-digit',
});

const SinglePointGlyph = ({ x, y, color }: { x: number; y: number; color: string }) => (
  <g pointerEvents="none">
    <circle cx={x} cy={y} r={6} fill="hsl(var(--background))" stroke={color} strokeWidth={2} />
    <circle cx={x} cy={y} r={2.25} fill={color} />
  </g>
);

export const TimeSeriesChart = ({
  series,
  formatValue,
  formatAxisValue = formatCompactNumber,
  mode = 'line',
  maximum,
  ariaLabel,
  height = 192,
  className,
}: TimeSeriesChartProps) => {
  const gradientPrefix = useId().replace(/:/g, '');
  const timestamps = series.flatMap(item =>
    item.points.map(point => point.timestamp.getTime()).filter(Number.isFinite)
  );
  const now = Date.now();
  const start = timestamps.length > 0 ? Math.min(...timestamps) : now;
  const end = timestamps.length > 0 ? Math.max(...timestamps) : now;
  const xDomain =
    start === end
      ? [new Date(start - 30_000), new Date(end + 30_000)]
      : [new Date(start), new Date(end)];
  const formatTime = timeFormatter(start, end);
  const calculatedMaximum = useMemo(() => {
    if (maximum != null) return maximum;
    if (mode === 'stacked') {
      const totals = new Map<number, number>();
      for (const item of series) {
        for (const point of item.points) {
          const timestamp = point.timestamp.getTime();
          totals.set(timestamp, (totals.get(timestamp) ?? 0) + point.value);
        }
      }
      return Math.max(2, ...totals.values());
    }
    return Math.max(2, ...series.flatMap(item => item.points.map(point => point.value)));
  }, [maximum, mode, series]);
  const yMaximum = maximum ?? calculatedMaximum * 1.08;
  const singleStackPoint =
    mode === 'stacked' && new Set(timestamps).size === 1
      ? [
          {
            timestamp: new Date(start),
            value: series.reduce((total, item) => total + (item.points[0]?.value ?? 0), 0),
          },
        ]
      : [];

  if (timestamps.length === 0) return null;

  return (
    <ParentSize className={className} debounceTime={50} style={{ height }}>
      {({ width, height }) =>
        width > 0 && height > 0 ? (
          <XYChart
            accessibilityLabel={ariaLabel}
            width={width}
            height={height}
            margin={{ top: 10, right: 10, bottom: 28, left: 42 }}
            theme={chartTheme}
            xScale={{ type: 'time', domain: xDomain }}
            yScale={{ type: 'linear', domain: [0, yMaximum], nice: true, zero: true }}>
            <defs>
              {series.map(item => (
                <linearGradient
                  key={item.key}
                  id={`${gradientPrefix}-${item.key}`}
                  x1="0"
                  x2="0"
                  y1="0"
                  y2="1">
                  <stop
                    offset="0%"
                    stopColor={item.color}
                    stopOpacity={mode === 'stacked' ? 0.42 : 0.2}
                  />
                  <stop offset="100%" stopColor={item.color} stopOpacity={0.015} />
                </linearGradient>
              ))}
            </defs>
            <Grid columns={false} numTicks={3} />
            <Axis
              orientation="bottom"
              numTicks={width < 420 ? 3 : 5}
              tickFormat={value => formatTime.format(value as Date)}
              hideTicks
            />
            <Axis
              orientation="left"
              numTicks={3}
              tickFormat={value => formatAxisValue(Number(value))}
              hideAxisLine
              hideTicks
            />
            {mode === 'stacked' ? (
              <AreaStack renderLine>
                {series.map(item => (
                  <AreaSeries
                    key={item.key}
                    dataKey={item.key}
                    data={item.points}
                    xAccessor={xAccessor}
                    yAccessor={yAccessor}
                    fill={`url(#${gradientPrefix}-${item.key})`}
                    lineProps={{ stroke: item.color, strokeWidth: 2 }}
                  />
                ))}
              </AreaStack>
            ) : (
              series.map(item => (
                <AreaSeries
                  key={item.key}
                  dataKey={item.key}
                  data={item.points}
                  xAccessor={xAccessor}
                  yAccessor={yAccessor}
                  fill={`url(#${gradientPrefix}-${item.key})`}
                  renderLine
                  lineProps={{ stroke: item.color, strokeWidth: 2.25 }}
                />
              ))
            )}
            {series.map(item =>
              mode === 'line' && item.points.length === 1 ? (
                <GlyphSeries
                  key={`${item.key}-single-point`}
                  dataKey={`${item.key}-single-point`}
                  data={item.points}
                  xAccessor={xAccessor}
                  yAccessor={yAccessor}
                  enableEvents={false}
                  renderGlyph={({ x, y }) => <SinglePointGlyph x={x} y={y} color={item.color} />}
                />
              ) : null
            )}
            {singleStackPoint.length === 1 && (
              <GlyphSeries
                dataKey="stack-total-single-point"
                data={singleStackPoint}
                xAccessor={xAccessor}
                yAccessor={yAccessor}
                enableEvents={false}
                renderGlyph={({ x, y }) => (
                  <SinglePointGlyph
                    x={x}
                    y={y}
                    color={series[series.length - 1]?.color ?? 'hsl(var(--primary))'}
                  />
                )}
              />
            )}
            <Tooltip<TimeSeriesPoint>
              snapTooltipToDatumX
              showVerticalCrosshair
              showSeriesGlyphs
              verticalCrosshairStyle={{
                stroke: 'hsl(var(--muted-foreground))',
                strokeDasharray: '3 4',
                strokeOpacity: 0.65,
              }}
              glyphStyle={{
                fill: 'hsl(var(--background))',
                strokeWidth: 2,
              }}
              unstyled
              applyPositionStyle
              className="z-50 min-w-40 rounded-lg border bg-popover/95 p-2.5 text-popover-foreground shadow-elevated backdrop-blur"
              renderTooltip={({ tooltipData }) => {
                const nearest = tooltipData?.nearestDatum?.datum;
                if (!nearest) return null;
                return (
                  <div className="space-y-2">
                    <div className="font-mono text-[10px] text-muted-foreground">
                      {timestampFormatter.format(nearest.timestamp)}
                    </div>
                    <div className="space-y-1">
                      {series.map(item => {
                        const point = tooltipData?.datumByKey[item.key]?.datum;
                        if (!point) return null;
                        return (
                          <div
                            key={item.key}
                            className="flex items-center justify-between gap-5 text-xs">
                            <span className="flex items-center gap-1.5 text-muted-foreground">
                              <span
                                className="h-1.5 w-1.5 rounded-full"
                                style={{ backgroundColor: item.color }}
                              />
                              {item.label}
                            </span>
                            <span className="font-mono font-medium tabular-nums">
                              {formatValue(point.value)}
                            </span>
                          </div>
                        );
                      })}
                    </div>
                  </div>
                );
              }}
            />
          </XYChart>
        ) : null
      }
    </ParentSize>
  );
};
