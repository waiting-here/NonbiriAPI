import { act, screen, waitFor } from '@testing-library/react';
import { CancelledError } from '@tanstack/react-query';
import { describe, expect, it, vi } from 'vitest';
import { ApiError } from '@shared/query/http';
import { renderWithProviders } from '../../../../test/unit/support';
import { coreKeys } from '../core/queries';
import type {
  HomeAnnouncementCapability,
  HomeAnnouncementLoader,
  HomeAnnouncementPage,
  HomeAnnouncementSummary,
} from '../core/types';
import { announcementDismissalKey, type AnnouncementDetail } from './data';
import { HomeAnnouncements } from './HomeAnnouncements';
import { collectHomeAnnouncementSummaries } from './homeAnnouncementsData';

type CompleteSummary = HomeAnnouncementSummary & {
  epoch: string;
  revision: string;
  severity: 'info' | 'warning' | 'important';
  pinned: boolean;
  dismissible: boolean;
  published_at: number;
  expires_at: number | null;
  effective_language: 'zh' | 'en';
  fallback_from: 'zh' | 'en' | null;
};

const HOME_EPOCH = `b1e_${'A'.repeat(21)}Q`;

function opaqueAnnouncementId(label: string): string {
  const suffix = label
    .replace(/[^A-Za-z0-9_-]/g, 'A')
    .slice(0, 21)
    .padEnd(21, 'A');
  return `ann_${suffix}Q`;
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((settle, fail) => {
    resolve = settle;
    reject = fail;
  });
  return { promise, resolve, reject };
}

function summary(label: string, overrides: Partial<CompleteSummary> = {}): CompleteSummary {
  return {
    epoch: HOME_EPOCH,
    id: opaqueAnnouncementId(label),
    revision: '1',
    severity: 'info',
    pinned: false,
    dismissible: true,
    published_at: 1_700_000_000,
    expires_at: null,
    effective_language: 'en',
    fallback_from: null,
    title: `Announcement ${label}`,
    excerpt: `Excerpt ${label}`,
    ...overrides,
  };
}

function detail(
  value: CompleteSummary,
  renderedBody = `<p>Body ${value.title.replace(/^Announcement /, '')}</p>`,
): AnnouncementDetail {
  return {
    epoch: value.epoch,
    id: value.id,
    revision: value.revision,
    severity: value.severity,
    pinned: value.pinned,
    dismissible: value.dismissible,
    published_at: value.published_at,
    expires_at: value.expires_at,
    effective_language: value.effective_language,
    fallback_from: value.fallback_from,
    title: value.title,
    rendered_body: renderedBody,
  };
}

function page(
  data: HomeAnnouncementSummary[],
  next_cursor: string | null = null,
): HomeAnnouncementPage {
  return { data, next_cursor };
}

function available(load: HomeAnnouncementLoader): HomeAnnouncementCapability {
  return { state: 'available', load };
}

type RenderedProviders = Awaited<ReturnType<typeof renderWithProviders>>;

function confirmedSession(accountId: string, language: 'en' | 'zh' = 'en') {
  return {
    user: {
      id: accountId,
      username: `home-user-${accountId}`,
      effective_level: 1,
      lang: language,
      shell_projection: 'preserve',
    },
  };
}

function confirmSession(rendered: RenderedProviders, accountId: string): void {
  act(() => {
    rendered.queryClient.setQueryData(coreKeys.session, confirmedSession(accountId));
  });
}

