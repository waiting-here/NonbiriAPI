import { apiFetch } from '@shared/query/http';
import { decoded, idempotentOptions, queryPath } from './api';
import {
  amount,
  array,
  boolean,
  decimal,
  decimalID,
  integer,
  invalidResponse,
  nullableDecimal,
  nullableDecimalID,
  nullableString,
  nullableUnixSecond,
  oneOf,
  page,
  record,
  string,
  unixSecond,
  type CursorPage,
} from './wire';

export type CharityRole = 'admin' | 'steward';
export type DonationStatus = 'pending' | 'approved' | 'rejected' | 'deleted' | 'expired';
export type CharityState = 'pending' | 'available' | 'disabled' | 'suspended' | 'exhausted' | 'expired' | 'ended';

export interface ManagedDonationKey {
  id: string;
  endpoint_key_id: string | null;
  display_head: string;
  display_tail: string;
  safe_source: { base_url: string; connector_type: string };
  physical_enabled: boolean;
  charity_state: CharityState;
  limits: { price: string | null; calls: string | null; tokens: string | null };
  usage: { price_used: string; price_inflight: string; calls_used: string; calls_inflight: string; tokens_used: string; tokens_inflight: string };
  token_reserve: number;
  streak: { generation: string; count: string; failure_disabled: boolean };
  ended_reason: string | null;
  safe_note: string;
}

interface DonationCommon {
  id: string;
  status: DonationStatus;
  revision: string;
  description: string;
  review_result: { decision: 'approve' | 'reject'; reason: string; reviewed_at: number } | null;
  expires_at: number | null;
  keys: ManagedDonationKey[];
  reviewer: { user_id: string | null; role: 'admin' | 'steward' } | null;
  created_at: number;
  updated_at: number;
}

export interface AdminDonation extends DonationCommon {
  owner: { user_id: string; discord_id: string | null; display_name: string } | null;
}

export interface StewardDonation extends DonationCommon {
  owner: { user_id: string; display_name: string };
}

function normalizeManagedKey(value: unknown, label: string): ManagedDonationKey {
  const root = record(value, ['id', 'endpoint_key_id', 'display_head', 'display_tail', 'safe_source', 'physical_enabled', 'charity_state', 'limits', 'usage', 'token_reserve', 'streak', 'ended_reason', 'safe_note'], label);
  const source = record(root.safe_source, ['base_url', 'connector_type'], `${label} source`);
  const limits = record(root.limits, ['price', 'calls', 'tokens'], `${label} limits`);
  const usage = record(root.usage, ['price_used', 'price_inflight', 'calls_used', 'calls_inflight', 'tokens_used', 'tokens_inflight'], `${label} usage`);
  const streak = record(root.streak, ['generation', 'count', 'failure_disabled'], `${label} streak`);
  const state = oneOf(root.charity_state, ['pending', 'available', 'disabled', 'suspended', 'exhausted', 'expired', 'ended'] as const, `${label} state`);
  const endedReason = nullableString(root.ended_reason, `${label} ended reason`, { min: 1, max: 128, bytes: 512 });
  if ((state === 'ended' || state === 'expired') !== (endedReason !== null)) invalidResponse(`${label} terminal state`);
  return {
    id: decimalID(root.id, `${label} id`),
    endpoint_key_id: nullableDecimalID(root.endpoint_key_id, `${label} endpoint key id`),
    display_head: string(root.display_head, `${label} display head`, { max: 8, bytes: 32 }),
    display_tail: string(root.display_tail, `${label} display tail`, { max: 8, bytes: 32 }),
    safe_source: {
      base_url: string(source.base_url, `${label} canonical base URL`, { min: 1, max: 4_096, bytes: 4_096 }),
      connector_type: string(source.connector_type, `${label} connector`, { min: 1, max: 64, bytes: 64, ascii: true }),
    },
    physical_enabled: boolean(root.physical_enabled, `${label} physical switch`),
    charity_state: state,
    limits: {
      price: nullableAmount(limits.price, `${label} price limit`),
      calls: nullableDecimal(limits.calls, `${label} call limit`),
      tokens: nullableDecimal(limits.tokens, `${label} token limit`),
    },
    usage: {
      price_used: amount(usage.price_used, `${label} price used`, false),
      price_inflight: amount(usage.price_inflight, `${label} price inflight`, false),
      calls_used: decimal(usage.calls_used, `${label} calls used`),
      calls_inflight: decimal(usage.calls_inflight, `${label} calls inflight`),
      tokens_used: decimal(usage.tokens_used, `${label} tokens used`),
      tokens_inflight: decimal(usage.tokens_inflight, `${label} tokens inflight`),
    },
    token_reserve: integer(root.token_reserve, `${label} token reserve`, 0),
    streak: {
      generation: decimal(streak.generation, `${label} streak generation`),
      count: decimal(streak.count, `${label} streak count`),
      failure_disabled: boolean(streak.failure_disabled, `${label} failure-disabled marker`),
    },
    ended_reason: endedReason,
    safe_note: string(root.safe_note, `${label} safe note`, { max: 1_024, bytes: 4_096, multiline: true }),
  };
}

