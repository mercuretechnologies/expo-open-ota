import { useState } from 'react';
import { AlertTriangle, CheckCircle2, ExternalLink, Pencil, Trash2 } from 'lucide-react';
import { api, describeApiError } from '@/lib/api';
import { useToast } from '@/hooks/use-toast';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { DeleteDialog } from '@/components/ui/delete-dialog';
import { FilePickerRow } from './AndroidCredentialsForm';

const PLAY_SERVICE_ACCOUNT_DOCS =
  'https://developers.google.com/android-publisher/getting_started?hl=en';

type Props = {
  identifierId: string;
  identifier: string;
  hasKey: boolean;
  serviceAccountEmail?: string;
  projectId?: string;
  canManage: boolean;
  disabledReason?: string;
  onChanged: () => void;
};

const MetadataRow = ({ label, value }: { label: string; value: string }) => (
  <div className="flex items-center justify-between gap-4 py-2.5">
    <span className="text-sm text-muted-foreground">{label}</span>
    <span className="font-mono text-xs font-medium">{value}</span>
  </div>
);

const ServiceAccountHelp = () => (
  <p className="py-3 text-xs leading-relaxed text-muted-foreground">
    Google requires the app and its first release to be set up in Play Console. To automate later
    submissions, create a service account, download its JSON key, then grant its email access in
    Play Console.{' '}
    <a
      href={PLAY_SERVICE_ACCOUNT_DOCS}
      target="_blank"
      rel="noreferrer"
      className="inline-flex items-center gap-1 font-medium text-foreground underline underline-offset-4">
      Google Play API setup guide <ExternalLink className="h-3 w-3" />
    </a>
  </p>
);

export const GooglePlayServiceAccountCard = ({
  identifierId,
  identifier,
  hasKey,
  serviceAccountEmail,
  projectId,
  canManage,
  disabledReason,
  onChanged,
}: Props) => {
  const { toast } = useToast();
  const [serviceAccountKey, setServiceAccountKey] = useState('');
  const [fileName, setFileName] = useState<string | null>(null);
  const [isReplacing, setIsReplacing] = useState(false);
  const [isSaving, setIsSaving] = useState(false);
  const [isDeleting, setIsDeleting] = useState(false);
  const [isDeleteDialogOpen, setIsDeleteDialogOpen] = useState(false);

  const clearSelection = () => {
    setServiceAccountKey('');
    setFileName(null);
  };

  const handlePick = async (file: File) => {
    if (disabledReason) return;
    try {
      const contents = await file.text();
      JSON.parse(contents);
      setServiceAccountKey(contents);
      setFileName(file.name);
    } catch {
      toast({
        title: 'Invalid service account key',
        description: 'The selected file could not be read or is not valid JSON.',
        variant: 'destructive',
      });
    }
  };

  const handleSave = async () => {
    if (disabledReason || !serviceAccountKey) return;
    setIsSaving(true);
    try {
      await api.saveGooglePlayServiceAccountKey(identifierId, serviceAccountKey);
      onChanged();
      clearSelection();
      setIsReplacing(false);
      toast({
        title: 'Service account saved',
        description: 'Google Play submissions can now use this service account.',
      });
    } catch (error) {
      const message = describeApiError(error, 'Error saving service account');
      toast({ title: message.title, description: message.description, variant: 'destructive' });
    } finally {
      setIsSaving(false);
    }
  };

  const handleDelete = async () => {
    setIsDeleting(true);
    try {
      await api.deleteGooglePlayServiceAccountKey(identifierId);
      onChanged();
      setIsDeleteDialogOpen(false);
      setIsReplacing(false);
      clearSelection();
      toast({
        title: 'Service account removed',
        description: 'The signing keystore was not changed.',
      });
    } catch (error) {
      const message = describeApiError(error, 'Error removing service account');
      toast({ title: message.title, description: message.description, variant: 'destructive' });
    } finally {
      setIsDeleting(false);
    }
  };

  const showEditor = !hasKey || isReplacing;

  return (
    <>
      <Card className={disabledReason ? 'opacity-60' : undefined}>
        <CardHeader className="flex-row items-center justify-between space-y-0 border-b py-4">
          <div className="flex items-center gap-3">
            <CardTitle className="text-base">Google Play service account</CardTitle>
            {hasKey ? (
              <span className="inline-flex items-center gap-1.5 text-sm font-medium text-emerald-700 dark:text-emerald-300">
                <CheckCircle2 className="h-4 w-4" /> Configured
              </span>
            ) : (
              <span className="text-sm text-muted-foreground">Not configured</span>
            )}
          </div>
          {hasKey && canManage && !isReplacing && (
            <div className="flex gap-2">
              <Button variant="outline" size="sm" onClick={() => setIsReplacing(true)}>
                <Pencil className="h-3.5 w-3.5" /> Replace
              </Button>
              <Button
                variant="ghost"
                size="sm"
                onClick={() => setIsDeleteDialogOpen(true)}
                className="text-muted-foreground hover:bg-destructive/10 hover:text-destructive">
                <Trash2 className="h-3.5 w-3.5" /> Remove
              </Button>
            </div>
          )}
        </CardHeader>
        <CardContent className="pt-2">
          {disabledReason && (
            <div
              role="alert"
              className="mt-3 flex items-start gap-2 rounded-md border border-amber-500/40 bg-amber-500/10 p-3 text-xs text-amber-900 dark:text-amber-200">
              <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
              <span>{disabledReason}</span>
            </div>
          )}
          {!showEditor ? (
            <div className="divide-y">
              {serviceAccountEmail && (
                <MetadataRow label="Service account" value={serviceAccountEmail} />
              )}
              {projectId && <MetadataRow label="Google Cloud project" value={projectId} />}
              {!serviceAccountEmail && !projectId && (
                <MetadataRow label="Credentials" value="Stored securely" />
              )}
              <ServiceAccountHelp />
            </div>
          ) : (
            <div className="space-y-4 py-3">
              <ServiceAccountHelp />
              {canManage && (
                <>
                  <FilePickerRow
                    accept=".json,application/json"
                    fileName={fileName}
                    onPick={handlePick}
                    onClear={clearSelection}
                    disabled={!!disabledReason}
                  />
                  <div className="flex justify-end gap-2">
                    {isReplacing && (
                      <Button
                        variant="ghost"
                        onClick={() => {
                          setIsReplacing(false);
                          clearSelection();
                        }}>
                        Cancel
                      </Button>
                    )}
                    <Button
                      onClick={handleSave}
                      disabled={!!disabledReason || !serviceAccountKey || isSaving}>
                      {isSaving ? 'Saving…' : isReplacing ? 'Replace key' : 'Save key'}
                    </Button>
                  </div>
                </>
              )}
            </div>
          )}
        </CardContent>
      </Card>

      <DeleteDialog
        isOpen={isDeleteDialogOpen}
        onClose={() => setIsDeleteDialogOpen(false)}
        onConfirm={handleDelete}
        isDeleting={isDeleting}
        title="Remove Google Play service account"
        resourceName={`${identifier} service account`}
        descriptionText="The service account key will be permanently removed. The signing keystore will not be changed."
        confirmButtonText="Remove service account"
        isDeletingButtonText="Removing…"
      />
    </>
  );
};
