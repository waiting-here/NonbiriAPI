import { apiFetch } from '@shared/query/http';

export interface AssetRule {
  mode: 'percent' | 'fixed';
  value: string;
  floor: string;
}
export interface Policy {
  enabled: boolean;
  decay: {
    enabled: boolean;
    inactive_days: number | null;
    interval_days: number | null;
    assets: { general: AssetRule | null; game: AssetRule | null };
  };
  protection: { enabled: boolean; inactive_days: number | null };
}
export interface Configuration extends Policy {
  revision: string;
  decay_grace_until: number;
  protection_grace_until: number;
  updated_at: number;
}
export interface Status {
  configuration: Configuration;
  activity: {
    observation_started_at: number;
    last_active_at: number | null;
    last_decay_at: number | null;
    activity_seq: string;
    activity_epoch: string;
  };
  exempt_reason: string;
  decay_at: number | null;
  protection_at: number | null;
}
export interface PreviewEntry {
  user_id: string;
  exempt_reason: string;
  action: 'none' | 'decay' | 'protection';
  scheduled_at: number | null;
  general_milli: string;
  game_milli: string;
}
export interface Preview {
  data: PreviewEntry[];
  next_cursor: string | null;
  as_of: number;
  configuration: Configuration;
}
export interface Run {
  id: string;
  user_id: string | null;
  policy_revision: string;
  action: 'decay' | 'protection';
  general_milli: string;
  game_milli: string;
  created_at: number;
}
export interface Runs {
  data: Run[];
  next_cursor: string | null;
}
export interface Audit {
  id: string;
  actor_user_id: string | null;
  action: string;
  policy_revision: string;
  details: unknown;
  created_at: number;
}
export interface Audits {
  data: Audit[];
  next_cursor: string | null;
}
const root = '/admin/api/inactivity-policy';
export const getPolicy = (signal?: AbortSignal) => apiFetch<Configuration>(root, { signal });
export const getStatus = (signal?: AbortSignal) =>
  apiFetch<Status>('/api/inactivity-policy/status', { signal });
export const putPolicy = (revision: string, policy: Policy, key: string) =>
  apiFetch<Configuration>(root, {
    method: 'PUT',
    headers: { 'Idempotency-Key': key },
    json: { expected_revision: revision, policy },
  });
export const previewPolicy = (revision: string, policy: Policy, cursor?: string) =>
  apiFetch<Preview>(`${root}/preview`, {
    method: 'POST',
    json: { expected_revision: revision, policy, page_size: 100, ...(cursor ? { cursor } : {}) },
  });
export const getRuns = (cursor?: string, signal?: AbortSignal) =>
  apiFetch<Runs>(`${root}/runs${cursor ? `?cursor=${encodeURIComponent(cursor)}` : ''}`, {
    signal,
  });

export function policyOnly(config: Configuration): Policy {
  return {
    enabled: config.enabled,
    decay: structuredClone(config.decay),
    protection: structuredClone(config.protection),
  };
}
export const getAudits = (cursor?: string, signal?: AbortSignal) =>
  apiFetch<Audits>(`${root}/audits${cursor ? `?cursor=${encodeURIComponent(cursor)}` : ''}`, {
    signal,
  });
export function credits(milli: string): string {
  if (!/^-?(0|[1-9][0-9]{0,38})$/.test(milli)) return '—';
  const value = BigInt(milli);
  const sign = value < 0n ? '-' : '';
  const absolute = value < 0n ? -value : value;
  const fraction = (absolute % 1000n).toString().padStart(3, '0').replace(/0+$/, '');
  return `${sign}${absolute / 1000n}${fraction ? `.${fraction}` : ''}`;
}
