import { useEffect, useRef } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  ApiError,
  apiFetch,
  isForbidden,
  isNotFoundError,
  isUnauthorized,
  refetchAuthoritativeQueries,
} from '@shared/query/http';
import {
  asRecord,
  hasControlCharacters,
  isListPayload,
  listResult,
  opaqueID,
  positiveDecimalIDNumber,
  text,
  type UnknownRecord,
} from '@shared/query/normalize';

export type CharityManagementFrame = 'admin' | 'steward';
export type CharityPricingMode = 'per_request' | 'per_token';

export interface ManagementPriceSet {
  request_user_price_milli: string;
  request_donor_reward_milli: string;
  uncached_user_price_milli: string;
  cache_write_user_price_milli: string;
  cache_read_user_price_milli: string;
  output_user_price_milli: string;
  uncached_donor_reward_milli: string;
  cache_write_donor_reward_milli: string;
  cache_read_donor_reward_milli: string;
  output_donor_reward_milli: string;
}

export interface ManagementCharityModel {
  id: string;
  provider: string;
  model: string;
  full_name: string;
  enabled: boolean;
  flatten_tool_calls: boolean;
  pricing_mode: CharityPricingMode;
  prices: ManagementPriceSet;
  discount: { percent: number; enabled: boolean; start_at?: number; end_at?: number };
  success_samples: number;
  success_count: number;
}

export interface ManagementDonationKey {
  id: string;
  endpoint_key_id?: string;
  display?: string;
  max_concurrency: number;
  rpm_limit: number;
  credits_usage_cap_milli: string;
  credits_used_milli: string;
  credits_reserved_milli: string;
  enabled: boolean;
  force_store_false: boolean | 'not_applicable';
}

export interface ManagementReview {
  id: string;
  reviewer_role: string;
  action: string;
  note: string;
  created_at: number;
}

export interface ManagementDonation {
  id: string;
  user_id?: string;
  endpoint_id?: string;
  endpoint_base_url: string;
  status: string;
  enabled: boolean;
  description: string;
  review_note: string;
  expires_at?: number;
  reviewed_at?: number;
  created_at: number;
  updated_at: number;
  keys: ManagementDonationKey[];
  reviews: ManagementReview[];
}

export interface ManagementBinding {
  id: string;
  charity_model_id: string;
  donation_key_id: string;
  upstream_model_id: string;
  ord: number;
  endpoint_base_url: string;
  key_display?: string;
  donation_key_enabled: boolean;
}

export interface ManagementList<T> {
  items: T[];
  hasMore: boolean;
  total: number;
}

export const charityManagementKeys = {
  root: (frame: CharityManagementFrame) => ['charity-management', frame] as const,
  // Deliberately outside the removable management root: a 403 sentinel must
  // survive sensitive-cache eviction and cannot be recreated as `false` by a
  // stale capability query while the session refresh is in flight.
  capability: (frame: CharityManagementFrame) => ['charity-capability', frame] as const,
  donations: (frame: CharityManagementFrame, page: number, status: string) =>
    ['charity-management', frame, 'donations', page, status] as const,
  donation: (frame: CharityManagementFrame, id: string) =>
    ['charity-management', frame, 'donation', id] as const,
  models: (frame: CharityManagementFrame) => ['charity-management', frame, 'models'] as const,
  bindings: (frame: CharityManagementFrame, modelId: string) =>
    ['charity-management', frame, 'bindings', modelId] as const,
  settings: ['charity-management', 'admin', 'settings'] as const,
};

interface ManagementRevocation {
  timestamp: number;
  /** The exact station subject that lost the capability, never just a role. */
  subject: string;
  /** The session-request generation at which the capability was revoked. */
  generation: number;
}

interface ManagementSessionAuthority {
  /** Monotonic generation incremented when a station-session request starts. */
  generation: number;
  subject?: string;
  elevated: boolean;
}

const managementRevocations = new WeakMap<object, Map<CharityManagementFrame, ManagementRevocation>>();
const managementSessionAuthorities = new WeakMap<object, Map<CharityManagementFrame, ManagementSessionAuthority>>();

function stationQueryOwned(queryKey: readonly unknown[], frame: CharityManagementFrame): boolean {
  const stationRoot = frame === 'admin' ? 'admin' : 'user';
  if (queryKey[0] === stationRoot) return true;
  return queryKey[0] === 'charity-management' && queryKey[1] === frame;
}

/**
 * Remove every account-owned query for one station, including queries that
 * are not part of the charity-management projection.  The capability
 * sentinel intentionally lives under a separate root and is restored by the
 * caller after this eviction.
 */
function evictStationQueries(
  client: ReturnType<typeof useQueryClient>,
  frame: CharityManagementFrame,
  preserveSession = false,
): void {
  const predicate = (query: { queryKey: readonly unknown[] }): boolean => {
    if (!stationQueryOwned(query.queryKey, frame)) return false;
    if (preserveSession && query.queryKey.length === 2 && query.queryKey[1] === 'session') return false;
    return true;
  };
  void client.cancelQueries({ predicate }).catch(() => undefined);
  client.removeQueries({ predicate });
  // Each station owns its QueryClient. Completed mutations may otherwise keep
  // account-scoped response data while the same SPA authenticates a different
  // subject, and clearing only queries does not affect a mounted mutation.
  client.getMutationCache().clear();
}

function sessionAuthority(
  client: ReturnType<typeof useQueryClient>,
  frame: CharityManagementFrame,
): ManagementSessionAuthority {
  let frames = managementSessionAuthorities.get(client);
  if (!frames) {
    frames = new Map();
    managementSessionAuthorities.set(client, frames);
  }
  let authority = frames.get(frame);
  if (!authority) {
    authority = { generation: 0, elevated: false };
    frames.set(frame, authority);
  }
  return authority;
}

export interface StationSessionSnapshot {
  readonly generation: number;
  readonly subject: string;
}

export class StationSessionChangedError extends Error {
  readonly stationSessionChanged = true;

  constructor() {
    super('The station session changed while the request was in flight.');
    this.name = 'StationSessionChangedError';
  }
}

export function isStationSessionChanged(error: unknown): error is StationSessionChangedError {
  return error instanceof StationSessionChangedError;
}

function seedStationAuthorityFromCache(
  client: ReturnType<typeof useQueryClient>,
  frame: CharityManagementFrame,
): ManagementSessionAuthority {
  const authority = sessionAuthority(client, frame);
  if (authority.subject) return authority;
  const sessionKey = frame === 'admin' ? ['admin', 'session'] as const : ['user', 'session'] as const;
  const identity = managementSessionIdentity(frame, client.getQueryData(sessionKey));
  if (!identity) return authority;
  authority.generation += 1;
  authority.subject = identity.subject;
  authority.elevated = identity.elevated;
  return authority;
}