function nullableAmount(value: unknown, label: string): string | null {
  return value === null ? null : amount(value, label, false);
}

function normalizeDonationCommon(root: ReturnType<typeof record>, label: string): DonationCommon {
  const status = oneOf(root.status, ['pending', 'approved', 'rejected', 'deleted', 'expired'] as const, `${label} status`);
  let review: DonationCommon['review_result'] = null;
  if (root.review_result !== null) {
    const value = record(root.review_result, ['decision', 'reason', 'reviewed_at'], `${label} review`);
    review = {
      decision: oneOf(value.decision, ['approve', 'reject'] as const, `${label} review decision`),
      reason: string(value.reason, `${label} review reason`, { min: 1, max: 1_024, bytes: 4_096, multiline: true }),
      reviewed_at: unixSecond(value.reviewed_at, `${label} review time`),
    };
  }
  if (status === 'pending' && review !== null) invalidResponse(`${label} pending review`);
  if ((status === 'approved' || status === 'expired') && review?.decision !== 'approve') invalidResponse(`${label} approved review`);
  if (status === 'rejected' && review?.decision !== 'reject') invalidResponse(`${label} rejected review`);
  return {
    id: decimalID(root.id, `${label} id`), status,
    revision: decimal(root.revision, `${label} revision`, { positive: true }),
    description: string(root.description, `${label} donor description`, { max: 4_096, bytes: 16_384, multiline: true }),
    review_result: review,
    expires_at: nullableUnixSecond(root.expires_at, `${label} expiry`),
    keys: array(root.keys, `${label} keys`, 100).map((item) => normalizeManagedKey(item, `${label} key`)),
    reviewer: root.reviewer === null ? null : (() => { const reviewer = record(root.reviewer, ['user_id', 'role'], `${label} reviewer`); return { user_id: nullableDecimalID(reviewer.user_id, `${label} reviewer user id`), role: oneOf(reviewer.role, ['admin', 'steward'] as const, `${label} reviewer role`) }; })(),
    created_at: unixSecond(root.created_at, `${label} creation time`),
    updated_at: unixSecond(root.updated_at, `${label} update time`),
  };
}

export function normalizeAdminDonation(value: unknown): AdminDonation {
  const fields = ['id', 'status', 'revision', 'description', 'review_result', 'expires_at', 'keys', 'owner', 'reviewer', 'created_at', 'updated_at'] as const;
  const root = record(value, fields, 'administrator donation');
  const common = normalizeDonationCommon(root, 'administrator donation');
  let owner: AdminDonation['owner'] = null;
  if (root.owner !== null) {
    const item = record(root.owner, ['user_id', 'discord_id', 'display_name'], 'administrator donation owner');
    owner = { user_id: decimalID(item.user_id, 'administrator donation owner id'), discord_id: nullableString(item.discord_id, 'administrator donation Discord id', { max: 128, bytes: 128, ascii: true }), display_name: string(item.display_name, 'administrator donation owner display', { min: 1, max: 128, bytes: 512 }) };
  }
  return { ...common, owner };
}

