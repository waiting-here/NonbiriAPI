import { act, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { useLocation } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { UsersPage } from '../../admin/pages/UsersPage';
import { UserDeletion } from '../../admin/pages/UserDeletion';
import { AnnouncementDetailPage } from '../../admin/pages/AnnouncementDetailPage';
import { StewardPage } from '../../user/pages/StewardPage';
import { AnnouncementEditor } from './AnnouncementEditor';
import type { AdminUser } from '../operations/managedUsers';

const userFixture = (id = '7', level = 1): AdminUser => ({
  id,
  username: 'member-' + id,
  discord_id: null,
  avatar_url: null,
  guild_nick: null,
  guild_avatar_url: null,
  is_admin: false,
  is_banned: false,
  banned_reason: '',
  banned_until: null,
  charity_suspended_until: null,
  endpoint_limit: null,
  effective_endpoint_limit: '5',
  rpm_limit: null,
  effective_rpm_limit: '60',
  concurrency_limit: null,
  effective_concurrency_limit: '5',
  lang: 'en',
  balance: '0',
  game_balance: '-1.25',
  donation_credit: '0',
  level: {
    manual: level === 5 ? 5 : null,
    automatic: level === 5 ? 1 : level,
    effective: level,
    display_name: 'Lv' + level,
  },
  game_profile_public: false,
  revision: '1',
  created_at: 1_800_000_000,
  updated_at: 1_800_000_000,
  usage: {
    total_requests: '0',
    total_uncached_input_tokens: '0',
    total_cache_write_input_tokens: '0',
    total_cache_read_input_tokens: '0',
    total_output_tokens: '0',
    total_prompt_tokens: '0',
    total_completion_tokens: '0',
    total_unknown_usage_requests: '0',
  },
});
function session(level = 5) {
  const user = userFixture('9', level);
  const fields = Object.fromEntries(
    Object.entries(user).filter(
      ([key]) => !['discord_id', 'is_admin', 'banned_reason', 'level', 'revision'].includes(key),
    ),
  );
  return {
    user: { ...fields, avatar: null, effective_level: level, level_display_name: 'Lv' + level },
  };
}
const page = (data: unknown[]) => ({
  data,
  next_cursor: null,
  pagination: { page: '1', page_size: 20, total_items: String(data.length), total_pages: '1' },
});
const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'content-type': 'application/json' } });
interface Call {
  path: string;
  method: string;
  body: Record<string, unknown>;
  headers: Headers;
}
function install(handler: (call: Call) => unknown | Promise<unknown>) {
  const calls: Call[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(input), window.location.origin);
      const call = {
        path: url.pathname + url.search,
        method: init?.method ?? 'GET',
        body: init?.body ? (JSON.parse(String(init.body)) as Record<string, unknown>) : {},
        headers: new Headers(init?.headers),
      };
      calls.push(call);
      let body: unknown;
      if (call.path.endsWith('/time-zones'))
        body = { version: 'go1.26.6-zoneinfo', zones: ['UTC'] };
      else body = await handler(call);
      return body instanceof Response ? body : json(body);
    }),
  );
  return calls;
}
function LocationProbe() {
  return <output data-testid="location">{useLocation().search}</output>;
}
afterEach(() => {
  vi.unstubAllGlobals();
  localStorage.clear();
});