export function captureStationSession(
  client: ReturnType<typeof useQueryClient>,
  frame: CharityManagementFrame,
): StationSessionSnapshot {
  const authority = seedStationAuthorityFromCache(client, frame);
  if (!authority.subject) throw new StationSessionChangedError();
  return { generation: authority.generation, subject: authority.subject };
}

export function stationSessionMatches(
  client: ReturnType<typeof useQueryClient>,
  frame: CharityManagementFrame,
  snapshot: StationSessionSnapshot,
): boolean {
  const authority = sessionAuthority(client, frame);
  return authority.generation === snapshot.generation && authority.subject === snapshot.subject;
}

/** Bind one account-scoped request to the exact station subject and generation. */
export async function stationSessionWrite<T>(
  client: ReturnType<typeof useQueryClient>,
  frame: CharityManagementFrame,
  request: () => Promise<T>,
): Promise<T> {
  const snapshot = captureStationSession(client, frame);
  try {
    const value = await request();
    if (!stationSessionMatches(client, frame, snapshot)) throw new StationSessionChangedError();
    return value;
  } catch (error) {
    if (!stationSessionMatches(client, frame, snapshot)) throw new StationSessionChangedError();
    if (isUnauthorized(error) || isForbidden(error)) clearStationSession(client, frame);
    throw error;
  }
}

/** Begin a fresh station-session request before calling the server. */
export function beginManagementSessionRequest(
  client: ReturnType<typeof useQueryClient>,
  frame: CharityManagementFrame,
): number {
  const authority = sessionAuthority(client, frame);
  authority.generation += 1;
  return authority.generation;
}

/** Drop management projections and the old subject at an explicit logout. */
export function clearManagementSession(
  client: ReturnType<typeof useQueryClient>,
  frame: CharityManagementFrame,
): void {
  // Keep the mounted session query object but replace its identity with an
  // explicit closed value. Layouts disable the observer after logout intent,
  // so a failed network logout cannot immediately fetch the old identity
  // back into the shell.
  clearStationSession(client, frame, true);
  const sessionKey = frame === 'admin' ? ['admin', 'session'] as const : ['user', 'session'] as const;
  client.setQueryData(sessionKey, null);
}

/** Close a station and evict all of its account-owned query projections. */
export function clearStationSession(
  client: ReturnType<typeof useQueryClient>,
  frame: CharityManagementFrame,
  preserveSession = true,
): void {
  const authority = sessionAuthority(client, frame);
  authority.generation += 1;
  authority.subject = undefined;
  authority.elevated = false;
  clearRevoked(client, frame);
  evictStationQueries(client, frame, preserveSession);
  // Keep an active session observer from immediately recreating the old
  // account after an auth failure or logout. `null` carries no identity and
  // lets the existing observer publish its error/closed state without a
  // follow-up network read racing the station boundary.
  if (preserveSession) {
    const sessionKey = frame === 'admin' ? ['admin', 'session'] as const : ['user', 'session'] as const;
    client.setQueryData(sessionKey, null);
  }
  // Logout and an unknown subject are closed states. A disabled observer must
  // not recreate the placeholder `false` while an old component is unmounting.
  client.setQueryData(charityManagementKeys.capability(frame), true);
}

/**
 * Close a station after its current authoritative session request failed.
 * A stale request may fail after a newer login has completed, so
 * the generation check must happen before touching identity or projections.
 */
export function failManagementSessionRequest(
  client: ReturnType<typeof useQueryClient>,
  frame: CharityManagementFrame,
  generation: number,
): boolean {
  const authority = sessionAuthority(client, frame);
  if (authority.generation !== generation) return false;
  // Keep the authoritative session query object alive so React Query can
  // publish this request's error to its observers, but erase its old identity
  // before the error is delivered. Logout and explicit mutation auth failures
  // still use the default full removal path.
  clearStationSession(client, frame, true);
  const sessionKey = frame === 'admin' ? ['admin', 'session'] as const : ['user', 'session'] as const;
  client.setQueryData(sessionKey, null);
  return true;
}

function evictManagementProjection(
  client: ReturnType<typeof useQueryClient>,
  frame: CharityManagementFrame,
): void {
  client.removeQueries({ queryKey: charityManagementKeys.root(frame) });
  if (frame === 'steward') {
    client.removeQueries({ queryKey: ['user', 'steward-logs'] });
    client.removeQueries({ queryKey: ['user', 'charity-models'] });
    client.removeQueries({ queryKey: ['user', 'donations'] });
  }
}

function markRevoked(client: ReturnType<typeof useQueryClient>, frame: CharityManagementFrame): void {
  let frames = managementRevocations.get(client);
  if (!frames) {
    frames = new Map();
    managementRevocations.set(client, frames);
  }
  const authority = sessionAuthority(client, frame);
  frames.set(frame, {
    timestamp: Date.now(),
    subject: authority.subject ?? '',
    generation: authority.generation,
  });
}

function clearRevoked(client: ReturnType<typeof useQueryClient>, frame: CharityManagementFrame): void {
  managementRevocations.get(client)?.delete(frame);
}

/**
 * Record a normalized station-session query success for sentinel recovery.
 *
 * The request generation is checked before any authority or cache state is
 * changed. This makes an old response harmless after logout/login or a
 * same-level account switch. The subject is deliberately derived from the
 * normalized identity, never from a role alone.
 */
export function noteManagementSessionSuccess(
  client: ReturnType<typeof useQueryClient>,
  frame: CharityManagementFrame,
  value: unknown,
  generation: number,
): boolean {
  const authority = sessionAuthority(client, frame);
  if (authority.generation !== generation) return false;
  const identity = managementSessionIdentity(frame, value);
  if (!identity) return false;
  const previousSubject = authority.subject;
  const previousElevated = authority.elevated;
  authority.subject = identity.subject;
  authority.elevated = identity.elevated;

  // A new subject must never inherit a revoked latch or sensitive projection
  // from the previous login. The same subject is allowed to recover only from
  // a later, successful generation that is still elevated.
  if (previousSubject !== identity.subject) {
    clearRevoked(client, frame);
    client.setQueryData(charityManagementKeys.capability(frame), identity.elevated ? false : true);
    // The observer that owns the current session query will write the fresh
    // normalized value immediately after this authority transition. Clear the
    // previous identity first so a same-level account switch cannot render the
    // old profile during that hand-off.
    const sessionKey = frame === 'admin' ? ['admin', 'session'] as const : ['user', 'session'] as const;
    client.setQueryData(sessionKey, null);
    // A subject transition is a station boundary, not merely a management
    // capability transition.  Do not leave endpoint/key/model/log/donation
    // projections under the same React Query keys for the next account.
    // Keep the current authoritative session query alive so its observer can
    // receive this new subject. All other station projections are old-account
    // data and must be removed before the value is exposed to the UI.
    evictStationQueries(client, frame, true);
    if (!identity.elevated) markRevoked(client, frame);
  }
  const marker = managementRevocations.get(client)?.get(frame);
  if (marker
    && marker.subject === identity.subject
    && generation > marker.generation
    && identity.elevated) {
    clearRevoked(client, frame);
    client.setQueryData(charityManagementKeys.capability(frame), false);
  }
  if (frame === 'steward' && previousElevated && !identity.elevated) {
    // A successful authoritative demotion is itself a capability loss. Do not
    // start another session refresh here; this response is already fresh.
    markRevoked(client, frame);
    client.setQueryData(charityManagementKeys.capability(frame), true);
    evictManagementProjection(client, frame);
  }
  return true;
}

