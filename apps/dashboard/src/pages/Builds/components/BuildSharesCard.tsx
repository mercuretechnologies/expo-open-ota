import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Link2, Link2Off, Plus } from 'lucide-react';
import { api, BuildRecord, BuildShareRecord, describeApiError } from '@/lib/api';
import { useSelectedApp } from '@/lib/SelectedAppContext';
import { useToast } from '@/hooks/use-toast';
import { formatTimestamp } from '@/lib/utils';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Skeleton } from '@/components/ui/skeleton';
import { ApiError } from '@/components/APIError';
import { AdminOnlyNote } from '@/components/ui/admin-only-note';
import { canShareBuild, shareState, ShareState } from '../format';

const stateBadge: Record<ShareState, { label: string; className: string }> = {
  active: {
    label: 'Active',
    className: 'border-emerald-400/25 bg-emerald-400/10 text-emerald-700 dark:text-emerald-300',
  },
  expired: {
    label: 'Expired',
    className: 'border-border bg-muted text-muted-foreground',
  },
  revoked: {
    label: 'Revoked',
    className: 'border-border bg-muted text-muted-foreground',
  },
};

const ShareRow = ({
  share,
  onRevoke,
  revoking,
}: {
  share: BuildShareRecord;
  onRevoke: () => void;
  revoking: boolean;
}) => {
  const state = shareState(share);
  const badge = stateBadge[state];
  return (
    <li className="flex flex-col gap-2 px-3 py-2.5 sm:flex-row sm:items-center sm:justify-between">
      <div className="min-w-0 space-y-1">
        <div className="flex items-center gap-2">
          <Link2 className="h-4 w-4 shrink-0 text-muted-foreground" />
          <span className="text-sm font-medium">Created {formatTimestamp(share.createdAt)}</span>
          <Badge variant="outline" className={badge.className}>
            {badge.label}
          </Badge>
        </div>
        <p className="text-xs text-muted-foreground">
          {state === 'revoked'
            ? `Revoked ${formatTimestamp(share.revokedAt)}`
            : state === 'expired'
              ? `Expired ${formatTimestamp(share.expiresAt)}`
              : `Expires ${formatTimestamp(share.expiresAt)}`}
        </p>
      </div>
      {state === 'active' && (
        <Button
          variant="ghost"
          size="sm"
          disabled={revoking}
          onClick={onRevoke}
          className="h-8 text-muted-foreground hover:bg-destructive/10 hover:text-destructive"
          title="Revoke install link">
          <Link2Off className="h-3.5 w-3.5" />
          {revoking ? 'Revoking…' : 'Revoke'}
        </Button>
      )}
    </li>
  );
};

export const BuildSharesCard = ({
  build,
  canShare,
  onCreateShare,
}: {
  build: BuildRecord;
  canShare: boolean;
  onCreateShare: () => void;
}) => {
  const { selectedAppId } = useSelectedApp();
  const { toast } = useToast();
  const queryClient = useQueryClient();
  const [revoking, setRevoking] = useState<string | null>(null);

  const shareable = canShareBuild(build);
  const sharesQuery = useQuery({
    queryKey: ['build-shares', selectedAppId, build.id],
    queryFn: () => api.getBuildShares(build.id),
    enabled: !!selectedAppId && canShare && build.artifactType === 'apk',
  });
  const shares = sharesQuery.data?.shares ?? [];
  const activeCount = shares.filter(share => shareState(share) === 'active').length;

  const revoke = async (share: BuildShareRecord) => {
    setRevoking(share.id);
    try {
      await api.revokeBuildShare(build.id, share.id);
      await queryClient.invalidateQueries({ queryKey: ['build-shares', selectedAppId, build.id] });
      toast({
        title: 'Install link revoked',
        description: 'This link no longer allows downloads.',
      });
    } catch (error) {
      const message = describeApiError(error, 'Could not revoke install link');
      toast({ title: message.title, description: message.description, variant: 'destructive' });
    } finally {
      setRevoking(null);
    }
  };

  return (
    <Card>
      <CardHeader className="flex-row items-center justify-between space-y-0 border-b py-4">
        <div className="space-y-1">
          <CardTitle className="flex items-center gap-2 text-base">
            <Link2 className="h-4 w-4 text-muted-foreground" />
            Install links
            {canShare && build.artifactType === 'apk' && (
              <Badge variant="secondary" className="ml-1 font-normal">
                {activeCount} active
              </Badge>
            )}
          </CardTitle>
          <CardDescription>
            Links and QR codes that install this build on a device without signing in.
          </CardDescription>
        </div>
        {canShare && shareable && (
          <Button size="sm" onClick={onCreateShare}>
            <Plus className="h-3.5 w-3.5" /> New install link
          </Button>
        )}
      </CardHeader>
      <CardContent className="space-y-4 pt-4">
        {build.artifactType !== 'apk' ? (
          <p className="text-sm text-muted-foreground">
            Only APK builds can be installed from a link. This build is an app bundle meant for the
            Play Store.
          </p>
        ) : (
          <>
            {!canShare ? (
              <AdminOnlyNote>
                You do not have permission to share this app's builds. Ask an admin to grant you
                access.
              </AdminOnlyNote>
            ) : (
              <>
                {build.status !== 'ready' && (
                  <p className="text-sm text-muted-foreground">
                    Install links can be created once the build is ready.
                  </p>
                )}
                {sharesQuery.isLoading ? (
                  <Skeleton className="h-10 w-full" />
                ) : sharesQuery.error ? (
                  <ApiError error={sharesQuery.error} onRetry={() => void sharesQuery.refetch()} />
                ) : shares.length === 0 ? (
                  <p className="text-sm text-muted-foreground">No install link yet.</p>
                ) : (
                  <ul className="divide-y rounded-lg border">
                    {shares.map(share => (
                      <ShareRow
                        key={share.id}
                        share={share}
                        revoking={revoking === share.id}
                        onRevoke={() => void revoke(share)}
                      />
                    ))}
                  </ul>
                )}
                {shares.length > 0 && (
                  <p className="text-xs text-muted-foreground">
                    A link is shown only when it is created. To hand this build to someone new,
                    create another link.
                  </p>
                )}
              </>
            )}
          </>
        )}
      </CardContent>
    </Card>
  );
};
