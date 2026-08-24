import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ApiError, apiFetch } from '@shared/query/http';
import {
  asArray,
  asRecord,
  booleanValue,
  integerValue,
  idValue,
  isListPayload,
  listResult,
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
  force_store_false: boolean;
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
  donations: (frame: CharityManagementFrame, page: number, status: string) =>
    ['charity-management', frame, 'donations', page, status] as const,
  donation: (frame: CharityManagementFrame, id: string) =>
    ['charity-management', frame, 'donation', id] as const,
  models: (frame: CharityManagementFrame) => ['charity-management', frame, 'models'] as const,
  bindings: (frame: CharityManagementFrame, modelId: string) =>
    ['charity-management', frame, 'bindings', modelId] as const,
  settings: ['charity-management', 'admin', 'settings'] as const,
};

function basePath(frame: CharityManagementFrame): string {
  return frame === 'admin' ? '/admin/api' : '/api/steward';
}

function path(frame: CharityManagementFrame, suffix: string): string {
  return `${basePath(frame)}${suffix}`;
}

function recordValue(record: UnknownRecord, key: string): unknown {
  return record[key];
}

function invalidResponse(field: string): never {
  throw new ApiError('invalid_response', `The server returned an invalid ${field}.`, 200);
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

function additivePolicyBoolean(value: unknown, field: string): boolean {
  if (value === undefined) return false;
  if (typeof value !== 'boolean') return invalidResponse(field);
  return value;
}

function optionalUnix(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isSafeInteger(value) && value > 0 ? value : undefined;
}

function fragment(record: UnknownRecord): string | undefined {
  const head = typeof recordValue(record, 'display_head') === 'string' ? recordValue(record, 'display_head') as string : '';
  const tail = typeof recordValue(record, 'display_tail') === 'string' ? recordValue(record, 'display_tail') as string : '';
  return head || tail ? `${head}${head && tail ? '…' : ''}${tail}` : undefined;
}

function listPayload(value: unknown): { items: unknown[]; hasMore: boolean; total: number } {
  if (!isListPayload(value)) throw new ApiError('invalid_response', 'The server returned an invalid list.', 200);
  const record = asRecord(value);
  const result = listResult(value, 100);
  const rawMore = record?.has_more;
  return {
    items: result.items,
    hasMore: typeof rawMore === 'boolean' ? rawMore : result.hasNext,
    total: typeof record?.total === 'number' && Number.isSafeInteger(record.total) ? Math.max(0, record.total) : result.items.length,
  };
}

export function normalizeManagementCharityModel(value: unknown): ManagementCharityModel {
  const record = asRecord(value) ?? {};
  const rawPrices = asRecord(recordValue(record, 'prices')) ?? {};
  const rawDiscount = asRecord(recordValue(record, 'discount')) ?? {};
  const price = (key: string) => amount(recordValue(rawPrices, key));
  const start = optionalUnix(recordValue(rawDiscount, 'start_at'));
  const end = optionalUnix(recordValue(rawDiscount, 'end_at'));
  return {
    id: idValue(recordValue(record, 'id')),
    provider: text(recordValue(record, 'provider'), 128, '—'),
    model: text(recordValue(record, 'model'), 256, '—'),
    full_name: text(recordValue(record, 'full_name'), 512, '—'),
    enabled: booleanValue(recordValue(record, 'enabled')),
    flatten_tool_calls: additivePolicyBoolean(
      recordValue(record, 'flatten_tool_calls'), 'charity tool-call policy',
    ),
    pricing_mode: recordValue(record, 'pricing_mode') === 'per_token' ? 'per_token' : 'per_request',
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
      percent: Math.max(0, Math.min(100, integerValue(recordValue(rawDiscount, 'percent')))),
      enabled: booleanValue(recordValue(rawDiscount, 'enabled')),
      ...(start !== undefined ? { start_at: start } : {}),
      ...(end !== undefined ? { end_at: end } : {}),
    },
    success_samples: Math.max(0, integerValue(recordValue(record, 'success_samples'))),
    success_count: Math.max(0, integerValue(recordValue(record, 'success_count'))),
  };
}

export function normalizeManagementDonationKey(value: unknown): ManagementDonationKey {
  const record = asRecord(value) ?? {};
  const endpointKey = recordValue(record, 'endpoint_key_id');
  return {
    id: idValue(recordValue(record, 'id')),
    ...(endpointKey !== null && endpointKey !== undefined ? { endpoint_key_id: idValue(endpointKey) } : {}),
    ...(fragment(record) ? { display: fragment(record) } : {}),
    max_concurrency: boundedInteger(recordValue(record, 'max_concurrency'), 0, 100_000, 'donation key concurrency'),
    rpm_limit: boundedInteger(recordValue(record, 'rpm_limit'), 0, 4_096, 'donation key RPM'),
    credits_usage_cap_milli: amount(recordValue(record, 'credits_usage_cap_milli')),
    credits_used_milli: amount(recordValue(record, 'credits_used_milli')),
    credits_reserved_milli: amount(recordValue(record, 'credits_reserved_milli')),
    enabled: booleanValue(recordValue(record, 'enabled'), true),
    force_store_false: additivePolicyBoolean(
      recordValue(record, 'force_store_false'), 'donation-key store policy',
    ),
  };
}