describe('shared account management', () => {
  it.each(['admin', 'steward'] as const)(
    'submits canonical nullable limits and rejects invalid input for %s',
    async (role) => {
      const base = role === 'admin' ? '/admin/api/users' : '/api/steward/users';
      let target = userFixture();
      const calls = install((call) => {
        if (call.path === '/admin/api/session') return { admin: { username: 'fixture-admin' } };
        if (call.path === '/api/session') return session();
        if (call.path.startsWith(base + '?')) return page([target]);
        if (call.path === base + '/7' && call.method === 'GET') return target;
        if (call.path === base + '/7' && call.method === 'PATCH') {
          target = { ...target, endpoint_limit: call.body.endpoint_limit as string, revision: '2' };
          return target;
        }
        throw new Error('Unexpected request ' + call.path);
      });
      const view = await renderWithProviders(role === 'admin' ? <UsersPage /> : <StewardPage />, {
        station: role === 'admin' ? 'admin' : 'user',
        role: role === 'admin' ? 'admin' : 'level5',
        route: role === 'admin' ? '/users?user=7' : '/steward?tab=users&user=7',
      });
      view.queryClient.setQueryData(
        role === 'admin' ? ['admin', 'session'] : ['user', 'session'],
        role === 'admin' ? { admin: { username: 'fixture-admin' } } : session(),
      );
      const endpoint = await screen.findByLabelText('Endpoint limit');
      const rpm = screen.getByLabelText('RPM limit');
      const concurrency = screen.getByLabelText('In-flight concurrency limit');
      const save = screen.getByRole('button', { name: 'Save limits' });
      fireEvent.change(endpoint, { target: { value: '99' } });
      fireEvent.change(concurrency, { target: { value: '999' } });
      for (const value of ['0', '4097', '1.5', '01']) {
        fireEvent.change(rpm, { target: { value } });
        expect(save).toBeDisabled();
      }
      fireEvent.change(rpm, { target: { value: '' } });
      await view.user.click(save);
      await waitFor(() => expect(calls.filter((call) => call.method === 'PATCH')).toHaveLength(1));
      const write = calls.find((call) => call.method === 'PATCH')!;
      expect(write.body).toEqual({
        mode: 'profile',
        expected_revision: '1',
        endpoint_limit: '99',
        rpm_limit: null,
        concurrency_limit: '999',
        lang: 'en',
      });
      expect(write.headers.get('Idempotency-Key')).toBeTruthy();
      if (role === 'steward') {
        expect(
          within(screen.getByLabelText('Set level')).queryByRole('option', { name: '5' }),
        ).toBeNull();
        expect(
          within(screen.getByLabelText('Target')).queryByRole('option', { name: 'Donor reward' }),
        ).toBeNull();
        expect(screen.queryByRole('button', { name: 'Delete' })).toBeNull();
        expect(calls.every((call) => !call.path.startsWith('/admin/'))).toBe(true);
      }
    },
  );

  it.each([
    ['9', 5],
    ['7', 5],
  ] as const)('keeps steward target %s read only', async (id, level) => {
    const target = userFixture(id, level);
    install((call) => {
      if (call.path === '/api/session') return session();
      if (call.path.startsWith('/api/steward/users?')) return page([target]);
      if (call.path === '/api/steward/users/' + id) return target;
      throw new Error('Unexpected request ' + call.path);
    });
    await renderWithProviders(<StewardPage />, {
      station: 'user',
      role: 'level5',
      route: '/steward?tab=users&user=' + id,
    });
    expect(
      await screen.findByText(/Your account and other stewards can be viewed here/),
    ).toBeVisible();
    expect(screen.queryByRole('button', { name: 'Save limits' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Ban' })).toBeNull();
  });

  it('resets page and selection when effective level changes', async () => {
    const calls = install((call) => {
      if (call.path === '/api/session') return session();
      if (call.path.startsWith('/api/steward/users?')) return page([]);
      throw new Error('Unexpected request ' + call.path);
    });
    const view = await renderWithProviders(
      <>
        <StewardPage />
        <LocationProbe />
      </>,
      {
        station: 'user',
        role: 'level5',
        route: '/steward?tab=users&page=2',
      },
    );
    const level = await screen.findByRole('combobox', { name: 'Level' });
    await view.user.selectOptions(level, '4');
    await waitFor(() =>
      expect(
        calls.some((call) => call.path === '/api/steward/users?level=4&page=1&page_size=20'),
      ).toBe(true),
    );
    expect(screen.getByTestId('location')).toHaveTextContent('level=4');
    expect(screen.getByTestId('location')).toHaveTextContent('page=1');
  });

  it('discards the editing view when a mutation discovers demotion', async () => {
    let currentLevel = 5;
    install((call) => {
      if (call.path === '/api/session') return session(currentLevel);
      if (call.path.startsWith('/api/steward/users?')) return page([userFixture()]);
      if (call.path === '/api/steward/users/7' && call.method === 'GET') return userFixture();
      if (call.method === 'PATCH') {
        currentLevel = 4;
        return json({ error: { code: 'forbidden', message: 'Forbidden' } }, 403);
      }
      throw new Error('Unexpected request ' + call.path);
    });
    const view = await renderWithProviders(<StewardPage />, {
      station: 'user',
      role: 'level5',
      route: '/steward?tab=users&user=7',
    });
    view.queryClient.setQueryData(['user', 'session'], session());
    fireEvent.change(await screen.findByLabelText('Endpoint limit'), { target: { value: '99' } });
    await view.user.click(screen.getByRole('button', { name: 'Save limits' }));
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Save limits' })).toBeNull());
    expect(screen.queryByDisplayValue('99')).toBeNull();
    expect(view.queryClient.getQueryData(['user', 'session'])).toBeNull();
  });
});

describe('shared announcement editor', () => {
  it('closes a private announcement draft when a steward is demoted', async () => {
    let currentLevel = 5;
    install((call) => {
      if (call.path === '/api/session') return session(currentLevel);
      if (call.path.startsWith('/api/steward/announcements?')) return page([]);
      if (call.path === '/api/steward/announcements' && call.method === 'POST') {
        currentLevel = 4;
        return json({ error: { code: 'forbidden', message: 'Forbidden' } }, 403);
      }
      throw new Error('Unexpected request ' + call.path);
    });
    const view = await renderWithProviders(<StewardPage />, {
      station: 'user',
      role: 'level5',
      route: '/steward?tab=announcements',
    });
    view.queryClient.setQueryData(['user', 'session'], session());
    await view.user.click(await screen.findByRole('button', { name: 'Create draft' }));
    await view.user.click(screen.getByRole('button', { name: 'Create private draft' }));
    await waitFor(() =>
      expect(screen.queryByRole('button', { name: 'Create private draft' })).toBeNull(),
    );
    expect(view.queryClient.getQueryData(['user', 'session'])).toBeNull();
  });

  it('shows an administrator session error without a disabled-query spinner', async () => {
    const calls = install(() =>
      json({ error: { code: 'unauthorized', message: 'Unauthorized' } }, 401),
    );
    await renderWithProviders(<AnnouncementDetailPage />, { station: 'admin', role: 'admin' });
    expect(await screen.findByRole('heading', { name: 'Sign-in required' })).toBeVisible();
    expect(calls).toHaveLength(1);
  });

  it('uses steward preview, publish and confirmed delete routes', async () => {
    const id = 'ann_' + 'A'.repeat(22);
    const item = {
      id,
      state: 'draft',
      revision: '1',
      draft: { zh: null, en: { title: 'Fixture notice', body: 'Body' } },
      published: null,
      severity: 'info',
      pinned: false,
      dismissible: true,
      expires_at: null,
      withdrawn_at: null,
      created_at: 1_800_000_000,
      updated_at: 1_800_000_000,
    };
    const base = '/api/steward/announcements/' + id;
    const calls = install((call) => {
      if (call.path === base && call.method === 'GET') return item;
      if (call.path === base + '/preview')
        return { rendered_zh: null, rendered_en: '<p>Body</p>', render_profile_version: '1' };
      if (call.path === base + '/publish' || call.method === 'DELETE')
        return new Response(null, { status: 204 });
      throw new Error('Unexpected request ' + call.path);
    });
    const view = await renderWithProviders(
      <AnnouncementEditor
        role="steward"
        account="9"
        announcementId={id}
        backTo="/steward?tab=announcements"
      />,
      { station: 'user', role: 'level5' },
    );
    view.queryClient.setQueryData(['user', 'session'], session());
    await screen.findByLabelText('English title');
    await view.user.click(screen.getByRole('button', { name: 'Preview' }));
    expect(await screen.findByRole('heading', { name: 'Server preview · 1' })).toBeVisible();
    await view.user.click(screen.getByRole('button', { name: 'Publish' }));
    await view.user.click(
      within(screen.getByRole('alertdialog')).getByRole('button', { name: 'Publish' }),
    );
    await waitFor(() => expect(calls.some((call) => call.path === base + '/publish')).toBe(true));
    await view.user.type(
      screen.getByLabelText('Reason (required for withdraw/delete)'),
      'Retire fixture',
    );
    await view.user.click(screen.getByRole('button', { name: 'Permanently delete' }));
    expect(calls.some((call) => call.method === 'DELETE')).toBe(false);
    await view.user.click(
      within(screen.getByRole('alertdialog')).getByRole('button', { name: 'DELETE permanently' }),
    );
    await waitFor(() =>
      expect(calls.find((call) => call.method === 'DELETE')?.body).toEqual({
        expected_revision: '1',
        confirmation: 'DELETE',
        reason: 'Retire fixture',
      }),
    );
    expect(calls.every((call) => !call.path.startsWith('/admin/'))).toBe(true);
  });
});

describe('administrator account deletion', () => {
  it('keeps password confirmation inside the dialog and stops after a revision change during elevation', async () => {
    let finish!: (value: Response) => void;
    const pending = new Promise<Response>((resolve) => {
      finish = resolve;
    });
    const calls = install((call) => {
      if (call.path === '/admin/api/auth/elevate') return pending;
      throw new Error('Unexpected request ' + call.path);
    });
    const refresh = vi.fn(async () => undefined);
    const view = await renderWithProviders(
      <UserDeletion user={userFixture()} refresh={refresh} />,
      { station: 'admin', role: 'admin' },
    );
    view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'fixture-admin' } });
    await view.user.click(screen.getByRole('button', { name: 'Delete' }));
    const dialog = within(screen.getByRole('alertdialog'));
    await view.user.type(dialog.getByLabelText('Administrator password'), 'synthetic-password');
    await view.user.type(
      dialog.getByLabelText('Type DELETE to confirm immediate account deletion'),
      'DELETE',
    );
    await view.user.click(dialog.getByRole('button', { name: 'Delete user permanently' }));
    await waitFor(() => expect(calls).toHaveLength(1));
    view.rerender(<UserDeletion user={{ ...userFixture(), revision: '2' }} refresh={refresh} />);
    await act(async () => {
      finish(json({ token: 'synthetic-elevation-token', expires_at: 1_900_000_000 }));
      await pending;
    });
    expect(calls.filter((call) => call.method === 'DELETE')).toHaveLength(0);
    expect(screen.queryByRole('alertdialog')).toBeNull();
    expect(refresh).not.toHaveBeenCalled();
  });
});
