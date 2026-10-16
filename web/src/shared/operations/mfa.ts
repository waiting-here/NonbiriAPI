import { apiFetch } from '@shared/query/http';
import { boolean, nullableUnixSecond, record, string, unixSecond } from './wire';

export interface MfaStatus {
  enrolled: boolean;
  required: boolean;
  verified: boolean;
  verified_until: number | null;
}
export interface MfaSetup {
  secret: string;
  otpauth_uri: string;
  expires_at: number;
}

export function decodeMfaStatus(value: unknown): MfaStatus {
  const root = record(
    value,
    ['enrolled', 'required', 'verified', 'verified_until'],
    'authenticator status',
  );
  return {
    enrolled: boolean(root.enrolled, 'authenticator enrollment'),
    required: boolean(root.required, 'authenticator policy'),
    verified: boolean(root.verified, 'authenticator verification'),
    verified_until: nullableUnixSecond(root.verified_until, 'authenticator verification expiry'),
  };
}

export async function getMfa(signal?: AbortSignal): Promise<MfaStatus> {
  return decodeMfaStatus(await apiFetch('/api/auth/mfa', { signal }));
}

// Enrollment material goes directly to component memory, never to a query cache.
export async function setupMfa(signal?: AbortSignal): Promise<MfaSetup> {
  const value = await apiFetch('/api/auth/mfa/setup', { method: 'POST', json: {}, signal });
  const root = record(value, ['secret', 'otpauth_uri', 'expires_at'], 'authenticator setup');
  return {
    secret: string(root.secret, 'authenticator secret', { min: 1 }),
    otpauth_uri: string(root.otpauth_uri, 'authenticator URI', { min: 1 }),
    expires_at: unixSecond(root.expires_at, 'authenticator setup expiry'),
  };
}

export async function submitMfa(
  action: 'confirm' | 'verify' | 'disable',
  code: string,
  signal?: AbortSignal,
): Promise<MfaStatus> {
  return decodeMfaStatus(
    await apiFetch(`/api/auth/mfa/${action}`, {
      method: 'POST',
      json: { code },
      signal,
    }),
  );
}

export async function getManagedMfa(id: string): Promise<{ enrolled: boolean }> {
  const root = record(
    await apiFetch(`/admin/api/users/${encodeURIComponent(id)}/mfa`),
    ['enrolled'],
    'authenticator enrollment',
  );
  return { enrolled: boolean(root.enrolled, 'authenticator enrollment') };
}

export async function resetManagedMfa(id: string, token: string): Promise<void> {
  await apiFetch(`/admin/api/users/${encodeURIComponent(id)}/mfa/reset`, {
    method: 'POST',
    headers: { 'X-Elevated-Token': token },
  });
}
