import { AlertTriangle, CheckCircle2, Loader2 } from 'lucide-react';
import { BuildStatus } from '@/lib/api';
import { cn } from '@/lib/utils';
import { statusLabel } from '../format';

export const BuildStatusBadge = ({
  status,
  iconOnly = false,
}: {
  status: BuildStatus;
  iconOnly?: boolean;
}) => {
  if (iconOnly) {
    const Icon = status === 'ready' ? CheckCircle2 : status === 'failed' ? AlertTriangle : Loader2;
    return (
      <span className="inline-flex shrink-0" title={statusLabel(status)}>
        <Icon
          className={cn(
            'h-[18px] w-[18px]',
            status === 'ready'
              ? 'fill-emerald-600 text-white dark:fill-emerald-500'
              : status === 'failed'
                ? 'text-destructive'
                : 'animate-spin text-primary motion-reduce:animate-none'
          )}
          aria-hidden="true"
        />
        <span className="sr-only">{statusLabel(status)}</span>
      </span>
    );
  }
  if (status === 'ready') {
    return (
      <span className="inline-flex items-center gap-1.5 text-sm font-medium text-emerald-700 dark:text-emerald-300">
        <CheckCircle2 className="h-3.5 w-3.5" /> {statusLabel(status)}
      </span>
    );
  }
  if (status === 'uploading' || status === 'building') {
    return (
      <span className="inline-flex items-center gap-1.5 text-sm font-medium text-amber-700 dark:text-amber-400">
        <Loader2 className="h-3.5 w-3.5 animate-spin motion-reduce:animate-none" />{' '}
        {statusLabel(status)}
      </span>
    );
  }
  return (
    <span className="inline-flex items-center gap-1.5 text-sm font-medium text-destructive">
      <AlertTriangle className="h-3.5 w-3.5" /> {statusLabel(status)}
    </span>
  );
};