function basePath(frame: CharityManagementFrame): string {
  return frame === 'admin' ? '/admin/api' : '/api/steward';
}

function path(frame: CharityManagementFrame, suffix: string): string {
  return `${basePath(frame)}${suffix}`;
}

function managementDonationListKey(frame: CharityManagementFrame) {
  return ['charity-management', frame, 'donations'] as const;
}

function managementModelListKey(frame: CharityManagementFrame) {
  return ['charity-management', frame, 'models'] as const;
}

/**
 * This is a local, fail-closed capability latch.  It is intentionally not
 * inferred from a stale session role: a management 401/403 is authoritative
 * until the next page/session lifecycle creates a fresh query client.
 */
export function useManagementCapability(frame: CharityManagementFrame) {
  const client = useQueryClient();
  const previousEffectiveLevel = useRef<number | undefined>(undefined);
  const capability = useQuery({
    queryKey: charityManagementKeys.capability(frame),
    queryFn: async () => false,
    initialData: false,
    // The server never supplies this local fail-closed latch. Keeping its
    // observer disabled prevents broad cache invalidation from refetching the
    // placeholder queryFn and silently reopening revoked controls.
    enabled: false,
    staleTime: Infinity,
    retry: false,
  });
  const sessionKey = frame === 'admin' ? ['admin', 'session'] as const : ['user', 'session'] as const;
  // Subscribe to the station session without creating an unsolicited read.
  // A real station session query, when mounted, shares this key and updates
  // this observer after login/role restoration.
  const session = useQuery({
    queryKey: sessionKey,
    // This observer is deliberately disabled.  The station's authoritative
    // session query owns this key and will update every observer after a
    // login/role transition; a disabled queryFn must still return data when
    // invoked by a test/client refetch, otherwise TanStack Query warns and
    // treats the result as an invalid cache write.
    queryFn: async () => null,
    enabled: false,
    retry: false,
  });
  useEffect(() => {
    if (frame !== 'steward') return;
    const level = sessionEffectiveLevel(session.data);
    const previous = previousEffectiveLevel.current;
    // A mounted steward page can remain alive while the session query is
    // downgraded.  Treat that authoritative transition as a capability loss
    // even if no management request happened to race the downgrade.
    if (previous === 5 && level !== 5) {
      managementCapabilityLoss(client, frame);
    }
    previousEffectiveLevel.current = level;
  }, [client, frame, session.data]);
  useEffect(() => {
    const marker = managementRevocations.get(client)?.get(frame);
    const authority = sessionAuthority(client, frame);
    if (capability.data !== true || !marker || authority.generation <= marker.generation) return;
    if (!session.error
      && authority.subject === marker.subject
      && authority.elevated
      && authoritativeSessionRestored(frame, session.data)) {
      clearRevoked(client, frame);
      client.setQueryData(charityManagementKeys.capability(frame), false);
    }
  }, [capability.data, client, frame, session.data, session.dataUpdatedAt, session.error]);
  return {
    ...capability,
    authorityReady: managementAuthorityReady(client, frame),
  };
}

function useManagementReadEnabled(
  frame: CharityManagementFrame,
  requested: boolean,
): { client: ReturnType<typeof useQueryClient>; enabled: boolean } {
  const client = useQueryClient();
  const capability = useManagementCapability(frame);
  return {
    client,
    enabled: requested && capability.data !== true && managementAuthorityReady(client, frame),
  };
}

function handleManagementReadError(
  client: ReturnType<typeof useQueryClient>,
  frame: CharityManagementFrame,
  error: unknown,
): void {
  if (isManagementCapabilityError(error)) managementCapabilityLoss(client, frame);
}

function assertManagementWriteAllowed(
  client: ReturnType<typeof useQueryClient>,
  frame: CharityManagementFrame,
): ManagementReadSnapshot {
  return captureManagementAuthority(client, frame);
}

function assertManagementReadAllowed(
  client: ReturnType<typeof useQueryClient>,
  frame: CharityManagementFrame,
  snapshot?: ManagementReadSnapshot,
): void {
  const authority = sessionAuthority(client, frame);
  if (snapshot && (authority.generation !== snapshot.generation || authority.subject !== snapshot.subject)) {
    throw new ManagementSessionChangedError();
  }
  if (client.getQueryData(charityManagementKeys.capability(frame)) === true) {
    throw new ApiError('forbidden', 'Management capability is no longer active.', 403);
  }
}

function managementAuthorityChanged(
  client: ReturnType<typeof useQueryClient>,
  frame: CharityManagementFrame,
  snapshot: ManagementReadSnapshot,
): boolean {
  const authority = sessionAuthority(client, frame);
  return authority.generation !== snapshot.generation || authority.subject !== snapshot.subject;
}

async function managementRead<T>(
  client: ReturnType<typeof useQueryClient>,
  frame: CharityManagementFrame,
  request: () => Promise<T>,
): Promise<T> {
  // Capture both generation and exact subject. A session switch during the
  // await invalidates this response even if both identities have the same
  // elevated role. Missing authority is rejected by the same fail-closed
  // snapshot used by writes; standalone tests seed a subject explicitly.
  const snapshot = managementReadSnapshot(client, frame);
  try {
    const value = await request();
    assertManagementReadAllowed(client, frame, snapshot);
    return value;
  } catch (error) {
    // A rejected old request is just as stale as an old success. In
    // particular, its 401/403 must not revoke the subject that replaced it.
    if (managementAuthorityChanged(client, frame, snapshot)) throw new ManagementSessionChangedError();
    throw error;
  }
}

async function managementWrite<T>(
  client: ReturnType<typeof useQueryClient>,
  frame: CharityManagementFrame,
  request: () => Promise<T>,
): Promise<T> {
  const snapshot = assertManagementWriteAllowed(client, frame);
  try {
    const value = await request();
    assertManagementReadAllowed(client, frame, snapshot);
    return value;
  } catch (error) {
    // Convert every late response, including a rejected 401/403, into the
    // non-authoritative session-changed error before mutation reconciliation.
    if (managementAuthorityChanged(client, frame, snapshot)) throw new ManagementSessionChangedError();
    throw error;
  }
}

