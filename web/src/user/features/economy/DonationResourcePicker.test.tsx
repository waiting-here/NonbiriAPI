import { useEffect, useReducer, useState, type ReactNode } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { useLocation, useNavigate } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  beginManagementSessionRequest,
  noteManagementSessionSuccess,
} from '@shared/charityManagement';
import { economyKeys } from './queries';
import { DonationResourcePicker } from './DonationResourcePicker';
import type { EndpointKeyChoice } from './types';
import { installJsonFetchFixtures, renderWithProviders } from '../../../../test/unit/support';

const ACCOUNT_A = '5';
const ACCOUNT_B = '6';

function sessionFixture(id: string, username = `fixture-${id}`) {
  return {
    user: {
      id,
      username,
      avatar: null,
      avatar_url: null,
      guild_nick: null,
      guild_avatar_url: null,
      lang: 'en',
      is_banned: false,
      banned_until: null,
      charity_suspended_until: null,
      endpoint_limit: null,
      effective_endpoint_limit: '100',
      rpm_limit: null,
      effective_rpm_limit: '60',
      concurrency_limit: null,
      effective_concurrency_limit: '5',
      balance: '0',
      donation_credit: '0',
      effective_level: 5,
      level_display_name: 'Lv5',
      game_profile_public: false,
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
    },
  };
}

const sessionA = sessionFixture(ACCOUNT_A);
const sessionB = sessionFixture(ACCOUNT_B);

function endpointWire(
  id: string,
  overrides: Record<string, unknown> = {},
): Record<string, unknown> {
  return {
    id,
    connector_type: 'openai-compatible',
    base_url: `https://endpoint-${id}.example/v1`,
    origin: { kind: 'custom' },
    note: `endpoint-${id}`,
    enabled: true,
    revision: '1',
    key_count: '1',
    created_at: 1_700_000_000,
    updated_at: 1_700_000_001,
    ...overrides,
  };
}

function keyBrowse(
  eligibility: 'eligible' | 'already_donated' | 'security_processing' = 'eligible',
) {
  return {
    donation_eligibility: eligibility,
    model_count: '0',
    binding_count: '0',
    available_binding_count: '0',
    discovery: {
      state: 'unknown',
      revision: '1',
      result: null,
      safe_class: 'none',
      observed_at: null,
      count: null,
    },
    preview: [],
  };
}

function keyWire(
  endpointId: string,
  id: string,
  eligibility: 'eligible' | 'already_donated' | 'security_processing' = 'eligible',
  overrides: Record<string, unknown> = {},
  withBrowse = true,
): Record<string, unknown> {
  const base: Record<string, unknown> = {
    id,
    endpoint_id: endpointId,
    display_head: `head-${id}`,
    display_tail: `tail-${id}`,
    note: `key-${id}`,
    enabled: true,
    force_store_false: false,
    max_concurrency: 2,
    max_rpm: 30,
    suspension_state: 'none',
    revision: '1',
    created_at: 1_700_000_000,
    updated_at: 1_700_000_001,
    ...overrides,
  };
  if (withBrowse) base.browse = keyBrowse(eligibility);
  return base;
}

function numberedPage(
  data: unknown[],
  page: string,
  totalItems: number,
  pageSize = 20,
): Record<string, unknown> {
  return {
    data,
    next_cursor: null,
    pagination: {
      page,
      page_size: pageSize,
      total_items: String(totalItems),
      total_pages: String(Math.max(1, Math.ceil(totalItems / pageSize))),
    },
  };
}

function endpointPath(page = '1', pageSize = 20, search = ''): string {
  const params = new URLSearchParams({ page, page_size: String(pageSize) });
  if (search) params.set('q', search);
  return `/api/endpoints?${params.toString()}`;
}

function keyPath(endpointId: string, page = '1', pageSize = 20, search = ''): string {
  const params = new URLSearchParams({ page, page_size: String(pageSize) });
  if (search) params.set('q', search);
  return `/api/endpoints/${endpointId}/keys?${params.toString()}`;
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
  });
}

function installPickerServer(
  handler: (url: URL, method: string, init?: RequestInit) => Response | Promise<Response>,
) {
  const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
    const raw = input instanceof Request ? input.url : String(input);
    const url = new URL(raw, window.location.origin);
    return handler(url, (init?.method ?? 'GET').toUpperCase(), init);
  });
  vi.stubGlobal('fetch', fetchMock);
  return fetchMock;
}

function SeedSession({ session, children }: { session: unknown; children: ReactNode }) {
  const queryClient = useQueryClient();
  const [ready, markReady] = useReducer(() => true, false);
  useEffect(() => {
    const generation = beginManagementSessionRequest(queryClient, 'steward');
    if (!noteManagementSessionSuccess(queryClient, 'steward', session, generation)) return;
    queryClient.setQueryData(['user', 'session'], session);
    markReady();
  }, [queryClient, session]);
  return ready ? <>{children}</> : null;
}

