import { apiFetch } from '@shared/query/http';
import type { InputTuple, Level } from './engine/types';

export interface FatFishAmounts {
  unlock_cost: string;
  ticket_price: string;
  first_clear_reward: string;
  star_rewards: [string, string, string];
}
export function formatScoreUnits(raw: string | number): string {
  const value = String(raw);
  if (!/^(0|[1-9][0-9]{0,17})$/.test(value)) return '—';
  const units = BigInt(value);
  return `${units / 100n}.${(units % 100n).toString().padStart(2, '0')}`;
}
export interface FatFishProgress {
  unlocked: boolean;
  passed: boolean;
  best_stars: number;
  best_score_units: string;
  best_at_ms: number | null;
}
export interface FatFishConditionHint {
  kind: string;
  met: boolean;
  node_id?: string;
  min?: number;
  hidden_count?: number;
  children?: FatFishConditionHint[];
}
export interface FatFishNode {
  id: string;
  period_id: string;
  title?: string;
  description?: string;
  map_x: number;
  map_y: number;
  order: number;
  hidden: boolean;
  eligible: boolean;
  progress: FatFishProgress;
  condition_hint?: FatFishConditionHint;
  revision?: string;
  version_id?: string;
  content_hash?: string;
  amounts?: FatFishAmounts;
  level?: Level;
}
export interface FatFishPeriod {
  id: string;
  title: string;
  description: string;
  state: 'draft' | 'open' | 'closed';
  visible: boolean;
  paused: boolean;
  past_public: boolean;
  starts_at: number;
  ends_at: number;
  revision: string;
  leaderboard_final: boolean;
  nodes?: FatFishNode[];
}
export interface FatFishPage<T> { items: T[]; page: number; page_size: number; has_more: boolean }
export interface FatFishResult {
  state: string;
  reason: string;
  terminal_tick: number;
  fed: number;
  total: number;
  bowl_counts: { id: number; count: number }[];
  passed: boolean;
  stars: number;
  score_units: string;
  engine_version: number;
  scoring_version: number;
  content_hash: string;
  final_state_hash: string;
  seed_commit: string;
  commitment_verified: boolean;
  ticket_charge: string;
  ticket_refund: string;
  rewards: string;
  verification_duration_ms?: number;
}
export interface FatFishChallenge {
  id: string;
  state: 'prepared' | 'active' | 'verifying' | 'settled_pass' | 'settled_fail' | 'abandoned' | 'expired' | 'cancelled_refunded';
  period_id?: string;
  node_id?: string;
  version_id: string;
  node_revision?: string;
  content_hash: string;
  engine_version: number;
  scoring_version: number;
  seed_commit: string;
  seed?: string;
  level?: Level;
  prepared_at_ms: number;
  prepare_until_ms: number;
  start_at_ms: number | null;
  end_at_ms: number | null;
  submit_until_ms: number | null;
  server_now_ms: number;
  ticket_price: string;
  result?: FatFishResult;
}
export interface FatFishLeaderboardRow {
  rank: string;
  score_units: string;
  achieved_at_ms: number;
  is_me: boolean;
  identity: { kind: string; display_name?: string; avatar_url?: string | null };
}
export interface FatFishLeaderboard {
  period_id: string;
  node_id?: string;
  final: boolean;
  page: number;
  page_size: number;
  total: number;
  rows: FatFishLeaderboardRow[];
}

const prefix = '/api/limited-activities/fat-fish';
const segment = (id: string) => encodeURIComponent(id);
const mutation = <T>(path: string, json: unknown, key: string) => apiFetch<T>(path, {
  method: 'POST', json, headers: { 'Idempotency-Key': key },
});

export const fatFishApi = {
  periods: (page = 1) => apiFetch<FatFishPage<FatFishPeriod>>(`${prefix}/periods?page=${page}`),
  period: (id: string) => apiFetch<FatFishPeriod>(`${prefix}/periods/${segment(id)}`),
  node: (periodID: string, nodeID: string) => apiFetch<FatFishNode>(`${prefix}/periods/${segment(periodID)}/nodes/${segment(nodeID)}`),
  unlock: (periodID: string, nodeID: string, revision: string, key: string) =>
    mutation<FatFishProgress>(`${prefix}/periods/${segment(periodID)}/nodes/${segment(nodeID)}/unlock`, { expected_revision: revision }, key),
  current: (capability = '') => apiFetch<FatFishChallenge | null>(`${prefix}/challenges/current`, {
    headers: capability ? { 'X-FatFish-Tab-Capability': capability } : {},
  }),
  history: (page = 1, limit: 20 | 50 | 100 = 20) => apiFetch<FatFishPage<FatFishChallenge>>(`${prefix}/history?page=${page}&limit=${limit}`),
  leaderboard: (periodID: string, nodeID = '', page = 1, pageSize: 20 | 50 | 100 = 20) =>
    apiFetch<FatFishLeaderboard>(`${prefix}/periods/${segment(periodID)}/leaderboard?page=${page}&page_size=${pageSize}${nodeID ? `&node_id=${segment(nodeID)}` : ''}`),
};

export interface FatFishSubmission { tab_capability: string; inputs: InputTuple[]; terminal_tick: number }
export interface FatFishChallengeTransport {
  prepareScope: string;
  prepare(tabCapabilityHash: string, idempotencyKey: string): Promise<FatFishChallenge>;
  start(challengeID: string, tabCapability: string, idempotencyKey: string): Promise<FatFishChallenge>;
  read(challengeID: string, tabCapability: string): Promise<FatFishChallenge>;
  submit(challengeID: string, payload: FatFishSubmission, idempotencyKey: string): Promise<FatFishChallenge>;
  current?(tabCapability: string): Promise<FatFishChallenge | null>;
  abandon?(challengeID: string, tabCapability: string, idempotencyKey: string): Promise<FatFishChallenge>;
}

export function userChallengeTransport(periodID: string, nodeID: string, revision: string): FatFishChallengeTransport {
  const challengePath = (id: string) => `${prefix}/challenges/${segment(id)}`;
  return {
    prepareScope: `user:${periodID}:${nodeID}:${revision}`,
    prepare: (tabCapabilityHash, key) => mutation(`${prefix}/challenges/prepare`, {
      period_id: periodID, node_id: nodeID, expected_revision: revision,
      tab_capability_hash: tabCapabilityHash,
    }, key),
    start: (id, tabCapability, key) => mutation(`${challengePath(id)}/start`, { tab_capability: tabCapability }, key),
    read: (id, tabCapability) => apiFetch(challengePath(id), {
      headers: { 'X-FatFish-Tab-Capability': tabCapability },
    }),
    submit: (id, payload, key) => mutation(`${challengePath(id)}/submit`, payload, key),
    current: fatFishApi.current,
    abandon: (id, tabCapability, key) => mutation(`${challengePath(id)}/abandon`, { tab_capability: tabCapability }, key),
  };
}