function isManagementCapabilityError(error: unknown): boolean {
  return isForbidden(error) || isUnauthorized(error);
}

interface ManagementReadSnapshot {
  generation: number;
  subject?: string;
}

class ManagementSessionChangedError extends Error {
  readonly managementSessionChanged = true;

  constructor() {
    super('The management session changed while the request was in flight.');
    this.name = 'ManagementSessionChangedError';
  }
}

function isManagementSessionChanged(error: unknown): error is ManagementSessionChangedError {
  return error instanceof ManagementSessionChangedError;
}

function managementAuthorityReady(
  client: ReturnType<typeof useQueryClient>,
  frame: CharityManagementFrame,
): boolean {
  const authority = sessionAuthority(client, frame);
  return Boolean(authority.subject && authority.elevated);
}

function captureManagementAuthority(
  client: ReturnType<typeof useQueryClient>,
  frame: CharityManagementFrame,
): ManagementReadSnapshot {
  assertManagementReadAllowed(client, frame);
  const authority = sessionAuthority(client, frame);
  if (!authority.subject || !authority.elevated) throw new ManagementSessionChangedError();
  return { generation: authority.generation, subject: authority.subject };
}

function managementReadSnapshot(
  client: ReturnType<typeof useQueryClient>,
  frame: CharityManagementFrame,
): ManagementReadSnapshot {
  return captureManagementAuthority(client, frame);
}

function managementSessionIdentity(
  frame: CharityManagementFrame,
  value: unknown,
): { subject: string; elevated: boolean } | undefined {
  const record = asRecord(value);
  if (!record) return undefined;
  if (frame === 'admin') {
    const admin = asRecord(record.admin);
    const username = admin?.username;
    if (!sessionUsername(username)) return undefined;
    return { subject: JSON.stringify(['admin', username]), elevated: true };
  }
  const identity = sessionUserIdentity(value);
  if (!identity) return undefined;
  return {
    subject: JSON.stringify(['user', identity.user.id, identity.user.username]),
    elevated: identity.effective_level === 5,
  };
}

function authoritativeSessionRestored(frame: CharityManagementFrame, value: unknown): boolean {
  const record = asRecord(value);
  if (!record) return false;
  if (frame === 'admin') {
    const admin = asRecord(record.admin);
    return sessionUsername(admin?.username);
  }
  return sessionUserIdentity(value)?.effective_level === 5;
}

function sessionEffectiveLevel(value: unknown): number | undefined {
  return sessionUserIdentity(value)?.effective_level;
}

function sessionUsername(value: unknown): value is string {
  return typeof value === 'string'
    && value.trim().length > 0
    && value.length <= 128
    && !hasControlCharacters(value);
}

function sessionUserIdentity(value: unknown): { user: UnknownRecord; effective_level: number } | undefined {
  const record = asRecord(value);
  const user = asRecord(record?.user);
  const id = opaqueID(user?.id);
  const effectiveLevel = user?.effective_level;
  if (!id || !/^[1-9]\d*$/.test(id) || !sessionUsername(user?.username)
    || typeof effectiveLevel !== 'number' || !Number.isSafeInteger(effectiveLevel)
    || effectiveLevel < 1 || effectiveLevel > 5) {
    return undefined;
  }
  try {
    if (BigInt(id) > BigInt(Number.MAX_SAFE_INTEGER)) return undefined;
  } catch {
    return undefined;
  }
  return { user, effective_level: effectiveLevel };
}

function sessionCacheValue(
  frame: CharityManagementFrame,
  value: unknown,
): UnknownRecord | undefined {
  const record = asRecord(value);
  if (!record) return undefined;
  if (frame === 'admin') {
    const admin = asRecord(record.admin);
    if (!sessionUsername(admin?.username)) return undefined;
    return {
      ...record,
      admin,
    };
  }
  const identity = sessionUserIdentity(value);
  if (!identity) return undefined;
  return {
    ...record,
    user: identity.user,
  };
}

async function refreshManagementSession(
  client: ReturnType<typeof useQueryClient>,
  frame: CharityManagementFrame,
): Promise<void> {
  const sessionKey = frame === 'admin' ? ['admin', 'session'] as const : ['user', 'session'] as const;
  const generation = beginManagementSessionRequest(client, frame);
  try {
    // Always issue one fresh session read. A cached/stale session projection
    // is not sufficient to reopen a latch that was closed by a 401/403.
    const value = await apiFetch<unknown>(frame === 'admin' ? '/admin/api/session' : '/api/session');
    const cacheValue = sessionCacheValue(frame, value);
    if (!cacheValue) {
      failManagementSessionRequest(client, frame, generation);
      return;
    }
    if (!noteManagementSessionSuccess(client, frame, cacheValue, generation)) return;
    client.setQueryData(sessionKey, cacheValue);
  } catch {
    // A failed or malformed refresh also discards the old subject so a
    // mounted page cannot retain management controls after session loss.
    failManagementSessionRequest(client, frame, generation);
  }
}

export function managementCapabilityLoss(client: ReturnType<typeof useQueryClient>, frame: CharityManagementFrame): void {
  // A 403 during a write can mean that a live admin/L5 capability was revoked
  // after the page loaded.  Drop every sensitive management projection before
  // refreshing the session; the page then re-renders without write controls.
  if (client.getQueryData(charityManagementKeys.capability(frame)) === true) return;
  markRevoked(client, frame);
  client.setQueryData(charityManagementKeys.capability(frame), true);
  // Abort before eviction so an in-flight observer cannot repopulate a
  // sensitive management projection while the capability/session changes.
  void client.cancelQueries({ queryKey: charityManagementKeys.root(frame), exact: false }).catch(() => undefined);
  evictManagementProjection(client, frame);
  void refreshManagementSession(client, frame);
}

async function reconcileManagementMutation(
  client: ReturnType<typeof useQueryClient>,
  frame: CharityManagementFrame,
  specs: readonly {
    queryKey: readonly unknown[];
    exact?: boolean;
    ignoreError?: (error: unknown) => boolean;
    removeOnIgnoredError?: boolean;
  }[],
  requestError: unknown,
): Promise<void> {
  if (isManagementSessionChanged(requestError)) return;
  const refreshError = await refetchAuthoritativeQueries(client, specs);
  if (isManagementSessionChanged(refreshError)) return;
  if (isManagementCapabilityError(requestError) || isManagementCapabilityError(refreshError)) {
    managementCapabilityLoss(client, frame);
  }
  if (!requestError && refreshError) throw refreshError;
}

