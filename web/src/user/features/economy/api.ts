import { ApiError, apiFetch, isApiError } from '@shared/query/http';
import { validFailureThreshold } from '@shared/operations/failurePolicy';
import {
  normalizeActivitiesSnapshot,
  normalizeCharityCapability,
  normalizeDonation,
  normalizeThursdayContributionResult,
  normalizeWelfareClaimResult,
} from './normalize';
import type {
  ActivitiesSnapshot,
  CharityCapability,
  Donation,
  ThursdayContributionResult,
  WelfareClaimResult,
} from './types';

const MAX_INT64 = 9_223_372_036_854_775_807n;
const DECIMAL_ID = /^[1-9][0-9]{0,18}$/;
const PERIOD_ID = /^thu_[A-Za-z0-9_-]{21}[AQgw]$/;
const MAX_UNIX_SECONDS = 253_402_300_799;

function invalidRequest(message: string): never {
  throw new ApiError('invalid_request', message, 400);
}

function requireDecimalID(value: string, field: string): void {
  if (typeof value !== 'string' || !DECIMAL_ID.test(value) || BigInt(value) > MAX_INT64) {
    invalidRequest(`Invalid ${field}.`);
  }
}

function requireRevision(value: string): void {
  requireDecimalID(value, 'revision');
}

function requireDonationDescription(value: string): void {
  if (typeof value !== 'string' || Array.from(value).length > 1024) {
    invalidRequest('Invalid donation description.');
  }
  for (const character of value) {
    const codePoint = character.codePointAt(0) ?? 0;
    if (codePoint < 0x20 || (codePoint >= 0x7f && codePoint <= 0x9f)) {
      invalidRequest('Invalid donation description.');
    }
  }
}

function requireDonationExpiry(value: number | null): void {
  if (value !== null && (!Number.isSafeInteger(value) || value < 0 || value > MAX_UNIX_SECONDS)) {
    invalidRequest('Invalid donation key expiry.');
  }
}

function createMutationIdentity(): string {
  const cryptoValue = globalThis.crypto;
  if (typeof cryptoValue?.randomUUID === 'function')
    return cryptoValue.randomUUID().replaceAll('-', '');
  if (typeof cryptoValue?.getRandomValues !== 'function') {
    throw new ApiError(
      'service_unavailable',
      'A secure mutation identity could not be created.',
      503,
    );
  }
  const bytes = new Uint8Array(24);
  cryptoValue.getRandomValues(bytes);
  return Array.from(bytes, (value) => value.toString(16).padStart(2, '0')).join('');
}

function mutationHeaders(): HeadersInit {
  return { 'Idempotency-Key': createMutationIdentity() };
}

export async function getCharityCapability(signal?: AbortSignal): Promise<CharityCapability> {
  return normalizeCharityCapability(await apiFetch<unknown>('/api/charity/models', { signal }));
}

export async function getDonation(id: string, signal?: AbortSignal): Promise<Donation> {
  requireDecimalID(id, 'donation id');
  const donation = normalizeDonation(
    await apiFetch<unknown>(`/api/donations/${encodeURIComponent(id)}`, { signal }),
  );
  if (donation.id !== id) throw new ApiError('invalid_response', 'Invalid donation identity.', 200);
  return donation;
}

export interface CreateDonationInput {
  discordPublicThanks: boolean;
  description: string;
  keys: { endpointKeyId: string; expiresAt: number | null; failureDisableThreshold?: string }[];
  ownershipAuthorized: true;
}

export async function createDonation(input: CreateDonationInput): Promise<Donation> {
  requireDonationDescription(input.description);
  if (
    input.ownershipAuthorized !== true ||
    typeof input.discordPublicThanks !== 'boolean' ||
    !Array.isArray(input.keys) ||
    input.keys.length < 1 ||
    input.keys.length > 100 ||
    new Set(input.keys.map((key) => key?.endpointKeyId)).size !== input.keys.length
  ) {
    invalidRequest('Invalid donation submission.');
  }
  input.keys.forEach((key) => {
    if (key === null || typeof key !== 'object') invalidRequest('Invalid donation key.');
    requireDecimalID(key.endpointKeyId, 'endpoint key id');
    requireDonationExpiry(key.expiresAt);
    if (
      key.failureDisableThreshold !== undefined &&
      !validFailureThreshold(key.failureDisableThreshold)
    )
      invalidRequest('Invalid failure threshold.');
  });
  return normalizeDonation(
    await apiFetch<unknown>('/api/donations', {
      method: 'POST',
      headers: mutationHeaders(),
      json: {
        description: input.description,
        keys: input.keys.map((key) => ({
          endpoint_key_id: key.endpointKeyId,
          expires_at: key.expiresAt,
          ...(key.failureDisableThreshold === undefined
            ? {}
            : { failure_disable_threshold: key.failureDisableThreshold }),
        })),
        ownership_authorized: input.ownershipAuthorized,
        discord_public_thanks: input.discordPublicThanks,
      },
    }),
  );
}

export async function editDonation(
  id: string,
  description: string,
  expectedRevision: string,
): Promise<Donation> {
  requireDecimalID(id, 'donation id');
  requireRevision(expectedRevision);
  requireDonationDescription(description);
  return normalizeDonation(
    await apiFetch<unknown>(`/api/donations/${encodeURIComponent(id)}`, {
      method: 'PATCH',
      headers: mutationHeaders(),
      json: { description, expected_revision: expectedRevision },
    }),
  );
}

export async function withdrawDonation(id: string, expectedRevision: string): Promise<Donation> {
  requireDecimalID(id, 'donation id');
  requireRevision(expectedRevision);
  return normalizeDonation(
    await apiFetch<unknown>(`/api/donations/${encodeURIComponent(id)}/withdraw`, {
      method: 'POST',
      headers: mutationHeaders(),
      json: { expected_revision: expectedRevision },
    }),
  );
}

export async function terminateDonation(id: string, expectedRevision: string): Promise<Donation> {
  requireDecimalID(id, 'donation id');
  requireRevision(expectedRevision);
  return normalizeDonation(
    await apiFetch<unknown>(`/api/donations/${encodeURIComponent(id)}/terminate`, {
      method: 'POST',
      headers: mutationHeaders(),
      json: { expected_revision: expectedRevision, confirmation: 'terminate' },
    }),
  );
}

export async function getActivities(signal?: AbortSignal): Promise<ActivitiesSnapshot> {
  return normalizeActivitiesSnapshot(await apiFetch<unknown>('/api/activities', { signal }));
}

export async function claimWelfare(): Promise<WelfareClaimResult> {
  return normalizeWelfareClaimResult(
    await apiFetch<unknown>('/api/activities/welfare/claims', {
      method: 'POST',
      headers: mutationHeaders(),
    }),
  );
}

export async function contributeThursday(
  periodId: string,
  expectedRevision: string,
): Promise<ThursdayContributionResult> {
  if (!PERIOD_ID.test(periodId)) invalidRequest('Invalid Thursday period id.');
  requireRevision(expectedRevision);
  return normalizeThursdayContributionResult(
    await apiFetch<unknown>('/api/activities/thursday/contributions', {
      method: 'POST',
      headers: mutationHeaders(),
      json: { period_id: periodId, expected_revision: expectedRevision },
    }),
  );
}

export function isConflictError(error: unknown): boolean {
  return isApiError(error) && (error.status === 409 || error.code === 'conflict');
}

export function isResponseUnknown(error: unknown): boolean {
  return (
    isApiError(error) &&
    (error.status === 0 || error.code === 'invalid_response' || error.code === 'network_error')
  );
}
