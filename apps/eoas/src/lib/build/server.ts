import originalFetch, { RequestInit, Response } from 'node-fetch';
import { validate as isUuid } from 'uuid';

import { detectServerImplementation, getAuthHeaders, retrieveCredentials } from '../auth';
import { fetchWithRetries } from '../fetch';

export type BuildPlatform = 'android' | 'ios';

export type EnvironmentSelection = { channel: string } | { environment: string };

// Returns the identifier-scoped endpoint the other build routes hang off.
export async function resolveIdentifier(
  root: string,
  platform: BuildPlatform,
  applicationId: string
): Promise<string> {
  const { identifierId } = await request<{ identifierId: string }>(
    `${root}/resolve/${platform}/${encodeURIComponent(applicationId)}`
  );
  if (!isUuid(identifierId)) {
    throw new Error('Invalid identifier resolution response.');
  }
  return `${root}/${identifierId}`;
}

export async function fetchEnvironment(
  endpoint: string,
  selection: EnvironmentSelection
): Promise<Record<string, string>> {
  const { variables } = await request<{ variables?: Record<string, unknown> }>(
    `${endpoint}/environment?${new URLSearchParams(selection)}`
  );
  if (!variables || Object.values(variables).some(value => typeof value !== 'string')) {
    throw new Error('Invalid server environment response.');
  }
  return variables as Record<string, string>;
}

// Every listed field must be a non-empty string; partial records are refused.
export async function fetchCredentials<T extends object>(
  endpoint: string,
  platform: BuildPlatform,
  fields: (keyof T & string)[]
): Promise<T> {
  const credentials = await request<Record<string, unknown>>(`${endpoint}/credentials/${platform}`);
  if (fields.some(field => typeof credentials[field] !== 'string' || !credentials[field])) {
    throw new Error(`Incomplete ${platform} signing credentials.`);
  }
  return credentials as T;
}

// One attempt only: an uncertain response may already have reserved the number,
// so this POST is never replayed automatically.
export async function allocateBuildNumber(endpoint: string): Promise<string> {
  const allocation = await request<{ buildNumber?: unknown }>(`${endpoint}/build-number`, {
    method: 'POST',
    retry: false,
  });
  const buildNumber = String(allocation.buildNumber);
  if (!/^[1-9][0-9]*$/.test(buildNumber)) {
    throw new Error('Invalid build number allocation response.');
  }
  return buildNumber;
}

async function request<T>(
  url: string,
  { method = 'GET', retry = true }: { method?: string; retry?: boolean } = {}
): Promise<T> {
  const credentials = retrieveCredentials();
  if (detectServerImplementation() !== 'eoo' || !credentials.token) {
    throw new Error('Build requires a registered EOO_TOKEN.');
  }
  const init: RequestInit = {
    method,
    headers: getAuthHeaders(credentials),
    redirect: 'error',
    timeout: 30000,
  };
  let response: Response;
  try {
    response = await (retry ? fetchWithRetries(url, init) : originalFetch(url, init));
  } catch {
    throw new Error(
      `Build server request failed (${method})${retry ? '' : '; no automatic retry was made'}.`
    );
  }
  if (!response.ok) {
    throw new Error(
      `Build server returned HTTP ${response.status}. Check token permissions, identifier and environment selection.`
    );
  }
  try {
    return (await response.json()) as T;
  } catch {
    throw new Error('Invalid build server response.');
  }
}