function validPatchResult(value: unknown, field: string): UnknownRecord {
  const record = asRecord(value);
  if (!record || typeof record.key !== 'string' || !Object.prototype.hasOwnProperty.call(record, 'value')) {
    return invalidResponse(field);
  }
  return record;
}

function recordValue(record: UnknownRecord, key: string): unknown {
  return record[key];
}

function invalidResponse(field: string): never {
  throw new ApiError('invalid_response', `The server returned an invalid ${field}.`, 200);
}

function requiredRecord(value: unknown, field: string): UnknownRecord {
  const record = asRecord(value);
  if (!record) return invalidResponse(field);
  return record;
}

function requiredText(value: unknown, max: number, field: string, allowEmpty = false): string {
  if (typeof value !== 'string'
    || value.length > max
    || hasControlCharacters(value)
    || (!allowEmpty && !value.trim())) return invalidResponse(field);
  return value;
}

/** Display-only fragments may be safely cleaned and bounded before rendering. */
function displayText(value: unknown, max: number, field: string): string | undefined {
  if (value === undefined || value === null) return undefined;
  if (typeof value !== 'string') return invalidResponse(field);
  return text(value, max) || undefined;
}

function requiredID(value: unknown, field = 'id'): string {
  const id = opaqueID(value);
  if (!id) return invalidResponse(field);
  return id;
}

function optionalID(value: unknown, field = 'id'): string | undefined {
  if (value === null || value === undefined) return undefined;
  return requiredID(value, field);
}

function donationStatus(value: unknown): string {
  if (value === 'pending' || value === 'approved' || value === 'rejected' || value === 'deleted') {
    return value;
  }
  return invalidResponse('donation status');
}

function mutationID(value: string, field: string): string {
  return requiredID(value, field);
}

function amount(value: unknown, field = 'amount'): string {
  if (typeof value !== 'string' || !/^(0|[1-9]\d*)$/.test(value) || value.length > 19) {
    return invalidResponse(field);
  }
  try {
    if (BigInt(value) > 9_223_372_036_854_775_807n) return invalidResponse(field);
  } catch {
    return invalidResponse(field);
  }
  return value;
}

function boundedInteger(value: unknown, minimum: number, maximum: number, field: string): number {
  if (!Number.isSafeInteger(value) || (value as number) < minimum || (value as number) > maximum) {
    return invalidResponse(field);
  }
  return value as number;
}

function requiredCount(value: unknown, field: string, maximum = Number.MAX_SAFE_INTEGER): number {
  return boundedInteger(value, 0, maximum, field);
}

function requiredUnixSeconds(value: unknown, field: string): number {
  return boundedInteger(value, 1, Number.MAX_SAFE_INTEGER, field);
}

function requiredPolicyBoolean(value: unknown, field: string): boolean {
  if (typeof value !== 'boolean') return invalidResponse(field);
  return value;
}

function donationStorePolicy(value: unknown): boolean | 'not_applicable' {
  // Management donation responses currently omit connector_type.  The
  // server's formal projection omits this OpenAI-only field for Anthropic, so
  // an absent value is displayed read-only as not_applicable.  This is not a
  // claim that an omitted field came from OpenAI; a future self-describing
  // management wire can make that distinction fail-closed without guessing.
  if (value === undefined) return 'not_applicable';
  if (typeof value !== 'boolean') return invalidResponse('donation-key store policy');
  return value;
}

function optionalUnix(value: unknown): number | undefined {
  if (value === undefined || value === null) return undefined;
  if (typeof value !== 'number' || !Number.isSafeInteger(value) || value <= 0) {
    return invalidResponse('timestamp');
  }
  return value;
}

function fragment(record: UnknownRecord): string | undefined {
  const head = displayText(recordValue(record, 'display_head'), 8, 'display head') ?? '';
  const tail = displayText(recordValue(record, 'display_tail'), 8, 'display tail') ?? '';
  return head || tail ? `${head}${head && tail ? '…' : ''}${tail}` : undefined;
}

function listPayload(value: unknown): { items: unknown[]; hasMore: boolean; total: number } {
  if (!isListPayload(value)) throw new ApiError('invalid_response', 'The server returned an invalid list.', 200);
  const record = asRecord(value);
  if (record) {
    if (!Array.isArray(record.data)) return invalidResponse('list data');
    if (typeof record.has_more !== 'boolean') return invalidResponse('list has_more');
    if (!Number.isSafeInteger(record.total) || (record.total as number) < 0) return invalidResponse('list total');
  }
  const result = listResult(value, 100);
  return {
    items: result.items,
    hasMore: record ? record.has_more as boolean : result.hasNext,
    total: record ? record.total as number : result.items.length,
  };
}

function dataArrayPayload(value: unknown, field: string): unknown[] {
  const record = requiredRecord(value, field);
  // This management endpoint has one deliberately narrow wire shape. Do not
  // silently accept pagination/diagnostic fields that could be mistaken for a
  // different authorization or resource projection.
  if (Object.keys(record).length !== 1
    || !Object.prototype.hasOwnProperty.call(record, 'data')
    || !Array.isArray(record.data)) {
    return invalidResponse(`${field} data`);
  }
  return record.data;
}

export function normalizeManagementCharityModel(value: unknown): ManagementCharityModel {
  const record = requiredRecord(value, 'charity model');
  const rawPrices = requiredRecord(recordValue(record, 'prices'), 'charity model prices');
  const rawDiscount = requiredRecord(recordValue(record, 'discount'), 'charity model discount');
  const pricingMode = recordValue(record, 'pricing_mode');
  if (pricingMode !== 'per_token' && pricingMode !== 'per_request') return invalidResponse('charity pricing mode');
  const price = (key: string) => amount(recordValue(rawPrices, key));
  const start = optionalUnix(recordValue(rawDiscount, 'start_at'));
  const end = optionalUnix(recordValue(rawDiscount, 'end_at'));
  const samples = requiredCount(recordValue(record, 'success_samples'), 'charity model success sample count');
  const success = requiredCount(recordValue(record, 'success_count'), 'charity model success count');
  if (success > samples) return invalidResponse('charity model success count');
  return {
    id: requiredID(recordValue(record, 'id'), 'charity model id'),
    provider: requiredText(recordValue(record, 'provider'), 128, 'charity model provider'),
    model: requiredText(recordValue(record, 'model'), 256, 'charity model name'),
    full_name: requiredText(recordValue(record, 'full_name'), 512, 'charity model full name'),
    enabled: typeof recordValue(record, 'enabled') === 'boolean'
      ? recordValue(record, 'enabled') as boolean
      : invalidResponse('charity model enabled'),
    flatten_tool_calls: requiredPolicyBoolean(recordValue(record, 'flatten_tool_calls'), 'charity tool-call policy'),
    pricing_mode: pricingMode,
    prices: {
      request_user_price_milli: price('request_user_price_milli'),
      request_donor_reward_milli: price('request_donor_reward_milli'),
      uncached_user_price_milli: price('uncached_user_price_milli'),
      cache_write_user_price_milli: price('cache_write_user_price_milli'),
      cache_read_user_price_milli: price('cache_read_user_price_milli'),
      output_user_price_milli: price('output_user_price_milli'),
      uncached_donor_reward_milli: price('uncached_donor_reward_milli'),
      cache_write_donor_reward_milli: price('cache_write_donor_reward_milli'),
      cache_read_donor_reward_milli: price('cache_read_donor_reward_milli'),
      output_donor_reward_milli: price('output_donor_reward_milli'),
    },
    discount: {
      percent: boundedInteger(recordValue(rawDiscount, 'percent'), 0, 100, 'charity discount percent'),
      enabled: typeof recordValue(rawDiscount, 'enabled') === 'boolean'
        ? recordValue(rawDiscount, 'enabled') as boolean
        : invalidResponse('charity discount enabled'),
      ...(start !== undefined ? { start_at: start } : {}),
      ...(end !== undefined ? { end_at: end } : {}),
    },
    success_samples: samples,
    success_count: success,
  };
}

