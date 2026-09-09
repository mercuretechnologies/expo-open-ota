import { randomUUID } from 'crypto';

import { createBuildOutputRedactor } from './errors';
import { BuildLogEvent, BuildLogSink } from './log';
import { request } from './server';

const BATCH_BYTES = 32 * 1024;
const MAX_LOG_BYTES = 10 * 1024 * 1024;
const MAX_PENDING_BYTES = 1024 * 1024;

export function createBuildLogStream(
  endpoint: string,
  buildId: string,
  secrets: string[],
  warn: (message: string) => void
): BuildLogSink {
  const redact = createBuildOutputRedactor(secrets);
  let queued = '';
  let queuedBytes = 0;
  let totalBytes = 0;
  let offset = 0;
  let batch: { offset: number; content: string } | undefined;
  let failures = 0;
  let stopped = false;
  let closing = false;
  let capped = false;
  let inFlight: Promise<void> | undefined;

  const stop = (reason: string): void => {
    if (stopped) {
      return;
    }
    stopped = true;
    clearInterval(timer);
    queued = '';
    batch = undefined;
    warn(`${reason} The build continues; full logs remain in the local log file.`);
  };

  const flush = async (deadline: number): Promise<void> => {
    while (!stopped && (batch || queued) && Date.now() < deadline) {
      if (!batch) {
        const bytes = Buffer.from(queued);
        const end = bytes.lastIndexOf(10, Math.min(BATCH_BYTES, bytes.length) - 1) + 1;
        const content = bytes.subarray(0, end).toString('utf8');
        batch = { offset, content };
        queued = queued.slice(content.length);
        queuedBytes -= Buffer.byteLength(content);
      }
      const nextOffset = batch.offset + Buffer.byteLength(batch.content);
      try {
        const response = await request<{ nextOffset: number }>(
          `${endpoint}/artifacts/${buildId}/logs`,
          { method: 'POST', body: { ...batch, format: 'ndjson' }, retry: false, timeout: 5000 }
        );
        if (response.nextOffset !== nextOffset) {
          throw new Error('Invalid log offset');
        }
        offset = nextOffset;
        batch = undefined;
        failures = 0;
      } catch {
        // Keep the exact batch: a timeout may have happened after the server stored it.
        if (++failures >= 3) {
          stop('Log streaming stopped after repeated upload failures.');
        }
        return;
      }
    }
  };

  const timer = setInterval(() => {
    if (!inFlight) {
      inFlight = flush(Date.now() + 2000).finally(() => {
        inFlight = undefined;
      });
    }
  }, 2000);
  timer.unref();

  return {
    write(event: BuildLogEvent): void {
      if (stopped || closing || capped) {
        return;
      }
      // Mask the complete message before splitting. Each batch contains complete JSON records.
      let message = redact(event.msg).replace(/\0/g, '');
      let first = true;
      do {
        let end = Math.min(message.length, 4000);
        if (end < message.length && /[\uD800-\uDBFF]/.test(message[end - 1])) {
          end--;
        }
        const entry = {
          ...event,
          logId: first ? event.logId : randomUUID(),
          msg: message.slice(0, end),
        };
        const content = JSON.stringify(entry) + '\n';
        const bytes = Buffer.byteLength(content);
        if (totalBytes + bytes > MAX_LOG_BYTES) {
          capped = true;
          warn('Streamed logs reached the 10 MiB limit. Full logs remain in the local log file.');
          return;
        }
        if (queuedBytes + bytes > MAX_PENDING_BYTES) {
          stop('Log streaming stopped because uploads could not keep up with build output.');
          return;
        }
        queued += content;
        queuedBytes += bytes;
        totalBytes += bytes;
        message = message.slice(end);
        first = false;
      } while (message);
    },
    async close(): Promise<void> {
      closing = true;
      clearInterval(timer);
      const deadline = Date.now() + 10000;
      await inFlight;
      await flush(deadline);
      if (!stopped && (batch || queued)) {
        stop('Some final build logs could not be uploaded.');
      }
    },
  };
}
