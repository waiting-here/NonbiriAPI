import { ApiError, apiFetch, isApiError, isForbidden, isUnauthorized } from '@shared/query/http';
import {
  normalizeActivitiesSnapshot,
  normalizeCharityCapability,
  normalizeCursorPage,
  normalizeDonation,
  normalizeEndpoint,
  normalizeEndpointKey,
  normalizeThursdayContributionResult,
  normalizeWelfareClaimResult,
} from './normalize';
import type {
  ActivitiesSnapshot,
  CharityCapability,
  CursorPage,
  Donation,
  EndpointKeyChoice,
  EndpointKeySummary,
  EndpointSummary,
  ThursdayContributionResult,
  WelfareClaimResult,
} from './types';

const PAGE_LIMIT = 100;
const MAX_COLLECTED_ITEMS = 1000;
const MAX_PAGE_REQUESTS = 64;
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

/**
 * A cursor collection must be all-or-nothing for the account-level overview.
 * This typed error lets the page distinguish an incomplete authority read from
 * a normal API failure without exposing the bounded transport detail.
 */
export class DonationCollectionIncompleteError extends ApiError {
  readonly incomplete = true;

  constructor() {
    super('invalid_response', 'The donation overview could not be loaded completely.', 200);
    this.name = 'DonationCollectionIncompleteError';
  }
}

export function isDonationCollectionIncomplete(error: unknown): boolean {
  return error instanceof DonationCollectionIncompleteError;
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

function pagedPath(path: string, cursor: string | null): string {
  const query = new URLSearchParams({ limit: String(PAGE_LIMIT) });
  if (cursor) query.set('cursor', cursor);
  return `${path}?${query.toString()}`;
}

async function collectPages<T>(
  path: string,
  field: string,
  normalizeItem: (value: unknown) => T,
  identify: (value: T) => string,
  signal?: AbortSignal,
): Promise<T[]> {
  const values: T[] = [];
  const seenCursors = new Set<string>();
  const seenItems = new Set<string>();
  let nextCursor: string | null = null;
  for (let pageNumber = 0; pageNumber < MAX_PAGE_REQUESTS; pageNumber += 1) {
    const payload: unknown = await apiFetch<unknown>(pagedPath(path, nextCursor), { signal });
    const page: CursorPage<T> = normalizeCursorPage(
      payload,
      field,
      normalizeItem,
      PAGE_LIMIT,
      identify,
    );
    for (const item of page.data) {
      const identity = identify(item);
      if (seenItems.has(identity)) {
        throw new ApiError(
          'invalid_response',
          `The server returned a repeated ${field} identity.`,
          200,
        );
      }
      seenItems.add(identity);
    }
    values.push(...page.data);
    if (values.length > MAX_COLLECTED_ITEMS) {
      throw new ApiError('invalid_response', `The server returned too many ${field} items.`, 200);
    }
    if (page.nextCursor === null) return values;
    if (seenCursors.has(page.nextCursor)) {
      throw new ApiError(
        'invalid_response',
        `The server returned a repeated ${field} cursor.`,
        200,
      );
    }
    seenCursors.add(page.nextCursor);
    nextCursor = page.nextCursor;
  }
  throw new ApiError('invalid_response', `The server returned too many ${field} pages.`, 200);
}

async function mapWithConcurrency<T, R>(
  values: readonly T[],
  concurrency: number,
  mapper: (value: T) => Promise<R>,
): Promise<R[]> {
  const output = new Array<R>(values.length);
  let cursor = 0;
  const workers = Array.from({ length: Math.min(concurrency, values.length) }, async () => {
    for (;;) {
      const index = cursor;
      cursor += 1;
      if (index >= values.length) return;
      output[index] = await mapper(values[index]);
    }
  });
  await Promise.all(workers);
  return output;
}

export async function getCharityCapability(signal?: AbortSignal): Promise<CharityCapability> {
  return normalizeCharityCapability(await apiFetch<unknown>('/api/charity/models', { signal }));
}

export async function getDonations(signal?: AbortSignal): Promise<Donation[]> {
  try {
    return await collectPages(
      '/api/donations',
      'donations',
      normalizeDonation,
      (value) => value.id,
      signal,
    );
  } catch (error) {
    // Any failed page, malformed cursor, duplicate identity or collection
    // bound is an incomplete overview and must be retried as one collection.
    // Authentication/authorization errors are station-boundary signals rather
    // than a partial collection; preserve them so the session gate can react.
    if (!isUnauthorized(error) && !isForbidden(error)) {
      throw new DonationCollectionIncompleteError();
    }
    throw error;
  }
}

export async function getDonation(id: string, signal?: AbortSignal): Promise<Donation> {
  requireDecimalID(id, 'donation id');
  return normalizeDonation(
    await apiFetch<unknown>(`/api/donations/${encodeURIComponent(id)}`, { signal }),
  );
}

export async function getEndpointChoices(
  donations: readonly Donation[],
  signal?: AbortSignal,
): Promise<EndpointKeyChoice[]> {
  const endpoints = await collectPages<EndpointSummary>(
    '/api/endpoints',
    'endpoints',
    normalizeEndpoint,
    (value) => value.id,
    signal,
  );
  const keyGroups = await mapWithConcurrency(endpoints, 4, (endpoint) =>
    collectPages<EndpointKeySummary>(
      `/api/endpoints/${encodeURIComponent(endpoint.id)}/keys`,
      'endpoint keys',
      normalizeEndpointKey,
      (value) => value.id,
      signal,
    ),
  );
  const totalKeys = keyGroups.reduce((total, group) => total + group.length, 0);
  if (totalKeys > MAX_COLLECTED_ITEMS) {
    throw new ApiError('invalid_response', 'The server returned too many endpoint key items.', 200);
  }
  const activeMemberships = new Set<string>();
  for (const donation of donations) {
    if (donation.status !== 'pending' && donation.status !== 'approved') continue;
    for (const key of donation.keys) {
      if (!key.endpointKeyId) continue;
      if (activeMemberships.has(key.endpointKeyId)) {
        throw new ApiError(
          'invalid_response',
          'The server returned a repeated active donation membership.',
          200,
        );
      }
      activeMemberships.add(key.endpointKeyId);
    }
  }
  const seenEndpointKeys = new Set<string>();
  return endpoints.flatMap((endpoint, endpointIndex) =>
    keyGroups[endpointIndex].map((key) => {
      if (key.endpointId !== endpoint.id || seenEndpointKeys.has(key.id)) {
        throw new ApiError(
          'invalid_response',
          'The server returned an invalid endpoint key association.',
          200,
        );
      }
      seenEndpointKeys.add(key.id);
      return {
        endpoint,
        key,
        eligibility:
          key.suspensionState === 'security_processing'
            ? 'security_processing'
            : activeMemberships.has(key.id)
              ? 'already_donated'
              : 'eligible',
      };
    }),
  );
}

export interface CreateDonationInput {
  description: string;
  keys: { endpointKeyId: string; expiresAt: number | null }[];
  ownershipAuthorized: true;
}

export async function createDonation(input: CreateDonationInput): Promise<Donation> {
  requireDonationDescription(input.description);
  if (
    input.ownershipAuthorized !== true ||
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
        })),
        ownership_authorized: input.ownershipAuthorized,
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
