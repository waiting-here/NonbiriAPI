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

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

describe('home independent capability states', () => {
  it('keeps confirmed profile, economy, usage, and announcement data when the game summary fails', async () => {
    const envelope = canonicalEnvelope();
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: string | URL | Request) => {
        if (String(input) === '/api/me') return jsonResponse(envelope);
        throw new Error(`Unexpected request: ${String(input)}`);
      }),
    );
    const adapters: HomeAdapters = {
      checkin: { state: 'unavailable' },
      games: {
        state: 'available',
        load: async () => {
          throw new Error('bounded game summary failure');
        },
      },
      announcements: {
        state: 'available',
        load: async () => [
          { id: '41', title: 'Planned maintenance', excerpt: 'A short confirmed summary.' },
        ],
      },
    };

    await renderWithProviders(<HomeDashboard user={envelope.user} adapters={adapters} />, {
      station: 'user',
      role: 'user',
      locale: 'en',
    });

    expect(screen.getByText('Guild Alice')).toBeVisible();
    expect(screen.getByRole('heading', { name: 'Lifetime usage' })).toBeVisible();
    expect(await screen.findByText('-1.5')).toBeVisible();
    expect(await screen.findByRole('heading', { name: 'Continue or view results' })).toBeVisible();
    expect(screen.getByText('Could not load this section')).toBeVisible();
    expect(await screen.findByText('Planned maintenance')).toBeVisible();
    expect(screen.getByText('A short confirmed summary.')).toBeVisible();
  });

  it('hides successful empty game and announcement summaries', async () => {
    const envelope = canonicalEnvelope();
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => jsonResponse(envelope)),
    );
    const adapters: HomeAdapters = {
      checkin: { state: 'unavailable' },
      games: { state: 'available', load: async () => [] },
      announcements: { state: 'available', load: async () => [] },
    };

    await renderWithProviders(<HomeDashboard user={envelope.user} adapters={adapters} />, {
      station: 'user',
      role: 'user',
      locale: 'en',
    });

    await waitFor(() => {
      expect(
        screen.queryByRole('heading', { name: 'Continue or view results' }),
      ).not.toBeInTheDocument();
      expect(screen.queryByRole('heading', { name: 'Announcements' })).not.toBeInTheDocument();
    });
    expect(screen.getByRole('heading', { name: 'Daily check-in' })).toBeVisible();
  });

  it('does not turn an available capability with no loader into a successful empty summary', async () => {
    const envelope = canonicalEnvelope();
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse(envelope)));
    const adapters = {
      checkin: { state: 'unavailable' },
      games: { state: 'available' },
      announcements: { state: 'unavailable' },
    } as unknown as HomeAdapters;

    await renderWithProviders(<HomeDashboard user={envelope.user} adapters={adapters} />, {
      station: 'user',
      role: 'user',
      locale: 'en',
    });

    expect(
      await screen.findByRole('heading', { name: 'Continue or view results' }),
    ).toBeVisible();
    expect(await screen.findByText('Could not load this section')).toBeVisible();
  });

  it('GET-reconciles an unknown check-in response without automatically resubmitting', async () => {
    const envelope = canonicalEnvelope();
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse(envelope)));
    const reconciliation = deferred<HomeCheckinStatus>();
    const initial: HomeCheckinStatus = {
      enabled: true,
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
      checkin: { state: 'available', load, submit },
      games: { state: 'available', load: async () => [] },
      announcements: { state: 'available', load: async () => [] },
    };

    const rendered = await renderWithProviders(
      <HomeDashboard user={envelope.user} adapters={adapters} />,
      { station: 'user', role: 'user', locale: 'en' },
    );
    await rendered.user.click(await screen.findByRole('button', { name: 'Check in' }));

    expect(await screen.findByText(/response was lost/i)).toBeVisible();
    expect(submit).toHaveBeenCalledTimes(1);
    expect(load).toHaveBeenCalledTimes(2);

    await act(async () => {
      reconciliation.resolve({
        ...initial,
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
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse(envelope)));
    const maximum = '340282366920938463463374607431768211.455';
    const initial: HomeCheckinStatus = {
      enabled: true,
      checked_in_today: false,
      balance: '-1.5',
      award_min: maximum,
      award_max: maximum,
      balance_cap: maximum,
    };
    const load = vi
      .fn<(signal?: AbortSignal) => Promise<HomeCheckinStatus>>()
      .mockResolvedValueOnce(initial)
      .mockResolvedValueOnce({ ...initial, checked_in_today: true, balance: maximum });
    const submit = vi.fn(async () => ({ award: maximum, balance: maximum }));
    const adapters: HomeAdapters = {
      checkin: { state: 'available', load, submit },
      games: { state: 'available', load: async () => [] },
      announcements: { state: 'available', load: async () => [] },
    };

    const rendered = await renderWithProviders(
      <HomeDashboard user={envelope.user} adapters={adapters} />,
      { station: 'user', role: 'user', locale: 'en' },
    );
    await rendered.user.click(await screen.findByRole('button', { name: 'Check in' }));

    await waitFor(() => expect(document.body.textContent).toContain(maximum));
    expect(submit).toHaveBeenCalledTimes(1);
    expect(load).toHaveBeenCalledTimes(2);
    expect(screen.getByText('Checked in')).toBeVisible();
  });

  it('disables capped lower-level check-in while preserving the level-three bypass', async () => {
    const envelope = canonicalEnvelope();
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse(envelope)));
    const load = vi.fn(async (): Promise<HomeCheckinStatus> => ({
      enabled: true,
      checked_in_today: false,
      balance: '10',
      award_min: '1',
      award_max: '2',
      balance_cap: '10',
    }));
    const submit = vi.fn(async () => ({ award: '1', balance: '11' }));
    const adapters: HomeAdapters = {
      checkin: { state: 'available', load, submit },
      games: { state: 'available', load: async () => [] },
      announcements: { state: 'available', load: async () => [] },
    };
    const lowerLevel = { ...envelope.user, effective_level: 2 as const };
    const rendered = await renderWithProviders(
      <HomeDashboard user={lowerLevel} adapters={adapters} />,
      { station: 'user', role: 'user', locale: 'en' },
    );

    expect(await screen.findByRole('button', { name: 'Check in' })).toBeDisabled();
    expect(screen.getByText(/limit only decides whether you can check in/i)).toBeVisible();

    rendered.rerender(
      <HomeDashboard user={{ ...lowerLevel, effective_level: 3 as const }} adapters={adapters} />,
    );
    expect(screen.getByRole('button', { name: 'Check in' })).toBeEnabled();
  });

  it('keeps a committed receipt visible when its follow-up GET fails and retries only the read', async () => {
    const envelope = canonicalEnvelope();
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse(envelope)));
    const initial: HomeCheckinStatus = {
      enabled: true,
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
      return { ...initial, checked_in_today: true, balance: '12' };
    });
    const submit = vi.fn(async () => ({ award: '2', balance: '12' }));
    const adapters: HomeAdapters = {
      checkin: { state: 'available', load, submit },
      games: { state: 'available', load: async () => [] },
      announcements: { state: 'available', load: async () => [] },
    };

    const rendered = await renderWithProviders(
      <HomeDashboard user={envelope.user} adapters={adapters} />,
      { station: 'user', role: 'user', locale: 'en' },
    );
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
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse(envelope)));
    const initial: HomeCheckinStatus = {
      enabled: true,
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
    const submit = vi.fn(async () => ({ award: '1', balance: '11' }));
    const adapters: HomeAdapters = {
      checkin: { state: 'available', load, submit },
      games: { state: 'available', load: async () => [] },
      announcements: { state: 'available', load: async () => [] },
    };
    const rendered = await renderWithProviders(
      <HomeDashboard user={envelope.user} adapters={adapters} />,
      { station: 'user', role: 'user', locale: 'en' },
    );

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
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse(current)));
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
      checkin: { state: 'unavailable' },
      games: { state: 'available', load: loadGames },
      announcements: { state: 'available', load: async () => [] },
    };
    const rendered = await renderWithProviders(
      <HomeDashboard key={first.user.id} user={first.user} adapters={adapters} />,
      { station: 'user', role: 'user', locale: 'en' },
    );
    rendered.queryClient.setQueryData(coreKeys.session, { user: { id: first.user.id } });
    await waitFor(() => expect(loadGames).toHaveBeenCalledTimes(1));

    current = second;
    rendered.queryClient.setQueryData(coreKeys.session, { user: { id: second.user.id } });
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
      expect(rendered.queryClient.getQueryData(coreKeys.home(first.user.id, 'games'))).toBeUndefined(),
    );
    expect(rendered.queryClient.getQueryData(coreKeys.home(second.user.id, 'games'))).toEqual([]);
  });
});

