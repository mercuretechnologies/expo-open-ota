import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Copy, Pencil, Plus, Trash2 } from 'lucide-react';
import { api, ApiKeyRecord, ApiProblemError } from '@/lib/api';
import { useSelectedApp } from '@/lib/SelectedAppContext';
import { useSettings } from '@/lib/SettingsContext';
import { useToast } from '@/hooks/use-toast';
import { Button } from '@/components/ui/button';
import { PageHeader } from '@/components/PageHeader';
import { ResourceCreateForm } from '@/components/ui/resource-create-form';
import { DataTable } from '@/components/DataTable';
import { TimestampCell } from '@/components/ui/timestamp-cell';
import { DeleteDialog } from '@/components/ui/delete-dialog';
import { AdminOnlyNote } from '@/components/ui/admin-only-note';
import { useAppPermission } from '@/ee/lib/PermissionsContext';
import { ApiKeyAccessSheet } from '@/ee/components/ApiKeyAccessSheet';

export const ApiTokens = () => {
  const { CONTROL_PLANE_ENABLED } = useSettings();
  // Display gating only: the server re-checks the permission on its routes.
  const canManageApiKeys = useAppPermission('apikeys:manage', 'admin-only');
  const { selectedAppId } = useSelectedApp();
  const { toast } = useToast();
  const queryClient = useQueryClient();

  const [newKeyName, setNewKeyName] = useState('');
  const [generatedToken, setGeneratedToken] = useState<string | null>(null);
  const [isCreatingKey, setIsCreatingKey] = useState(false);
  const [keyToRevoke, setKeyToRevoke] = useState<ApiKeyRecord | null>(null);
  const [isRevokingKey, setIsRevokingKey] = useState(false);
  const [keyToRestrict, setKeyToRestrict] = useState<ApiKeyRecord | null>(null);

  const apiKeysQuery = useQuery({
    queryKey: ['apiKeys', selectedAppId],
    queryFn: () => api.getApiKeys(),
    enabled: !!selectedAppId && CONTROL_PLANE_ENABLED,
  });

  // What each token is allowed to do, summarized by domain. The
  // editing itself lives in ApiKeyAccessSheet.
  const apiKeyAccessQuery = useQuery({
    queryKey: ['apiKeyAccess', selectedAppId],
    queryFn: () => api.getApiKeyAccess(),
    enabled: !!selectedAppId && CONTROL_PLANE_ENABLED,
  });
  const accessByKeyId = new Map(
    (apiKeyAccessQuery.data ?? []).map(access => [access.apiKeyId, access])
  );

  const licenseQuery = useQuery({
    queryKey: ['license'],
    queryFn: () => api.getLicense(),
    enabled: CONTROL_PLANE_ENABLED,
  });

  const renderAccess = (apiKeyId: string, domain: 'updates' | 'build' | 'submit' | 'allowedIps') => {
    if (!licenseQuery.data) {
      return <span className="text-xs text-muted-foreground">{licenseQuery.isError ? 'Access unavailable' : 'Loading access…'}</span>;
    }
    if (!licenseQuery.data.valid) {
      return <span className="rounded-md bg-muted px-2 py-1 text-xs font-medium">Unrestricted</span>;
    }
    const access = accessByKeyId.get(apiKeyId);
    if (!access) {
      return <span className="text-xs text-muted-foreground">{apiKeyAccessQuery.isError ? 'Access unavailable' : 'Loading access…'}</span>;
    }
    const count = domain === 'allowedIps' ? access.allowedIps.length : access[domain].rules.length;
    const unit = { updates: 'branch rule', build: 'identifier', submit: 'destination', allowedIps: 'IP rule' }[domain];
    const empty = domain === 'updates' ? 'No access' : domain === 'allowedIps' ? 'Any IP' : 'Full access';
    return (
      <span className={`inline-flex h-6 items-center whitespace-nowrap rounded-md px-2 text-xs font-medium ${
        count > 0
          ? 'bg-primary/10 text-primary'
          : domain === 'updates' || domain === 'allowedIps'
            ? 'bg-muted text-muted-foreground'
            : 'bg-emerald-500/10 text-emerald-700 dark:text-emerald-300'
      }`}>
        {count > 0 ? `${count} ${unit}${count > 1 ? 's' : ''}` : empty}
      </span>
    );
  };

  const handleCreateApiKey = async () => {
    if (!newKeyName.trim()) return;
    setIsCreatingKey(true);
    try {
      const response = await api.createApiKey(newKeyName.trim());
      setGeneratedToken(response.apiKey);
      setNewKeyName('');
      queryClient.invalidateQueries({ queryKey: ['apiKeys', selectedAppId] });
      queryClient.invalidateQueries({ queryKey: ['apiKeyAccess', selectedAppId] });
    } catch (error) {
      let errorTitle = 'Error creating token';
      let errorMessage = 'An unexpected error occurred.';
      if (error instanceof ApiProblemError) {
        errorTitle = error.title;
        errorMessage = error.detail;
      } else if (error instanceof Error) {
        errorMessage = error.message;
      }
      toast({ title: errorTitle, description: errorMessage, variant: 'destructive' });
    } finally {
      setIsCreatingKey(false);
    }
  };

  const handleExecuteRevocation = async () => {
    if (!keyToRevoke) return;
    setIsRevokingKey(true);
    try {
      await api.revokeApiKey(keyToRevoke.id);
      queryClient.invalidateQueries({ queryKey: ['apiKeys', selectedAppId] });
      queryClient.invalidateQueries({ queryKey: ['apiKeyAccess', selectedAppId] });
      toast({
        title: 'Token revoked',
        description: `"${keyToRevoke.name}" can no longer be used.`,
      });
      setKeyToRevoke(null);
    } catch (error) {
      let errorTitle = 'Revocation failed';
      let errorMessage = 'Could not revoke the token.';
      if (error instanceof ApiProblemError) {
        errorTitle = error.title;
        errorMessage = error.detail;
      } else if (error instanceof Error) {
        errorMessage = error.message;
      }
      toast({ title: errorTitle, description: errorMessage, variant: 'destructive' });
    } finally {
      setIsRevokingKey(false);
    }
  };

  const handleCopyToken = async () => {
    if (!generatedToken) return;
    await navigator.clipboard.writeText(generatedToken);
    toast({ title: 'Copied', description: 'Token copied to your clipboard.' });
  };

  if (!CONTROL_PLANE_ENABLED) {
    return (
      <div className="w-full">
        <PageHeader
          title="API tokens"
          description="Tokens let external tools, like your CI pipeline, publish updates for this app."
        />
        <div className="rounded-xl border border-dashed bg-muted/30 p-8 text-center text-sm text-muted-foreground">
          On a stateless deployment, tokens are configured through environment variables, so there
          is nothing to manage here.
        </div>
      </div>
    );
  }

  return (
    <div className="w-full">
      <PageHeader
        title="API tokens"
        description="Tokens let external tools, like your CI pipeline, publish updates for this app. Each token is shown only once, at creation."
      />

      <div className="space-y-4">
        {!canManageApiKeys && (
          <AdminOnlyNote>
            You do not have permission to manage this app's tokens. Ask an admin to grant you
            access.
          </AdminOnlyNote>
        )}
        {canManageApiKeys && (
          <div className="flex justify-end">
            <ResourceCreateForm
              id="token-name"
              label="Token name"
              placeholder="Token name, e.g. ci-pipeline"
              inputValue={newKeyName}
              onInputChange={setNewKeyName}
              onSubmit={handleCreateApiKey}
              isSubmitting={isCreatingKey}
              buttonText="Create token"
              icon={Plus}
            />
          </div>
        )}

        {generatedToken && (
          <div className="rounded-xl border bg-muted/40 p-4">
            <p className="text-sm font-medium">Here is your new token</p>
            <p className="mt-0.5 text-xs text-muted-foreground">
              Copy it now, it will not be shown again.
              {licenseQuery.data?.valid &&
                ' New tokens have full Build and Submit access for this app, and no Updates access. Use Edit in the Access column to configure permissions.'}
            </p>
            <div className="mt-3 flex items-center gap-2">
              <code className="flex-1 select-all break-all rounded-lg border bg-background p-2.5 font-mono text-xs">
                {generatedToken}
              </code>
              <Button variant="outline" size="sm" onClick={handleCopyToken}>
                <Copy className="h-3.5 w-3.5" /> Copy
              </Button>
              <Button
                variant="ghost"
                size="sm"
                onClick={() => setGeneratedToken(null)}
                className="text-muted-foreground">
                Dismiss
              </Button>
            </div>
          </div>
        )}

        <DataTable
          loading={apiKeysQuery.isLoading}
          columns={[
            {
              header: 'Name',
              accessorKey: 'name',
              cell: ({ row }) => <span className="font-medium">{row.original.name}</span>,
            },
            {
              header: 'Token',
              accessorKey: 'hint',
              cell: ({ row }) => (
                <span className="font-mono text-xs text-muted-foreground">
                  {row.original.hint}…
                </span>
              ),
            },
            {
              header: 'Created',
              accessorKey: 'createdAt',
              cell: ({ row }) => <TimestampCell dateString={row.original.createdAt} />,
            },
            {
              header: 'Last used',
              accessorKey: 'lastUsedAt',
              cell: ({ row }) => {
                const lastUsed = row.original.lastUsedAt;
                if (!lastUsed) {
                  return <span className="text-muted-foreground/60">Never</span>;
                }
                return <TimestampCell dateString={lastUsed} />;
              },
            },
            ...(['updates', 'build', 'submit'] as const).map(domain => ({
              header: { updates: 'Updates', build: 'Build', submit: 'Submit' }[domain],
              id: domain,
              cell: ({ row }: { row: { original: ApiKeyRecord } }) => renderAccess(row.original.id, domain),
            })),
            ...(licenseQuery.data?.valid && apiKeyAccessQuery.data?.some(access => access.allowedIps.length > 0)
              ? [{
                  header: 'IP allowlist',
                  id: 'allowedIps',
                  cell: ({ row }: { row: { original: ApiKeyRecord } }) => renderAccess(row.original.id, 'allowedIps'),
                }]
              : []),
            ...(canManageApiKeys
              ? [
                  {
                    header: '',
                    id: 'actions',
                    cell: ({ row }: { row: { original: ApiKeyRecord } }) => (
                      <div className="flex justify-end gap-1">
                        <Button
                          variant="ghost"
                          size="icon"
                          onClick={() => setKeyToRestrict(row.original)}
                          className="h-8 w-8 text-muted-foreground"
                          title="Edit access"
                          aria-label={`Edit access for ${row.original.name}`}>
                          <Pencil />
                        </Button>
                        <Button
                          variant="ghost"
                          size="sm"
                          onClick={() => setKeyToRevoke(row.original)}
                          className="h-8 w-8 p-0 text-muted-foreground hover:bg-destructive/10 hover:text-destructive"
                          title="Revoke token"
                          aria-label={`Revoke token ${row.original.name}`}>
                          <Trash2 />
                        </Button>
                      </div>
                    ),
                  },
                ]
              : []),
          ]}
          data={apiKeysQuery.data ?? []}
          emptyMessage="No tokens yet. Create one to publish updates from your CI."
        />
      </div>

      <ApiKeyAccessSheet apiKey={keyToRestrict} onClose={() => setKeyToRestrict(null)} />

      <DeleteDialog
        isOpen={!!keyToRevoke}
        onClose={() => setKeyToRevoke(null)}
        onConfirm={handleExecuteRevocation}
        isDeleting={isRevokingKey}
        title="Revoke token"
        resourceName={keyToRevoke?.name}
        descriptionText="Anything still using this token (CI jobs, scripts, integrations) will stop working immediately. This cannot be undone."
        confirmButtonText="Revoke token"
        isDeletingButtonText="Revoking…"
      />
    </div>
  );
};