describe('home announcement previews', () => {
  it('serially scans cursor pages, skips hidden revisions, and stops after three selections', async () => {
    const hidden = summary('hidden');
    const first = [hidden, summary('one'), summary('two')];
    const second = [summary('three'), summary('four')];
    const calls: (string | null)[] = [];
    let active = 0;
    let maximumActive = 0;
    const loader = vi.fn<HomeAnnouncementLoader>(async (cursor) => {
      calls.push(cursor);
      active += 1;
      maximumActive = Math.max(maximumActive, active);
      await Promise.resolve();
      active -= 1;
      return cursor === null ? page(first, 'YQ') : page(second, 'Yg');
    });

    window.localStorage.setItem(announcementDismissalKey('1', hidden), '1');
    await expect(collectHomeAnnouncementSummaries(loader, '1', new Set())).resolves.toEqual([
      first[1],
      first[2],
      second[0],
    ]);

    expect(calls).toEqual([null, 'YQ']);
    expect(loader).toHaveBeenCalledTimes(2);
    expect(maximumActive).toBe(1);
  });

  it('stops the cursor scan when the active request is aborted', async () => {
    const controller = new AbortController();
    const loader = vi.fn<HomeAnnouncementLoader>(async () => {
      controller.abort();
      return page([summary('aborted')], 'YQ');
    });

    await expect(
      collectHomeAnnouncementSummaries(loader, '1', new Set(), controller.signal),
    ).rejects.toBeInstanceOf(CancelledError);
    expect(loader).toHaveBeenCalledTimes(1);
  });

  it('loads at most three safe details concurrently while retaining explicit links', async () => {
    const summaries = [summary('one'), summary('two'), summary('three')];
    const loader = vi.fn<HomeAnnouncementLoader>(async () => page(summaries));
    let active = 0;
    let maximumActive = 0;
    const loadDetail = vi.fn(async (id: string) => {
      active += 1;
      maximumActive = Math.max(maximumActive, active);
      await Promise.resolve();
      active -= 1;
      const selected = summaries.find((item) => item.id === id);
      if (!selected) throw new Error('unexpected detail');
      const label = selected.title.replace(/^Announcement /, '');
      return detail(
        selected,
        `<h2>Body ${label}</h2><p>Text <code>code</code> <a href="https://example.com/read">Read safe detail</a></p>`,
      );
    });

    const rendered = await renderWithProviders(
      <HomeAnnouncements
        accountId="1"
        language="en"
        capability={available(loader)}
        loadDetail={loadDetail}
      />,
      { station: 'user', role: 'user', locale: 'en' },
    );
    confirmSession(rendered, '1');

    expect(await screen.findByRole('heading', { name: 'Announcements' })).toBeVisible();
    await waitFor(() => expect(loadDetail).toHaveBeenCalledTimes(3));
    expect(maximumActive).toBe(3);
    expect(await screen.findByRole('heading', { name: 'Body one' })).toBeVisible();
    expect(screen.getAllByRole('link', { name: 'Read safe detail' })[0]).toHaveAttribute(
      'href',
      'https://example.com/read',
    );
    expect(document.querySelector('a a')).toBeNull();
  });

  it('hides a dismissible card in memory and persists its epoch/account/revision key', async () => {
    const hidden = summary('hidden');
    const persistent = summary('persistent', { dismissible: false });
    const loader = vi.fn<HomeAnnouncementLoader>(async () => page([hidden, persistent]));
    const rendered = await renderWithProviders(
      <HomeAnnouncements accountId="1" language="en" capability={available(loader)} />,
      { station: 'user', role: 'user', locale: 'en' },
    );
    confirmSession(rendered, '1');

    await rendered.user.click(await screen.findByRole('button', { name: 'Hide from home' }));
    await waitFor(() => expect(screen.queryByText(hidden.title)).not.toBeInTheDocument());
    expect(screen.getByText(persistent.title)).toBeVisible();
    expect(window.localStorage.getItem(announcementDismissalKey('1', hidden))).toBe('1');
    expect(loader).toHaveBeenCalledTimes(2);
  });

  it('keeps the dismissal effective for this page when storage writes fail', async () => {
    const hidden = summary('storage-failure');
    const loader = vi.fn<HomeAnnouncementLoader>(async () => page([hidden]));
    const setItem = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('storage unavailable');
    });
    const rendered = await renderWithProviders(
      <HomeAnnouncements accountId="1" language="en" capability={available(loader)} />,
      { station: 'user', role: 'user', locale: 'en' },
    );
    confirmSession(rendered, '1');

    await rendered.user.click(await screen.findByRole('button', { name: 'Hide from home' }));
    await waitFor(() => expect(screen.queryByText(hidden.title)).not.toBeInTheDocument());
    expect(setItem).toHaveBeenCalled();
    setItem.mockRestore();
  });

  it('hides the whole section when every returned announcement is already hidden', async () => {
    const hidden = summary('all-hidden');
    window.localStorage.setItem(announcementDismissalKey('1', hidden), '1');
    const loader = vi.fn<HomeAnnouncementLoader>(async () => page([hidden]));

    const rendered = await renderWithProviders(
      <HomeAnnouncements accountId="1" language="en" capability={available(loader)} />,
      { station: 'user', role: 'user', locale: 'en' },
    );
    confirmSession(rendered, '1');

    await waitFor(() => {
      expect(screen.queryByRole('heading', { name: 'Announcements' })).not.toBeInTheDocument();
    });
    expect(loader).toHaveBeenCalledTimes(1);
  });

  it('keeps a persistent announcement visible without a hide action', async () => {
    const persistent = summary('persistent-only', { dismissible: false });
    const loader = vi.fn<HomeAnnouncementLoader>(async () => page([persistent]));

    const rendered = await renderWithProviders(
      <HomeAnnouncements accountId="1" language="en" capability={available(loader)} />,
      { station: 'user', role: 'user', locale: 'en' },
    );
    confirmSession(rendered, '1');

    expect(await screen.findByText(persistent.title)).toBeVisible();
    expect(screen.queryByRole('button', { name: 'Hide from home' })).not.toBeInTheDocument();
  });

  it('keeps the summary and offers a retry when detail identity changes', async () => {
    const selected = summary('identity');
    const mismatched = { ...detail(selected), revision: '2' };
    const loadDetail = vi
      .fn()
      .mockResolvedValueOnce(mismatched)
      .mockResolvedValueOnce(detail(selected, '<h2>Recovered body</h2>'));
    const loader = vi.fn<HomeAnnouncementLoader>(async () => page([selected]));
    const rendered = await renderWithProviders(
      <HomeAnnouncements
        accountId="1"
        language="en"
        capability={available(loader)}
        loadDetail={loadDetail}
      />,
      { station: 'user', role: 'user', locale: 'en' },
    );
    confirmSession(rendered, '1');

    expect(await screen.findByText('The full announcement could not be loaded.')).toBeVisible();
    expect(screen.getByText(selected.title)).toBeVisible();
    expect(screen.getByText(selected.excerpt)).toBeVisible();
    await rendered.user.click(screen.getByRole('button', { name: 'Retry' }));
    expect(await screen.findByRole('heading', { name: 'Recovered body' })).toBeVisible();
    expect(loadDetail).toHaveBeenCalledTimes(2);
  });

  it('does not query before the session boundary is confirmed', async () => {
    const loader = vi.fn<HomeAnnouncementLoader>(async () => page([summary('blocked')]));
    await renderWithProviders(
      <HomeAnnouncements
        accountId="1"
        language="en"
        capability={available(loader)}
        sessionReady={false}
      />,
      { station: 'user', role: 'user', locale: 'en' },
    );

    expect(loader).not.toHaveBeenCalled();
    expect(screen.queryByRole('heading', { name: 'Announcements' })).not.toBeInTheDocument();
  });

  it('drops a late page from the previous account after a session switch', async () => {
    const oldSummary = summary('old-account');
    const newSummary = summary('new-account');
    const first = deferred<HomeAnnouncementPage>();
    let pageCalls = 0;
    const loader = vi.fn<HomeAnnouncementLoader>(async () => {
      pageCalls += 1;
      return pageCalls === 1 ? first.promise : page([newSummary]);
    });
    const loadDetail = vi.fn(async (id: string) =>
      detail(id === oldSummary.id ? oldSummary : newSummary),
    );
    const rendered = await renderWithProviders(
      <HomeAnnouncements
        accountId="1"
        language="en"
        capability={available(loader)}
        loadDetail={loadDetail}
      />,
      { station: 'user', role: 'user', locale: 'en' },
    );
    confirmSession(rendered, '1');
    await waitFor(() => expect(loader).toHaveBeenCalledTimes(1));

    act(() => {
      rendered.queryClient.setQueryData(coreKeys.session, confirmedSession('2'));
      rendered.rerender(
        <HomeAnnouncements
          accountId="2"
          language="en"
          capability={available(loader)}
          loadDetail={loadDetail}
        />,
      );
    });
    expect(await screen.findByText(newSummary.title)).toBeVisible();

    await act(async () => {
      first.resolve(page([oldSummary]));
      await first.promise;
    });
    expect(screen.queryByText(oldSummary.title)).not.toBeInTheDocument();
  });

  it('drops a late page from the previous language after a language switch', async () => {
    const oldSummary = summary('english');
    const newSummary = summary('chinese', {
      effective_language: 'zh',
      title: '中文公告',
      excerpt: '中文摘要',
    });
    const first = deferred<HomeAnnouncementPage>();
    let pageCalls = 0;
    const loader = vi.fn<HomeAnnouncementLoader>(async () => {
      pageCalls += 1;
      return pageCalls === 1 ? first.promise : page([newSummary]);
    });
    const loadDetail = vi.fn(async (id: string) =>
      detail(id === oldSummary.id ? oldSummary : newSummary),
    );
    const rendered = await renderWithProviders(
      <HomeAnnouncements
        accountId="1"
        language="en"
        capability={available(loader)}
        loadDetail={loadDetail}
      />,
      { station: 'user', role: 'user', locale: 'en' },
    );
    confirmSession(rendered, '1');
    await waitFor(() => expect(loader).toHaveBeenCalledTimes(1));

    act(() => {
      rendered.rerender(
        <HomeAnnouncements
          accountId="1"
          language="zh"
          capability={available(loader)}
          loadDetail={loadDetail}
        />,
      );
    });
    expect(await screen.findByText(newSummary.title)).toBeVisible();

    await act(async () => {
      first.resolve(page([oldSummary]));
      await first.promise;
    });
    expect(screen.queryByText(oldSummary.title)).not.toBeInTheDocument();
  });

  it('clears the user session after an unauthorized announcement page response', async () => {
    const failure = deferred<HomeAnnouncementPage>();
    const loader = vi.fn<HomeAnnouncementLoader>(async () => failure.promise);
    const rendered = await renderWithProviders(
      <HomeAnnouncements accountId="1" language="en" capability={available(loader)} />,
      { station: 'user', role: 'user', locale: 'en' },
    );
    confirmSession(rendered, '1');
    await waitFor(() => expect(loader).toHaveBeenCalledTimes(1));

    await act(async () => {
      failure.reject(new ApiError('unauthorized', 'Authentication is required.', 401));
      await expect(failure.promise).rejects.toMatchObject({ status: 401 });
    });
    await waitFor(() => expect(rendered.queryClient.getQueryData(coreKeys.session)).toBeNull());
  });
});