export function normalizeManagementDonationKey(value: unknown): ManagementDonationKey {
  const record = requiredRecord(value, 'donation key');
  const endpointKey = optionalID(recordValue(record, 'endpoint_key_id'), 'endpoint key id');
  const display = fragment(record);
  return {
    id: requiredID(recordValue(record, 'id'), 'donation key id'),
    ...(endpointKey ? { endpoint_key_id: endpointKey } : {}),
    ...(display ? { display } : {}),
    max_concurrency: boundedInteger(recordValue(record, 'max_concurrency'), 0, 100_000, 'donation key concurrency'),
    rpm_limit: boundedInteger(recordValue(record, 'rpm_limit'), 0, 4_096, 'donation key RPM'),
    credits_usage_cap_milli: amount(recordValue(record, 'credits_usage_cap_milli')),
    credits_used_milli: amount(recordValue(record, 'credits_used_milli')),
    credits_reserved_milli: amount(recordValue(record, 'credits_reserved_milli')),
    enabled: typeof recordValue(record, 'enabled') === 'boolean'
      ? recordValue(record, 'enabled') as boolean
      : invalidResponse('donation key enabled'),
    force_store_false: donationStorePolicy(recordValue(record, 'force_store_false')),
  };
}

export function normalizeManagementDonation(value: unknown, detailed: boolean): ManagementDonation {
  const record = requiredRecord(value, 'donation');
  const rawKeys = detailed ? recordValue(record, 'keys') : undefined;
  const rawReviews = detailed ? recordValue(record, 'reviews') : undefined;
  const userID = optionalID(recordValue(record, 'user_id'), 'user id');
  const endpointID = optionalID(recordValue(record, 'endpoint_id'), 'endpoint id');
  if (detailed && !Array.isArray(rawKeys)) return invalidResponse('donation keys list');
  if (detailed && rawReviews !== undefined && !Array.isArray(rawReviews)) return invalidResponse('donation review list');
  const status = donationStatus(recordValue(record, 'status'));
  const expiresAt = optionalUnix(recordValue(record, 'expires_at'));
  const reviewedAt = optionalUnix(recordValue(record, 'reviewed_at'));
  return {
    id: requiredID(recordValue(record, 'id'), 'donation id'),
    ...(userID ? { user_id: userID } : {}),
    ...(endpointID ? { endpoint_id: endpointID } : {}),
    endpoint_base_url: requiredText(recordValue(record, 'endpoint_base_url'), 2048, 'donation endpoint base URL'),
    status,
    enabled: typeof recordValue(record, 'enabled') === 'boolean'
      ? recordValue(record, 'enabled') as boolean
      : invalidResponse('donation enabled'),
    description: requiredText(recordValue(record, 'description'), 4096, 'donation description', true),
    review_note: requiredText(recordValue(record, 'review_note'), 4096, 'donation review note', true),
    ...(expiresAt !== undefined ? { expires_at: expiresAt } : {}),
    ...(reviewedAt !== undefined ? { reviewed_at: reviewedAt } : {}),
    created_at: requiredUnixSeconds(recordValue(record, 'created_at'), 'donation created timestamp'),
    updated_at: requiredUnixSeconds(recordValue(record, 'updated_at'), 'donation updated timestamp'),
    keys: detailed ? (rawKeys as unknown[]).map(normalizeManagementDonationKey) : [],
    reviews: detailed && Array.isArray(rawReviews)
      ? rawReviews.map((raw) => {
          const item = requiredRecord(raw, 'donation review');
          return {
            id: requiredID(recordValue(item, 'id'), 'review id'),
            reviewer_role: requiredText(recordValue(item, 'reviewer_role'), 32, 'reviewer role'),
            action: requiredText(recordValue(item, 'action'), 32, 'review action'),
            note: requiredText(recordValue(item, 'note'), 4096, 'review note', true),
            created_at: requiredUnixSeconds(recordValue(item, 'created_at'), 'review created timestamp'),
          };
        })
      : [],
  };
}

function normalizeBinding(value: unknown): ManagementBinding {
  const record = requiredRecord(value, 'charity binding');
  // Charity management bindings use the explicit key_display_* wire names;
  // the donation-key detail wire's display_head/display_tail names must not
  // be silently substituted here.
  const head = displayText(recordValue(record, 'key_display_head'), 8, 'key display head') ?? '';
  const tail = displayText(recordValue(record, 'key_display_tail'), 8, 'key display tail') ?? '';
  const display = head || tail ? `${head}${head && tail ? '…' : ''}${tail}` : undefined;
  return {
    id: requiredID(recordValue(record, 'id'), 'binding id'),
    charity_model_id: requiredID(recordValue(record, 'charity_model_id'), 'charity model id'),
    donation_key_id: requiredID(recordValue(record, 'donation_key_id'), 'donation key id'),
    upstream_model_id: requiredText(recordValue(record, 'upstream_model_id'), 256, 'upstream model id'),
    ord: boundedInteger(recordValue(record, 'ord'), 0, 1_000_000, 'binding order'),
    endpoint_base_url: requiredText(recordValue(record, 'endpoint_base_url'), 2048, 'endpoint base URL'),
    ...(display ? { key_display: display } : {}),
    donation_key_enabled: typeof recordValue(record, 'donation_key_enabled') === 'boolean'
      ? recordValue(record, 'donation_key_enabled') as boolean
      : invalidResponse('donation key enabled'),
  };
}

