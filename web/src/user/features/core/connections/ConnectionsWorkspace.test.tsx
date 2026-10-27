import { fireEvent, screen, waitFor } from '@testing-library/react';
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
    await view.user.click(screen.getByRole('button', { name: 'Service settings' }));
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
});
