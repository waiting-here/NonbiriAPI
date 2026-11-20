import { CancelledError } from '@tanstack/react-query';
import { ApiError } from '@shared/query/http';
import { array, record } from '@shared/operations/wire';
import { coreRequest } from '../core/request';
import { normalizeAnnouncementDetail, type AnnouncementDetail } from './data';

type Reader = {
  id: string;
  signal?: AbortSignal;
  resolve: (value: AnnouncementDetail) => void;
  reject: (error: unknown) => void;
};

/** Keep independent card retries while combining the visible cards into one authorized read. */
export function createHomeBodyLoader(currentSession: () => boolean) {
  let pending: Reader[] = [];
  async function flush() {
    const readers = pending;
    pending = [];
    const live = readers.filter((reader) => !reader.signal?.aborted && currentSession());
    for (const reader of readers) if (!live.includes(reader)) reader.reject(new CancelledError());
    if (!live.length) return;
    const controller = new AbortController();
    const cancelIfEmpty = () => {
      if (live.every((r) => r.signal?.aborted)) controller.abort();
    };
    for (const reader of live) reader.signal?.addEventListener('abort', cancelIfEmpty);
    try {
      const ids = [...new Set(live.map((reader) => reader.id))];
      const query = new URLSearchParams({ ids: ids.join(',') });
      const response = await coreRequest(`/api/announcements/selected?${query}`, {
        signal: controller.signal,
      });
      if (response.status !== 200)
        throw new ApiError(
          'invalid_response',
          'Unable to read the selected notices.',
          response.status,
        );
      const envelope = record(response.payload, ['data'], 'selected announcements');
      const details = array(envelope.data, 'selected announcements', 3).map(
        normalizeAnnouncementDetail,
      );
      const byId = new Map(details.map((detail) => [detail.id, detail]));
      if (byId.size !== details.length || details.some((detail) => !ids.includes(detail.id)))
        throw new ApiError(
          'invalid_response',
          'Unexpected announcement in the selected notices.',
          200,
        );
      for (const reader of live) {
        const detail = byId.get(reader.id);
        if (reader.signal?.aborted || !currentSession()) reader.reject(new CancelledError());
        else if (detail) reader.resolve(detail);
        else
          reader.reject(
            new ApiError('not_found', 'This announcement is no longer available.', 404),
          );
      }
    } catch (error) {
      for (const reader of live) reader.reject(error);
    } finally {
      for (const reader of live) reader.signal?.removeEventListener('abort', cancelIfEmpty);
    }
  }
  return (id: string, signal?: AbortSignal) =>
    new Promise<AnnouncementDetail>((resolve, reject) => {
      pending.push({ id, signal, resolve, reject });
      if (pending.length === 1)
        queueMicrotask(() => {
          void flush();
        });
    });
}
