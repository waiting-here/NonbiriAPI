import { enumValue, exactRecord, invalidResponse, safeInteger, unixTime } from '../common/strict';
import { gameRequest } from '../common/request';
import type { Selection } from './types';

export interface SavedPreset {
  readonly slot: number;
  readonly revision: string;
  readonly mode: 'quick' | 'standard';
  readonly loadout: {
    readonly role: string;
    readonly harness: string | null;
    readonly skills: readonly string[];
  };
  readonly updatedAt: number;
}

export interface CustomPresetList {
  readonly capacity: 10;
  readonly slots: readonly SavedPreset[];
}

export interface SavePresetIntent {
  readonly slot: number;
  readonly expectedRevision: string;
  readonly mode: 'quick' | 'standard';
  readonly loadout: Selection;
  readonly key: string;
}

function identifier(value: unknown, field: string): string {
  if (typeof value !== 'string' || value.length < 1 || value.length > 64) invalidResponse(field);
  return value;
}

export function savedPreset(value: unknown): SavedPreset {
  const r = exactRecord(value, ['slot', 'revision', 'mode', 'loadout', 'updated_at']);
  const revision = r.revision;
  if (
    typeof revision !== 'string' ||
    !/^[1-9][0-9]{0,18}$/.test(revision) ||
    BigInt(revision) > 9_223_372_036_854_775_807n
  )
    invalidResponse('custom preset revision');
  const loadout = exactRecord(r.loadout, ['role', 'harness', 'skills']);
  if (!Array.isArray(loadout.skills) || loadout.skills.length < 1 || loadout.skills.length > 6)
    invalidResponse('custom preset skills');
  const skills = loadout.skills.map((skill) => identifier(skill, 'custom preset skill'));
  if (new Set(skills).size !== skills.length) invalidResponse('custom preset skills');
  return {
    slot: safeInteger(r.slot, 1, 10, 'custom preset slot'),
    revision,
    mode: enumValue(r.mode, ['quick', 'standard'], 'custom preset mode'),
    loadout: {
      role: identifier(loadout.role, 'custom preset role'),
      harness:
        loadout.harness === null ? null : identifier(loadout.harness, 'custom preset harness'),
      skills,
    },
    updatedAt: unixTime(r.updated_at, 'custom preset time'),
  };
}

export function customPresetList(value: unknown): CustomPresetList {
  const r = exactRecord(value, ['capacity', 'slots']);
  safeInteger(r.capacity, 10, 10, 'custom preset capacity');
  if (!Array.isArray(r.slots) || r.slots.length > 10) invalidResponse('custom presets');
  const slots = r.slots.map(savedPreset);
  for (let i = 1; i < slots.length; i++)
    if (slots[i].slot <= slots[i - 1].slot) invalidResponse('custom preset order');
  return { capacity: 10, slots };
}

export async function fetchCustomPresets(signal?: AbortSignal): Promise<CustomPresetList> {
  const result = await gameRequest<unknown>('/api/games/likes/loadouts', {
    signal,
    expectedStatuses: [200],
  });
  return customPresetList(result.data);
}

export async function saveCustomPreset(intent: SavePresetIntent): Promise<SavedPreset> {
  const result = await gameRequest<unknown>(`/api/games/likes/loadouts/${intent.slot}`, {
    method: 'PUT',
    json: {
      expected_revision: intent.expectedRevision,
      mode: intent.mode,
      loadout: intent.loadout,
    },
    idempotencyKey: intent.key,
    expectedStatuses: [200],
  });
  const item = savedPreset(result.data);
  if (item.slot !== intent.slot || item.mode !== intent.mode)
    invalidResponse('saved custom preset');
  return item;
}