describe('account language commit boundary', () => {
  it('switches UI, document language, storage, and account-scoped cache only after PATCH succeeds', async () => {
    const envelope = canonicalEnvelope();
    const updated: UserEnvelope = {
      user: { ...envelope.user, lang: 'zh', updated_at: envelope.user.updated_at + 1 },
    };
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const method = init?.method ?? 'GET';
      if (String(input) === '/api/me' && method === 'GET') return jsonResponse(envelope);
      if (String(input) === '/api/me' && method === 'PATCH') return jsonResponse(updated);
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

    await rendered.user.selectOptions(screen.getByLabelText('Language'), 'zh');
    expect(document.documentElement.lang).toBe('en');
    expect(window.localStorage.getItem('nb.lang')).toBeNull();
    await rendered.user.click(screen.getByRole('button', { name: 'Save' }));

    expect(await screen.findByText('语言已保存。')).toBeVisible();
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

    await rendered.user.selectOptions(await screen.findByLabelText('Language'), 'zh');
    await rendered.user.click(screen.getByRole('button', { name: 'Save' }));

    expect(await screen.findByText('语言已保存。')).toBeVisible();
    expect(screen.getByLabelText('语言')).toHaveValue('zh');
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

    await rendered.user.selectOptions(screen.getByLabelText('Language'), 'zh');
    await rendered.user.click(screen.getByRole('button', { name: 'Save' }));

    await waitFor(() => expect(screen.getByLabelText('Language')).toHaveValue('en'));
    expect(document.documentElement.lang).toBe('en');
    expect(window.localStorage.getItem('nb.lang')).toBeNull();
    expect(screen.getByText(/The response was lost/)).toBeVisible();
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

    await rendered.user.selectOptions(screen.getByLabelText('Language'), 'zh');
    await rendered.user.click(screen.getByRole('button', { name: 'Save' }));
    const replay = await screen.findByRole('button', { name: 'Retry the same operation' });
    expect(screen.getByLabelText('Language')).toHaveValue('en');
    expect(document.documentElement.lang).toBe('en');

    await rendered.user.click(replay);

    expect(await screen.findByText('语言已保存。')).toBeVisible();
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

    await rendered.user.selectOptions(screen.getByLabelText('Language'), 'zh');
    await rendered.user.click(screen.getByRole('button', { name: 'Save' }));
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
    await waitFor(() => expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled());

    expect(rendered.queryClient.getQueryData(coreKeys.session)).toEqual(nextSession);
    expect(document.documentElement.lang).toBe('en');
    expect(window.localStorage.getItem('nb.lang')).toBeNull();
    expect(screen.queryByText('语言已保存。')).not.toBeInTheDocument();
  });
});

