import { Interface, createInterface } from 'readline';
import { Readable } from 'stream';

import { createBuildOutputRedactor } from './errors';

export function streamBuildOutput(
  stream: Readable,
  secrets: string[],
  write: (line: string) => void
): Interface {
  const redact = createBuildOutputRedactor(secrets);
  // Buffer complete lines so a secret split across data events stays masked.
  const lines = createInterface({ input: stream, crlfDelay: Infinity });
  lines.on('line', line => {
    write(redact(line));
  });
  return lines;
}