export function normalizeStewardDonation(value: unknown): StewardDonation {
  const fields = ['id', 'status', 'revision', 'description', 'review_result', 'expires_at', 'keys', 'owner', 'reviewer', 'created_at', 'updated_at'] as const;
  const root = record(value, fields, 'steward donation');
  const common = normalizeDonationCommon(root, 'steward donation');
  const item = record(root.owner, ['user_id', 'display_name'], 'steward donation owner');
  return { ...common, owner: { user_id: decimalID(item.user_id, 'steward owner id'), display_name: string(item.display_name, 'steward owner display', { min: 1, max: 128, bytes: 512 }) } };
}

export interface CharityModel {
  id: string; provider: string; model: string; full_name: string; enabled: boolean;
  pricing: { mode: 'per_request'; user_price: string; donor_reward: string } | { mode: 'per_token'; user_prices: TokenPrices; donor_rewards: TokenPrices };
  discount: { enabled: boolean; percent: number; start_at: number | null; end_at: number | null };
  flatten_tool_calls: boolean; revision: string; binding_revision: string; binding_count: string;
  rolling_success: { sample_count: string; success_count: string; percent: string | null };
  created_at: number; updated_at: number;
}
export interface TokenPrices { uncached_input: string; cache_write_input: string; cache_read_input: string; output: string }

export interface CharityBinding {
  id: string;
  ord: number;
  donation_key_id: string;
  donation_id: string;
  source: { connector_type: string; canonical_base_url: string; display_head: string; display_tail: string };
  upstream_model_id: string;
  source_types: ('automatic' | 'manual')[];
}
export type CharityBindingCandidate = Omit<CharityBinding, 'id' | 'ord'>;
export interface CharityBindings { bindings: CharityBinding[]; binding_revision: string }