export function normalizeManagementDonation(value: unknown, detailed: boolean): ManagementDonation {
  const record = asRecord(value) ?? {};
  const rawKeys = asArray(recordValue(record, 'keys'));
  const rawReviews = asArray(recordValue(record, 'reviews'));
  const userID = recordValue(record, 'user_id');
  return {
    id: idValue(recordValue(record, 'id')),
    ...(userID !== null && userID !== undefined ? { user_id: idValue(userID) } : {}),
    endpoint_base_url: text(recordValue(record, 'endpoint_base_url'), 2048, '—'),
    status: text(recordValue(record, 'status'), 32, 'pending'),
    enabled: booleanValue(recordValue(record, 'enabled')),
    description: text(recordValue(record, 'description'), 4096),
    review_note: text(recordValue(record, 'review_note'), 4096),
    ...(optionalUnix(recordValue(record, 'expires_at')) !== undefined ? { expires_at: optionalUnix(recordValue(record, 'expires_at')) } : {}),
    ...(optionalUnix(recordValue(record, 'reviewed_at')) !== undefined ? { reviewed_at: optionalUnix(recordValue(record, 'reviewed_at')) } : {}),
    created_at: Math.max(0, integerValue(recordValue(record, 'created_at'))),
    updated_at: Math.max(0, integerValue(recordValue(record, 'updated_at'))),
    keys: detailed ? rawKeys.map(normalizeManagementDonationKey) : [],
    reviews: detailed
      ? rawReviews.map((raw) => {
          const item = asRecord(raw) ?? {};
          return {
            id: idValue(recordValue(item, 'id')),
            reviewer_role: text(recordValue(item, 'reviewer_role'), 32),
            action: text(recordValue(item, 'action'), 32),
            note: text(recordValue(item, 'note'), 4096),
            created_at: Math.max(0, integerValue(recordValue(item, 'created_at'))),
          };
        })
      : [],
  };
}

function normalizeBinding(value: unknown): ManagementBinding {
  const record = asRecord(value) ?? {};
  return {
    id: idValue(recordValue(record, 'id')),
    charity_model_id: idValue(recordValue(record, 'charity_model_id')),
    donation_key_id: idValue(recordValue(record, 'donation_key_id')),
    upstream_model_id: text(recordValue(record, 'upstream_model_id'), 256, '—'),
    ord: Math.max(0, integerValue(recordValue(record, 'ord'))),
    endpoint_base_url: text(recordValue(record, 'endpoint_base_url'), 2048, '—'),
    ...(fragment(record) ? { key_display: fragment(record) } : {}),
    donation_key_enabled: booleanValue(recordValue(record, 'donation_key_enabled')),
  };
}

export function useManagementDonations(frame: CharityManagementFrame, page: number, status: string) {
  return useQuery({
    queryKey: charityManagementKeys.donations(frame, page, status),
    queryFn: async (): Promise<ManagementList<ManagementDonation>> => {
      const params = new URLSearchParams({ page: String(page), page_size: '20' });
      if (status) params.set('status', status);
      const result = listPayload(await apiFetch<unknown>(path(frame, `/donations?${params}`)));
      return { items: result.items.map((item) => normalizeManagementDonation(item, false)), hasMore: result.hasMore, total: result.total };
    },
  });
}

export function useManagementDonation(frame: CharityManagementFrame, id: string | undefined) {
  return useQuery({
    queryKey: id ? charityManagementKeys.donation(frame, id) : [...charityManagementKeys.root(frame), 'donation', 'none'],
    queryFn: async () => {
      if (!id) throw new ApiError('invalid_request', 'A donation id is required.', 400);
      return normalizeManagementDonation(await apiFetch<unknown>(path(frame, `/donations/${encodeURIComponent(id)}`)), true);
    },
    enabled: Boolean(id),
  });
}

export function useManagementModels(frame: CharityManagementFrame) {
  return useQuery({
    queryKey: charityManagementKeys.models(frame),
    queryFn: async () => {
      const result = listPayload(await apiFetch<unknown>(path(frame, '/charity-models?page=1&page_size=100')));
      return result.items.map(normalizeManagementCharityModel);
    },
  });
}

