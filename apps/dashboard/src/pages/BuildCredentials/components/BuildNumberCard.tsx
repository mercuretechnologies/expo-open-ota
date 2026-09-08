import { useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { api, AppIdentifier, describeApiError } from '@/lib/api';
import { useSelectedApp } from '@/lib/SelectedAppContext';
import { useToast } from '@/hooks/use-toast';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';

type Props = {
  identifier: AppIdentifier;
  canManage: boolean;
};

export const BuildNumberCard = ({ identifier, canManage }: Props) => {
  const { selectedAppId } = useSelectedApp();
  const { toast } = useToast();
  const queryClient = useQueryClient();
  const [buildNumber, setBuildNumber] = useState(String(identifier.buildNumber));
  const [isSaving, setIsSaving] = useState(false);
  const counterName = identifier.platform === 'android' ? 'versionCode' : 'buildNumber';

  const handleSave = async (e: React.FormEvent) => {
    e.preventDefault();
    const valid =
      identifier.platform === 'android'
        ? /^(0|[1-9][0-9]*)$/.test(buildNumber) &&
          buildNumber.length <= 10 &&
          Number(buildNumber) <= 2_100_000_000
        : /^(0|[1-9][0-9]*)(\.(0|[1-9][0-9]*))*$/.test(buildNumber);
    if (!valid) {
      toast({
        title: 'Invalid build number',
        description:
          identifier.platform === 'android'
            ? 'Enter an integer from 0 to 2,100,000,000.'
            : 'Enter non-negative integers separated by dots, without leading zeros.',
        variant: 'destructive',
      });
      return;
    }
    setIsSaving(true);
    try {
      await api.setAppIdentifierBuildNumber(identifier.id, buildNumber);
      queryClient.invalidateQueries({ queryKey: ['identifiers', selectedAppId] });
      toast({ title: 'Build number updated' });
    } catch (error) {
      const message = describeApiError(error, 'Error updating build number');
      toast({ title: message.title, description: message.description, variant: 'destructive' });
    } finally {
      setIsSaving(false);
    }
  };

  return (
    <Card>
      <CardHeader className="border-b">
        <CardTitle className="text-base">Build number</CardTitle>
        <CardDescription>
          Last {counterName} handed to a build. Integer counters increase by one for each
          reservation.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3 pt-4">
        {canManage ? (
          <form onSubmit={handleSave} className="flex items-end gap-3">
            <div className="space-y-2">
              <Label htmlFor="build-number">Last assigned {counterName}</Label>
              <Input
                id="build-number"
                type="text"
                inputMode={identifier.platform === 'android' ? 'numeric' : 'text'}
                required
                value={buildNumber}
                onChange={e => setBuildNumber(e.target.value)}
                disabled={isSaving}
                className="w-40"
              />
            </div>
            <Button type="submit" disabled={isSaving}>
              {isSaving ? 'Saving…' : 'Save'}
            </Button>
          </form>
        ) : (
          <p className="text-sm">
            Last assigned: <span className="font-medium">{identifier.buildNumber}</span>
          </p>
        )}
        {canManage && (
          <p className="text-xs text-muted-foreground">
            Use 0 to start at 1. Lowering it can hand out a number a build already used.
            {identifier.platform === 'ios' &&
              ' Values with dots can be saved, but their automatic increment is not implemented.'}
          </p>
        )}
      </CardContent>
    </Card>
  );
};
