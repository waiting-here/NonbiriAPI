import { createTimeDraft, timeDraftValue, type TimeDraft } from '@shared/time';
import type { AdminAnnouncement } from './managedAnnouncements';

export interface DraftState {
  title_zh: string;
  body_zh: string;
  title_en: string;
  body_en: string;
  severity: 'info' | 'warning' | 'important';
  pinned: boolean;
  dismissible: boolean;
  expires_at: TimeDraft;
}

export interface AnnouncementSaveInput {
  revision: string;
  title_zh: string;
  body_zh: string;
  title_en: string;
  body_en: string;
  severity: DraftState['severity'];
  pinned: boolean;
  dismissible: boolean;
  expires_at: number | null;
}

export const fromAuthority = (item: AdminAnnouncement): DraftState => ({
  title_zh: item.draft.zh?.title ?? '',
  body_zh: item.draft.zh?.body ?? '',
  title_en: item.draft.en?.title ?? '',
  body_en: item.draft.en?.body ?? '',
  severity: item.severity,
  pinned: item.pinned,
  dismissible: item.dismissible,
  expires_at: createTimeDraft(item.expires_at, 'minute'),
});

export type DraftField = keyof DraftState;
const fields = [
  'title_zh',
  'body_zh',
  'title_en',
  'body_en',
  'severity',
  'pinned',
  'dismissible',
  'expires_at',
] as const;

function sameField(key: DraftField, left: DraftState, right: DraftState) {
  if (key !== 'expires_at') return left[key] === right[key];
  const instant = (draft: TimeDraft) =>
    !draft.invalidInput && draft.text === draft.originalText
      ? draft.originalEpoch
      : timeDraftValue(draft);
  const leftTime = instant(left.expires_at);
  return leftTime !== undefined && leftTime === instant(right.expires_at);
}

export function draftChanged(base: DraftState, local: DraftState) {
  return fields.some((key) => !sameField(key, local, base));
}

export function mergeDraft(
  base: DraftState,
  local: DraftState,
  latest: DraftState,
  unresolved: DraftField[],
) {
  const draft = { ...local };
  const conflicts: DraftField[] = [];
  for (const key of fields) {
    if (sameField(key, local, latest)) continue;
    if (unresolved.includes(key)) conflicts.push(key);
    else if (sameField(key, base, local)) Object.assign(draft, { [key]: latest[key] });
    else if (!sameField(key, base, latest)) conflicts.push(key);
  }
  return { draft, conflicts };
}
