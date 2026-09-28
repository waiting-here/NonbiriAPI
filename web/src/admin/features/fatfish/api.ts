import type { Level } from '@shared/fatfish/engine/types';
import { apiFetch } from '@shared/query/http';
import { idempotentOptions, queryPath } from '@shared/operations/api';

const base = '/admin/api/limited-activities/fat-fish';
const part = (value: string) => encodeURIComponent(value);

export interface Page<T> { items: T[]; page: number; page_size: number; has_more: boolean }
export interface LevelRecord {
  id: string; title: string; description: string; revision: string;
  draft?: Level; created_at: number; updated_at: number;
}
export interface VersionRecord {
  id: string; level_id: string; content_hash: string; engine_version: number;
  scoring_version: number; duration_seconds: number; maximum_stars: number;
  content?: Level; created_at: number;
}
export interface Amounts {
  unlock_cost: string; ticket_price: string; first_clear_reward: string;
  star_rewards: [string, string, string];
}
export type Condition = Record<string, never> | { all: Condition[] } | { any: Condition[] }
  | { passed: string } | { stars: { node: string; min: number } }
  | { passed_count: number } | { total_stars: number };
export interface NodeRecord {
  id: string; period_id: string; title: string; description: string;
  map_x: number; map_y: number; order: number; revision: string;
  version_id: string; content_hash?: string; hidden: boolean;
  condition?: Condition; amounts?: Amounts;
}
export interface PeriodRecord {
  id: string; title: string; description: string; state: 'draft' | 'open' | 'closed';
  visible: boolean; paused: boolean; past_public: boolean;
  starts_at: number; ends_at: number; revision: string;
  leaderboard_final: boolean; nodes?: NodeRecord[];
}
export interface GraphValidation {
  publishable: boolean; reachable: string[]; unreachable: string[]; missing_playtests: string[];
}
export interface PlaytestRecord {
  id: string; version_id: string; passed: boolean; stars: number;
  score_units: string; duration_ms: number; created_at: number;
}
export interface LevelInput { title: string; description: string; draft: Level; expected_revision?: string }
export interface PeriodInput {
  title: string; description: string; visible: boolean; paused: boolean; past_public: boolean;
  starts_at: number; ends_at: number; expected_revision?: string;
}
export interface NodeInput {
  title: string; description: string; map_x: number; map_y: number; order: number;
  version_id: string; condition: Condition; hidden_until_eligible: boolean; amounts: Amounts;
  expected_revision?: string; expected_period_revision: string;
}

export const listLevels = (page = 1) => apiFetch<Page<LevelRecord>>(queryPath(`${base}/levels`, { page }));
export const getLevel = (id: string) => apiFetch<LevelRecord>(`${base}/levels/${part(id)}`);
export const listVersions = (id: string, page = 1) => apiFetch<Page<VersionRecord>>(queryPath(`${base}/levels/${part(id)}/versions`, { page }));
export const getVersion = (id: string) => apiFetch<VersionRecord>(`${base}/versions/${part(id)}`);
export const exportLevel = (id: string) => apiFetch<LevelInput>(`${base}/levels/${part(id)}/export`);
export const validateLevelOnServer = (level: Level, key: string) => apiFetch<VersionRecord>(`${base}/levels/validate`, idempotentOptions(key, { method: 'POST', json: { level } }));
export const saveLevel = (id: string | null, input: LevelInput, key: string) => apiFetch<LevelRecord>(id ? `${base}/levels/${part(id)}` : `${base}/levels`, idempotentOptions(key, { method: id ? 'PUT' : 'POST', json: input }));
export const deleteLevel = (id: string, expected_revision: string, key: string) => apiFetch<{ id: string; revision: string; deleted_at: number }>(`${base}/levels/${part(id)}`, idempotentOptions(key, { method: 'DELETE', json: { expected_revision } }));
export const publishVersion = (id: string, expected_revision: string, key: string) => apiFetch<VersionRecord>(`${base}/levels/${part(id)}/versions`, idempotentOptions(key, { method: 'POST', json: { expected_revision } }));

export const listPeriods = (page = 1) => apiFetch<Page<PeriodRecord>>(queryPath(`${base}/periods`, { page }));
export const getPeriod = (id: string) => apiFetch<PeriodRecord>(`${base}/periods/${part(id)}`);
export const getNode = (periodID: string, nodeID: string) => apiFetch<NodeRecord>(`${base}/periods/${part(periodID)}/nodes/${part(nodeID)}`);
export const validatePeriod = (id: string) => apiFetch<GraphValidation>(`${base}/periods/${part(id)}/validate`);
export const savePeriod = (id: string | null, input: PeriodInput, key: string) => apiFetch<PeriodRecord>(id ? `${base}/periods/${part(id)}` : `${base}/periods`, idempotentOptions(key, { method: id ? 'PUT' : 'POST', json: input }));
export const saveNode = (periodID: string, nodeID: string | null, input: NodeInput, key: string) => apiFetch<NodeRecord>(nodeID ? `${base}/periods/${part(periodID)}/nodes/${part(nodeID)}` : `${base}/periods/${part(periodID)}/nodes`, idempotentOptions(key, { method: nodeID ? 'PUT' : 'POST', json: input }));
export const changePeriodState = (id: string, action: 'publish' | 'close' | 'reopen', expected_revision: string, key: string) => apiFetch<PeriodRecord>(`${base}/periods/${part(id)}/${action}`, idempotentOptions(key, { method: 'POST', json: { expected_revision } }));
export const listPlaytests = (versionID: string) => apiFetch<PlaytestRecord[]>(queryPath(`${base}/playtests`, { version_id: versionID }));