describe('account deletion confirmation', () => {
  it('requires exact DELETE and forwards the one-shot elevation token only in the authorized request', async () => {
    window.sessionStorage.setItem('nb.pending.elevation', 'delete');
    window.sessionStorage.setItem('nb.pending.elevation.account', '1');
    window.sessionStorage.setItem('nb.account.1.secret-draft', 'session-secret');
    window.localStorage.setItem('nb.account.1.private-state', 'local-private');
    window.localStorage.setItem('nb.lang', 'en');
    document.cookie = 'nb_elevated=elevated_token; Path=/; SameSite=Lax';
    const deleteAccount = vi.fn(async () => undefined);
    const adapter: AccountLifecycleAdapter = {
      capabilities: { exportV4: false, deleteAccount: true },
      beginElevation: vi.fn(async () => 'https://identity.example.test/elevate'),
      exportV4: vi.fn(async () => ({ blob: new Blob(), schemaVersion: 4 }) as const),
      deleteAccount,
      readAccountAuthority: vi.fn(async () => 'active' as const),
    };
    const rendered = await renderWithProviders(
      <AccountLifecyclePanel accountId="1" adapter={adapter} />,
      {
        station: 'user',
        role: 'user',
        locale: 'en',
      },
    );
    rendered.queryClient.setQueryData(coreKeys.session, { user: { id: '1' } });
    rendered.queryClient.setQueryData(['user', 'legacy-private'], { private: true });

    const dialog = await screen.findByRole('alertdialog');
    await rendered.user.click(
      within(dialog).getByRole('button', { name: 'Permanently delete account' }),
    );
    expect(within(dialog).getByText('Type DELETE exactly.')).toBeVisible();
    expect(deleteAccount).not.toHaveBeenCalled();

    await rendered.user.type(within(dialog).getByLabelText('Type DELETE to continue'), 'DELETE');
    await rendered.user.click(
      within(dialog).getByRole('button', { name: 'Permanently delete account' }),
    );
    await waitFor(() => expect(deleteAccount).toHaveBeenCalledTimes(1));
    expect(deleteAccount).toHaveBeenCalledWith({
      accountId: '1',
      elevatedToken: 'elevated_token',
      confirmation: 'DELETE',
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
      capabilities: { exportV4: false, deleteAccount: true },
      beginElevation: vi.fn(async () => 'https://identity.example.test/elevate'),
      exportV4: vi.fn(async () => ({ blob: new Blob(), schemaVersion: 4 }) as const),
      deleteAccount,
      readAccountAuthority: vi.fn(async () => 'active' as const),
    };
    const rendered = await renderWithProviders(
      <AccountLifecyclePanel accountId="1" adapter={adapter} />,
      {
        station: 'user',
        role: 'user',
        locale: 'en',
      },
    );
    rendered.queryClient.setQueryData(coreKeys.session, { user: { id: '1' } });

    const dialog = await screen.findByRole('alertdialog');
    await rendered.user.type(within(dialog).getByLabelText('Type DELETE to continue'), 'DELETE');
    await rendered.user.click(
      within(dialog).getByRole('button', { name: 'Permanently delete account' }),
    );
    await waitFor(() => expect(deleteAccount).toHaveBeenCalledTimes(1));

    rendered.rerender(<AccountLifecyclePanel accountId="2" adapter={adapter} />);
    const currentSession = { user: { id: '2' } };
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
      capabilities: { exportV4: false, deleteAccount: true },
      beginElevation: vi.fn(async () => 'https://identity.example.test/elevate'),
      exportV4: vi.fn(async () => ({ blob: new Blob(), schemaVersion: 4 }) as const),
      deleteAccount,
      readAccountAuthority,
    };
    const rendered = await renderWithProviders(
      <AccountLifecyclePanel accountId="1" adapter={adapter} />,
      { station: 'user', role: 'user', locale: 'en' },
    );
    rendered.queryClient.setQueryData(coreKeys.session, { user: { id: '1' } });

    const dialog = await screen.findByRole('alertdialog');
    await rendered.user.type(within(dialog).getByLabelText('Type DELETE to continue'), 'DELETE');
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
    const exportV4 = vi.fn(async () => {
      throw new ApiError('network_error', 'The network request failed.', 0);
    });
    const adapter: AccountLifecycleAdapter = {
      capabilities: { exportV4: true, deleteAccount: false },
      beginElevation,
      exportV4,
      deleteAccount: vi.fn(async () => undefined),
      readAccountAuthority: vi.fn(async () => 'active' as const),
    };
    const rendered = await renderWithProviders(
      <AccountLifecyclePanel accountId="1" adapter={adapter} />,
      { station: 'user', role: 'user', locale: 'en' },
    );
    rendered.queryClient.setQueryData(coreKeys.session, { user: { id: '1' } });

    const dialog = await screen.findByRole('alertdialog');
    await rendered.user.click(within(dialog).getByRole('button', { name: 'Create export' }));

    expect(await screen.findByText(/verify your Discord identity again to create a new export/i)).toBeVisible();
    expect(exportV4).toHaveBeenCalledTimes(1);
    expect(exportV4).toHaveBeenCalledWith({
      accountId: '1',
      elevatedToken: 'unknown_export_token',
    });
    expect(beginElevation).not.toHaveBeenCalled();
    expect(screen.getByRole('button', { name: 'Request export' })).toBeEnabled();
    expect(document.cookie).not.toContain('unknown_export_token');
  });
});
