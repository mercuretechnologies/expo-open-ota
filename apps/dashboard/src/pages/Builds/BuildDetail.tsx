import { ReactNode, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useNavigate, useParams } from 'react-router';
import { Check, Copy, Download } from 'lucide-react';
import { api, ApiProblemError, BuildRecord, describeApiError } from '@/lib/api';
import { useSelectedApp } from '@/lib/SelectedAppContext';
import { useSettings } from '@/lib/SettingsContext';
import { useToast } from '@/hooks/use-toast';
import { formatTimestamp } from '@/lib/utils';
import { shortRuntimeVersion } from '@/lib/update-format';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import { Card, CardContent } from '@/components/ui/card';
import { Skeleton } from '@/components/ui/skeleton';
import { ApiError } from '@/components/APIError';
import { AdminOnlyNote } from '@/components/ui/admin-only-note';
import { GitCommitLink } from '@/components/GitCommitLink';
import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbLink,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator,
} from '@/components/ui/breadcrumb';
import { useAppPermission } from '@/ee/lib/PermissionsContext';
import { PlatformLogo } from '@/pages/BuildCredentials/components/PlatformLogo';
import { BuildStatusBadge } from './components/BuildStatusBadge';
import {
  artifactLabel,
  buildFileName,
  buildVersionLabel,
  formatBytes,
  formatDuration,
} from './format';

const CopyButton = ({ value, label }: { value: string; label: string }) => {
  const [copied, setCopied] = useState(false);
  return (
    <Button
      type="button"
      variant="ghost"
      size="icon"
      className="h-6 w-6 shrink-0 text-muted-foreground hover:text-foreground"
      onClick={async () => {
        try {
          await navigator.clipboard.writeText(value);
          setCopied(true);
          setTimeout(() => setCopied(false), 1500);
        } catch {
          setCopied(false);
        }
      }}>
      {copied ? (
        <Check className="h-3.5 w-3.5 text-emerald-700 dark:text-emerald-300" />
      ) : (
        <Copy className="h-3.5 w-3.5" />
      )}
      <span className="sr-only">Copy {label}</span>
    </Button>
  );
};

const DetailSection = ({ title, children }: { title: string; children: ReactNode }) => (
  <section className="space-y-2">
    <h3 className="text-sm font-medium">{title}</h3>
    <div className="divide-y rounded-xl border bg-card shadow-sm">{children}</div>
  </section>
);

const DetailRow = ({ label, children }: { label: string; children: ReactNode }) => (
  <div className="flex items-center justify-between gap-4 px-4 py-2.5">
    <span className="shrink-0 text-sm text-muted-foreground">{label}</span>
    <div className="flex min-w-0 items-center gap-1 text-sm font-medium">{children}</div>
  </div>
);

const MonoValue = ({ value }: { value: string }) => (
  <code className="truncate font-mono text-xs" title={value}>
    {value}
  </code>
);

const actorTypeLabel = (actorType: string) =>
  actorType === 'api_key' ? 'API token' : actorType === 'user' ? 'User' : actorType;

const NotFound = ({ onBack }: { onBack: () => void }) => (
  <div className="w-full">
    <div className="rounded-xl border border-dashed bg-muted/30 p-8 text-center text-sm text-muted-foreground">
      This build does not exist or was deleted.
      <div className="mt-4">
        <Button variant="outline" onClick={onBack}>
          Back to builds
        </Button>
      </div>
    </div>
  </div>
);