function normalizeModel(value: unknown, label: string): CharityModel {
  const root = record(value, ['id', 'provider', 'model', 'full_name', 'enabled', 'pricing', 'discount', 'flatten_tool_calls', 'revision', 'binding_revision', 'binding_count', 'rolling_success', 'created_at', 'updated_at'], label);
  const provider = string(root.provider, `${label} provider`, { min: 1, max: 64, bytes: 256 });
  const model = string(root.model, `${label} model`, { min: 1, max: 64, bytes: 256 });
  const fullName = string(root.full_name, `${label} full name`, { min: 7, max: 133, bytes: 521 });
  if (fullName !== `[公益]${provider}/${model}`) invalidResponse(`${label} full name`);
  const pricingRoot = record(root.pricing, ['mode', 'user_price', 'donor_reward', 'user_prices', 'donor_rewards'], `${label} pricing`, ['mode']);
  const mode = oneOf(pricingRoot.mode, ['per_request', 'per_token'] as const, `${label} pricing mode`);
  let pricing: CharityModel['pricing'];
  if (mode === 'per_request') {
    if (!Object.hasOwn(pricingRoot, 'user_price') || !Object.hasOwn(pricingRoot, 'donor_reward') || Object.hasOwn(pricingRoot, 'user_prices') || Object.hasOwn(pricingRoot, 'donor_rewards')) invalidResponse(`${label} request pricing`);
    pricing = { mode, user_price: amount(pricingRoot.user_price, `${label} user price`, false), donor_reward: amount(pricingRoot.donor_reward, `${label} donor reward`, false) };
  } else {
    if (!Object.hasOwn(pricingRoot, 'user_prices') || !Object.hasOwn(pricingRoot, 'donor_rewards') || Object.hasOwn(pricingRoot, 'user_price') || Object.hasOwn(pricingRoot, 'donor_reward')) invalidResponse(`${label} token pricing`);
    pricing = { mode, user_prices: normalizeTokenPrices(pricingRoot.user_prices, `${label} user prices`), donor_rewards: normalizeTokenPrices(pricingRoot.donor_rewards, `${label} donor rewards`) };
  }
  const discount = record(root.discount, ['enabled', 'percent', 'start_at', 'end_at'], `${label} discount`);
  const startAt = nullableUnixSecond(discount.start_at, `${label} discount start`);
  const endAt = nullableUnixSecond(discount.end_at, `${label} discount end`);
  if (startAt !== null && endAt !== null && startAt > endAt) invalidResponse(`${label} discount window`);
  const rolling = record(root.rolling_success, ['sample_count', 'success_count', 'percent'], `${label} rolling success`);
  const samples = decimal(rolling.sample_count, `${label} sample count`);
  const success = decimal(rolling.success_count, `${label} success count`);
  if (BigInt(success) > BigInt(samples)) invalidResponse(`${label} success count`);
  const percent = nullableString(rolling.percent, `${label} success percent`, { max: 6, bytes: 6, ascii: true });
  if ((samples === '0') !== (percent === null) || (percent !== null && !/^(0|[1-9][0-9]?|100)(\.[0-9]{1,2})?$/.test(percent))) invalidResponse(`${label} success percent`);
  return {
    id: decimalID(root.id, `${label} id`), provider, model, full_name: fullName,
    enabled: boolean(root.enabled, `${label} enabled`), pricing,
    discount: { enabled: boolean(discount.enabled, `${label} discount enabled`), percent: integer(discount.percent, `${label} discount percent`, 0, 100), start_at: startAt, end_at: endAt },
    flatten_tool_calls: boolean(root.flatten_tool_calls, `${label} flatten tool calls`),
    revision: decimal(root.revision, `${label} revision`, { positive: true }),
    binding_revision: decimal(root.binding_revision, `${label} binding revision`),
    binding_count: decimal(root.binding_count, `${label} binding count`),
    rolling_success: { sample_count: samples, success_count: success, percent },
    created_at: unixSecond(root.created_at, `${label} creation time`), updated_at: unixSecond(root.updated_at, `${label} update time`),
  };
}

function normalizeTokenPrices(value: unknown, label: string): TokenPrices {
  const root = record(value, ['uncached_input', 'cache_write_input', 'cache_read_input', 'output'], label);
  return { uncached_input: amount(root.uncached_input, `${label} uncached input`, false), cache_write_input: amount(root.cache_write_input, `${label} cache write`, false), cache_read_input: amount(root.cache_read_input, `${label} cache read`, false), output: amount(root.output, `${label} output`, false) };
}

export const normalizeAdminCharityModel = (value: unknown) => normalizeModel(value, 'administrator charity model');
export const normalizeStewardCharityModel = (value: unknown) => normalizeModel(value, 'steward charity model');

function normalizeSource(value: unknown, label: string): CharityBinding['source'] {
  const root = record(value, ['connector_type', 'canonical_base_url', 'display_head', 'display_tail'], label);
  return {
    connector_type: string(root.connector_type, `${label} connector`, { min: 1, max: 64, bytes: 64, ascii: true }),
    canonical_base_url: string(root.canonical_base_url, `${label} base URL`, { min: 1, max: 4_096, bytes: 4_096 }),
    display_head: string(root.display_head, `${label} display head`, { max: 8, bytes: 32 }),
    display_tail: string(root.display_tail, `${label} display tail`, { max: 8, bytes: 32 }),
  };
}

function normalizeSourceTypes(value: unknown, label: string): ('automatic' | 'manual')[] {
  const values = array(value, label, 2).map((item) => oneOf(item, ['automatic', 'manual'] as const, label));
  if (values.length === 0 || new Set(values).size !== values.length) invalidResponse(label);
  return values;
}

