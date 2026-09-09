import { CancelledError } from '@tanstack/react-query';
import { array, cursor as decodeCursor, invalidResponse, record } from '@shared/operations/wire';
import { announcementDismissalKey, normalizeAnnouncementSummary } from './data';
import type { HomeAnnouncementLoader, HomeAnnouncementSummary } from '../core/types';

export function readAnnouncementDismissal(key: string): boolean {
  if (typeof localStorage === 'undefined') return false;
  try {
    return localStorage.getItem(key) === '1';
  } catch {
    return false;
  }
}

export async function collectHomeAnnouncementSummaries(
  loader: HomeAnnouncementLoader,
  accountId: string,
  dismissedKeys: ReadonlySet<string>,
  signal?: AbortSignal,
  isDismissed = (key: string) => readAnnouncementDismissal(key),
): Promise<HomeAnnouncementSummary[]> {
  const selected: HomeAnnouncementSummary[] = [];
  let cursor: string | null = null;
  // Brent's cycle detector retains one checkpoint, regardless of the number
  // of hidden pages. Cancellation, the end cursor or three visible summaries
  // end a valid scan; a fixed page cap would hide later notices.
  let checkpoint: string | null = null;
  let power = 1;
  let span = 0;
  for (;;) {
    if (signal?.aborted) throw new CancelledError();
    const page = record(
      await loader(cursor, signal),
      ['data', 'next_cursor'],
      'home announcement page',
    );
    if (signal?.aborted) throw new CancelledError();
    const data = array(page.data, 'home announcement page', 100).map(normalizeAnnouncementSummary);
    const next = decodeCursor(page.next_cursor, 'home announcement cursor');
    for (const item of data) {
      if (selected.some((current) => current.id === item.id)) continue;
      const key = announcementDismissalKey(accountId, item);
      if (item.dismissible && (dismissedKeys.has(key) || isDismissed(key))) continue;
      selected.push(item);
      if (selected.length === 3) return selected;
    }
    if (next === null) return selected;
    if (next === cursor || next === checkpoint) invalidResponse('home announcement cursor');
    span += 1;
    if (span === power) {
      checkpoint = next;
      power = Math.min(power * 2, Number.MAX_SAFE_INTEGER);
      span = 0;
    }
    cursor = next;
  }
}
