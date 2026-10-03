import { useLayoutEffect } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { act, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import { ApiError } from '@shared/query/http';
import { HomeDashboard } from '../../pages/HomePage';
import { AccountLanguageForm, AccountLifecyclePanel, AccountWorkspace } from './AccountWorkspace';
import { coreKeys } from './queries';
import { normalizeUserEnvelope } from './normalizers';
import type {
  AccountLifecycleAdapter,
  HomeAdapters,
  HomeAnnouncementPage,
  HomeAnnouncementSummary,
  HomeCheckinStatus,
  UserEnvelope,
} from './types';

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((settle) => {
    resolve = settle;
  });
  return { promise, resolve };
}

function canonicalEnvelope(): UserEnvelope {
  const raw = JSON.parse(
    readFileSync(resolve(process.cwd(), '..', 'internal/auth/testdata/user_envelope.json'), 'utf8'),
  ) as unknown;
  return normalizeUserEnvelope(raw);
}

function sharedSession(user: UserEnvelope['user']) {
  return {
    user: {
      id: user.id,
      username: user.username,
      effective_level: user.effective_level,
      lang: user.lang,
      shell_projection: 'preserve',
    },
  };
}

const HOME_EPOCH = `b1e_${'A'.repeat(21)}Q`;

function opaqueAnnouncementId(label: string): string {
  const suffix = label
    .replace(/[^A-Za-z0-9_-]/g, 'A')
    .slice(0, 21)
    .padEnd(21, 'A');
  return `ann_${suffix}Q`;
}

