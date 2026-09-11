import { stripVTControlCharacters } from 'util';
export function formatBuildError(stage: string, error: unknown, secrets: string[]): string {
  const failure =
    error && typeof error === 'object'
      ? (error as { stdout?: unknown; stderr?: unknown; message?: unknown; status?: unknown })
      : {};
  const output = [failure.stdout, failure.stderr]
    .filter((value): value is string => typeof value === 'string' && value.trim() !== '')
    .join('\n');
  let detail =
    output || (typeof failure.message === 'string' ? failure.message : 'Unknown tool error');
  detail = createBuildOutputRedactor(secrets)(detail);
  if (detail.length > 12000) {
    detail = `[earlier output omitted]\n${detail.slice(-12000)}`;
  }
  const status = typeof failure.status === 'number' ? ` (exit code ${failure.status})` : '';
  return `${stage} failed${status}.\n\n${detail.trim()}`;
}

export function createBuildOutputRedactor(secrets: string[]): (output: string) => string {
  const sensitive = [
    ...new Set(
      secrets
        .filter(Boolean)
        .flatMap(value => [
          value,
          encodeURIComponent(value),
          JSON.stringify(value).slice(1, -1),
          ...value.split(/\r?\n/).filter(Boolean),
        ])
    ),
  ].sort((a, b) => b.length - a.length);
  if (sensitive.length) {
    const pattern = sensitive.map(value => value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')).join('|');
    const expression = new RegExp(pattern, 'g');
    return (output: string) => stripVTControlCharacters(output).replace(expression, '[REDACTED]');
  }
  return stripVTControlCharacters;
}

// Shorter variable values ("1", "true", "production") are flags or names whose
// redaction would mangle ordinary tool output.
const MIN_REDACTED_VALUE_LENGTH = 12;

// Values masked in streamed output and error messages: the caller's credentials,
// server variables except public ones, and secret-looking process.env entries.
export function secretsToRedact(
  variables: Record<string, string>,
  credentials: string[]
): string[] {
  return [
    ...credentials,
    ...Object.entries(variables)
      .filter(([name]) => !name.startsWith('EXPO_PUBLIC_'))
      .map(([, value]) => value),
    ...Object.entries(process.env)
      .filter(([name]) => /token|password|secret|private.?key|credential/i.test(name))
      .map(([, value]) => value ?? ''),
  ].filter(
    (value, index) => index < credentials.length || value.length >= MIN_REDACTED_VALUE_LENGTH
  );
}
