import { screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { defaultStewardPermissions, hasStewardPermission } from '../operations/stewardPermissions';
import { EndpointManagement } from './EndpointManagement';
import copy from '../i18n/common/en.json';

const baseURL = 'https://provider.example/v1?region=eu%2Fwest';
function fixture(managed = false) {
  const requests: { path: string; method: string; body?: unknown }[] = [];
  const page = (data: unknown[]) => ({
    data,
    next_cursor: null,
    pagination: { page: '1', page_size: 20, total_items: String(data.length), total_pages: '1' },
  });
  const user = { user_id: '12', endpoint_count: '1', key_count: '1', enabled_count: '1' };
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(input), location.origin);
      requests.push({
        path: url.pathname,
        method: init?.method ?? 'GET',
        body: init?.body ? JSON.parse(String(init.body)) : undefined,
      });
      expect(url.pathname).toMatch(/^\/api\/steward\/overview\/endpoints/);
      if (init?.method === 'PATCH') return new Response(null, { status: 204 });
      if (url.pathname.endsWith('/users')) expect(url.searchParams.get('base_url')).toBe(baseURL);
      const data = url.pathname.endsWith('/users')
        ? [user]
        : [
            {
              base_url: baseURL,
              tags: ['user_gateway'],
              manual_tags: ['user_gateway'],
              tag_rules: managed
                ? [{ pattern: '*.example', action: 'add', tags: ['user_gateway'] }]
                : [],
              user_count: '1',
              endpoint_count: '1',
              key_count: '1',
              users: [user],
            },
          ];
      return new Response(JSON.stringify(page(data)), {
        headers: { 'content-type': 'application/json' },
      });
    }),
  );
  return requests;
}

function Page({
  level = 5,
  read = false,
  tags = false,
}: {
  level?: number;
  read?: boolean;
  tags?: boolean;
}) {
  const policy = defaultStewardPermissions(5);
  policy.permissions['endpoints.read'] = read;
  policy.permissions['endpoints.tags'] = tags;
  const user = { effective_level: level, steward_permissions: policy };
  return (
    <EndpointManagement
      role="steward"
      account="7"
      scopeReady
      canRead={hasStewardPermission(user, 'endpoints.read')}
      canTag={hasStewardPermission(user, 'endpoints.tags')}
    />
  );
}

describe('shared endpoint management', () => {
  it('explains matched rules and prevents manual selection of managed endpoints', async () => {
    const requests = fixture(true);
    await renderWithProviders(<Page level={6} />, { station: 'user', route: '/steward/endpoints' });
    await screen.findByText(baseURL);
    expect(screen.getByText('Manual editing unavailable · Managed by *.example')).toBeVisible();
    expect(screen.getByRole('checkbox', { name: `Select ${baseURL}` })).toBeDisabled();
    expect(screen.getByRole('checkbox', { name: 'Select endpoints on this page' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Add tag' })).toBeDisabled();
    expect(requests.every((request) => request.method === 'GET')).toBe(true);
  });

  it.each([false, true])(
    'requires read permission even when tag permission is %s',
    async (tags) => {
      const requests = fixture();
      await renderWithProviders(<Page tags={tags} />, {
        station: 'user',
        route: '/steward/endpoints',
      });
      expect(await screen.findByText(copy.management.permissions.denied)).toBeVisible();
      expect(screen.queryByRole('button', { name: 'Add tag' })).not.toBeInTheDocument();
      expect(requests).toEqual([]);
    },
  );

  it('grants overview and exact user expansion without tag controls', async () => {
    const requests = fixture();
    const view = await renderWithProviders(<Page read />, {
      station: 'user',
      route: '/steward/endpoints',
    });
    await screen.findByText(baseURL);
    expect(screen.getByText(copy.management.endpoints.readOnly)).toBeVisible();
    expect(screen.queryByRole('checkbox')).not.toBeInTheDocument();
    await view.user.click(screen.getByRole('button', { name: 'Show users' }));
    expect(await screen.findByText('12')).toBeVisible();
    expect(requests.some((request) => request.path.endsWith('/users'))).toBe(true);
    expect(requests.every((request) => request.method === 'GET')).toBe(true);
  });

  it.each([5, 6])('uses the same tag editor for authorized actual level %s', async (level) => {
    const requests = fixture();
    const view = await renderWithProviders(<Page level={level} read tags />, {
      station: 'user',
      route: '/steward/endpoints',
    });
    await screen.findByText(baseURL);
    const editor = screen.getAllByRole('combobox', { name: 'Tags' })[1];
    for (const tag of ['user_gateway', 'steward_gateway', 'invalid_entry']) {
      await view.user.click(
        screen.getByRole('checkbox', { name: 'Select endpoints on this page' }),
      );
      await view.user.selectOptions(editor, tag);
      await view.user.click(screen.getByRole('button', { name: 'Add tag' }));
      await waitFor(() =>
        expect(
          requests.some(
            (request) =>
              request.method === 'PATCH' && (request.body as { tag: string }).tag === tag,
          ),
        ).toBe(true),
      );
      await waitFor(() => expect(screen.getByRole('button', { name: 'Add tag' })).toBeDisabled());
    }
    expect(
      requests.filter((request) => request.method === 'PATCH').map((request) => request.body),
    ).toEqual(
      ['user_gateway', 'steward_gateway', 'invalid_entry'].map((tag) => ({
        base_urls: [baseURL],
        tag,
        add: true,
      })),
    );
  });
});