function homeAnnouncementSummary(
  label: string,
  overrides: Partial<HomeAnnouncementSummary> = {},
): HomeAnnouncementSummary {
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

function homeAnnouncementPage(data: HomeAnnouncementSummary[] = []): HomeAnnouncementPage {
  return { data, next_cursor: null };
}

type RenderedProviders = Awaited<ReturnType<typeof renderWithProviders>>;

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

async function renderHomeDashboard(
  user: UserEnvelope['user'],
  adapters: HomeAdapters,
  locale: 'en' | 'zh' = 'en',
): Promise<RenderedProviders> {
  const rendered = await renderWithProviders(<HomeDashboard user={user} adapters={adapters} />, {
    station: 'user',
    role: 'user',
    locale,
  });
  act(() => {
    rendered.queryClient.setQueryData(coreKeys.session, sharedSession(user));
  });
  return rendered;
}

describe('home independent capability states', () => {
  it('checks in each wallet independently and refreshes the shared balances', async () => {
    const envelope = canonicalEnvelope();
    envelope.user.balance = '0';
    envelope.user.game_balance = '-0.001';
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => jsonResponse(envelope)),
    );
    const makeCheckin = (asset: 'general' | 'game', amount: string) => {
      let checked = false;
      return {
        state: 'available' as const,
        load: vi.fn(async (): Promise<HomeCheckinStatus> => ({
          enabled: true,
          mutually_exclusive: false,
          blocked_by_other_checkin: false,
          asset_type: asset,
          checked_in_today: checked,
          balance: asset === 'game' ? envelope.user.game_balance : envelope.user.balance,
          award_min: amount,
          award_max: amount,
          balance_cap: '0',
        })),
        submit: vi.fn(async () => {
          checked = true;
          if (asset === 'game') envelope.user.game_balance = '1.999';
          else envelope.user.balance = amount;
          return { asset_type: asset, award: amount, balance: asset === 'game' ? '1.999' : amount };
        }),
      };
    };
    const general = makeCheckin('general', '1');
    const game = makeCheckin('game', '2');
    const view = await renderHomeDashboard(envelope.user, {
      checkin: general,
      gameCheckin: game,
      games: { state: 'available', load: async () => [] },
      announcements: { state: 'available', load: async () => homeAnnouncementPage() },
    });
    const card = (title: string) =>
      screen.getByRole('heading', { name: title }).closest('section')!;
    const generalButton = await within(card('General-credit check-in')).findByRole('button', {
      name: 'Check in',
    });
    const gameButton = await within(card('Game-credit check-in')).findByRole('button', {
      name: 'Check in',
    });
    expect(screen.queryByText(/Choose one check-in each site day/)).not.toBeInTheDocument();
    await view.user.click(gameButton);
    await waitFor(() => expect(gameButton).toBeDisabled());
    expect(generalButton).toBeEnabled();
    expect(general.submit).not.toHaveBeenCalled();
    expect(game.submit).toHaveBeenCalledTimes(1);
    await view.user.click(generalButton);
    await waitFor(() => expect(generalButton).toBeDisabled());
    expect(general.submit).toHaveBeenCalledTimes(1);
    expect(game.submit).toHaveBeenCalledTimes(1);
    await waitFor(() =>
      expect(
        view.queryClient.getQueryData<UserEnvelope>(coreKeys.me(envelope.user.id))?.user,
      ).toMatchObject({ balance: '1', game_balance: '1.999' }),
    );
  });

  it('shows the choice rule even when one check-in endpoint is disabled', async () => {
    const envelope = canonicalEnvelope();
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => jsonResponse(envelope)),
    );
    await renderHomeDashboard(envelope.user, {
      checkin: {
        state: 'available',
        load: async () => ({
          enabled: false,
          mutually_exclusive: true,
          blocked_by_other_checkin: false,
        }),
        submit: vi.fn(),
      },
      gameCheckin: { state: 'unavailable' },
      games: { state: 'available', load: async () => [] },
      announcements: { state: 'available', load: async () => homeAnnouncementPage() },
    });
    expect(await screen.findByRole('note')).toHaveTextContent('Choose one check-in each site day');
    expect(screen.getByRole('heading', { name: 'General-credit check-in' })).toBeVisible();
    expect(screen.getByRole('heading', { name: 'Game-credit check-in' })).toBeVisible();
  });

  it('shows a daily choice and blocks only the other wallet after a successful claim', async () => {
    const envelope = canonicalEnvelope();
    envelope.user.balance = '0';
    envelope.user.game_balance = '0';
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => jsonResponse(envelope)),
    );
    let chosen: 'general' | 'game' | null = null;
    const makeCheckin = (asset: 'general' | 'game') => ({
      state: 'available' as const,
      load: vi.fn(async (): Promise<HomeCheckinStatus> => ({
        enabled: true,
        mutually_exclusive: true,
        blocked_by_other_checkin: chosen !== null && chosen !== asset,
        asset_type: asset,
        checked_in_today: chosen === asset,
        balance: asset === 'game' ? envelope.user.game_balance : envelope.user.balance,
        award_min: '1',
        award_max: '1',
        balance_cap: '0',
      })),
      submit: vi.fn(async () => {
        chosen = asset;
        if (asset === 'general') envelope.user.balance = '1';
        else envelope.user.game_balance = '1';
        return { asset_type: asset, award: '1', balance: '1' };
      }),
    });
    const general = makeCheckin('general');
    const game = makeCheckin('game');
    const view = await renderHomeDashboard(envelope.user, {
      checkin: general,
      gameCheckin: game,
      games: { state: 'available', load: async () => [] },
      announcements: { state: 'available', load: async () => homeAnnouncementPage() },
    });
    const card = (title: string) =>
      screen.getByRole('heading', { name: title }).closest('section')!;
    const generalCard = card('General-credit check-in');
    const gameCard = card('Game-credit check-in');
    expect(await screen.findByRole('note')).toHaveTextContent('Choose one check-in each site day');
    await view.user.click(await within(generalCard).findByRole('button', { name: 'Check in' }));

    await waitFor(() => {
      expect(within(generalCard).getByText('Checked in')).toBeVisible();
      expect(within(gameCard).getByText('Other check-in claimed')).toBeVisible();
      expect(within(gameCard).getByRole('button', { name: 'Check in' })).toBeDisabled();
    });
    expect(within(gameCard).queryByText('Checked in')).not.toBeInTheDocument();
    expect(general.load).toHaveBeenCalledTimes(2);
    expect(game.load).toHaveBeenCalledTimes(2);
    expect(game.submit).not.toHaveBeenCalled();
    await waitFor(() =>
      expect(
        view.queryClient.getQueryData<UserEnvelope>(coreKeys.me(envelope.user.id))?.user,
      ).toMatchObject({ balance: '1', game_balance: '0' }),
    );
  });

  it('refreshes both wallet statuses and balances after a cross-wallet conflict', async () => {
    const envelope = canonicalEnvelope();
    envelope.user.balance = '0';
    envelope.user.game_balance = '0';
    let otherClaimed = false;
    const fetchMe = vi.fn(async () => jsonResponse(envelope));
    vi.stubGlobal('fetch', fetchMe);
    const generalLoad = vi.fn(async (): Promise<HomeCheckinStatus> => ({
      enabled: true,
      mutually_exclusive: true,
      blocked_by_other_checkin: false,
      asset_type: 'general',
      checked_in_today: otherClaimed,
      balance: envelope.user.balance,
      award_min: '1',
      award_max: '1',
      balance_cap: '0',
    }));
    const gameLoad = vi.fn(async (): Promise<HomeCheckinStatus> => ({
      enabled: true,
      mutually_exclusive: true,
      blocked_by_other_checkin: otherClaimed,
      asset_type: 'game',
      checked_in_today: false,
      balance: envelope.user.game_balance,
      award_min: '1',
      award_max: '1',
      balance_cap: '0',
    }));
    const gameSubmit = vi.fn(async () => {
      otherClaimed = true;
      envelope.user.balance = '1';
      throw new ApiError('already_checked_in', 'Already checked in today.', 409);
    });
    const view = await renderHomeDashboard(envelope.user, {
      checkin: { state: 'available', load: generalLoad, submit: vi.fn() },
      gameCheckin: { state: 'available', load: gameLoad, submit: gameSubmit },
      games: { state: 'available', load: async () => [] },
      announcements: { state: 'available', load: async () => homeAnnouncementPage() },
    });
    const gameCard = screen
      .getByRole('heading', { name: 'Game-credit check-in' })
      .closest('section')!;
    await view.user.click(await within(gameCard).findByRole('button', { name: 'Check in' }));

    await waitFor(() => {
      expect(within(gameCard).getByText('Other check-in claimed')).toBeVisible();
      expect(within(gameCard).getByRole('button', { name: 'Check in' })).toBeDisabled();
    });
    expect(gameSubmit).toHaveBeenCalledTimes(1);
    expect(generalLoad).toHaveBeenCalledTimes(2);
    expect(gameLoad).toHaveBeenCalledTimes(2);
    expect(fetchMe.mock.calls.length).toBeGreaterThanOrEqual(2);
  });

  it('keeps confirmed profile, economy, usage, and announcement data when the game summary fails', async () => {
    const envelope = canonicalEnvelope();
    const announcement = homeAnnouncementSummary('planned', {
      title: 'Planned maintenance',
      excerpt: 'A short confirmed summary.',
    });
    const { excerpt, ...announcementDetail } = announcement;
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: string | URL | Request) => {
        if (String(input) === '/api/me') return jsonResponse(envelope);
        if (String(input) === `/api/announcements/${announcement.id}`)
          return jsonResponse({
            ...announcementDetail,
            rendered_body: `<p>${excerpt}</p>`,
          });
        throw new Error(`Unexpected request: ${String(input)}`);
      }),
    );
    const adapters: HomeAdapters = {
      gameCheckin: { state: 'unavailable' },
      checkin: { state: 'unavailable' },
      games: {
        state: 'available',
        load: async () => {
          throw new Error('bounded game summary failure');
        },
      },
      announcements: {
        state: 'available',
        load: async () => homeAnnouncementPage([announcement]),
      },
    };

    await renderHomeDashboard(envelope.user, adapters);

    expect(screen.getByText('Guild Alice')).toBeVisible();
    expect(screen.getByRole('heading', { name: 'Lifetime usage' })).toBeVisible();
    expect(await screen.findByText('-1.5')).toBeVisible();
    expect(await screen.findByRole('heading', { name: 'Continue or view results' })).toBeVisible();
    expect(screen.getByText('Could not load this section')).toBeVisible();
    expect(await screen.findByText('Planned maintenance')).toBeVisible();
    expect(screen.getByText('A short confirmed summary.')).toBeVisible();
    await waitFor(() =>
      expect(
        screen.getByText('A short confirmed summary.').closest('.ops-announcement-body'),
      ).not.toBeNull(),
    );
  });

  it('hides successful empty game and announcement summaries', async () => {
    const envelope = canonicalEnvelope();
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => jsonResponse(envelope)),
    );
    const adapters: HomeAdapters = {
      gameCheckin: { state: 'unavailable' },
      checkin: { state: 'unavailable' },
      games: { state: 'available', load: async () => [] },
      announcements: { state: 'available', load: async () => homeAnnouncementPage() },
    };

    await renderHomeDashboard(envelope.user, adapters);

    await waitFor(() => {
      expect(
        screen.queryByRole('heading', { name: 'Continue or view results' }),
      ).not.toBeInTheDocument();
      expect(screen.queryByRole('heading', { name: 'Announcements' })).not.toBeInTheDocument();
    });
    expect(screen.getByRole('heading', { name: 'General-credit check-in' })).toBeVisible();
  });

  it('does not turn an available capability with no loader into a successful empty summary', async () => {
    const envelope = canonicalEnvelope();
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => jsonResponse(envelope)),
    );
    const adapters = {
      gameCheckin: { state: 'unavailable' },
      checkin: { state: 'unavailable' },
      games: { state: 'available' },
      announcements: { state: 'unavailable' },
    } as unknown as HomeAdapters;

    await renderHomeDashboard(envelope.user, adapters);

    expect(await screen.findByRole('heading', { name: 'Continue or view results' })).toBeVisible();
    expect(await screen.findByText('Could not load this section')).toBeVisible();
  });

  it('GET-reconciles an unknown check-in response without automatically resubmitting', async () => {
    const envelope = canonicalEnvelope();
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => jsonResponse(envelope)),
    );
    const reconciliation = deferred<HomeCheckinStatus>();
    const initial: HomeCheckinStatus = {
      enabled: true,
      mutually_exclusive: false,
      blocked_by_other_checkin: false,
      asset_type: 'general',
      checked_in_today: false,
      balance: '-1.5',
      award_min: '1',
      award_max: '2',
      balance_cap: '340282366920938463463374607431768211.455',
    };
    let reads = 0;
    const load = vi.fn(async () => {
      reads += 1;
      return reads === 1 ? initial : reconciliation.promise;
    });
    const submit = vi.fn(async () => {
      throw new ApiError('network_error', 'The network request failed.', 0);
    });
    const adapters: HomeAdapters = {
      gameCheckin: { state: 'unavailable' },
      checkin: { state: 'available', load, submit },
      games: { state: 'available', load: async () => [] },
      announcements: { state: 'available', load: async () => homeAnnouncementPage() },
    };

    const rendered = await renderHomeDashboard(envelope.user, adapters);
    await rendered.user.click(await screen.findByRole('button', { name: 'Check in' }));

    expect(await screen.findByText(/Could not confirm the result/i)).toBeVisible();
    expect(submit).toHaveBeenCalledTimes(1);
    expect(load).toHaveBeenCalledTimes(2);

    await act(async () => {
      reconciliation.resolve({
        ...initial,
        asset_type: 'general',
        checked_in_today: true,
        balance: '0.5',
      });
      await reconciliation.promise;
    });

    expect(await screen.findByText('Checked in')).toBeVisible();
    expect(screen.getByRole('button', { name: 'Check in' })).toBeDisabled();
    expect(submit).toHaveBeenCalledTimes(1);
  });

  it('preserves exact check-in amounts and refreshes authority after a committed response', async () => {
    const envelope = canonicalEnvelope();
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => jsonResponse(envelope)),
    );
    const maximum = '340282366920938463463374607431768211.455';
    const initial: HomeCheckinStatus = {
      enabled: true,
      mutually_exclusive: false,
      blocked_by_other_checkin: false,
      asset_type: 'general',
      checked_in_today: false,
      balance: '-1.5',
      award_min: maximum,
      award_max: maximum,
      balance_cap: maximum,
    };
    const load = vi
      .fn<(signal?: AbortSignal) => Promise<HomeCheckinStatus>>()
      .mockResolvedValueOnce(initial)
      .mockResolvedValueOnce({
        ...initial,
        asset_type: 'general',
        checked_in_today: true,
        balance: maximum,
      });
    const submit = vi.fn(async () => ({
      asset_type: 'general' as const,
      award: maximum,
      balance: maximum,
    }));
    const adapters: HomeAdapters = {
      gameCheckin: { state: 'unavailable' },
      checkin: { state: 'available', load, submit },
      games: { state: 'available', load: async () => [] },
      announcements: { state: 'available', load: async () => homeAnnouncementPage() },
    };

    const rendered = await renderHomeDashboard(envelope.user, adapters);
    await rendered.user.click(await screen.findByRole('button', { name: 'Check in' }));

    await waitFor(() => expect(document.body.textContent).toContain(maximum));
    expect(submit).toHaveBeenCalledTimes(1);
    expect(load).toHaveBeenCalledTimes(2);
    expect(screen.getByText('Checked in')).toBeVisible();
  });

  it('disables capped check-in for every level', async () => {
    const envelope = canonicalEnvelope();
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => jsonResponse(envelope)),
    );
    const load = vi.fn(async (): Promise<HomeCheckinStatus> => ({
      enabled: true,
      mutually_exclusive: false,
      blocked_by_other_checkin: false,
      asset_type: 'general',
      checked_in_today: false,
      balance: '10',
      award_min: '1',
      award_max: '2',
      balance_cap: '10',
    }));
    const submit = vi.fn(async () => ({
      asset_type: 'general' as const,
      award: '1',
      balance: '11',
    }));
    const adapters: HomeAdapters = {
      gameCheckin: { state: 'unavailable' },
      checkin: { state: 'available', load, submit },
      games: { state: 'available', load: async () => [] },
      announcements: { state: 'available', load: async () => homeAnnouncementPage() },
    };
    const lowerLevel = { ...envelope.user, effective_level: 2 as const };
    const rendered = await renderHomeDashboard(lowerLevel, adapters);

    expect(await screen.findByRole('button', { name: 'Check in' })).toBeDisabled();
    expect(screen.getByText(/applies to every level/i)).toBeVisible();

    rendered.rerender(
      <HomeDashboard user={{ ...lowerLevel, effective_level: 3 as const }} adapters={adapters} />,
    );
    expect(screen.getByRole('button', { name: 'Check in' })).toBeDisabled();
    expect(screen.getByText(/applies to every level/i)).toBeVisible();
  });

  it('keeps a committed receipt visible when its follow-up GET fails and retries only the read', async () => {
    const envelope = canonicalEnvelope();
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => jsonResponse(envelope)),
    );
    const initial: HomeCheckinStatus = {
      enabled: true,
      mutually_exclusive: false,
      blocked_by_other_checkin: false,
      asset_type: 'general',
      checked_in_today: false,
      balance: '10',
      award_min: '1',
      award_max: '2',
      balance_cap: '100',
    };
    let reads = 0;
    const load = vi.fn(async (): Promise<HomeCheckinStatus> => {
      reads += 1;
      if (reads === 1) return initial;
      if (reads === 2) throw new ApiError('network_error', 'The network request failed.', 0);
      return { ...initial, asset_type: 'general', checked_in_today: true, balance: '12' };
    });
    const submit = vi.fn(async () => ({
      asset_type: 'general' as const,
      award: '2',
      balance: '12',
    }));
    const adapters: HomeAdapters = {
      gameCheckin: { state: 'unavailable' },
      checkin: { state: 'available', load, submit },
      games: { state: 'available', load: async () => [] },
      announcements: { state: 'available', load: async () => homeAnnouncementPage() },
    };

    const rendered = await renderHomeDashboard(envelope.user, adapters);
    await rendered.user.click(await screen.findByRole('button', { name: 'Check in' }));

    expect(await screen.findByText(/Checked in: awarded 2 credits/)).toBeVisible();
    expect(
      screen.getByText(/Check-in succeeded, but the latest status could not be refreshed/),
    ).toBeVisible();
    expect(submit).toHaveBeenCalledTimes(1);
    expect(load).toHaveBeenCalledTimes(2);

    await rendered.user.click(screen.getByRole('button', { name: 'Refresh' }));
    await waitFor(() =>
      expect(
        screen.queryByText(/Check-in succeeded, but the latest status could not be refreshed/),
      ).not.toBeInTheDocument(),
    );
    expect(screen.getByText(/Checked in: awarded 2 credits/)).toBeVisible();
    expect(screen.getByText('Checked in')).toBeVisible();
    expect(submit).toHaveBeenCalledTimes(1);
    expect(load).toHaveBeenCalledTimes(3);
  });

  it('uses a successful post-midnight authority read for the next day without losing the receipt', async () => {
    const envelope = canonicalEnvelope();
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => jsonResponse(envelope)),
    );
    const initial: HomeCheckinStatus = {
      enabled: true,
      mutually_exclusive: false,
      blocked_by_other_checkin: false,
      asset_type: 'general',
      checked_in_today: false,
      balance: '10',
      award_min: '1',
      award_max: '1',
      balance_cap: '100',
    };
    const load = vi
      .fn<(signal?: AbortSignal) => Promise<HomeCheckinStatus>>()
      .mockResolvedValueOnce(initial)
      .mockResolvedValueOnce({ ...initial, balance: '11' });
    const submit = vi.fn(async () => ({
      asset_type: 'general' as const,
      award: '1',
      balance: '11',
    }));
    const adapters: HomeAdapters = {
      gameCheckin: { state: 'unavailable' },
      checkin: { state: 'available', load, submit },
      games: { state: 'available', load: async () => [] },
      announcements: { state: 'available', load: async () => homeAnnouncementPage() },
    };
    const rendered = await renderHomeDashboard(envelope.user, adapters);

    await rendered.user.click(await screen.findByRole('button', { name: 'Check in' }));
    expect(await screen.findByText(/Checked in: awarded 1 credit/)).toBeVisible();
    expect(screen.getByRole('button', { name: 'Check in' })).toBeEnabled();
    expect(submit).toHaveBeenCalledTimes(1);
  });

  it('discards a late home summary after the shared session switches accounts', async () => {
    const first = canonicalEnvelope();
    const second: UserEnvelope = {
      user: { ...first.user, id: '2', username: 'second-user', guild_nick: null },
    };
    let current = first;
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => jsonResponse(current)),
    );
    const lateGames = deferred<
      Array<{
        game: 'linklink';
        route_id: 'game-linklink';
        kind: 'continue';
        resource_id: string;
        state: 'active';
      }>
    >();
    let gameReads = 0;
    const loadGames = vi.fn(async () => {
      gameReads += 1;
      return gameReads === 1 ? lateGames.promise : [];
    });
    const adapters: HomeAdapters = {
      gameCheckin: { state: 'unavailable' },
      checkin: { state: 'unavailable' },
      games: { state: 'available', load: loadGames },
      announcements: { state: 'available', load: async () => homeAnnouncementPage() },
    };
    const rendered = await renderWithProviders(
      <HomeDashboard key={first.user.id} user={first.user} adapters={adapters} />,
      { station: 'user', role: 'user', locale: 'en' },
    );
    rendered.queryClient.setQueryData(coreKeys.session, sharedSession(first.user));
    await waitFor(() => expect(loadGames).toHaveBeenCalledTimes(1));

    current = second;
    rendered.queryClient.setQueryData(coreKeys.session, sharedSession(second.user));
    rendered.rerender(
      <HomeDashboard key={second.user.id} user={second.user} adapters={adapters} />,
    );
    await waitFor(() => expect(loadGames).toHaveBeenCalledTimes(2));

    await act(async () => {
      lateGames.resolve([
        {
          game: 'linklink',
          route_id: 'game-linklink',
          kind: 'continue',
          resource_id: `ll_${'A'.repeat(22)}`,
          state: 'active',
        },
      ]);
      await lateGames.promise;
    });

    await waitFor(() =>
      expect(
        rendered.queryClient.getQueryData(coreKeys.home(first.user.id, 'games')),
      ).toBeUndefined(),
    );
    expect(rendered.queryClient.getQueryData(coreKeys.home(second.user.id, 'games'))).toEqual([]);
  });
});

