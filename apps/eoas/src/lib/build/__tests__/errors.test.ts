import { describe, expect, it } from 'vitest';

import { formatBuildError } from '../errors';

describe('build diagnostics', () => {
  it('shows the stage, exit code and actual tool output', () => {
    const message = formatBuildError(
      'Validating Android bundle',
      {
        status: 1,
        stdout: 'Bundling failed',
        stderr: 'Error: Expo Router is incompatible with React Navigation.',
      },
      []
    );
    expect(message).toContain('Validating Android bundle failed (exit code 1)');
    expect(message).toContain('Bundling failed');
    expect(message).toContain('Expo Router is incompatible with React Navigation.');
  });

  it('redacts known secrets in both streams, including URL-encoded values', () => {
    const message = formatBuildError(
      'Building',
      {
        stdout: 'token=token/secret',
        stderr: 'password=signing-secret url=token%2Fsecret',
      },
      ['token/secret', 'signing-secret']
    );
    expect(message).not.toContain('token/secret');
    expect(message).not.toContain('token%2Fsecret');
    expect(message).not.toContain('signing-secret');
    expect(message).toContain('[REDACTED]');
  });

  it('falls back to the spawn error when there is no output', () => {
    expect(formatBuildError('Checking Java', new Error('spawn java ENOENT'), [])).toContain(
      'spawn java ENOENT'
    );
  });

  it('keeps the tail of long output after redaction', () => {
    const message = formatBuildError(
      'Building',
      {
        stderr: `${'progress\n'.repeat(3000)}password=secret-value\nActual failure`,
      },
      ['secret-value']
    );
    expect(message).toContain('earlier output omitted');
    expect(message).toContain('Actual failure');
    expect(message).not.toContain('secret-value');
    expect(message.length).toBeLessThan(13000);
  });
});
