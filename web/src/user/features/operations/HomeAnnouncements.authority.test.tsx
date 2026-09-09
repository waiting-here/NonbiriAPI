import { Buffer } from 'node:buffer';
import { act, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import {
  beginManagementSessionRequest,
  noteManagementSessionSuccess,
} from '@shared/charityManagement';
import { ApiError } from '@shared/query/http';
import { renderWithProviders } from '../../../../test/unit/support';
import { coreKeys } from '../core/queries';
import type { HomeAnnouncementPage, HomeAnnouncementSummary } from '../core/types';
import { HomeAnnouncements } from './HomeAnnouncements';
import { collectHomeAnnouncementSummaries } from './homeAnnouncementsData';

const session = { user: { id: '7', username: 'reader', effective_level: 1, lang: 'en' } };
function summary(index: number, revision = '1'): HomeAnnouncementSummary {
  const bytes = Buffer.alloc(16);
  bytes.writeUInt32BE(index, 12);
  return {
    id: `ann_${bytes.toString('base64url')}`,
    epoch: `b1e_${'A'.repeat(22)}`,
    revision,
    severity: 'info',
    pinned: false,
    dismissible: false,
    published_at: 1_800_000_000,
    expires_at: null,
    effective_language: 'en',
    fallback_from: null,
    title: `Notice ${index}`,
    excerpt: `Excerpt ${index}`,
  };
}
const page = (...data: HomeAnnouncementSummary[]): HomeAnnouncementPage => ({
  data,
  next_cursor: null,
});
function detail(item: HomeAnnouncementSummary) {
  const { excerpt: _excerpt, ...common } = item;
  void _excerpt;
  return { ...common, rendered_body: `<p>Body revision ${item.revision}</p>` };
}

describe('home announcement authority', () => {
  it.each([2, 3, 7])(
    'rejects a repeating cursor cycle of length %i without retaining all pages',
    async (length) => {
      let calls = 0;
      const load = vi.fn(async () => {
        const next = Buffer.from(String(calls++ % length)).toString('base64url');
        return { data: [], next_cursor: next };
      });
      await expect(collectHomeAnnouncementSummaries(load, '7', new Set())).rejects.toMatchObject({
        code: 'invalid_response',
      });
      expect(calls).toBeLessThanOrEqual(length * 3);
    },
  );

  it('purges the current user station after an authoritative unauthorized response', async () => {
    let reject!: (error: unknown) => void;
    const pending = new Promise<HomeAnnouncementPage>((_resolve, fail) => {
      reject = fail;
    });
    const load = vi.fn(() => pending);
    const view = await renderWithProviders(
      <HomeAnnouncements accountId="7" language="en" capability={{ state: 'available', load }} />,
      { station: 'user' },
    );
    await act(async () => {
      view.queryClient.setQueryData(coreKeys.session, session);
      view.queryClient.setQueryData(['user', 'resources', 'private'], { name: 'Private endpoint' });
      view.queryClient.setQueryData(['admin', 'independent'], { ready: true });
    });
    await waitFor(() => expect(load).toHaveBeenCalledTimes(1));
    await act(async () => {
      reject(new ApiError('unauthorized', 'Expired session', 401));
    });
    await waitFor(() => expect(view.queryClient.getQueryData(coreKeys.session)).toBeNull());
    expect(view.queryClient.getQueryData(['user', 'resources', 'private'])).toBeUndefined();
    expect(view.queryClient.getQueryData(['admin', 'independent'])).toEqual({ ready: true });
  });

  it('waits for an explicitly confirmed session before reading announcements', async () => {
    const load = vi.fn(async () => page());
    const view = await renderWithProviders(
      <HomeAnnouncements accountId="7" language="en" capability={{ state: 'available', load }} />,
      { station: 'user' },
    );
    expect(load).not.toHaveBeenCalled();
    await act(async () => {
      view.queryClient.setQueryData(coreKeys.session, session);
    });
    await waitFor(() => expect(load).toHaveBeenCalledTimes(1));
  });

  it('continues beyond one hundred hidden pages until three visible notices are found', async () => {
    const hidden = { ...summary(1), dismissible: true };
    const visible = [summary(2), summary(3), summary(4)];
    let calls = 0;
    const load = vi.fn(async () => {
      calls += 1;
      return calls <= 105
        ? { data: [hidden], next_cursor: Buffer.from(String(calls)).toString('base64url') }
        : page(...visible);
    });
    await expect(
      collectHomeAnnouncementSummaries(load, '7', new Set(), undefined, () => true),
    ).resolves.toEqual(visible);
    expect(calls).toBe(106);
  });

  it('refreshes the summary before retrying a detail whose revision changed', async () => {
    const first = summary(1);
    const next = summary(1, '2');
    const load = vi.fn().mockResolvedValueOnce(page(first)).mockResolvedValue(page(next));
    const loadDetail = vi.fn(async () => detail(next));
    const view = await renderWithProviders(
      <HomeAnnouncements
        accountId="7"
        language="en"
        capability={{ state: 'available', load }}
        loadDetail={loadDetail}
      />,
      { station: 'user' },
    );
    await act(async () => {
      view.queryClient.setQueryData(coreKeys.session, session);
    });
    await view.user.click(await screen.findByRole('button', { name: 'Retry' }));
    await screen.findByText('Body revision 2');
    expect(load).toHaveBeenCalledTimes(2);
    expect(loadDetail).toHaveBeenCalledTimes(2);
  });

  it('ignores a late unauthorized response after the same account confirms a new session', async () => {
    let reject!: (error: unknown) => void;
    const pending = new Promise<HomeAnnouncementPage>((_resolve, fail) => {
      reject = fail;
    });
    const load = vi
      .fn()
      .mockReturnValueOnce(pending)
      .mockResolvedValue(page(summary(1)));
    const view = await renderWithProviders(
      <HomeAnnouncements
        accountId="7"
        language="en"
        capability={{ state: 'available', load }}
        loadDetail={async () => detail(summary(1))}
      />,
      { station: 'user' },
    );
    await act(async () => {
      view.queryClient.setQueryData(coreKeys.session, session);
    });
    await waitFor(() => expect(load).toHaveBeenCalledTimes(1));
    await act(async () => {
      const generation = beginManagementSessionRequest(view.queryClient, 'steward');
      expect(noteManagementSessionSuccess(view.queryClient, 'steward', session, generation)).toBe(
        true,
      );
      view.queryClient.setQueryData(coreKeys.session, session);
    });
    await act(async () => {
      reject(new ApiError('unauthorized', 'Expired session', 401));
    });
    expect(view.queryClient.getQueryData(coreKeys.session)).toEqual(session);
    await screen.findByText('Body revision 1');
  });
});