function ControlledPicker({
  accountId = ACCOUNT_A,
  initialSelected = [],
  disabled = false,
  enabled = true,
  renderSelected,
  onReadStateChange,
}: {
  accountId?: string;
  initialSelected?: EndpointKeyChoice[];
  disabled?: boolean;
  enabled?: boolean;
  renderSelected?: (choice: EndpointKeyChoice) => ReactNode;
  onReadStateChange?: (blocked: boolean) => void;
}) {
  const [selected, setSelected] = useState(initialSelected);
  return (
    <>
      <DonationResourcePicker
        accountId={accountId}
        selected={selected}
        onChange={setSelected}
        disabled={disabled}
        enabled={enabled}
        renderSelected={renderSelected}
        onReadStateChange={onReadStateChange}
      />
      <output data-testid="selected-ids">
        {selected.map((choice) => `${choice.endpoint.id}:${choice.key.id}`).join(',')}
      </output>
    </>
  );
}

function LocationProbe() {
  const location = useLocation();
  const navigate = useNavigate();
  return (
    <>
      <output data-testid="location-search">{location.search}</output>
      <button type="button" onClick={() => navigate('/?endpoint_page=2&endpoint_id=1&key_page=2')}>
        open nested page
      </button>
      <button type="button" onClick={() => navigate(-1)}>
        pop back
      </button>
    </>
  );
}