function normalizeBinding(value: unknown, label: string): CharityBinding {
  const root = record(value, ['id', 'ord', 'donation_key_id', 'donation_id', 'source', 'upstream_model_id', 'source_types'], label);
  return {
    id: decimalID(root.id, `${label} id`), ord: integer(root.ord, `${label} order`, 0, 1_000_000),
    donation_key_id: decimalID(root.donation_key_id, `${label} donation key id`), donation_id: decimalID(root.donation_id, `${label} donation id`),
    source: normalizeSource(root.source, `${label} source`), upstream_model_id: string(root.upstream_model_id, `${label} upstream model`, { min: 1, max: 512, bytes: 2_048 }),
    source_types: normalizeSourceTypes(root.source_types, `${label} source types`),
  };
}

function normalizeCandidate(value: unknown, label: string): CharityBindingCandidate {
  const root = record(value, ['donation_key_id', 'donation_id', 'source', 'upstream_model_id', 'source_types'], label);
  return {
    donation_key_id: decimalID(root.donation_key_id, `${label} donation key id`), donation_id: decimalID(root.donation_id, `${label} donation id`),
    source: normalizeSource(root.source, `${label} source`), upstream_model_id: string(root.upstream_model_id, `${label} upstream model`, { min: 1, max: 512, bytes: 2_048 }),
    source_types: normalizeSourceTypes(root.source_types, `${label} source types`),
  };
}

function normalizeBindings(value: unknown, label: string): CharityBindings {
  const root = record(value, ['bindings', 'binding_revision'], label);
  const bindings = array(root.bindings, `${label} bindings`, 10_000).map((entry) => normalizeBinding(entry, `${label} binding`));
  if (new Set(bindings.map((entry) => entry.id)).size !== bindings.length || bindings.some((entry, index) => entry.ord !== index)) invalidResponse(`${label} binding order`);
  return { bindings, binding_revision: decimal(root.binding_revision, `${label} revision`) };
}

const charityRoot = (role: CharityRole) => role === 'admin'
  ? ['admin', 'operations', 'charity'] as const
  : ['user', 'operations', 'steward', 'charity'] as const;

export const charityKeys = {
  root: charityRoot,
  donations: (role: CharityRole, status: string, cursor: string | null) => [...charityRoot(role), 'donations', status, cursor] as const,
  donation: (role: CharityRole, id: string) => [...charityRoot(role), 'donation', id] as const,
  models: (role: CharityRole, query: string, enabled: string, cursor: string | null) => [...charityRoot(role), 'models', query, enabled, cursor] as const,
  bindings: (role: CharityRole, id: string) => [...charityRoot(role), 'model', id, 'bindings'] as const,
  candidates: (role: CharityRole, id: string, query: string, cursor: string | null) => [...charityRoot(role), 'model', id, 'candidates', query, cursor] as const,
};

