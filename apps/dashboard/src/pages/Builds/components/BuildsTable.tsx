import { Box, Clock3, KeyRound, Layers2 } from 'lucide-react';
import { useQuery } from '@tanstack/react-query';
import { Link, useNavigate } from 'react-router';
import { api, BuildRecord } from '@/lib/api';
import { useSelectedApp } from '@/lib/SelectedAppContext';
import { formatCompactTimestamp, formatTimestamp } from '@/lib/utils';
import { shortRuntimeVersion } from '@/lib/update-format';
import { Skeleton } from '@/components/ui/skeleton';
import { GitCommitLink } from '@/components/GitCommitLink';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { PlatformLogo } from '@/pages/BuildCredentials/components/PlatformLogo';
import { BuildStatusBadge } from './BuildStatusBadge';
import { buildVersionLabel, formatBytes, formatDuration } from '../format';

export const BuildsTable = ({ builds, loading }: { builds: BuildRecord[]; loading: boolean }) => {
  const navigate = useNavigate();
  const { selectedAppId } = useSelectedApp();
  const appDetailsQuery = useQuery({
    queryKey: ['appDetails', selectedAppId],
    queryFn: () => api.getApp(selectedAppId!),
    enabled: !!selectedAppId,
  });

  return (
    <div className="overflow-hidden rounded-2xl border bg-card">
      <Table className="min-w-[1040px]" aria-label="Builds">
        <TableHeader className="bg-transparent">
          <TableRow className="hover:bg-transparent">
            <TableHead scope="col" className="w-[34%] pl-5">
              Build
            </TableHead>
            <TableHead scope="col" className="w-[13%]">
              Git ref
            </TableHead>
            <TableHead scope="col" className="w-[12%]">
              Profile
            </TableHead>
            <TableHead scope="col" className="w-[12%]">
              Runtime
            </TableHead>
            <TableHead scope="col" className="w-[13%]">
              Channel
            </TableHead>
            <TableHead scope="col" className="w-[16%] pr-5">
              Created by
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody className="[&_td]:py-2">
          {loading &&
            builds.length === 0 &&
            Array.from({ length: 3 }, (_, row) => (
              <TableRow key={row}>
                {Array.from({ length: 6 }, (_, cell) => (
                  <TableCell key={cell} className="h-14 first:pl-5 last:pr-5">
                    <Skeleton className="h-4 w-3/4" />
                  </TableCell>
                ))}
              </TableRow>
            ))}
          {builds.map(build => {
            const { metadata } = build;
            const title =
              build.artifactType === 'aab' ? 'Android Play Store build' : 'Android APK build';
            const building = build.status === 'building';
            const duration = building
              ? Date.now() - new Date(metadata.startedAt).getTime()
              : metadata.durationMs;
            const apiToken = build.actorType === 'api_key';
            const destination = `/builds/${encodeURIComponent(build.id)}`;

            return (
              <TableRow
                key={build.id}
                className="cursor-pointer hover:bg-muted/40 focus-within:bg-muted/40"
                onClick={() => navigate(destination)}>
                <TableCell className="pl-5">
                  <div className="flex items-center gap-3">
                    <BuildStatusBadge status={build.status} iconOnly />
                    <div className="min-w-0">
                      <Link
                        to={destination}
                        aria-label={`${title} ${buildVersionLabel(build)}`}
                        onClick={event => event.stopPropagation()}
                        title={`${build.applicationId}${build.size ? ` · ${formatBytes(build.size)}` : ''}`}
                        className="flex items-center gap-2 rounded-sm font-medium leading-5 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-card">
                        <PlatformLogo platform={build.platform} className="h-3.5 w-3.5 shrink-0" />
                        <span>
                          {title}{' '}
                          <span className="ml-1 whitespace-nowrap">{buildVersionLabel(build)}</span>
                        </span>
                      </Link>
                      <div className="mt-0.5 flex items-center gap-3 text-xs leading-4 text-muted-foreground">
                        <time
                          dateTime={build.createdAt}
                          title={formatTimestamp(build.createdAt, true) ?? undefined}>
                          {formatCompactTimestamp(build.createdAt)}
                        </time>
                        {duration > 0 && (
                          <span
                            className="inline-flex items-center gap-1.5 whitespace-nowrap"
                            title={building ? 'Elapsed build time' : 'Build duration'}>
                            <Clock3 className="h-3.5 w-3.5 text-muted-foreground/70" />
                            {formatDuration(duration)}
                          </span>
                        )}
                      </div>
                    </div>
                  </div>
                </TableCell>
                <TableCell>
                  {metadata.gitCommit ? (
                    <div title={metadata.gitMessage}>
                      <GitCommitLink
                        commitHash={metadata.gitCommit}
                        gitUrl={appDetailsQuery.data?.gitUrl}
                      />
                    </div>
                  ) : (
                    <span className="text-muted-foreground">—</span>
                  )}
                </TableCell>
                <TableCell>
                  <div
                    title={[
                      metadata.profile,
                      metadata.environment ? `Environment: ${metadata.environment}` : '',
                    ]
                      .filter(Boolean)
                      .join('\n')}>
                    <div className="max-w-40 truncate">{metadata.profile}</div>
                    {metadata.mode && (
                      <div className="mt-0.5 text-xs text-muted-foreground">{metadata.mode}</div>
                    )}
                  </div>
                </TableCell>
                <TableCell>
                  {metadata.runtimeVersion ? (
                    <span className="flex items-center gap-2" title={metadata.runtimeVersion}>
                      <Layers2 className="h-4 w-4 shrink-0 text-muted-foreground" />
                      <span className="max-w-32 truncate">
                        {shortRuntimeVersion(metadata.runtimeVersion)}
                      </span>
                    </span>
                  ) : (
                    <span className="text-muted-foreground">—</span>
                  )}
                </TableCell>
                <TableCell>
                  {metadata.channel ? (
                    <span className="flex items-center gap-2" title={metadata.channel}>
                      <Box className="h-4 w-4 shrink-0 text-muted-foreground" />
                      <span className="max-w-40 truncate">{metadata.channel}</span>
                    </span>
                  ) : (
                    <span className="text-muted-foreground">—</span>
                  )}
                </TableCell>
                <TableCell className="pr-5">
                  <div className="flex items-center gap-2.5">
                    <span
                      className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-primary/10 text-sm font-medium text-primary"
                      aria-hidden="true">
                      {apiToken ? (
                        <KeyRound className="h-3.5 w-3.5" />
                      ) : (
                        build.actorDisplay.trim()[0]?.toUpperCase() || '?'
                      )}
                    </span>
                    <div className="min-w-0">
                      <div className="max-w-40 truncate" title={build.actorDisplay}>
                        {build.actorDisplay || 'Unknown'}
                      </div>
                      {apiToken && (
                        <div className="mt-0.5 text-xs text-muted-foreground">API token</div>
                      )}
                    </div>
                  </div>
                </TableCell>
              </TableRow>
            );
          })}
          {!loading && builds.length === 0 && (
            <TableRow className="hover:bg-transparent">
              <TableCell colSpan={6} className="h-32 text-center text-muted-foreground">
                No build uploaded yet. Build your app from the command line to see it here.
              </TableCell>
            </TableRow>
          )}
        </TableBody>
      </Table>
    </div>
  );
};