describe('account language commit boundary', () => {
  it('allows an unset account language to commit the visible fallback directly', async () => {
    const envelope = canonicalEnvelope();
    const legacy: UserEnvelope = {
      user: { ...envelope.user, lang: '' },
    };
    const updated: UserEnvelope = {
      user: { ...legacy.user, lang: 'zh', updated_at: legacy.user.updated_at + 1 },
    };
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const method = init?.method ?? 'GET';
      if (String(input) === '/api/me' && method === 'GET') return jsonResponse(legacy);
      if (String(input) === '/api/me' && method === 'PATCH') return jsonResponse(updated);
      throw new Error(`Unexpected request: ${method} ${String(input)}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(<AccountWorkspace user={legacy.user} />, {
      station: 'user',
      role: 'user',
      locale: 'zh',
    });
    rendered.queryClient.setQueryData(coreKeys.session, sharedSession(legacy.user));

    const chinese = await screen.findByRole('radio', { name: '中文' });
    expect(chinese).not.toBeChecked();
    expect(chinese).toBeEnabled();
    await rendered.user.click(chinese);

    expect(await screen.findByText('已保存。')).toBeVisible();
    const patchCall = fetchMock.mock.calls.find(([, init]) => init?.method === 'PATCH');
    expect(JSON.parse(String(patchCall?.[1]?.body))).toEqual({ lang: 'zh' });
    expect(screen.queryByRole('button', { name: '保存' })).not.toBeInTheDocument();
    expect(screen.getByRole('radio', { name: '中文' })).toBeChecked();
  });

  it('switches UI, document language, storage, and account-scoped cache only after PATCH succeeds', async () => {
    const envelope = canonicalEnvelope();
    const updated: UserEnvelope = {
      user: { ...envelope.user, lang: 'zh', updated_at: envelope.user.updated_at + 1 },
    };
    const patch = deferred<Response>();
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const method = init?.method ?? 'GET';
      if (String(input) === '/api/me' && method === 'GET') return jsonResponse(envelope);
      if (String(input) === '/api/me' && method === 'PATCH') return patch.promise;
      throw new Error(`Unexpected request: ${method} ${String(input)}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(<AccountLanguageForm user={envelope.user} />, {
      station: 'user',
      role: 'user',
      locale: 'en',
    });
    const session = sharedSession(envelope.user);
    rendered.queryClient.setQueryData(coreKeys.session, session);

    await rendered.user.click(screen.getByRole('radio', { name: '中文' }));
    expect(document.documentElement.lang).toBe('en');
    expect(window.localStorage.getItem('nb.lang')).toBeNull();
    await act(async () => {
      patch.resolve(jsonResponse(updated));
      await patch.promise;
    });

    expect(await screen.findByText('已保存。')).toBeVisible();
    const patchCall = fetchMock.mock.calls.find(([, init]) => init?.method === 'PATCH');
    expect(patchCall?.[0]).toBe('/api/me');
    expect(JSON.parse(String(patchCall?.[1]?.body))).toEqual({ lang: 'zh' });
    expect(new Headers(patchCall?.[1]?.headers).get('Idempotency-Key')).toMatch(
      /^[A-Za-z0-9_-]{22,128}$/,
    );
    expect(document.documentElement.lang).toBe('zh-CN');
    expect(window.localStorage.getItem('nb.lang')).toBe('zh');
    expect(rendered.queryClient.getQueryData(coreKeys.me(envelope.user.id))).toEqual(updated);
    expect(rendered.queryClient.getQueryData(coreKeys.session)).toEqual({
      user: { ...session.user, lang: 'zh' },
    });
  });

  it('retains the success state when the authoritative profile update changes the form props', async () => {
    const envelope = canonicalEnvelope();
    const updated: UserEnvelope = {
      user: { ...envelope.user, lang: 'zh', updated_at: envelope.user.updated_at + 1 },
    };
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
        if (String(input) === '/api/me' && (init?.method ?? 'GET') === 'GET')
          return jsonResponse(envelope);
        if (String(input) === '/api/me' && init?.method === 'PATCH') return jsonResponse(updated);
        throw new Error(`Unexpected request: ${init?.method ?? 'GET'} ${String(input)}`);
      }),
    );
    const rendered = await renderWithProviders(<AccountWorkspace user={envelope.user} />, {
      station: 'user',
      role: 'user',
      locale: 'en',
    });
    rendered.queryClient.setQueryData(coreKeys.session, sharedSession(envelope.user));

    await rendered.user.click(await screen.findByRole('radio', { name: '中文' }));

    expect(await screen.findByText('已保存。')).toBeVisible();
    expect(screen.getByRole('radio', { name: '中文' })).toBeChecked();
  });

  it('restores the confirmed selection and leaves language surfaces unchanged after a failed PATCH', async () => {
    const envelope = canonicalEnvelope();
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
        if (String(input) === '/api/me' && (init?.method ?? 'GET') === 'GET')
          return jsonResponse(envelope);
        return jsonResponse(
          { error: { code: 'maintenance', message: 'Temporarily unavailable.' } },
          503,
        );
      }),
    );
    const rendered = await renderWithProviders(<AccountLanguageForm user={envelope.user} />, {
      station: 'user',
      role: 'user',
      locale: 'en',
    });
    rendered.queryClient.setQueryData(coreKeys.session, sharedSession(envelope.user));

    await rendered.user.click(screen.getByRole('radio', { name: '中文' }));

    await waitFor(() => expect(screen.getByRole('radio', { name: 'English' })).toBeChecked());
    expect(document.documentElement.lang).toBe('en');
    expect(window.localStorage.getItem('nb.lang')).toBeNull();
    expect(screen.getByText(/Could not confirm the result/)).toBeVisible();
  });

  it('GET-reconciles a lost language response and explicitly reuses the exact operation identity', async () => {
    const envelope = canonicalEnvelope();
    const updated: UserEnvelope = {
      user: { ...envelope.user, lang: 'zh', updated_at: envelope.user.updated_at + 1 },
    };
    let patchCount = 0;
    const patchKeys: string[] = [];
    const patchBodies: unknown[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
        if (String(input) === '/api/me' && (init?.method ?? 'GET') === 'GET')
          return jsonResponse(envelope);
        if (String(input) === '/api/me' && init?.method === 'PATCH') {
          patchCount += 1;
          patchKeys.push(new Headers(init.headers).get('Idempotency-Key') ?? '');
          patchBodies.push(JSON.parse(String(init.body)));
          return patchCount === 1
            ? jsonResponse({ error: { code: 'internal', message: 'safe uncertain response' } }, 503)
            : jsonResponse(updated);
        }
        throw new Error(`Unexpected request: ${init?.method ?? 'GET'} ${String(input)}`);
      }),
    );
    const rendered = await renderWithProviders(<AccountLanguageForm user={envelope.user} />, {
      station: 'user',
      role: 'user',
      locale: 'en',
    });
    rendered.queryClient.setQueryData(coreKeys.session, sharedSession(envelope.user));

    await rendered.user.click(screen.getByRole('radio', { name: '中文' }));
    const replay = await screen.findByRole('button', { name: 'Retry' });
    expect(screen.getByRole('radio', { name: 'English' })).toBeChecked();
    expect(document.documentElement.lang).toBe('en');

    await rendered.user.click(replay);

    expect(await screen.findByText('已保存。')).toBeVisible();
    expect(patchKeys).toHaveLength(2);
    expect(patchKeys[0]).toBe(patchKeys[1]);
    expect(patchBodies).toEqual([{ lang: 'zh' }, { lang: 'zh' }]);
  });

  it('discards a late PATCH response after the real session switches accounts', async () => {
    const envelope = canonicalEnvelope();
    const updated: UserEnvelope = {
      user: { ...envelope.user, lang: 'zh', updated_at: envelope.user.updated_at + 1 },
    };
    const patch = deferred<Response>();
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      if (String(input) === '/api/me' && (init?.method ?? 'GET') === 'GET')
        return jsonResponse(envelope);
      if (String(input) === '/api/me' && init?.method === 'PATCH') return patch.promise;
      throw new Error(`Unexpected request: ${init?.method ?? 'GET'} ${String(input)}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(<AccountLanguageForm user={envelope.user} />, {
      station: 'user',
      role: 'user',
      locale: 'en',
    });
    rendered.queryClient.setQueryData(coreKeys.session, sharedSession(envelope.user));

    await rendered.user.click(screen.getByRole('radio', { name: '中文' }));
    await waitFor(() =>
      expect(fetchMock.mock.calls.some(([, init]) => init?.method === 'PATCH')).toBe(true),
    );
    const nextSession = {
      user: {
        id: '8',
        username: 'next-user',
        effective_level: 2,
        lang: 'en',
        marker: 'new-account',
      },
    };
    rendered.queryClient.setQueryData(coreKeys.session, nextSession);

    await act(async () => {
      patch.resolve(jsonResponse(updated));
      await patch.promise;
    });
    await waitFor(() => expect(screen.getByRole('radio', { name: 'English' })).toBeChecked());

    expect(rendered.queryClient.getQueryData(coreKeys.session)).toEqual(nextSession);
    expect(document.documentElement.lang).toBe('en');
    expect(window.localStorage.getItem('nb.lang')).toBeNull();
    expect(screen.queryByText('已保存。')).not.toBeInTheDocument();
  });
});

function LifecycleFixture(props: { accountId: string; adapter: AccountLifecycleAdapter }) {
  const client = useQueryClient();
  useLayoutEffect(() => {
    client.setQueryData(coreKeys.session, {
      user: {
        id: props.accountId,
        username: 'account-' + props.accountId,
        level: 2,
        effective_level: 2,
      },
    });
  }, [client, props.accountId]);
  return <AccountLifecyclePanel {...props} />;
}

describe('account deletion confirmation', () => {
  it('requires one explicit confirmation without handtyping and forwards the one-shot elevation token only in the authorized request', async () => {
    window.sessionStorage.setItem('nb.pending.elevation', 'delete');
    window.sessionStorage.setItem('nb.pending.elevation.account', '1');
    window.sessionStorage.setItem('nb.account.1.secret-draft', 'session-secret');
    window.localStorage.setItem('nb.account.1.private-state', 'local-private');
    window.localStorage.setItem('nb.lang', 'en');
    document.cookie = 'nb_elevated=elevated_token; Path=/; SameSite=Lax';
    const deleteAccount = vi.fn(async () => undefined);
    const adapter: AccountLifecycleAdapter = {
      capabilities: { exportAccount: false, deleteAccount: true },
      beginElevation: vi.fn(async () => 'https://identity.example.test/elevate'),
      exportAccount: vi.fn(async () => ({ blob: new Blob(), schemaVersion: 11 }) as const),
      deleteAccount,
      readAccountAuthority: vi.fn(async () => 'active' as const),
    };
    const rendered = await renderWithProviders(
      <LifecycleFixture accountId="1" adapter={adapter} />,
      {
        station: 'user',
        role: 'user',
        locale: 'en',
      },
    );
    rendered.queryClient.setQueryData(coreKeys.session, {
      user: { id: '1', username: 'account-1', level: 2, effective_level: 2 },
    });
    rendered.queryClient.setQueryData(['user', 'legacy-private'], { private: true });

    const dialog = await screen.findByRole('alertdialog');
    await rendered.user.click(
      within(dialog).getByRole('button', { name: 'Permanently delete account' }),
    );
    await waitFor(() => expect(deleteAccount).toHaveBeenCalledTimes(1));
    expect(deleteAccount).toHaveBeenCalledWith({
      accountId: '1',
      elevatedToken: 'elevated_token',
      confirmation: 'DELETE',
      signal: expect.any(AbortSignal),
    });
    expect(document.cookie).not.toContain('elevated_token');
    expect(window.sessionStorage.getItem('nb.pending.elevation')).toBeNull();
    await waitFor(() => expect(rendered.queryClient.getQueryData(coreKeys.session)).toBeNull());
    expect(rendered.queryClient.getQueryData(['user', 'legacy-private'])).toBeUndefined();
    expect(window.sessionStorage.getItem('nb.account.1.secret-draft')).toBeNull();
    expect(window.localStorage.getItem('nb.account.1.private-state')).toBeNull();
    expect(window.localStorage.getItem('nb.lang')).toBe('en');
  });

  it('ignores a completed deletion response after the account boundary changes', async () => {
    window.sessionStorage.setItem('nb.pending.elevation', 'delete');
    window.sessionStorage.setItem('nb.pending.elevation.account', '1');
    document.cookie = 'nb_elevated=late_delete_token; Path=/; SameSite=Lax';
    const completion = deferred<void>();
    const deleteAccount = vi.fn(() => completion.promise);
    const adapter: AccountLifecycleAdapter = {
      capabilities: { exportAccount: false, deleteAccount: true },
      beginElevation: vi.fn(async () => 'https://identity.example.test/elevate'),
      exportAccount: vi.fn(async () => ({ blob: new Blob(), schemaVersion: 11 }) as const),
      deleteAccount,
      readAccountAuthority: vi.fn(async () => 'active' as const),
    };
    const rendered = await renderWithProviders(
      <LifecycleFixture accountId="1" adapter={adapter} />,
      {
        station: 'user',
        role: 'user',
        locale: 'en',
      },
    );
    rendered.queryClient.setQueryData(coreKeys.session, {
      user: { id: '1', username: 'account-1', level: 2, effective_level: 2 },
    });

    const dialog = await screen.findByRole('alertdialog');
    await rendered.user.click(
      within(dialog).getByRole('button', { name: 'Permanently delete account' }),
    );
    await waitFor(() => expect(deleteAccount).toHaveBeenCalledTimes(1));

    rendered.rerender(<AccountLifecyclePanel accountId="2" adapter={adapter} />);
    const currentSession = {
      user: { id: '2', username: 'account-2', level: 2, effective_level: 2 },
    };
    rendered.queryClient.setQueryData(coreKeys.session, currentSession);
    await act(async () => {
      completion.resolve(undefined);
      await completion.promise;
    });

    expect(rendered.queryClient.getQueryData(coreKeys.session)).toEqual(currentSession);
  });

  it('never retries an unknown deletion and checks account authority only on user request', async () => {
    window.sessionStorage.setItem('nb.pending.elevation', 'delete');
    window.sessionStorage.setItem('nb.pending.elevation.account', '1');
    document.cookie = 'nb_elevated=unknown_delete_token; Path=/; SameSite=Lax';
    const deleteAccount = vi.fn(async () => {
      throw new ApiError('network_error', 'The network request failed.', 0);
    });
    const readAccountAuthority = vi
      .fn<AccountLifecycleAdapter['readAccountAuthority']>()
      .mockResolvedValueOnce('active')
      .mockResolvedValueOnce('deleted');
    const adapter: AccountLifecycleAdapter = {
      capabilities: { exportAccount: false, deleteAccount: true },
      beginElevation: vi.fn(async () => 'https://identity.example.test/elevate'),
      exportAccount: vi.fn(async () => ({ blob: new Blob(), schemaVersion: 11 }) as const),
      deleteAccount,
      readAccountAuthority,
    };
    const rendered = await renderWithProviders(
      <LifecycleFixture accountId="1" adapter={adapter} />,
      { station: 'user', role: 'user', locale: 'en' },
    );
    rendered.queryClient.setQueryData(coreKeys.session, {
      user: { id: '1', username: 'account-1', level: 2, effective_level: 2 },
    });

    const dialog = await screen.findByRole('alertdialog');
    await rendered.user.click(
      within(dialog).getByRole('button', { name: 'Permanently delete account' }),
    );

    expect(await screen.findByText(/deletion response was unknown/i)).toBeVisible();
    expect(deleteAccount).toHaveBeenCalledTimes(1);
    expect(readAccountAuthority).not.toHaveBeenCalled();
    expect(document.cookie).not.toContain('unknown_delete_token');
    expect(window.sessionStorage.getItem('nb.pending.elevation')).toBeNull();

    await rendered.user.click(screen.getByRole('button', { name: 'Check account status' }));
    expect(await screen.findByText(/account is still active/i)).toBeVisible();
    expect(readAccountAuthority).toHaveBeenCalledTimes(1);
    expect(deleteAccount).toHaveBeenCalledTimes(1);

    await rendered.user.click(screen.getByRole('button', { name: 'Check account status' }));
    await waitFor(() => expect(rendered.queryClient.getQueryData(coreKeys.session)).toBeNull());
    expect(readAccountAuthority).toHaveBeenCalledTimes(2);
    expect(deleteAccount).toHaveBeenCalledTimes(1);
  });

  it('requires fresh authorization after an unknown export response', async () => {
    window.sessionStorage.setItem('nb.pending.elevation', 'export');
    window.sessionStorage.setItem('nb.pending.elevation.account', '1');
    document.cookie = 'nb_elevated=unknown_export_token; Path=/; SameSite=Lax';
    const beginElevation = vi.fn(async () => 'https://identity.example.test/elevate');
    const exportAccount = vi.fn(async () => {
      throw new ApiError('network_error', 'The network request failed.', 0);
    });
    const adapter: AccountLifecycleAdapter = {
      capabilities: { exportAccount: true, deleteAccount: false },
      beginElevation,
      exportAccount,
      deleteAccount: vi.fn(async () => undefined),
      readAccountAuthority: vi.fn(async () => 'active' as const),
    };
    const rendered = await renderWithProviders(
      <LifecycleFixture accountId="1" adapter={adapter} />,
      { station: 'user', role: 'user', locale: 'en' },
    );
    rendered.queryClient.setQueryData(coreKeys.session, {
      user: { id: '1', username: 'account-1', level: 2, effective_level: 2 },
    });

    expect(
      await screen.findByText(/verify your Discord identity again to create a new export/i),
    ).toBeVisible();
    expect(exportAccount).toHaveBeenCalledTimes(1);
    expect(exportAccount).toHaveBeenCalledWith({
      accountId: '1',
      elevatedToken: 'unknown_export_token',
      signal: expect.any(AbortSignal),
    });
    expect(beginElevation).not.toHaveBeenCalled();
    expect(screen.getByRole('button', { name: 'Export…' })).toBeEnabled();
    expect(document.cookie).not.toContain('unknown_export_token');
  });
});