const base = (role: CharityRole) => role === 'admin' ? '/admin/api' : '/api/steward';
function decodeDonation(role: CharityRole, value: unknown): AdminDonation | StewardDonation {
  return role === 'admin' ? normalizeAdminDonation(value) : normalizeStewardDonation(value);
}
export const getManagedDonations = (role: CharityRole, status: string, cursor: string | null): Promise<CursorPage<AdminDonation | StewardDonation>> => decoded(queryPath(`${base(role)}/donations`, { status: status || undefined, cursor, limit: 50 }), (value) => page<AdminDonation | StewardDonation>(value, `${role} donation page`, (entry) => decodeDonation(role, entry)));
export const getManagedDonation = (role: CharityRole, id: string): Promise<AdminDonation | StewardDonation> => decoded(`${base(role)}/donations/${encodeURIComponent(decimalID(id, `${role} donation id`))}`, (value) => decodeDonation(role, value));
export const reviewManagedDonation = (role: CharityRole, id: string, body: unknown, key: string): Promise<AdminDonation | StewardDonation> => decoded(`${base(role)}/donations/${encodeURIComponent(decimalID(id, `${role} donation id`))}/review`, (value) => decodeDonation(role, value), idempotentOptions(key, { method: 'POST', json: body }));
export const patchManagedDonationKey = (role: CharityRole, id: string, keyID: string, body: unknown, key: string): Promise<AdminDonation | StewardDonation> => decoded(`${base(role)}/donations/${encodeURIComponent(decimalID(id, `${role} donation id`))}/keys/${encodeURIComponent(decimalID(keyID, `${role} donation key id`))}`, (value) => decodeDonation(role, value), idempotentOptions(key, { method: 'PATCH', json: body }));
export const getManagedCharityModels = (role: CharityRole, query: string, enabled: string, cursor: string | null): Promise<CursorPage<CharityModel>> => decoded(queryPath(`${base(role)}/charity-models`, { q: query || undefined, enabled: enabled || undefined, cursor, limit: 50 }), (value) => page(value, `${role} charity model page`, role === 'admin' ? normalizeAdminCharityModel : normalizeStewardCharityModel));
export const createManagedCharityModel = (role: CharityRole, body: unknown, key: string) => decoded(`${base(role)}/charity-models`, role === 'admin' ? normalizeAdminCharityModel : normalizeStewardCharityModel, idempotentOptions(key, { method: 'POST', json: body }));
export const patchManagedCharityModel = (role: CharityRole, id: string, body: unknown, key: string) => decoded(`${base(role)}/charity-models/${encodeURIComponent(decimalID(id, `${role} charity model id`))}`, role === 'admin' ? normalizeAdminCharityModel : normalizeStewardCharityModel, idempotentOptions(key, { method: 'PATCH', json: body }));
export async function deleteManagedCharityModel(role: CharityRole, id: string, revision: string, key: string): Promise<void> {
  await apiFetch<void>(`${base(role)}/charity-models/${encodeURIComponent(decimalID(id, `${role} charity model id`))}`, idempotentOptions(key, { method: 'DELETE', json: { expected_revision: revision, confirmation: 'DELETE' } }));
}
export const getManagedBindings = (role: CharityRole, id: string) => decoded(`${base(role)}/charity-models/${encodeURIComponent(decimalID(id, `${role} charity model id`))}/bindings`, (value) => normalizeBindings(value, `${role} charity bindings`));
export const getManagedBindingCandidates = (role: CharityRole, id: string, query: string, cursor: string | null): Promise<CursorPage<CharityBindingCandidate>> => decoded(queryPath(`${base(role)}/charity-models/${encodeURIComponent(decimalID(id, `${role} charity model id`))}/binding-candidates`, { q: query || undefined, cursor, limit: 50 }), (value) => page(value, `${role} binding candidate page`, (entry) => normalizeCandidate(entry, `${role} binding candidate`)));
export const addManagedBindings = (role: CharityRole, id: string, revision: string, selections: { donation_key_id: string; upstream_model_id: string }[], key: string) => decoded(`${base(role)}/charity-models/${encodeURIComponent(decimalID(id, `${role} charity model id`))}/bindings/batch`, (value) => normalizeBindings(value, `${role} charity bindings`), idempotentOptions(key, { method: 'POST', json: { expected_binding_revision: revision, selections } }));
export const orderManagedBindings = (role: CharityRole, id: string, revision: string, order: string[], key: string) => decoded(`${base(role)}/charity-models/${encodeURIComponent(decimalID(id, `${role} charity model id`))}/bindings/order`, (value) => normalizeBindings(value, `${role} charity bindings`), idempotentOptions(key, { method: 'PUT', json: { expected_binding_revision: revision, order } }));
export const deleteManagedBinding = (role: CharityRole, modelID: string, bindingID: string, revision: string, key: string) => decoded(`${base(role)}/charity-models/${encodeURIComponent(decimalID(modelID, `${role} charity model id`))}/bindings/${encodeURIComponent(decimalID(bindingID, `${role} charity binding id`))}`, (value) => normalizeBindings(value, `${role} charity bindings`), idempotentOptions(key, { method: 'DELETE', json: { expected_binding_revision: revision } }));
