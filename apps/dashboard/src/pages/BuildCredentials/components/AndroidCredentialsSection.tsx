import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Download, ExternalLink, KeyRound, Pencil } from 'lucide-react';
import { ApiError } from '@/components/APIError';
import { api, AndroidCredentialsMetadata, AppIdentifier, describeApiError } from '@/lib/api';
import { useSelectedApp } from '@/lib/SelectedAppContext';
import { useToast } from '@/hooks/use-toast';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Skeleton } from '@/components/ui/skeleton';
import { TimestampCell } from '@/components/ui/timestamp-cell';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { AndroidCredentialsForm } from './AndroidCredentialsForm';
import { GooglePlayServiceAccountCard } from './GooglePlayServiceAccountCard';

const PLAY_SIGNING_DOCS =
  'https://support.google.com/googleplay/android-developer/answer/9842756?hl=en';

type Props = {
  identifier: AppIdentifier;
  canManage: boolean;
};

const MetadataRow = ({ label, value }: { label: string; value: React.ReactNode }) => (
  <div className="flex items-center justify-between gap-4 py-2.5">
    <span className="text-sm text-muted-foreground">{label}</span>
    <span className="text-sm font-medium">{value}</span>
  </div>
);

export const AndroidCredentialsSection = ({ identifier, canManage }: Props) => {
  const { selectedAppId } = useSelectedApp();
  const { toast } = useToast();
  const queryClient = useQueryClient();

  const [isReplacing, setIsReplacing] = useState(false);
  const [isGenerateDialogOpen, setIsGenerateDialogOpen] = useState(false);
  const [isGenerating, setIsGenerating] = useState(false);
  const [isDownloading, setIsDownloading] = useState(false);

  const credentialsQuery = useQuery({
    queryKey: ['androidCredentials', selectedAppId, identifier.id],
    queryFn: () => api.getAndroidCredentials(identifier.id),
    enabled: !!selectedAppId,
  });

  const invalidate = () => {
    queryClient.invalidateQueries({ queryKey: ['identifiers', selectedAppId] });
    queryClient.invalidateQueries({
      queryKey: ['androidCredentials', selectedAppId, identifier.id],
    });
  };

  const handleGenerateCredentials = async () => {
    setIsGenerating(true);
    try {
      await api.generateAndroidCredentials(identifier.id);
      invalidate();
      toast({
        title: 'Keystore replaced',
        description:
          'The new upload keystore is ready. If Google Play already has a build for this app, reset its upload key before the next upload.',
      });
      setIsGenerateDialogOpen(false);
    } catch (error) {
      const message = describeApiError(error, 'Error generating keystore');
      toast({ title: message.title, description: message.description, variant: 'destructive' });
    } finally {
      setIsGenerating(false);
    }
  };

  const handleDownloadKeystore = async () => {
    setIsDownloading(true);
    try {
      const archive = await api.downloadAndroidKeystore(identifier.id);
      const url = URL.createObjectURL(archive);
      const link = document.createElement('a');
      link.href = url;
      link.download = `${identifier.identifier}-android-keystore.zip`;
      link.click();
      URL.revokeObjectURL(url);
    } catch (error) {
      const message = describeApiError(error, 'Error downloading keystore');
      toast({ title: message.title, description: message.description, variant: 'destructive' });
    } finally {
      setIsDownloading(false);
    }
  };

  if (credentialsQuery.isPending) {
    return <Skeleton className="h-48 w-full rounded-xl" />;
  }

  if (credentialsQuery.isError) {
    return (
      <ApiError error={credentialsQuery.error} onRetry={() => void credentialsQuery.refetch()} />
    );
  }

  const metadata: AndroidCredentialsMetadata | null | undefined = credentialsQuery.data;

  if (!metadata) {
    return (
      <div className="space-y-4">
        {canManage ? (
          <AndroidCredentialsForm
            identifierId={identifier.id}
            mode="setup"
            onSaved={() => setIsReplacing(false)}
          />
        ) : (
          <div className="rounded-xl border border-dashed bg-muted/30 p-8 text-center text-sm text-muted-foreground">
            No build credentials are configured for this identifier yet. Ask an admin to set them
            up.
          </div>
        )}
        <GooglePlayServiceAccountCard
          identifierId={identifier.id}
          identifier={identifier.identifier}
          hasKey={false}
          canManage={canManage}
          disabledReason="Configure an Android signing keystore before adding a Google Play service account."
          onChanged={invalidate}
        />
      </div>
    );
  }

  if (isReplacing) {
    return (
      <AndroidCredentialsForm
        identifierId={identifier.id}
        mode="replace"
        initialKeyAlias={metadata.keyAlias}
        onCancel={() => setIsReplacing(false)}
        onSaved={() => setIsReplacing(false)}
      />
    );
  }

  return (
    <div className="space-y-4">
      <Card>
        <CardHeader className="flex-row items-center justify-between space-y-0 border-b py-4">
          <CardTitle className="text-base">Signing keystore</CardTitle>
          {canManage && (
            <div className="flex flex-wrap gap-2">
              <Button
                variant="outline"
                size="sm"
                onClick={handleDownloadKeystore}
                disabled={isDownloading}>
                <Download className="h-3.5 w-3.5" /> {isDownloading ? 'Downloading…' : 'Download'}
              </Button>
              <Button variant="outline" size="sm" onClick={() => setIsGenerateDialogOpen(true)}>
                <KeyRound className="h-3.5 w-3.5" /> Generate new
              </Button>
              <Button variant="outline" size="sm" onClick={() => setIsReplacing(true)}>
                <Pencil className="h-3.5 w-3.5" /> Replace
              </Button>
            </div>
          )}
        </CardHeader>
        <CardContent className="divide-y pt-2">
          <MetadataRow
            label="Key alias"
            value={<span className="font-mono text-xs">{metadata.keyAlias}</span>}
          />
          <MetadataRow label="Created" value={<TimestampCell dateString={metadata.createdAt} />} />
          <MetadataRow label="Updated" value={<TimestampCell dateString={metadata.updatedAt} />} />
          <p className="py-3 text-xs leading-relaxed text-muted-foreground">
            For the app's first Google Play upload, no additional key setup is required. If Google
            Play has already accepted a build for this app, request an upload key reset before using
            a replacement keystore. The same upload key is used across all Play release tracks.{' '}
            <a
              href={PLAY_SIGNING_DOCS}
              target="_blank"
              rel="noreferrer"
              className="inline-flex items-center gap-1 font-medium text-foreground underline underline-offset-4">
              Google Play signing guide <ExternalLink className="h-3 w-3" />
            </a>
          </p>
        </CardContent>
      </Card>

      <GooglePlayServiceAccountCard
        identifierId={identifier.id}
        identifier={identifier.identifier}
        hasKey={metadata.hasGoogleServiceAccountKey}
        serviceAccountEmail={metadata.googleServiceAccountEmail}
        projectId={metadata.googleServiceAccountProjectId}
        canManage={canManage}
        onChanged={invalidate}
      />

      <Dialog open={isGenerateDialogOpen} onOpenChange={setIsGenerateDialogOpen}>
        <DialogContent className="sm:max-w-[460px]">
          <DialogHeader>
            <DialogTitle>Replace the upload keystore?</DialogTitle>
            <DialogDescription className="space-y-3 pt-2">
              <span className="block">
                xprem will permanently replace the current keystore and passwords with newly
                generated credentials.
              </span>
              <span className="block font-medium text-foreground">
                If this app has never been uploaded to Google Play, no reset is needed. If Google
                Play has already accepted a build, request an upload key reset before the next
                upload.
              </span>
              <span className="block">
                Download the current keystore first if you need a backup.
              </span>
            </DialogDescription>
          </DialogHeader>
          <DialogFooter className="border-t pt-4">
            <Button
              variant="outline"
              onClick={() => setIsGenerateDialogOpen(false)}
              disabled={isGenerating}>
              Cancel
            </Button>
            <Button
              variant="destructive"
              onClick={handleGenerateCredentials}
              disabled={isGenerating}>
              {isGenerating ? 'Generating…' : 'Generate and replace'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
};