export function useManagementDonations(frame: CharityManagementFrame, page: number, status: string) {
  const { client, enabled } = useManagementReadEnabled(frame, true);
  const query = useQuery({
    queryKey: charityManagementKeys.donations(frame, page, status),
    queryFn: async (): Promise<ManagementList<ManagementDonation>> => {
      const params = new URLSearchParams({ page: String(page), page_size: '20' });
      if (status) params.set('status', status);
       const result = listPayload(await managementRead(client, frame, () => apiFetch<unknown>(path(frame, `/donations?${params}`))));
      return { items: result.items.map((item) => normalizeManagementDonation(item, false)), hasMore: result.hasMore, total: result.total };
    },
    enabled,
    retry: false,
  });
  useEffect(() => { handleManagementReadError(client, frame, query.error); }, [client, frame, query.error]);
  return query;
}

export function useManagementDonation(frame: CharityManagementFrame, id: string | undefined) {
  const { client, enabled } = useManagementReadEnabled(frame, Boolean(id));
  const query = useQuery({
    queryKey: id ? charityManagementKeys.donation(frame, id) : [...charityManagementKeys.root(frame), 'donation', 'none'],
    queryFn: async () => {
      if (!id) throw new ApiError('invalid_request', 'A donation id is required.', 400);
      const donationID = mutationID(id, 'donation id');
       return normalizeManagementDonation(await managementRead(client, frame, () => apiFetch<unknown>(path(frame, `/donations/${encodeURIComponent(donationID)}`))), true);
    },
    enabled,
    retry: false,
  });
  useEffect(() => { handleManagementReadError(client, frame, query.error); }, [client, frame, query.error]);
  return query;
}

export function useManagementModels(frame: CharityManagementFrame) {
  const { client, enabled } = useManagementReadEnabled(frame, true);
  const query = useQuery({
    queryKey: charityManagementKeys.models(frame),
    queryFn: async () => {
       const result = listPayload(await managementRead(client, frame, () => apiFetch<unknown>(path(frame, '/charity-models?page=1&page_size=100'))));
      return result.items.map(normalizeManagementCharityModel);
    },
    enabled,
    retry: false,
  });
  useEffect(() => { handleManagementReadError(client, frame, query.error); }, [client, frame, query.error]);
  return query;
}

export function useManagementBindings(frame: CharityManagementFrame, modelId: string | undefined) {
  const { client, enabled } = useManagementReadEnabled(frame, Boolean(modelId));
  const query = useQuery({
    queryKey: modelId ? charityManagementKeys.bindings(frame, modelId) : [...charityManagementKeys.root(frame), 'bindings', 'none'],
    queryFn: async () => {
      if (!modelId) return [];
      const modelID = mutationID(modelId, 'charity model id');
      const result = dataArrayPayload(
        await managementRead(client, frame, () => apiFetch<unknown>(path(frame, `/charity-models/${encodeURIComponent(modelID)}/bindings`))),
        'charity bindings list',
      );
      return result.map(normalizeBinding);
    },
    enabled,
    retry: false,
  });
  useEffect(() => { handleManagementReadError(client, frame, query.error); }, [client, frame, query.error]);
  return query;
}

export function useReviewDonation(frame: CharityManagementFrame) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: async ({ id, ...body }: { id: string; action: string; note?: string; expires_at?: number | null; keys?: unknown[] }) => {
      const donationID = mutationID(id, 'donation id');
      return managementWrite(client, frame, async () => {
        const payload = await apiFetch<unknown>(path(frame, `/donations/${encodeURIComponent(donationID)}`), { method: 'PATCH', json: body });
        return normalizeManagementDonation(payload, true);
      });
    },
    onError: (error) => { if (isManagementCapabilityError(error)) managementCapabilityLoss(client, frame); },
    onSettled: async (_value, error, variables) => {
      await reconcileManagementMutation(client, frame, [
        { queryKey: managementDonationListKey(frame), exact: false },
        { queryKey: charityManagementKeys.donation(frame, variables.id), exact: true },
      ], error);
    },
  });
}

export function useDeleteManagedDonation(frame: CharityManagementFrame) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: async (id: string) => {
      const donationID = mutationID(id, 'donation id');
      return managementWrite(client, frame, async () => {
        await apiFetch<void>(path(frame, `/donations/${encodeURIComponent(donationID)}`), { method: 'DELETE' });
      });
    },
    onError: (error) => { if (isManagementCapabilityError(error)) managementCapabilityLoss(client, frame); },
    onSettled: async (_value, error, id) => {
      await reconcileManagementMutation(client, frame, [
        { queryKey: managementDonationListKey(frame), exact: false },
        {
          queryKey: charityManagementKeys.donation(frame, id),
          exact: true,
          ignoreError: isNotFoundError,
          removeOnIgnoredError: true,
        },
      ], error);
    },
  });
}

export interface CharityModelPayload {
  provider: string;
  model: string;
  pricing_mode: CharityPricingMode;
  enabled?: boolean;
  flatten_tool_calls?: boolean;
  prices: ManagementPriceSet;
  discount: { percent: number; enabled: boolean; start_at: number | null; end_at: number | null };
}

function wirePrices(prices: ManagementPriceSet): Record<string, string> {
  return {
    request_user_price: prices.request_user_price_milli,
    request_donor_reward: prices.request_donor_reward_milli,
    uncached_user_price: prices.uncached_user_price_milli,
    cache_write_user_price: prices.cache_write_user_price_milli,
    cache_read_user_price: prices.cache_read_user_price_milli,
    output_user_price: prices.output_user_price_milli,
    uncached_donor_reward: prices.uncached_donor_reward_milli,
    cache_write_donor_reward: prices.cache_write_donor_reward_milli,
    cache_read_donor_reward: prices.cache_read_donor_reward_milli,
    output_donor_reward: prices.output_donor_reward_milli,
  };
}

export function useCreateManagedModel(frame: CharityManagementFrame) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: async (body: CharityModelPayload) => {
      return managementWrite(client, frame, async () => {
        const payload = await apiFetch<unknown>(path(frame, '/charity-models'), { method: 'POST', json: { ...body, prices: wirePrices(body.prices) } });
        return normalizeManagementCharityModel(payload);
      });
    },
    onError: (error) => { if (isManagementCapabilityError(error)) managementCapabilityLoss(client, frame); },
    onSettled: async (_value, error) => {
      await reconcileManagementMutation(client, frame, [{ queryKey: managementModelListKey(frame), exact: true }], error);
    },
  });
}

