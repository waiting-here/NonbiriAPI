import { apiFetch } from '@shared/query/http';
import { idempotentOptions } from '@shared/operations/api';
import type { FatFishChallenge, FatFishChallengeTransport } from '@shared/fatfish/api';

const base = '/admin/api/limited-activities/fat-fish/playtests';
export interface CurrentPlaytest {
  id: string; revision: string; state: 'prepared' | 'active' | 'verifying';
  version_id: string; version_number?: string; level_title: string; content_hash: string;
  engine_version: number; expires_at_ms: number; server_now_ms: number;
}
export const currentPlaytest = () => apiFetch<CurrentPlaytest | null>(base + '/current');
export const abandonPlaytest = (id: string, revision: string, key: string) => apiFetch<FatFishChallenge>(
  `${base}/${encodeURIComponent(id)}/abandon`, idempotentOptions(key, {
    method: 'POST', json: { expected_revision: revision },
  }),
);
export function adminPlaytestTransport(versionID: string): FatFishChallengeTransport {
  const path = (id: string) => `${base}/${encodeURIComponent(id)}`;
  const read = (id: string, capability: string) => apiFetch<FatFishChallenge>(path(id), {
    headers: { 'X-FatFish-Tab-Capability': capability },
  });
  return {
    prepareScope: `playtest:${versionID}`, autoSubmit: true,
    prepare: (hash, key) => apiFetch(base, idempotentOptions(key, {
      method: 'POST', json: { version_id: versionID, tab_capability_hash: hash },
    })),
    start: (id, capability, key) => apiFetch(path(id) + '/start', idempotentOptions(key, {
      method: 'POST', json: { tab_capability: capability },
    })),
    read,
    submit: (id, payload, key) => apiFetch(path(id) + '/submit', idempotentOptions(key, { method: 'POST', json: payload })),
    current: async (capability) => {
      const current = await currentPlaytest();
      return current?.version_id === versionID ? read(current.id, capability) : null;
    },
    abandon: (id, _capability, key, revision) => {
      if (!revision) return Promise.reject(new Error('The playtest revision is unavailable.'));
      return abandonPlaytest(id, revision, key);
    },
  };
}
