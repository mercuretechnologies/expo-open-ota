import { useQuery } from '@tanstack/react-query';
import { api, UpdateHealthRecord, UpdateRecord } from '@/lib/api.ts';
import { ApiError } from '@/components/APIError';
import { DataTable } from '@/components/DataTable';
import { Badge } from '@/components/ui/badge.tsx';
import apple from '@/assets/apple.svg';
import android from '@/assets/android.svg';
import { UpdateDetailsRef, UpdateDetailsSheet } from '@/components/UpdateDetailsSheet';
import { useRef } from 'react';
import { useSelectedApp } from '@/lib/SelectedAppContext';
import { useSettings } from '@/lib/SettingsContext';
import { useAppPermission } from '@/ee/lib/PermissionsContext';
import { TimestampCell } from '@/components/ui/timestamp-cell';
import { UpdatesBreadcrumb } from '@/pages/Updates/components/UpdatesBreadcrumb';
import { UpdateRolloutCard } from '@/pages/Updates/components/UpdateRolloutCard';
import { HealthBadge } from '@/pages/Updates/components/HealthBadge';

export const UpdatesTable = ({
  branch,
  runtimeVersion,
  showBreadcrumb = true,
}: {
  branch: string;
  runtimeVersion: string;
  showBreadcrumb?: boolean;
}) => {
  const sheetRef = useRef<UpdateDetailsRef>(null);
  const { selectedAppId } = useSelectedApp();
  const { CONTROL_PLANE_ENABLED } = useSettings();
  const canManageUpdateRollout = useAppPermission('update-rollout:manage');
  const { data, isLoading, error } = useQuery({
    queryKey: ['updates', selectedAppId, branch, runtimeVersion],
    queryFn: () => api.getUpdates(branch, runtimeVersion),
    enabled: !!selectedAppId,
  });

  // Rollout state is read fresh (control-plane only). It drives the card above
  // the table and the "Control" markers in the passive Rollout column.
  const rolloutQuery = useQuery({
    queryKey: ['update-rollout', selectedAppId, branch, runtimeVersion],
    queryFn: () => api.getUpdateRollout(branch, runtimeVersion),
    enabled: !!selectedAppId && CONTROL_PLANE_ENABLED,
  });
  const activeRollout = rolloutQuery.data?.active ? rolloutQuery.data.updates : [];
  const controlIds = new Set(activeRollout.map(u => u.controlUpdateId).filter(Boolean));

  // Update health (adoption + launch failures) comes from the device
  // registry, uncached server-side. Rollback rows carry a literal
  // "Rollback to embedded" instead of a UUID: they have no health and must
  // not reach the endpoint. The server caps one request at 100 ids, so only
  // the newest 100 updates are scored (older ones cannot show a meaningful
  // score anyway).
  const isUuid = (value: string) =>
    /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(value);
  const updateUUIDs = [...(data ?? [])]
    .filter(u => isUuid(u.updateUUID))
    .sort((a, b) => b.createdAt.localeCompare(a.createdAt))
    .slice(0, 100)
    .map(u => u.updateUUID);
  // Poll fast while a rollout is live (the moment the score is being
  // watched), lazily otherwise.
  const healthQuery = useQuery({
    queryKey: ['update-health', selectedAppId, branch, runtimeVersion, updateUUIDs.join(',')],
    queryFn: () => api.getUpdateHealth(updateUUIDs),
    enabled: !!selectedAppId && CONTROL_PLANE_ENABLED && updateUUIDs.length > 0,
    refetchInterval: activeRollout.length > 0 ? 5_000 : 60_000,
  });
  const healthByUuid = healthQuery.data?.updates;

  // The health score is only shown where it is meaningful: the newest update
  // OF EACH PLATFORM (ios and android sibling publishes are distinct updates
  // with distinct UUIDs, and their timestamps tie at second precision), or an
  // update currently mid-rollout.
  const newestUuidByPlatform = new Map<string, string>();
  for (const u of data ?? []) {
    if (!isUuid(u.updateUUID)) continue;
    const current = (data ?? []).find(x => x.updateUUID === newestUuidByPlatform.get(u.platform));
    if (!current || u.createdAt.localeCompare(current.createdAt) >= 0) {
      newestUuidByPlatform.set(u.platform, u.updateUUID);
    }
  }
  const newestUuids = new Set(newestUuidByPlatform.values());

  // A rollout spans one row per platform, each a DISTINCT update: aggregate
  // devices and failures across the whole rollout set (and the control set)
  // so an iOS-only crash storm cannot hide behind a healthy Android row.
  const byNumericId = new Map((data ?? []).map(u => [u.updateId, u]));
  const aggregateHealth = (uuids: string[]): UpdateHealthRecord | undefined => {
    const entries = uuids
      .map(uuid => healthByUuid?.[uuid])
      .filter((entry): entry is UpdateHealthRecord => !!entry);
    if (entries.length === 0) return undefined;
    const devicesOnUpdate = entries.reduce((sum, e) => sum + e.devicesOnUpdate, 0);
    const launchFailures = entries.reduce((sum, e) => sum + e.launchFailures, 0);
    const attempts = devicesOnUpdate + launchFailures;
    return {
      devicesOnUpdate,
      launchFailures,
      healthPercent: attempts > 0 ? (100 * devicesOnUpdate) / attempts : null,
    };
  };
  const rolloutUuids = activeRollout
    .map(r => byNumericId.get(r.updateId)?.updateUUID)
    .filter((uuid): uuid is string => !!uuid && isUuid(uuid));
  const controlUuids = activeRollout
    .map(r => (r.controlUpdateId ? byNumericId.get(r.controlUpdateId)?.updateUUID : undefined))
    .filter((uuid): uuid is string => !!uuid && isUuid(uuid));
  const rolloutHealth = aggregateHealth(rolloutUuids);
  const controlHealth = aggregateHealth(controlUuids);

  return (
    <div className="w-full flex-1">
      {showBreadcrumb && <UpdatesBreadcrumb branch={branch} runtimeVersion={runtimeVersion} />}
      {!!error && <ApiError error={error} />}
      {!!rolloutQuery.error && <ApiError error={rolloutQuery.error} />}
      {!!healthQuery.error && <ApiError error={healthQuery.error} />}
      {CONTROL_PLANE_ENABLED && activeRollout.length > 0 && (
        <UpdateRolloutCard
          branch={branch}
          runtimeVersion={runtimeVersion}
          updates={activeRollout}
          canManageRollout={canManageUpdateRollout}
          rolloutHealth={rolloutHealth}
          controlHealth={controlHealth}
        />
      )}
      <UpdateDetailsSheet ref={sheetRef} branch={branch} runtimeVersion={runtimeVersion} />
      <DataTable
        loading={isLoading}
        columns={[
          {
            header: 'Update',
            accessorKey: 'updateId',
            cell: ({ row }) => <span className="font-medium">{row.original.updateId}</span>,
          },
          {
            header: 'UUID',
            accessorKey: 'updateUUID',
            cell: ({ row }) => (
              <span className="font-mono text-xs text-muted-foreground">
                {row.original.updateUUID}
              </span>
            ),
          },
          {
            header: 'Platform',
            accessorKey: 'platform',
            cell: ({ row }) => {
              const isIos = row.original.platform === 'ios';
              const isAndroid = row.original.platform === 'android';
              return (
                <div className="flex items-center gap-2">
                  {isIos && <img src={apple} className="w-4 brightness-0 dark:invert" alt="iOS" />}
                  {isAndroid && (
                    <img src={android} className="w-4 brightness-0 dark:invert" alt="Android" />
                  )}
                </div>
              );
            },
          },
          {
            header: 'Message',
            accessorKey: 'message',
            cell: ({ row }) => {
              const msg = row.original.message;
              return msg ? (
                <span className="block max-w-[200px] truncate text-sm text-muted-foreground">
                  {msg}
                </span>
              ) : (
                <span className="text-sm text-muted-foreground/60">No message</span>
              );
            },
          },
          {
            header: 'Commit',
            accessorKey: 'commitHash',
            cell: ({ row }) => {
              return (
                <Badge variant="outline" className="font-mono text-xs">
                  {row.original.commitHash.slice(0, 7)}
                </Badge>
              );
            },
          },
          ...(CONTROL_PLANE_ENABLED
            ? [
                {
                  // Every device currently running this update, straight
                  // from the device registry.
                  header: 'Devices',
                  id: 'devices',
                  cell: ({ row }: { row: { original: UpdateRecord } }) => {
                    const health = healthByUuid?.[row.original.updateUUID];
                    if (!health) {
                      return <span className="text-muted-foreground/40">-</span>;
                    }
                    return (
                      <span className="text-sm tabular-nums">
                        {health.devicesOnUpdate.toLocaleString()}
                      </span>
                    );
                  },
                },
                {
                  header: 'Health',
                  id: 'health',
                  cell: ({ row }: { row: { original: UpdateRecord } }) => {
                    const update = row.original;
                    const scored =
                      newestUuids.has(update.updateUUID) || update.rolloutPercentage != null;
                    if (!scored) {
                      return <span className="text-muted-foreground/40">-</span>;
                    }
                    return <HealthBadge health={healthByUuid?.[update.updateUUID]} />;
                  },
                },
                {
                  header: 'Rollout',
                  id: 'rollout',
                  cell: ({ row }: { row: { original: UpdateRecord } }) => {
                    const update = row.original;
                    if (update.rolloutPercentage != null) {
                      return (
                        <Badge className="border-emerald-400/25 bg-emerald-400/10 text-emerald-700 dark:text-emerald-300">
                          {update.rolloutPercentage}% rollout
                        </Badge>
                      );
                    }
                    // A rollout used to run on this update but has ended
                    // (finished or reverted, the record does not distinguish).
                    if (update.controlUpdateId != null) {
                      return (
                        <span className="text-xs text-muted-foreground/60">Rollout ended</span>
                      );
                    }
                    // This update is the control an active rollout falls back to.
                    if (controlIds.has(update.updateId)) {
                      return <Badge variant="outline">Control</Badge>;
                    }
                    return <span className="text-muted-foreground/40">None</span>;
                  },
                },
              ]
            : []),
          {
            header: 'Published',
            accessorKey: 'createdAt',
            cell: ({ row }) => <TimestampCell dateString={row.original.createdAt} showSeconds />,
          },
        ]}
        data={data ?? []}
        defaultSorting={[{ id: 'createdAt', desc: true }]}
        emptyMessage="No updates published for this runtime version yet."
        onRowClick={row => {
          sheetRef?.current?.openSheet(row);
        }}
      />
    </div>
  );
};