export function useManagementBindings(frame: CharityManagementFrame, modelId: string | undefined) {
  return useQuery({
    queryKey: modelId ? charityManagementKeys.bindings(frame, modelId) : [...charityManagementKeys.root(frame), 'bindings', 'none'],
    queryFn: async () => {
      if (!modelId) return [];
      const result = listPayload(await apiFetch<unknown>(path(frame, `/charity-models/${encodeURIComponent(modelId)}/bindings`)));
      return result.items.map(normalizeBinding);
    },
    enabled: Boolean(modelId),
  });
}

function invalidate(frame: CharityManagementFrame, client: ReturnType<typeof useQueryClient>) {
  void client.invalidateQueries({ queryKey: charityManagementKeys.root(frame) });
}

export function useReviewDonation(frame: CharityManagementFrame) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...body }: { id: string; action: string; note?: string; expires_at?: number | null; keys?: unknown[] }) =>
      apiFetch<unknown>(path(frame, `/donations/${encodeURIComponent(id)}`), { method: 'PATCH', json: body }),
    onSuccess: () => invalidate(frame, client),
  });
}

export function useDeleteManagedDonation(frame: CharityManagementFrame) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => apiFetch<void>(path(frame, `/donations/${encodeURIComponent(id)}`), { method: 'DELETE' }),
    onSuccess: () => invalidate(frame, client),
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
    mutationFn: (body: CharityModelPayload) => apiFetch<unknown>(path(frame, '/charity-models'), { method: 'POST', json: { ...body, prices: wirePrices(body.prices) } }),
    onSuccess: () => invalidate(frame, client),
  });
}

export function useUpdateManagedModel(frame: CharityManagementFrame) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...body }: { id: string; } & Partial<CharityModelPayload>) => apiFetch<unknown>(path(frame, `/charity-models/${encodeURIComponent(id)}`), { method: 'PATCH', json: { ...body, ...(body.prices ? { prices: wirePrices(body.prices) } : {}) } }),
    onSuccess: () => invalidate(frame, client),
  });
}

export function useDeleteManagedModel(frame: CharityManagementFrame) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => apiFetch<void>(path(frame, `/charity-models/${encodeURIComponent(id)}`), { method: 'DELETE' }),
    onSuccess: () => invalidate(frame, client),
  });
}

export interface CharityBindingPayload { donation_key_id: string; upstream_model_id: string; ord?: number; }

export function useCreateManagedBinding(frame: CharityManagementFrame) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: ({ modelId, ...body }: { modelId: string } & CharityBindingPayload) => apiFetch<unknown>(path(frame, `/charity-models/${encodeURIComponent(modelId)}/bindings`), { method: 'POST', json: { ...body, donation_key_id: Number(body.donation_key_id) } }),
    onSuccess: (_value, variables) => {
      void client.invalidateQueries({ queryKey: charityManagementKeys.bindings(frame, variables.modelId) });
      invalidate(frame, client);
    },
  });
}

export function useUpdateManagedBinding(frame: CharityManagementFrame) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: ({ modelId, bindingId, ...body }: { modelId: string; bindingId: string; ord?: number; upstream_model_id?: string }) => apiFetch<unknown>(path(frame, `/charity-models/${encodeURIComponent(modelId)}/bindings/${encodeURIComponent(bindingId)}`), { method: 'PATCH', json: body }),
    onSuccess: (_value, variables) => { void client.invalidateQueries({ queryKey: charityManagementKeys.bindings(frame, variables.modelId) }); },
  });
}

export function useDeleteManagedBinding(frame: CharityManagementFrame) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: ({ modelId, bindingId }: { modelId: string; bindingId: string }) => apiFetch<void>(path(frame, `/charity-models/${encodeURIComponent(modelId)}/bindings/${encodeURIComponent(bindingId)}`), { method: 'DELETE' }),
    onSuccess: (_value, variables) => { void client.invalidateQueries({ queryKey: charityManagementKeys.bindings(frame, variables.modelId) }); },
  });
}

export function useCharityAdminSettings(enabled: boolean) {
  return useQuery({
    queryKey: charityManagementKeys.settings,
    queryFn: async () => {
      const record = asRecord(await apiFetch<unknown>('/admin/api/site-config')) ?? {};
      return record;
    },
    enabled,
    staleTime: 0,
  });
}

export function usePatchCharityAdminSetting() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: ({ key, value }: { key: string; value: unknown }) => apiFetch<unknown>(`/admin/api/site-config/${encodeURIComponent(key)}`, { method: 'PATCH', json: { value } }),
    onSuccess: () => { void client.invalidateQueries({ queryKey: charityManagementKeys.settings }); },
  });
}