function selectedChoice(endpointId: string, keyId: string): EndpointKeyChoice {
  return {
    endpoint: {
      id: endpointId,
      connectorType: 'openai-compatible',
      baseUrl: `https://endpoint-${endpointId}.example/v1`,
      origin: { kind: 'custom' },
      note: `endpoint-${endpointId}`,
      enabled: true,
      revision: '1',
      keyCount: '1',
      createdAt: 1_700_000_000,
      updatedAt: 1_700_000_001,
    },
    key: {
      id: keyId,
      endpointId,
      displayHead: `head-${keyId}`,
      displayTail: `tail-${keyId}`,
      note: `key-${keyId}`,
      enabled: true,
      forceStoreFalse: false,
      maxConcurrency: 2,
      maxRPM: 30,
      suspensionState: 'none',
      revision: '1',
      createdAt: 1_700_000_000,
      updatedAt: 1_700_000_001,
    },
    eligibility: 'eligible',
  };
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('DonationResourcePicker', () => {
  it('keeps endpoint pagination controls visible when the source page is empty', async () => {
    const fetchMock = installPickerServer((url) => {
      if (url.pathname === '/api/endpoints' && url.search === '?page=1&page_size=20') {
        return jsonResponse(numberedPage([], '1', 0));
      }
      throw new Error(`Unexpected picker request: ${url.pathname}${url.search}`);
    });
    await renderWithProviders(
      <SeedSession session={sessionA}>
        <ControlledPicker />
      </SeedSession>,
      { station: 'user', role: 'user' },
    );

    expect(await screen.findByText('No matching endpoints')).toBeVisible();
    const navigation = screen.getByRole('navigation', { name: 'Pagination' });
    expect(navigation).toHaveTextContent('Page 1 of 1 · 0 items');
    expect(within(navigation).getByRole('combobox')).toHaveValue('20');
    expect(within(navigation).getByRole('button', { name: 'Previous' })).toBeDisabled();
    expect(within(navigation).getByRole('button', { name: 'Next' })).toBeDisabled();
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it('keeps key pagination controls visible when the key page is empty', async () => {
    const fetchMock = installPickerServer((url) => {
      if (url.pathname === '/api/endpoints' && url.search === '?page=1&page_size=20') {
        return jsonResponse(numberedPage([endpointWire('1')], '1', 1));
      }
      if (url.pathname === '/api/endpoints/1') {
        return jsonResponse(endpointWire('1'));
      }
      if (url.pathname === '/api/endpoints/1/keys' && url.search === '?page=1&page_size=20') {
        return jsonResponse(numberedPage([], '1', 0));
      }
      throw new Error(`Unexpected picker request: ${url.pathname}${url.search}`);
    });
    const rendered = await renderWithProviders(
      <SeedSession session={sessionA}>
        <ControlledPicker />
      </SeedSession>,
      { station: 'user', role: 'user' },
    );

    await rendered.user.click(await screen.findByRole('button', { name: /^endpoint-1(?!\d)/ }));
    expect(await screen.findByText('No matching keys')).toBeVisible();
    const navigations = screen.getAllByRole('navigation', { name: 'Pagination' });
    expect(navigations).toHaveLength(2);
    const navigation = navigations[1]!;
    expect(navigation).toHaveTextContent('Page 1 of 1 · 0 items');
    expect(within(navigation).getByRole('combobox')).toHaveValue('20');
    expect(within(navigation).getByRole('button', { name: 'Previous' })).toBeDisabled();
    expect(within(navigation).getByRole('button', { name: 'Next' })).toBeDisabled();
    expect(fetchMock.mock.calls.map(([input]) => String(input))).toEqual(
      expect.arrayContaining([endpointPath(), keyPath('1')]),
    );
  });

  it('keeps complete selections across three endpoint pages and two key pages', async () => {
    const endpointsPageOne = [
      endpointWire('1', { key_count: '21' }),
      ...Array.from({ length: 19 }, (_, index) => endpointWire(String(index + 10))),
    ];
    const endpointsPageTwo = [
      endpointWire('2'),
      ...Array.from({ length: 19 }, (_, index) => endpointWire(String(index + 30))),
    ];
    const endpointsPageThree = [endpointWire('3')];
    const firstKeys = Array.from({ length: 20 }, (_, index) =>
      keyWire('1', index === 0 ? '1' : String(100 + index)),
    );
    const secondKeys = [keyWire('1', '21', 'eligible', { note: 'key-21' })];
    const fetchMock = installJsonFetchFixtures([
      {
        method: 'GET',
        path: endpointPath(),
        body: numberedPage(endpointsPageOne, '1', 41),
      },
      {
        method: 'GET',
        path: endpointPath('2'),
        body: numberedPage(endpointsPageTwo, '2', 41),
      },
      {
        method: 'GET',
        path: endpointPath('3'),
        body: numberedPage(endpointsPageThree, '3', 41),
      },
      {
        method: 'GET',
        path: '/api/endpoints/1',
        body: endpointWire('1', { key_count: '21' }),
      },
      {
        method: 'GET',
        path: keyPath('1'),
        body: numberedPage(firstKeys, '1', 21),
      },
      {
        method: 'GET',
        path: keyPath('1', '2'),
        body: numberedPage(secondKeys, '2', 21),
      },
      {
        method: 'GET',
        path: keyPath('2'),
        body: numberedPage([keyWire('2', '22')], '1', 1),
      },
    ]);
    const rendered = await renderWithProviders(
      <SeedSession session={sessionA}>
        <ControlledPicker />
      </SeedSession>,
      { station: 'user', role: 'user' },
    );

    await rendered.user.click(await screen.findByRole('button', { name: /^endpoint-1(?!\d)/ }));
    await waitFor(() =>
      expect(screen.getAllByRole('checkbox', { name: /key-1/ }).length).toBeGreaterThan(0),
    );
    await rendered.user.click(screen.getAllByRole('checkbox', { name: /key-1/ })[0]!);
    const keyNavigations = screen.getAllByRole('navigation', { name: 'Pagination' });
    await rendered.user.click(within(keyNavigations[1]!).getByRole('button', { name: 'Next' }));
    await rendered.user.click(await screen.findByRole('checkbox', { name: /key-21/ }));

    await rendered.user.click(screen.getByRole('button', { name: 'Back to endpoints' }));
    const endpointNavigations = screen.getAllByRole('navigation', { name: 'Pagination' });
    await rendered.user.click(
      within(endpointNavigations[0]!).getByRole('button', { name: 'Next' }),
    );
    await rendered.user.click(await screen.findByRole('button', { name: /^endpoint-2(?!\d)/ }));
    await rendered.user.click(await screen.findByRole('checkbox', { name: /key-22/ }));

    expect(screen.getByTestId('selected-ids')).toHaveTextContent('1:1,1:21,2:22');
    await rendered.user.click(screen.getByRole('button', { name: 'Back to endpoints' }));
    const pageTwoNavigation = screen.getAllByRole('navigation', { name: 'Pagination' })[0]!;
    await rendered.user.click(within(pageTwoNavigation).getByRole('button', { name: 'Next' }));
    expect(await screen.findByRole('button', { name: /^endpoint-3(?!\d)/ })).toBeVisible();
    expect(screen.getByTestId('selected-ids')).toHaveTextContent('1:1,1:21,2:22');

    const endpointRequests = fetchMock.mock.calls
      .map(([input]) => String(input))
      .filter((path) => path.includes('/api/endpoints?'));
    expect(
      endpointRequests.every((path) => !path.includes('cursor') && !path.includes('limit')),
    ).toBe(true);
  });

  it('writes both search forms to URL, resets their page, and restores a POP page', async () => {
    const fetchMock = installJsonFetchFixtures([
      {
        method: 'GET',
        path: endpointPath('2'),
        body: numberedPage([endpointWire('2')], '2', 21),
      },
      {
        method: 'GET',
        path: endpointPath('1', 20, 'needle'),
        body: numberedPage([endpointWire('1', { note: 'needle endpoint' })], '1', 1),
      },
      {
        method: 'GET',
        path: endpointPath('1'),
        body: numberedPage(
          [
            endpointWire('1'),
            ...Array.from({ length: 19 }, (_, index) => endpointWire(String(index + 10))),
          ],
          '1',
          21,
        ),
      },
      {
        method: 'GET',
        path: keyPath('1'),
        body: numberedPage([keyWire('1', '1')], '1', 1),
      },
      {
        method: 'GET',
        path: keyPath('1', '1', 20, 'masked'),
        body: numberedPage([keyWire('1', '1')], '1', 1, 20),
      },
    ]);
    const rendered = await renderWithProviders(
      <SeedSession session={sessionA}>
        <ControlledPicker />
        <LocationProbe />
      </SeedSession>,
      { station: 'user', role: 'user', route: '/?endpoint_page=2' },
    );

    expect(await screen.findByRole('button', { name: /^endpoint-2(?!\d)/ })).toBeVisible();
    const endpointSearch = screen.getByRole('searchbox', { name: 'Search endpoints' });
    await rendered.user.type(endpointSearch, 'needle');
    await rendered.user.click(screen.getAllByRole('button', { name: 'Search' })[0]!);
    expect(await screen.findByTestId('location-search')).toHaveTextContent(
      'endpoint_q=needle&endpoint_page=1',
    );
    expect(await screen.findByRole('button', { name: /needle endpoint/ })).toBeVisible();

    await rendered.user.click(screen.getByRole('button', { name: 'pop back' }));
    expect(await screen.findByRole('button', { name: /^endpoint-2(?!\d)/ })).toBeVisible();
    expect(screen.getByRole('searchbox', { name: 'Search endpoints' })).toHaveValue('');
    // The control does not fan out key requests for endpoints that have not
    // been selected.
    expect(
      fetchMock.mock.calls.some(([input]) => String(input).includes('/api/endpoints/2/keys')),
    ).toBe(false);
    const endpointNavigation = screen.getAllByRole('navigation', { name: 'Pagination' })[0]!;
    await rendered.user.click(within(endpointNavigation).getByRole('button', { name: 'Previous' }));
    await rendered.user.click(screen.getByRole('button', { name: /^endpoint-1(?!\d)/ }));
    await waitFor(() =>
      expect(screen.getAllByRole('checkbox', { name: /key-1/ }).length).toBeGreaterThan(0),
    );
    const keySearch = screen.getByRole('searchbox', { name: 'Search keys' });
    await rendered.user.type(keySearch, 'masked');
    await rendered.user.click(screen.getAllByRole('button', { name: 'Search' })[1]!);
    expect(await screen.findByTestId('location-search')).toHaveTextContent(
      'endpoint_id=1&key_q=masked&key_page=1',
    );
    expect(fetchMock.mock.calls.some(([input]) => String(input).includes('q=masked'))).toBe(true);
  });

  it('restores endpoint and key pages together when a nested view is popped', async () => {
    const fetchMock = installJsonFetchFixtures([
      {
        method: 'GET',
        path: endpointPath(),
        body: numberedPage(
          [
            endpointWire('1'),
            ...Array.from({ length: 19 }, (_, index) => endpointWire(String(index + 10))),
          ],
          '1',
          21,
        ),
      },
      {
        method: 'GET',
        path: endpointPath('2'),
        body: numberedPage([endpointWire('1', { note: 'endpoint-1-page-2' })], '2', 21),
      },
      {
        method: 'GET',
        path: keyPath('1'),
        body: numberedPage(
          Array.from({ length: 20 }, (_, index) =>
            keyWire('1', String(index + 1), 'eligible', index === 0 ? { note: 'pop-key-one' } : {}),
          ),
          '1',
          21,
        ),
      },
      {
        method: 'GET',
        path: keyPath('1', '2'),
        body: numberedPage([keyWire('1', '21', 'eligible', { note: 'pop-key-two' })], '2', 21),
      },
    ]);
    const rendered = await renderWithProviders(
      <SeedSession session={sessionA}>
        <ControlledPicker />
        <LocationProbe />
      </SeedSession>,
      {
        station: 'user',
        role: 'user',
        route: '/?endpoint_page=1&endpoint_id=1&key_page=1',
      },
    );

    expect(await screen.findByRole('checkbox', { name: /pop-key-one/ })).toBeVisible();
    await rendered.user.click(screen.getByRole('button', { name: 'open nested page' }));
    expect(await screen.findByRole('checkbox', { name: /pop-key-two/ })).toBeVisible();
    expect(screen.getByRole('button', { name: /endpoint-1-page-2/ })).toBeVisible();

    await rendered.user.click(screen.getByRole('button', { name: 'pop back' }));
    expect(await screen.findByRole('checkbox', { name: /pop-key-one/ })).toBeVisible();
    expect(screen.getByRole('button', { name: /^endpoint-1(?!\d)/ })).toBeVisible();
    expect(fetchMock.mock.calls.map(([input]) => String(input))).toEqual(
      expect.arrayContaining([endpointPath(), endpointPath('2'), keyPath('1'), keyPath('1', '2')]),
    );
  });

  it('rejects a detail response whose id does not match the requested endpoint', async () => {
    const fetchMock = installJsonFetchFixtures([
      {
        method: 'GET',
        path: endpointPath(),
        body: numberedPage([endpointWire('2')], '1', 1),
      },
      {
        method: 'GET',
        path: '/api/endpoints/1',
        body: endpointWire('2'),
      },
    ]);
    await renderWithProviders(
      <SeedSession session={sessionA}>
        <ControlledPicker />
      </SeedSession>,
      { station: 'user', role: 'user', route: '/?endpoint_id=1' },
    );

    expect(await screen.findByRole('button', { name: 'Retry' })).toBeVisible();
    expect(screen.queryByRole('checkbox')).toBeNull();
    expect(fetchMock.mock.calls.map(([input]) => String(input))).toEqual(
      expect.arrayContaining([endpointPath(), '/api/endpoints/1']),
    );
  });

  it('drops a stale detail error once the requested endpoint appears on the current page', async () => {
    const firstEndpointPage = [
      endpointWire('2'),
      ...Array.from({ length: 19 }, (_, index) => endpointWire(String(index + 10))),
    ];
    const fetchMock = installPickerServer((url) => {
      if (url.pathname === '/api/endpoints' && url.search === '?page=1&page_size=20') {
        return jsonResponse(numberedPage(firstEndpointPage, '1', 21));
      }
      if (url.pathname === '/api/endpoints' && url.search === '?page=2&page_size=20') {
        return jsonResponse(numberedPage([endpointWire('1')], '2', 21));
      }
      if (url.pathname === '/api/endpoints/1') {
        return jsonResponse(
          { error: { code: 'service_unavailable', message: 'retry later' } },
          503,
        );
      }
      if (url.pathname === '/api/endpoints/1/keys' && url.search === '?page=1&page_size=20') {
        return jsonResponse(
          numberedPage([keyWire('1', '1', 'eligible', { note: 'detail-key' })], '1', 1),
        );
      }
      throw new Error(`Unexpected picker request: ${url.pathname}${url.search}`);
    });
    const rendered = await renderWithProviders(
      <SeedSession session={sessionA}>
        <ControlledPicker />
      </SeedSession>,
      { station: 'user', role: 'user', route: '/?endpoint_id=1' },
    );

    expect(await screen.findByRole('button', { name: 'Retry' })).toBeVisible();
    const endpointNavigation = screen.getAllByRole('navigation', { name: 'Pagination' })[0]!;
    await rendered.user.click(within(endpointNavigation).getByRole('button', { name: 'Next' }));
    expect(await screen.findByRole('checkbox', { name: /detail-key/ })).toBeVisible();
    expect(screen.queryByRole('button', { name: 'Retry' })).toBeNull();
    expect(fetchMock).toHaveBeenCalled();
  });

  it('allows 128 non-BMP search scalars and rejects the next scalar without truncating', async () => {
    const fetchMock = installPickerServer((url) => {
      if (url.pathname === '/api/endpoints') {
        return jsonResponse(numberedPage([endpointWire('1')], '1', 1));
      }
      throw new Error(`Unexpected picker request: ${url.pathname}${url.search}`);
    });
    const rendered = await renderWithProviders(
      <SeedSession session={sessionA}>
        <ControlledPicker />
      </SeedSession>,
      { station: 'user', role: 'user' },
    );

    expect(await screen.findByRole('button', { name: /^endpoint-1(?!\d)/ })).toBeVisible();
    const endpointSearch = screen.getByRole('searchbox', { name: 'Search endpoints' });
    const withinLimit = '😀'.repeat(128);
    fireEvent.change(endpointSearch, { target: { value: withinLimit } });
    expect(endpointSearch).toHaveValue(withinLimit);
    await rendered.user.click(screen.getByRole('button', { name: 'Search' }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));

    const overLimit = '😀'.repeat(129);
    fireEvent.change(endpointSearch, { target: { value: overLimit } });
    expect(endpointSearch).toHaveValue(overLimit);
    await rendered.user.click(screen.getByRole('button', { name: 'Search' }));
    expect(screen.getByText(/at most 128 Unicode characters/i)).toBeVisible();
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it('offers retry after a cached endpoint detail fails to refresh', async () => {
    let detailReads = 0;
    const readStates: boolean[] = [];
    installPickerServer((url) => {
      if (url.pathname === '/api/endpoints')
        return jsonResponse(numberedPage([endpointWire('2')], '1', 1));
      if (url.pathname === '/api/endpoints/1') {
        detailReads += 1;
        return detailReads === 2
          ? jsonResponse(
              { error: { code: 'temporarily_unavailable', message: 'retry later' } },
              503,
            )
          : jsonResponse(endpointWire('1'));
      }
      if (url.pathname === '/api/endpoints/1/keys')
        return jsonResponse(numberedPage([keyWire('1', '101')], '1', 1));
      throw new Error(`Unexpected picker request: ${url.pathname}${url.search}`);
    });
    const rendered = await renderWithProviders(
      <SeedSession session={sessionA}>
        <ControlledPicker onReadStateChange={(blocked) => readStates.push(blocked)} />
      </SeedSession>,
      { station: 'user', role: 'user', route: '/?endpoint_id=1' },
    );
    await rendered.user.click(await screen.findByRole('checkbox', { name: /key-101/ }));
    await waitFor(() => expect(readStates.at(-1)).toBe(false));
    await rendered.queryClient.invalidateQueries({
      queryKey: [...economyKeys.endpointChoicesRoot, ACCOUNT_A, 'endpoint-detail', '1'],
      exact: true,
    });
    const retry = await screen.findByRole('button', { name: 'Retry' });
    expect(readStates.at(-1)).toBe(true);
    expect(screen.getByTestId('selected-ids')).toHaveTextContent('1:101');
    await rendered.user.click(retry);
    await waitFor(() => expect(readStates.at(-1)).toBe(false));
    expect(await screen.findByRole('checkbox', { name: /key-101/ })).toBeChecked();
    expect(detailReads).toBe(3);
  });

  it.each(['endpoint', 'key'] as const)(
    'does not reuse rows from another %s search while the new response is pending',
    async (kind) => {
      let release: ((response: Response) => void) | undefined;
      installPickerServer((url) => {
        if (url.searchParams.get('q') === 'new-filter')
          return new Promise<Response>((resolve) => {
            release = resolve;
          });
        if (url.pathname === '/api/endpoints')
          return jsonResponse(numberedPage([endpointWire('1')], '1', 1));
        if (url.pathname === '/api/endpoints/1/keys')
          return jsonResponse(numberedPage([keyWire('1', '101')], '1', 1));
        throw new Error(`Unexpected picker request: ${url.pathname}${url.search}`);
      });
      const rendered = await renderWithProviders(
        <SeedSession session={sessionA}>
          <ControlledPicker />
        </SeedSession>,
        { station: 'user', role: 'user' },
      );
      const endpointButton = await screen.findByRole('button', { name: /^endpoint-1(?!\d)/ });
      if (kind === 'key') {
        await rendered.user.click(endpointButton);
        await screen.findByRole('checkbox', { name: /key-101/ });
      }
      const input = screen.getByRole('searchbox', {
        name: kind === 'endpoint' ? 'Search endpoints' : 'Search keys',
      });
      await rendered.user.type(input, 'new-filter');
      await rendered.user.click(
        within(input.closest('form')!).getByRole('button', { name: 'Search' }),
      );
      await waitFor(() => expect(release).toBeDefined());
      if (kind === 'endpoint')
        expect(screen.queryByRole('button', { name: /^endpoint-1(?!\d)/ })).toBeNull();
      else expect(screen.queryByRole('checkbox', { name: /key-101/ })).toBeNull();
      release!(jsonResponse(numberedPage([], '1', 0)));
      expect(
        await screen.findByText(kind === 'endpoint' ? 'No matching endpoints' : 'No matching keys'),
      ).toBeVisible();
    },
  );

  it('keeps old rows disabled during a background page read and offers retry after 503', async () => {
    let releaseSecondPage: ((response: Response) => void) | undefined;
    let keyPageCalls = 0;
    const fetchMock = installPickerServer((url) => {
      if (url.pathname === '/api/endpoints' && url.search === '?page=1&page_size=20') {
        return jsonResponse(numberedPage([endpointWire('1')], '1', 1));
      }
      if (url.pathname === '/api/endpoints/1/keys' && url.search === '?page=1&page_size=20') {
        keyPageCalls += 1;
        return keyPageCalls === 1
          ? jsonResponse({ error: { code: 'service_unavailable', message: 'retry later' } }, 503)
          : jsonResponse(
              numberedPage(
                Array.from({ length: 20 }, (_, index) => keyWire('1', String(index + 1))),
                '1',
                21,
              ),
            );
      }
      if (url.pathname === '/api/endpoints/1/keys' && url.search === '?page=2&page_size=20') {
        return new Promise<Response>((resolve) => {
          releaseSecondPage = resolve;
        });
      }
      throw new Error(`Unexpected picker request: ${url.pathname}${url.search}`);
    });
    const initialSelected = [selectedChoice('1', 'old')];
    const rendered = await renderWithProviders(
      <SeedSession session={sessionA}>
        <ControlledPicker initialSelected={initialSelected} />
      </SeedSession>,
      { station: 'user', role: 'user' },
    );

    await rendered.user.click(await screen.findByRole('button', { name: /^endpoint-1(?!\d)/ }));
    const retry = await screen.findByRole('button', { name: 'Retry' });
    expect(screen.getByTestId('selected-ids')).toHaveTextContent('1:old');
    await rendered.user.click(retry);
    await waitFor(() =>
      expect(screen.getAllByRole('checkbox', { name: /key-1/ }).length).toBeGreaterThan(0),
    );

    const keyNavigation = screen.getAllByRole('navigation', { name: 'Pagination' })[1]!;
    await rendered.user.click(within(keyNavigation).getByRole('button', { name: 'Next' }));
    await waitFor(() => expect(releaseSecondPage).toBeDefined());
    const oldRow = screen.getAllByRole('checkbox', { name: /key-1/ })[0]!;
    expect(oldRow).toBeDisabled();
    releaseSecondPage?.(jsonResponse(numberedPage([keyWire('1', '2')], '2', 21)));
    expect(await screen.findByRole('checkbox', { name: /key-2/ })).toBeEnabled();
    expect(fetchMock).toHaveBeenCalled();
  });

  it('uses page-only eligibility, leaves physical disablement selectable, and enforces 100', async () => {
    const choices = Array.from({ length: 100 }, (_, index) =>
      selectedChoice('1', `selected-${index + 1}`),
    );
    installJsonFetchFixtures([
      {
        method: 'GET',
        path: endpointPath(),
        body: numberedPage([endpointWire('1', { key_count: '4' })], '1', 1),
      },
      {
        method: 'GET',
        path: keyPath('1'),
        body: numberedPage(
          [
            keyWire('1', '101', 'eligible', { note: 'key-eligible' }),
            keyWire('1', '102', 'already_donated', { note: 'key-already' }),
            keyWire('1', '103', 'security_processing', { note: 'key-security' }),
            keyWire('1', '104', 'eligible', { note: 'key-physical', enabled: false }),
          ],
          '1',
          4,
        ),
      },
    ]);
    const rendered = await renderWithProviders(
      <SeedSession session={sessionA}>
        <ControlledPicker initialSelected={choices} />
      </SeedSession>,
      { station: 'user', role: 'user' },
    );

    await rendered.user.click(await screen.findByRole('button', { name: /^endpoint-1(?!\d)/ }));
    const eligible = await screen.findByRole('checkbox', { name: /key-eligible/ });
    expect(eligible).toBeDisabled();
    expect(screen.getByRole('checkbox', { name: /key-already/ })).toBeDisabled();
    expect(screen.getByRole('checkbox', { name: /key-security/ })).toBeDisabled();
    expect(screen.getByRole('checkbox', { name: /key-physical/ })).toBeDisabled();
    expect(screen.getByText(/physically disabled/i)).toBeVisible();
    expect(screen.getByText(/Already included in another donation/)).toBeVisible();
    expect(screen.getByText(/Security processing is still in progress/)).toBeVisible();

    await rendered.user.click(screen.getAllByRole('button', { name: 'Remove' })[0]!);
    expect(await screen.findByText('99 / 100 keys selected')).toBeVisible();
    expect(await screen.findByRole('checkbox', { name: /key-eligible/ })).toBeEnabled();
  });

  it('merges fresh key eligibility and revisions into the selected choice', async () => {
    const selectedWithExpiry = {
      ...selectedChoice('1', '101'),
      expiresAt: 1_700_000_123,
    } as EndpointKeyChoice & { expiresAt: number };
    const fetchMock = installJsonFetchFixtures([
      {
        method: 'GET',
        path: endpointPath(),
        body: numberedPage([endpointWire('1', { revision: '3', key_count: '2' })], '1', 1),
      },
      {
        method: 'GET',
        path: keyPath('1'),
        body: numberedPage(
          [
            keyWire('1', '101', 'already_donated', { revision: '2' }),
            ...Array.from({ length: 19 }, (_, index) => keyWire('1', String(index + 102))),
          ],
          '1',
          21,
        ),
      },
      {
        method: 'GET',
        path: keyPath('1', '2'),
        body: numberedPage([keyWire('1', '121')], '2', 21),
      },
    ]);
    const rendered = await renderWithProviders(
      <SeedSession session={sessionA}>
        <ControlledPicker
          initialSelected={[selectedWithExpiry, selectedChoice('1', '999')]}
          renderSelected={(choice) => (
            <output data-testid={`merged-${choice.key.id}`}>
              {choice.eligibility}:{choice.key.revision}:
              {(choice as EndpointKeyChoice & { expiresAt?: number }).expiresAt ?? ''}
            </output>
          )}
        />
      </SeedSession>,
      { station: 'user', role: 'user' },
    );

    await rendered.user.click(await screen.findByRole('button', { name: /^endpoint-1(?!\d)/ }));
    await waitFor(() =>
      expect(screen.getByTestId('merged-101')).toHaveTextContent('already_donated:2:1700000123'),
    );
    expect(screen.getByTestId('selected-ids')).toHaveTextContent('1:101,1:999');
    expect(screen.getByText(/no longer eligible/i)).toBeVisible();
    expect(fetchMock).toHaveBeenCalledTimes(2);
    const keyPagination = screen.getAllByRole('navigation', { name: 'Pagination' })[1]!;
    await rendered.user.click(within(keyPagination).getByRole('button', { name: 'Next' }));
    await screen.findByRole('checkbox', { name: /key-121/ });
    expect(screen.getByTestId('merged-101')).toHaveTextContent('already_donated:2:1700000123');
    expect(screen.getByText(/no longer eligible/i)).toBeVisible();
  });

  it('keeps selected resources paginated and pauses reads when disabled by the parent tab', async () => {
    const choices = Array.from({ length: 21 }, (_, index) =>
      selectedChoice('1', `selected-${index + 1}`),
    );
    const fetchMock = installJsonFetchFixtures([
      {
        method: 'GET',
        path: endpointPath(),
        body: numberedPage([endpointWire('1')], '1', 1),
      },
    ]);

    function EnabledHarness() {
      const [enabled, setEnabled] = useState(false);
      return (
        <>
          <button type="button" onClick={() => setEnabled(true)}>
            enable picker
          </button>
          <ControlledPicker
            initialSelected={choices}
            enabled={enabled}
            renderSelected={(choice) => (
              <output data-testid={`selected-slot-${choice.key.id}`}>{choice.key.id}</output>
            )}
          />
        </>
      );
    }

    const rendered = await renderWithProviders(
      <SeedSession session={sessionA}>
        <EnabledHarness />
      </SeedSession>,
      { station: 'user', role: 'user' },
    );

    expect(fetchMock).not.toHaveBeenCalled();
    expect(screen.getByText('21 / 100 keys selected')).toBeVisible();
    expect(screen.getByTestId('selected-slot-selected-1')).toBeVisible();
    const selectedRegion = screen.getByRole('region', { name: 'Selected resources' });
    const selectedNavigation = within(selectedRegion).getByRole('navigation', {
      name: 'Pagination',
    });
    expect(within(selectedNavigation).getByRole('button', { name: 'Next' })).toBeDisabled();

    await rendered.user.click(screen.getByRole('button', { name: 'enable picker' }));
    expect(await screen.findByRole('button', { name: /^endpoint-1(?!\d)/ })).toBeVisible();
    expect(fetchMock).toHaveBeenCalledTimes(1);
    await rendered.user.click(within(selectedNavigation).getByRole('button', { name: 'Next' }));
    expect(await screen.findByTestId('selected-slot-selected-21')).toBeVisible();
    expect(screen.queryByTestId('selected-slot-selected-1')).toBeNull();
  });

  it('fails closed when key browse eligibility is missing and rejects invalid search input', async () => {
    const fetchMock = installJsonFetchFixtures([
      {
        method: 'GET',
        path: endpointPath(),
        body: numberedPage([endpointWire('1')], '1', 1),
      },
      {
        method: 'GET',
        path: keyPath('1'),
        body: numberedPage([keyWire('1', '1', 'eligible', {}, false)], '1', 1),
      },
    ]);
    const rendered = await renderWithProviders(
      <SeedSession session={sessionA}>
        <ControlledPicker />
      </SeedSession>,
      { station: 'user', role: 'user' },
    );
    await rendered.user.click(await screen.findByRole('button', { name: /^endpoint-1(?!\d)/ }));
    expect(await screen.findByRole('button', { name: 'Retry' })).toBeVisible();
    expect(screen.queryByRole('checkbox')).toBeNull();

    const endpointSearch = screen.getByRole('searchbox', { name: 'Search endpoints' });
    fireEvent.change(endpointSearch, { target: { value: 'bad\u0001' } });
    await rendered.user.click(screen.getAllByRole('button', { name: 'Search' })[0]!);
    expect(screen.getByText(/control characters/i)).toBeVisible();
    expect(fetchMock.mock.calls.filter(([input]) => String(input).includes('q=bad'))).toHaveLength(
      0,
    );
  });

  it.each([401, 403])('clears private rows and identity after endpoint %s', async (status) => {
    const fetchMock = installPickerServer((url) => {
      if (url.pathname === '/api/endpoints') {
        return jsonResponse(
          { error: { code: status === 401 ? 'unauthorized' : 'forbidden', message: 'closed' } },
          status,
        );
      }
      throw new Error(`Unexpected picker request: ${url.pathname}${url.search}`);
    });
    const selected = [selectedChoice('1', '1')];
    const rendered = await renderWithProviders(
      <SeedSession session={sessionA}>
        <ControlledPicker initialSelected={selected} />
      </SeedSession>,
      { station: 'user', role: 'user' },
    );

    await waitFor(() => expect(rendered.queryClient.getQueryData(['user', 'session'])).toBeNull());
    expect(screen.getByText('0 / 100 keys selected')).toBeVisible();
    expect(screen.getByText('No resources selected yet.')).toBeVisible();
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it('does not let a late old-account 401 clear the new account', async () => {
    let releaseOld: ((response: Response) => void) | undefined;
    let endpointCalls = 0;
    const fetchMock = installPickerServer((url) => {
      if (url.pathname === '/api/endpoints' && url.search === '?page=1&page_size=20') {
        endpointCalls += 1;
        if (endpointCalls === 1) {
          return new Promise<Response>((resolve) => {
            releaseOld = resolve;
          });
        }
        return jsonResponse(numberedPage([endpointWire('6')], '1', 1));
      }
      throw new Error(`Unexpected picker request: ${url.pathname}${url.search}`);
    });

    function AccountSwitchHarness() {
      const [accountId, setAccountId] = useState(ACCOUNT_A);
      const currentSession = accountId === ACCOUNT_A ? sessionA : sessionB;
      return (
        <SeedSession session={currentSession}>
          <button type="button" onClick={() => setAccountId(ACCOUNT_B)}>
            switch account
          </button>
          <ControlledPicker accountId={accountId} />
        </SeedSession>
      );
    }

    const rendered = await renderWithProviders(<AccountSwitchHarness />, {
      station: 'user',
      role: 'user',
    });
    await waitFor(() => expect(releaseOld).toBeDefined());
    await rendered.user.click(screen.getByRole('button', { name: 'switch account' }));
    // Account B gets a fresh request after the session authority changes.
    await waitFor(() => {
      const requests = fetchMock.mock.calls.map(([input]) => String(input));
      expect(requests.length).toBeGreaterThanOrEqual(2);
    });
    releaseOld?.(jsonResponse({ error: { code: 'unauthorized', message: 'old account' } }, 401));
    expect(await screen.findByRole('button', { name: /^endpoint-6(?!\d)/ })).toBeVisible();
    expect(rendered.queryClient.getQueryData(['user', 'session'])).toEqual(sessionB);
    expect(fetchMock).toHaveBeenCalled();
  });

  it('reports its own read state but not the parent disabled prop', async () => {
    const readStates: boolean[] = [];
    installJsonFetchFixtures([
      {
        method: 'GET',
        path: endpointPath(),
        body: numberedPage([endpointWire('1')], '1', 1),
      },
    ]);
    const rendered = await renderWithProviders(
      <SeedSession session={sessionA}>
        <ControlledPicker disabled onReadStateChange={(blocked) => readStates.push(blocked)} />
      </SeedSession>,
      { station: 'user', role: 'user' },
    );
    expect(await screen.findByRole('button', { name: /^endpoint-1(?!\d)/ })).toBeDisabled();
    await waitFor(() => expect(readStates.at(-1)).toBe(false));
    expect(readStates.at(-1)).toBe(false);
    expect(rendered.container).toHaveTextContent('0 / 100 keys selected');
  });
});