export const BuildDetail = () => {
  const { buildId } = useParams<{ buildId: string }>();
  const { CONTROL_PLANE_ENABLED } = useSettings();
  const { selectedAppId } = useSelectedApp();
  const { toast } = useToast();
  const navigate = useNavigate();
  // Display gating only: the server re-checks the permission on its routes.
  const canRead = useAppPermission('build:read', 'any-member');
  const canDownload = useAppPermission('build:download', 'admin-only');

  const [isDownloading, setIsDownloading] = useState(false);

  const buildQuery = useQuery({
    queryKey: ['build', selectedAppId, buildId],
    queryFn: () => api.getBuild(buildId!),
    enabled: !!selectedAppId && !!buildId && CONTROL_PLANE_ENABLED && canRead,
    refetchInterval: query =>
      ['building', 'uploading'].includes(query.state.data?.status ?? '') ? 5000 : false,
  });
  const appDetailsQuery = useQuery({
    queryKey: ['appDetails', selectedAppId],
    queryFn: () => api.getApp(selectedAppId!),
    enabled: !!selectedAppId && CONTROL_PLANE_ENABLED,
  });

  const handleDownload = async (build: BuildRecord) => {
    setIsDownloading(true);
    try {
      const artifact = await api.downloadBuildArtifact(build.id);
      const url = URL.createObjectURL(artifact);
      const link = document.createElement('a');
      link.href = url;
      link.download = buildFileName(build);
      link.click();
      URL.revokeObjectURL(url);
    } catch (error) {
      const message = describeApiError(error, 'Could not download build');
      toast({ title: message.title, description: message.description, variant: 'destructive' });
    } finally {
      setIsDownloading(false);
    }
  };

  if (!CONTROL_PLANE_ENABLED) {
    return (
      <div className="w-full">
        <div className="rounded-xl border border-dashed bg-muted/30 p-8 text-center text-sm text-muted-foreground">
          Builds need a database to be stored in, so they are not available on a stateless
          deployment.
        </div>
      </div>
    );
  }

  if (!canRead) {
    return (
      <div className="w-full">
        <AdminOnlyNote>
          You do not have permission to view this app's builds. Ask an admin to grant you access.
        </AdminOnlyNote>
      </div>
    );
  }

  if (buildQuery.isLoading) {
    return (
      <div className="w-full space-y-4">
        <Skeleton className="h-8 w-72" />
        <Skeleton className="h-24 w-full rounded-xl" />
        <Skeleton className="h-48 w-full rounded-xl" />
        <Skeleton className="h-64 w-full rounded-xl" />
      </div>
    );
  }

  if (buildQuery.error) {
    if (buildQuery.error instanceof ApiProblemError && buildQuery.error.status === 404) {
      return <NotFound onBack={() => navigate('/builds')} />;
    }
    return (
      <div className="w-full">
        <ApiError error={buildQuery.error} onRetry={() => void buildQuery.refetch()} />
      </div>
    );
  }

  const build = buildQuery.data;
  if (!build) {
    return <NotFound onBack={() => navigate('/builds')} />;
  }

  const { metadata } = build;

  return (
    <div className="w-full space-y-6">
      <Breadcrumb>
        <BreadcrumbList>
          <BreadcrumbItem>
            <BreadcrumbLink className="cursor-pointer" onClick={() => navigate('/builds')}>
              Builds
            </BreadcrumbLink>
          </BreadcrumbItem>
          <BreadcrumbSeparator />
          <BreadcrumbItem>
            <BreadcrumbPage className="flex items-center gap-1.5">
              <PlatformLogo platform={build.platform} className="h-3.5 w-3.5" />
              {buildVersionLabel(build)}
            </BreadcrumbPage>
          </BreadcrumbItem>
        </BreadcrumbList>
      </Breadcrumb>

      <Card>
        <CardContent className="flex flex-col gap-4 py-5 sm:flex-row sm:items-center sm:justify-between">
          <div className="flex items-center gap-3">
            <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg border bg-muted/40">
              <PlatformLogo platform={build.platform} className="h-5 w-5" />
            </div>
            <div className="min-w-0">
              <div className="flex flex-wrap items-center gap-2">
                <span className="font-display text-lg font-semibold tracking-tight">
                  {buildVersionLabel(build)}
                </span>
                <Badge variant="outline">{artifactLabel(build)}</Badge>
                <BuildStatusBadge status={build.status} />
              </div>
              <p className="truncate font-mono text-sm text-muted-foreground">
                {build.applicationId}
              </p>
            </div>
          </div>
          <div className="flex shrink-0 items-center gap-2">
            {canDownload && (
              <Button
                disabled={build.status !== 'ready' || isDownloading}
                onClick={() => void handleDownload(build)}
                title={
                  build.status === 'ready'
                    ? `Download ${buildFileName(build)}`
                    : 'The build can be downloaded once it is ready.'
                }>
                <Download className="h-4 w-4" /> {isDownloading ? 'Downloading…' : 'Download'}
              </Button>
            )}
          </div>
        </CardContent>
      </Card>

      {build.status === 'failed' && (
        <div className="rounded-xl border border-destructive/30 bg-destructive/5 px-4 py-3 text-sm text-destructive">
          This build did not complete. Check its local log. If the artifact exists, retry its upload
          with eoas build:upload.
        </div>
      )}

      <div className="grid gap-6 lg:grid-cols-2">
        <DetailSection title="Build">
          <DetailRow label="Profile">{metadata.profile}</DetailRow>
          {metadata.mode && <DetailRow label="Mode">{metadata.mode}</DetailRow>}
          {metadata.environment && (
            <DetailRow label="Environment">{metadata.environment}</DetailRow>
          )}
          {metadata.channel && <DetailRow label="Channel">{metadata.channel}</DetailRow>}
          {metadata.version && <DetailRow label="Version">{metadata.version}</DetailRow>}
          <DetailRow label="Build number">{metadata.buildNumber || '—'}</DetailRow>
          {metadata.runtimeVersion && (
            <DetailRow label="Runtime version">
              <MonoValue value={shortRuntimeVersion(metadata.runtimeVersion)} />
              <CopyButton value={metadata.runtimeVersion} label="runtime version" />
            </DetailRow>
          )}
          {metadata.fingerprint && (
            <DetailRow label="Fingerprint">
              <MonoValue value={metadata.fingerprint} />
              <CopyButton value={metadata.fingerprint} label="fingerprint" />
            </DetailRow>
          )}
          {metadata.expoSdk && <DetailRow label="Expo SDK">{metadata.expoSdk}</DetailRow>}
          <DetailRow label="Command line version">{metadata.cliVersion}</DetailRow>
          <DetailRow label="Duration">
            {build.status === 'building' ? '—' : formatDuration(metadata.durationMs)}
          </DetailRow>
          <DetailRow label="Started">{formatTimestamp(metadata.startedAt, true) ?? '—'}</DetailRow>
          <DetailRow label="Finished">
            {metadata.finishedAt && !metadata.finishedAt.startsWith('0001-')
              ? formatTimestamp(metadata.finishedAt, true)
              : '—'}
          </DetailRow>
        </DetailSection>

        <div className="space-y-6">
          <DetailSection title="Source">
            {metadata.gitCommit ? (
              <>
                <DetailRow label="Commit">
                  <GitCommitLink
                    commitHash={metadata.gitCommit}
                    gitUrl={appDetailsQuery.data?.gitUrl}
                  />
                </DetailRow>
                {metadata.gitMessage && (
                  <div className="flex items-start justify-between gap-4 px-4 py-2.5">
                    <span className="shrink-0 text-sm text-muted-foreground">Message</span>
                    <p className="min-w-0 flex-1 whitespace-pre-wrap text-sm font-medium [overflow-wrap:anywhere]">
                      {metadata.gitMessage}
                    </p>
                  </div>
                )}
                <DetailRow label="Working tree">
                  {metadata.gitDirty ? (
                    <span className="text-amber-700 dark:text-amber-400">Uncommitted changes</span>
                  ) : (
                    'Clean'
                  )}
                </DetailRow>
              </>
            ) : (
              <div className="px-4 py-2.5 text-sm text-muted-foreground">
                No git information was recorded for this build.
              </div>
            )}
          </DetailSection>

          <DetailSection title="Artifact">
            <DetailRow label="Type">{artifactLabel(build)}</DetailRow>
            <DetailRow label="Size">{build.size ? formatBytes(build.size) : '—'}</DetailRow>
            {build.sha256 && (
              <DetailRow label="SHA-256">
                <MonoValue value={build.sha256} />
                <CopyButton value={build.sha256} label="checksum" />
              </DetailRow>
            )}
            <DetailRow label="Registered">
              {formatTimestamp(build.createdAt, true) ?? '—'}
            </DetailRow>
            {build.readyAt && (
              <DetailRow label="Ready">{formatTimestamp(build.readyAt, true) ?? '—'}</DetailRow>
            )}
            <DetailRow label="Built by">
              <span className="truncate" title={build.actorDisplay}>
                {build.actorDisplay}
              </span>
              <Badge variant="secondary" className="ml-1 font-normal">
                {actorTypeLabel(build.actorType)}
              </Badge>
            </DetailRow>
          </DetailSection>
        </div>
      </div>

    </div>
  );
};
