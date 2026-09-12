import { useInfiniteQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { useSelectedApp } from '@/lib/SelectedAppContext';
import { useSettings } from '@/lib/SettingsContext';
import { ApiError } from '@/components/APIError';
import { AdminOnlyNote } from '@/components/ui/admin-only-note';
import { useAppPermission } from '@/ee/lib/PermissionsContext';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import { PageHeader } from '@/components/PageHeader';
import { BuildsTable } from './components/BuildsTable';

const BUILDS_PAGE_SIZE = 20;

export const Builds = () => {
  const { CONTROL_PLANE_ENABLED } = useSettings();
  const { selectedAppId } = useSelectedApp();
  const canRead = useAppPermission('build:read', 'any-member');

  const buildsQuery = useInfiniteQuery({
    queryKey: ['builds', selectedAppId, BUILDS_PAGE_SIZE],
    queryFn: ({ pageParam }) => api.getBuilds(BUILDS_PAGE_SIZE, pageParam),
    initialPageParam: 0,
    getNextPageParam: (lastPage, allPages) => {
      const loaded = allPages.reduce((total, page) => total + page.builds.length, 0);
      return lastPage.builds.length > 0 && loaded < lastPage.count ? loaded : undefined;
    },
    enabled: !!selectedAppId && CONTROL_PLANE_ENABLED && canRead,
    refetchInterval: 10000,
  });
  const builds = buildsQuery.data?.pages.flatMap(page => page.builds) ?? [];
  const count = buildsQuery.data?.pages[0]?.count;

  if (!CONTROL_PLANE_ENABLED) {
    return (
      <div className="w-full">
        <PageHeader title="Builds" description="Local builds of your app." />
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
        <PageHeader title="Builds" description="Local builds of your app." />
        <AdminOnlyNote>
          You do not have permission to view this app's builds. Ask an admin to grant you access.
        </AdminOnlyNote>
      </div>
    );
  }

  if (buildsQuery.isError) {
    return <ApiError error={buildsQuery.error} onRetry={() => void buildsQuery.refetch()} />;
  }

  return (
    <div className="w-full">
      <PageHeader
        title={
          <span className="flex items-center gap-3">
            Builds
            {count !== undefined && (
              <Badge variant="secondary" className="font-normal">
                {count}
              </Badge>
            )}
          </span>
        }
        description="Local builds of your app. Open one to view its metadata or download it."
      />

      <BuildsTable builds={builds} loading={buildsQuery.isLoading} />

      {buildsQuery.hasNextPage && (
        <div className="mt-4 flex justify-center">
          <Button
            variant="outline"
            size="sm"
            disabled={buildsQuery.isFetchingNextPage}
            onClick={() => void buildsQuery.fetchNextPage()}>
            {buildsQuery.isFetchingNextPage ? 'Loading…' : 'Load more'}
          </Button>
        </div>
      )}
    </div>
  );
};
