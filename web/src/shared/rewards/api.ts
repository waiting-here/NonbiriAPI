import { apiFetch } from '@shared/query/http';
import { idempotentOptions, queryPath } from '@shared/operations/api';
export interface Asset {
  id: string;
  name: string;
  name_en: string;
  group: string;
  precision: number;
  maximum: string;
}
export interface Item {
  asset: string;
  quantity: string;
}
export interface Result extends Item {
  granted: string;
  skipped?: string;
  converted?: Item;
}
export interface Condition {
  op: 'all' | 'and' | 'or' | 'not' | 'compare';
  children?: Condition[];
  field?: string;
  asset?: string;
  compare?: string;
  value?: string;
}
export interface Input {
  kind: 'broadcast' | 'code';
  title: string;
  rewards: Item[];
  conditions: Condition;
  code?: string;
  enabled: boolean;
  quota?: number;
  starts_at?: number;
  ends_at?: number;
}
export interface Campaign extends Input {
  id: string;
  revision: number;
  claimed: number;
  matched: number;
  skipped: number;
  pending: number;
  failed: number;
  created_at: number;
}
export interface Preview {
  matched: number;
  banned: number;
  at: number;
}
export interface Receipt {
  id: number;
  campaign_id: string;
  title: string;
  kind: string;
  status: string;
  results: Result[];
  created_at: number;
  completed_at?: number;
}
export interface Redemption extends Receipt {
  already_claimed: boolean;
}
export interface Page<T> {
  items: T[];
  next_cursor: string;
}
export const rewardKeys = {
  admin: ['admin', 'rewards'] as const,
  user: (id: string) => ['user', id, 'rewards'] as const,
};
export const getAssets = (admin = false, signal?: AbortSignal) =>
  apiFetch<Asset[]>((admin ? '/admin' : '') + '/api/rewards/assets', { signal });
export const listCampaigns = (cursor: string, signal?: AbortSignal) =>
  apiFetch<Page<Campaign>>(queryPath('/admin/api/rewards', { cursor }), { signal });
export const previewReward = (input: Input) =>
  apiFetch<Preview>('/admin/api/rewards/preview', { method: 'POST', json: input });
export const createReward = (input: Input, key: string) =>
  apiFetch<Campaign>(
    '/admin/api/rewards/create',
    idempotentOptions(key, { method: 'POST', json: input }),
  );
export const updateReward = (
  input: Pick<
    Campaign,
    'id' | 'revision' | 'title' | 'enabled' | 'quota' | 'starts_at' | 'ends_at'
  >,
) =>
  apiFetch<Campaign>('/admin/api/rewards/update', {
    method: 'POST',
    json: {
      id: input.id,
      revision: input.revision,
      title: input.title,
      enabled: input.enabled,
      quota: input.quota,
      starts_at: input.starts_at,
      ends_at: input.ends_at,
    },
  });
export const retryReward = (id: string) =>
  apiFetch<Campaign>('/admin/api/rewards/retry', { method: 'POST', json: { id } });
export const getReceipts = (cursor: string, signal?: AbortSignal) =>
  apiFetch<Page<Receipt>>(queryPath('/api/rewards/history', { cursor }), { signal });
export const redeemReward = (code: string) =>
  apiFetch<Redemption | { rejection: string }>('/api/rewards/redeem', {
    method: 'POST',
    json: { code },
  });
