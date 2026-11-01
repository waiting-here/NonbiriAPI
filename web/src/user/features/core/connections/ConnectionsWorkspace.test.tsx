import { act, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { installNativeDialog } from '../../../../../test/unit/nativeDialog';
import { renderWithProviders } from '../../../../../test/unit/support';
import type { UserProfile } from '../types';
import { ConnectionsWorkspace } from './ConnectionsWorkspace';
import { CredentialPicker } from './CredentialPicker';

installNativeDialog();

const account: UserProfile = {
  id: '1',
  username: 'model-page-user',
  avatar: null,
  avatar_url: null,
  guild_nick: null,
  guild_avatar_url: null,
  lang: '',
  is_banned: false,
  banned_until: null,
  charity_suspended_until: null,
  endpoint_limit: null,
  effective_endpoint_limit: '20',
  rpm_limit: null,
  effective_rpm_limit: '60',
  concurrency_limit: null,
  effective_concurrency_limit: '10',
  global_rpm_limit: null,
  effective_global_rpm_limit: '60',
  light_rpm_limit: null,
  effective_light_rpm_limit: '30',
  light_concurrency_limit: null,
  effective_light_concurrency_limit: '15',
  balance: '1000',
  game_balance: '0',
  donation_credit: '0',
  effective_level: 2,
  level_display_name: 'Member',
  game_profile_public: false,
  charity_profile_public: false,
  automatic_restrictions: [],
  created_at: 1_700_000_000,
  updated_at: 1_700_000_001,
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
};

const connection = {
  id: '2',
  name: 'Connection 2',
  display_name: 'My API provider',
  note: 'For personal projects',
  primary_entry: { id: '11', base_url: 'https://api.example/v1', note: 'Primary service' },
  revision: '1',
  entry_count: '2',
  credential_count: '1',
  created_at: 1700000000,
  updated_at: 1700000001,
};
const page = (data: unknown[]) => ({
  data,
  next_cursor: null,
  pagination: { page: '1', page_size: 20, total_items: String(data.length), total_pages: '1' },
});
const json = (value: unknown) =>
  new Response(JSON.stringify(value), { headers: { 'Content-Type': 'application/json' } });
const credential = {
  id: '7',
  connection_id: connection.id,
  note: 'Project credential',
  enabled: true,
  revision: '1',
  credential_version: '1',
  display_head: 'head',
  display_tail: 'tail',
  created_at: 1700000000,
  updated_at: 1700000000,
};

function connectionReads() {
  const paths: string[] = [];
  const fetch = vi.fn<typeof globalThis.fetch>(async (input) => {
    const path = new URL(String(input), 'https://fixture.example').pathname;
    paths.push(path);
    if (path === '/api/connections/2') return json(connection);
    if (path === '/api/connections/2/entries') return json(page([]));
    if (path === '/api/connections/2/credentials') return json(page([credential]));
    if (path === '/api/credentials/7') return json(credential);
    if (path === '/api/credentials/7/impact')
      return json({
        ...page([]),
        kind: 'personal',
        summary: { endpoint_keys: '0', donation_keys: '0', discovery_sources: '0' },
      });
    throw new Error('Unexpected request: ' + path);
  });
  vi.stubGlobal('fetch', fetch);
  return { paths, fetch };
}

afterEach(() => {
  vi.unstubAllGlobals();
  localStorage.clear();
});

describe('connection identification', () => {
  it('shows entry context without per-row reads, searches entry text, and preserves the edit name', async () => {
    const requests: URL[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(async (input) => {
        const url = new URL(String(input), 'https://fixture.example');
        requests.push(url);
        if (url.pathname === '/api/connections') return json(page([connection]));
        if (url.pathname === '/api/connections/2') return json(connection);
        if (url.pathname === '/api/connections/2/entries') return json(page([]));
        throw new Error('Unexpected request: ' + url.pathname);
      }),
    );
    const view = await renderWithProviders(<ConnectionsWorkspace user={account} />, {
      station: 'user',
      role: 'user',
      route: '/endpoints?endpoint_id=99&key_id=88&focus_key_id=88&entry_id=99&credential_id=77',
    });
    const serviceLink = await screen.findByRole('link', { name: 'My API provider' });
    for (const field of ['endpoint_id', 'entry_id', 'key_id', 'focus_key_id', 'credential_id'])
      expect(
        new URL(serviceLink.getAttribute('href')!, 'https://fixture.example').searchParams.has(
          field,
        ),
      ).toBe(false);
    expect(screen.getByText(connection.primary_entry.base_url)).toHaveAttribute(
      'title',
      connection.primary_entry.base_url,
    );
    expect(screen.getByText(connection.primary_entry.note)).toBeVisible();
    expect(screen.getByText(connection.note)).toBeVisible();
    expect(screen.queryByText(connection.name)).not.toBeInTheDocument();
    expect(requests.map((request) => request.pathname)).toEqual(['/api/connections']);
    fireEvent.change(screen.getByPlaceholderText('Search service names, endpoint URLs or notes'), {
      target: { value: 'api.example' },
    });
    await view.user.click(screen.getByRole('button', { name: 'Search' }));
    await waitFor(() =>
      expect(requests.some((request) => request.searchParams.get('q') === 'api.example')).toBe(
        true,
      ),
    );
    await view.user.click(await screen.findByRole('link', { name: 'My API provider' }));
    await screen.findByRole('heading', { level: 1, name: 'My API provider' });
    await view.user.click(screen.getByRole('tab', { name: 'Service settings' }));
    expect(screen.getByRole('textbox', { name: 'Name' })).toHaveValue(connection.name);
    const back = screen.getByRole('link', { name: 'Back to my services' });
    expect(back.getAttribute('href')).toContain('connections_q=api.example');
  });

  it('lists only the current service keys and offers no cross-service navigation', async () => {
    const paths: string[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(async (input) => {
        const url = new URL(String(input), 'https://fixture.example');
        paths.push(url.pathname);
        if (url.pathname === '/api/connections/2/credentials') return json(page([]));
        throw new Error('Unexpected request: ' + url.pathname);
      }),
    );
    await renderWithProviders(
      <CredentialPicker accountId={account.id} connectionId="2" onPick={vi.fn()} />,
      { station: 'user', role: 'user' },
    );
    await screen.findByRole('heading', { name: 'Choose a service key' });
    await waitFor(() => expect(paths).toEqual(['/api/connections/2/credentials']));
    expect(screen.queryByText('Choose from another connection')).not.toBeInTheDocument();
  });

  it('loads tabs on demand and preserves settings and new-key drafts when switching', async () => {
    const { paths } = connectionReads();
    const view = await renderWithProviders(<ConnectionsWorkspace user={account} />, {
      station: 'user',
      role: 'user',
      route: '/endpoints?connection_id=2',
    });
    await screen.findByText(
      'This service has no endpoints yet. Add one to manage keys and models.',
    );
    expect(paths).toEqual(['/api/connections/2', '/api/connections/2/entries']);
    await view.user.click(screen.getByRole('tab', { name: 'Service settings' }));
    const name = screen.getByRole('textbox', { name: 'Name' });
    await view.user.clear(name);
    await view.user.type(name, 'Unfinished name');
    await view.user.click(screen.getByRole('tab', { name: 'Keys' }));
    await view.user.click(await screen.findByRole('button', { name: 'Add new key' }));
    await view.user.type(screen.getByLabelText('API key', { exact: true }), 'synthetic-tab-draft');
    await view.user.click(screen.getByRole('tab', { name: 'Endpoints' }));
    await view.user.click(screen.getByRole('tab', { name: 'Service settings' }));
    expect(screen.getByRole('textbox', { name: 'Name' })).toHaveValue('Unfinished name');
    await view.user.click(screen.getByRole('tab', { name: 'Keys' }));
    expect(screen.getByLabelText('API key', { exact: true })).toHaveValue('synthetic-tab-draft');
  });

  it('keeps a legacy credential detail selected across tabs and returns to the key list', async () => {
    const { paths } = connectionReads();
    const view = await renderWithProviders(<ConnectionsWorkspace user={account} />, {
      station: 'user',
      role: 'user',
      route: '/endpoints?connection_id=2&credential_id=7',
    });
    await screen.findByRole('heading', { name: credential.note });
    const back = screen.getByRole('button', { name: 'Back to service keys' });
    expect(screen.getByRole('tab', { name: 'Keys' })).toHaveAttribute('aria-selected', 'true');
    expect(paths).not.toContain('/api/connections/2/entries');
    await view.user.click(screen.getByRole('tab', { name: 'Endpoints' }));
    expect(back).not.toBeVisible();
    expect(screen.getByRole('tab', { name: 'Endpoints' })).toHaveAttribute('aria-selected', 'true');
    await view.user.click(screen.getByRole('tab', { name: 'Keys' }));
    expect(back).toBeVisible();
    expect(screen.getByRole('heading', { name: credential.note })).toBeVisible();
    await view.user.click(back);
    await screen.findByRole('table', { name: 'Keys' });
    expect(screen.getByRole('tab', { name: 'Keys' })).toHaveAttribute('aria-selected', 'true');
  });

  it('finishes an in-flight settings save after switching tabs without losing the request', async () => {
    const reads = connectionReads();
    const originalRead = reads.fetch.getMockImplementation()!;
    let finish!: () => void;
    const signals: (AbortSignal | null | undefined)[] = [];
    const expectedRevisions: unknown[] = [];
    let revision = 1;
    reads.fetch.mockImplementation(async (input, init) => {
      if (init?.method === 'PATCH') {
        signals.push(init.signal);
        expectedRevisions.push(JSON.parse(String(init.body)).expected_revision);
        if (expectedRevisions.length === 1)
          await new Promise<void>((resolve) => {
            finish = resolve;
          });
        revision++;
        return json({});
      }
      if (
        revision > 1 &&
        new URL(String(input), 'https://fixture.example').pathname === '/api/connections/2'
      )
        return json({ ...connection, name: 'Saved name', revision: String(revision) });
      return originalRead(input, init);
    });
    const view = await renderWithProviders(<ConnectionsWorkspace user={account} />, {
      station: 'user',
      role: 'user',
      route: '/endpoints?connection_id=2&connection_view=settings',
    });
    view.queryClient.setQueryData(['user', 'session'], {
      user: { id: account.id, username: account.username, effective_level: 2 },
    });
    const name = await screen.findByRole('textbox', { name: 'Name' });
    await view.user.clear(name);
    await view.user.type(name, 'Saved name');
    await view.user.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(signals).toHaveLength(1));
    await view.user.click(screen.getByRole('tab', { name: 'Keys' }));
    expect(signals[0]?.aborted).toBe(false);
    await act(async () => finish());
    await waitFor(() => expect(view.queryClient.isMutating()).toBe(0));
    expect(screen.getByRole('tab', { name: 'Keys' })).toHaveAttribute('aria-selected', 'true');
    await view.user.click(screen.getByRole('tab', { name: 'Service settings' }));
    await waitFor(() =>
      expect(screen.getByRole('textbox', { name: 'Name' })).toHaveValue('Saved name'),
    );
    await view.user.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(expectedRevisions).toEqual(['1', '2']));
    await waitFor(() => expect(view.queryClient.isMutating()).toBe(0));
  });
});