export function useUpdateManagedModel(frame: CharityManagementFrame) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: async ({ id, ...body }: { id: string; } & Partial<CharityModelPayload>) => {
      const modelID = mutationID(id, 'charity model id');
      return managementWrite(client, frame, async () => {
        const payload = await apiFetch<unknown>(path(frame, `/charity-models/${encodeURIComponent(modelID)}`), { method: 'PATCH', json: { ...body, ...(body.prices ? { prices: wirePrices(body.prices) } : {}) } });
        return normalizeManagementCharityModel(payload);
      });
    },
    onError: (error) => { if (isManagementCapabilityError(error)) managementCapabilityLoss(client, frame); },
    onSettled: async (_value, error) => {
      await reconcileManagementMutation(client, frame, [{ queryKey: managementModelListKey(frame), exact: true }], error);
    },
  });
}

export function useDeleteManagedModel(frame: CharityManagementFrame) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: async (id: string) => {
      const modelID = mutationID(id, 'charity model id');
      return managementWrite(client, frame, async () => {
        await apiFetch<void>(path(frame, `/charity-models/${encodeURIComponent(modelID)}`), { method: 'DELETE' });
      });
    },
    onError: (error) => { if (isManagementCapabilityError(error)) managementCapabilityLoss(client, frame); },
    onSettled: async (_value, error, id) => {
      await reconcileManagementMutation(client, frame, [
        { queryKey: managementModelListKey(frame), exact: true },
        {
          queryKey: charityManagementKeys.bindings(frame, id),
          exact: true,
          ignoreError: isNotFoundError,
          removeOnIgnoredError: true,
        },
      ], error);
    },
  });
}

export interface CharityBindingPayload { donation_key_id: string; upstream_model_id: string; ord?: number; }

export function useCreateManagedBinding(frame: CharityManagementFrame) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: async ({ modelId, ...body }: { modelId: string } & CharityBindingPayload) => {
      const modelID = mutationID(modelId, 'charity model id');
      const donationKeyID = mutationID(body.donation_key_id, 'donation key id');
      const numericDonationKeyID = positiveDecimalIDNumber(donationKeyID);
      if (numericDonationKeyID === undefined) return invalidResponse('donation key id');
      if (body.ord !== undefined && (!Number.isSafeInteger(body.ord) || body.ord < 0 || body.ord > 1_000_000)) return invalidResponse('binding order');
      return managementWrite(client, frame, async () => {
        const payload = await apiFetch<unknown>(path(frame, `/charity-models/${encodeURIComponent(modelID)}/bindings`), { method: 'POST', json: { ...body, donation_key_id: numericDonationKeyID } });
        return normalizeBinding(payload);
      });
    },
    onError: (error) => { if (isManagementCapabilityError(error)) managementCapabilityLoss(client, frame); },
    onSettled: async (_value, error, variables) => {
      await reconcileManagementMutation(client, frame, [
        {
          queryKey: charityManagementKeys.bindings(frame, variables.modelId), exact: true,
          ignoreError: isNotFoundError, removeOnIgnoredError: true,
        },
        { queryKey: managementModelListKey(frame), exact: true },
      ], error);
    },
  });
}

export function useUpdateManagedBinding(frame: CharityManagementFrame) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: async ({ modelId, bindingId, ...body }: { modelId: string; bindingId: string; ord?: number; upstream_model_id?: string }) => {
      const modelID = mutationID(modelId, 'charity model id');
      const bindingID = mutationID(bindingId, 'binding id');
      if (body.ord !== undefined && (!Number.isSafeInteger(body.ord) || body.ord < 0 || body.ord > 1_000_000)) return invalidResponse('binding order');
      return managementWrite(client, frame, async () => {
        const payload = await apiFetch<unknown>(path(frame, `/charity-models/${encodeURIComponent(modelID)}/bindings/${encodeURIComponent(bindingID)}`), { method: 'PATCH', json: body });
        return normalizeBinding(payload);
      });
    },
    onError: (error) => { if (isManagementCapabilityError(error)) managementCapabilityLoss(client, frame); },
    onSettled: async (_value, error, variables) => {
      await reconcileManagementMutation(client, frame, [
        {
          queryKey: charityManagementKeys.bindings(frame, variables.modelId), exact: true,
          ignoreError: isNotFoundError, removeOnIgnoredError: true,
        },
        { queryKey: managementModelListKey(frame), exact: true },
      ], error);
    },
  });
}

export function useDeleteManagedBinding(frame: CharityManagementFrame) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: async ({ modelId, bindingId }: { modelId: string; bindingId: string }) => {
      const modelID = mutationID(modelId, 'charity model id');
      const bindingID = mutationID(bindingId, 'binding id');
      return managementWrite(client, frame, async () => {
        await apiFetch<void>(path(frame, `/charity-models/${encodeURIComponent(modelID)}/bindings/${encodeURIComponent(bindingID)}`), { method: 'DELETE' });
      });
    },
    onError: (error) => { if (isManagementCapabilityError(error)) managementCapabilityLoss(client, frame); },
    onSettled: async (_value, error, variables) => {
      await reconcileManagementMutation(client, frame, [
        {
          queryKey: charityManagementKeys.bindings(frame, variables.modelId), exact: true,
          ignoreError: isNotFoundError, removeOnIgnoredError: true,
        },
        { queryKey: managementModelListKey(frame), exact: true },
      ], error);
    },
  });
}

export function useCharityAdminSettings(enabled: boolean) {
  const read = useManagementReadEnabled('admin', enabled);
  const query = useQuery({
    queryKey: charityManagementKeys.settings,
    queryFn: async () => {
      const record = requiredRecord(await managementRead(read.client, 'admin', () => apiFetch<unknown>('/admin/api/site-config')), 'charity site settings');
      if (typeof record.charity_enabled !== 'boolean'
        || typeof record.donation_accept_enabled !== 'boolean'
        || (record.charity_token_reserve_milli !== null
          && (typeof record.charity_token_reserve_milli !== 'string'
            || !/^(0|[1-9]\d*)$/.test(record.charity_token_reserve_milli)))) {
        return invalidResponse('charity site settings');
      }
      return record;
    },
    enabled: read.enabled,
    retry: false,
    staleTime: 0,
  });
  useEffect(() => { handleManagementReadError(read.client, 'admin', query.error); }, [read.client, query.error]);
  return query;
}

export function usePatchCharityAdminSetting() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: async ({ key, value }: { key: string; value: unknown }) => {
      return managementWrite(client, 'admin', async () => {
        const payload = await apiFetch<unknown>(`/admin/api/site-config/${encodeURIComponent(key)}`, { method: 'PATCH', json: { value } });
        return validPatchResult(payload, 'site configuration result');
      });
    },
    onError: (error) => { if (isManagementCapabilityError(error)) managementCapabilityLoss(client, 'admin'); },
    onSettled: async (_value, error) => {
      await reconcileManagementMutation(client, 'admin', [{ queryKey: charityManagementKeys.settings, exact: true }], error);
    },
  });
}
