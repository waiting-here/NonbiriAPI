import { useLocation } from 'react-router';
import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { CharitySourceBrowser } from './CharitySourceBrowser';

const sourceA = `dsg_${'A'.repeat(43)}`;
const sourceB = `dsg_${'B'.repeat(42)}A`;
const ruleA = `qlr_${'C'.repeat(21)}A`;

function jsonResponse(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function sourceSummary(sourceKey: string, index = 0) {
  return {
    source_key: sourceKey,
    safe_source: {
      kind: 'custom',
      connector_type: 'openai-compatible',
      base_url: `https://source-${index}.example.test/v1`,
    },
    donation_count: '1',
    key_count: '1',
    usable_key_count: '1',
    pending_donation_count: '1',
  };
}

function keySummary({
  id = '11',
  donationId = '7',
  sourceIndex = 0,
  idle = false,
}: {
  id?: string;
  donationId?: string;
  sourceIndex?: number;
  idle?: boolean;
} = {}) {
  return {
    binding_count: idle ? '0' : '2',
    idle,
    id,
    endpoint_key_id: id,
    display_head: `sk-${id}`,
    display_tail: 'tail',
    safe_source: {
      kind: 'custom',
      connector_type: 'openai-compatible',
      base_url: `https://source-${sourceIndex}.example.test/v1`,
    },
    physical_enabled: true,
    charity_state: 'available',
    limits: { price: '12.5', calls: '100', tokens: '1000' },
    usage: {
      price_used: '1.5',
      price_inflight: '0',
      calls_used: '2',
      calls_inflight: '1',
      tokens_used: '20',
      tokens_inflight: '5',
    },
    token_reserve: 32,
    authorized_expires_at: null,
    expires_at: 1_900_000_000,
    failure_disable_threshold: '10',
    streak: { generation: '1', count: '0', failure_disabled: false },
    ended_reason: null,
    safe_note: 'safe note',
    max_concurrency: 2,
    max_rpm: 30,
    donation_id: donationId,
    key_id: id,
    donation_revision: '2',
    rule_count: '1',
    rules: [
      {
        id: ruleA,
        mode: 'reset',
        interval: 'day',
        alignment: 'first_success',
        time_zone: 'UTC',
        week_starts_on: null,
        metric: 'calls',
        limit: '100',
        used: '2',
        reserved: '1',
        remaining: '97',
        state: 'available',
        period_start: null,
        period_end: null,
        next_transition_at: null,
      },
    ],
    handling: {
      state: 'pending',
      revision: '1',
      processed_at: null,
      processed_by_role: null,
      closed_at: null,
      closed_reason: null,
    },
  };
}

function page<T>(data: T[], pageNumber = '1', pageSize = 20, total = data.length) {
  const totalPages = total === 0 ? 1 : Math.ceil(total / pageSize);
  return {
    data,
    next_cursor: null,
    pagination: {
      page: pageNumber,
      page_size: pageSize,
      total_items: String(total),
      total_pages: String(totalPages),
    },
  };
}

function requestURL(input: string | URL | Request): URL {
  return new URL(input instanceof Request ? input.url : String(input), window.location.origin);
}

function sourceListForPage(pageNumber: string, pageSize: number, many = false) {
  if (!many) return page([sourceSummary(sourceA)], pageNumber, pageSize);
  if (pageNumber === '2') return page([sourceSummary(sourceB, 1)], pageNumber, pageSize, 21);
  return page(
    Array.from({ length: pageSize }, (_, index) =>
      sourceSummary(index === 0 ? sourceA : `dsg_${String(index).padStart(42, 'D')}A`, index),
    ),
    pageNumber,
    pageSize,
    21,
  );
}

function sourceKeyListForPage(pageNumber: string, pageSize: number, many = false, idle = false) {
  if (!many) return page([keySummary({ idle })], pageNumber, pageSize);
  if (pageNumber === '2') return page([keySummary({ id: '31', idle })], pageNumber, pageSize, 21);
  return page(
    Array.from({ length: pageSize }, (_, index) => keySummary({ id: String(11 + index), idle })),
    pageNumber,
    pageSize,
    21,
  );
}

function installFetch({
  role = 'admin',
  manySources = false,
  manyKeys = false,
  sourceStatus = 200,
  keyStatus = 200,
}: {
  role?: 'admin' | 'steward';
  manySources?: boolean;
  manyKeys?: boolean;
  sourceStatus?: number;
  keyStatus?: number;
} = {}) {
  const requests: URL[] = [];
  const prefix = role === 'admin' ? '/admin/api' : '/api/steward';
  const fetchMock = vi.fn<typeof fetch>(async (input, init) => {
    const url = requestURL(input);
    requests.push(url);
    if (url.pathname === `${prefix}/donation-sources`) {
      if (sourceStatus !== 200) return jsonResponse({}, sourceStatus);
      const pageNumber = url.searchParams.get('page') ?? '1';
      const pageSize = Number(url.searchParams.get('page_size') ?? '20');
      return jsonResponse(sourceListForPage(pageNumber, pageSize, manySources));
    }
    if (url.pathname.startsWith(`${prefix}/donation-sources/`) && url.pathname.endsWith('/keys')) {
      if (keyStatus !== 200) return jsonResponse({}, keyStatus);
      const pageNumber = url.searchParams.get('page') ?? '1';
      const pageSize = Number(url.searchParams.get('page_size') ?? '20');
      const idle = url.searchParams.get('idle') === 'yes';
      return jsonResponse(sourceKeyListForPage(pageNumber, pageSize, manyKeys, idle));
    }
    throw new Error(`Unexpected request: ${init?.method ?? 'GET'} ${url.pathname}${url.search}`);
  });
  vi.stubGlobal('fetch', fetchMock);
  return { fetchMock, requests };
}

function sourcePane(container: HTMLElement): HTMLElement {
  return container.querySelector('.charity-source-browser__sources') as HTMLElement;
}

function keyPane(container: HTMLElement): HTMLElement {
  return container.querySelector('.charity-source-browser__keys') as HTMLElement;
}

async function chooseSource(
  view: { user: { click: (element: Element) => Promise<void> } },
  container: HTMLElement,
) {
  const source = await within(sourcePane(container)).findByRole('button', {
    name: /Custom endpoint: https:\/\/source-0\.example\.test\/v1/,
  });
  await view.user.click(source);
  await within(keyPane(container)).findByText(/Key · sk-11/);
}

async function sourceButton(container: HTMLElement, index: number) {
  return within(sourcePane(container)).findByRole('button', {
    name: new RegExp(`Custom endpoint: https://source-${index}\\.example\\.test/v1`),
  });
}

function LocationProbe() {
  const location = useLocation();
  return <output data-testid="location">{location.search}</output>;
}

function BrowserHarness(props: React.ComponentProps<typeof CharitySourceBrowser>) {
  return (
    <>
      <CharitySourceBrowser {...props} />
      <LocationProbe />
    </>
  );
}

function currentSearch(): string {
  return screen.getByTestId('location').textContent ?? '';
}

describe('CharitySourceBrowser', () => {
  it('reads the default active source page and opens a real source-key page', async () => {
    const { requests } = installFetch();
    const onOpenDonation = vi.fn();
    const view = await renderWithProviders(
      <BrowserHarness role="admin" accountId="admin:one" enabled onOpenDonation={onOpenDonation} />,
      { station: 'admin', role: 'admin', route: '/charity' },
    );

    await within(sourcePane(view.container)).findByRole('button', {
      name: /Custom endpoint: https:\/\/source-0\.example\.test\/v1/,
    });
    const sourceRequest = requests.find(
      (request) => request.pathname === '/admin/api/donation-sources',
    );
    expect(sourceRequest?.searchParams.get('scope')).toBe('active');
    expect(sourceRequest?.searchParams.get('page')).toBe('1');
    expect(sourceRequest?.searchParams.get('page_size')).toBe('20');
    expect(sourceRequest?.searchParams.has('limit')).toBe(false);

    await chooseSource(view, view.container);
    const keyRequest = requests.find((request) => request.pathname.endsWith('/keys'));
    expect(keyRequest?.searchParams.get('scope')).toBe('active');
    expect(keyRequest?.searchParams.get('page')).toBe('1');
    expect(keyRequest?.searchParams.get('page_size')).toBe('20');
    expect(within(keyPane(view.container)).queryByText('safe note')).not.toBeInTheDocument();
    await view.user.click(
      within(keyPane(view.container)).getByRole('button', { name: /Manage donation #7/ }),
    );
    expect(onOpenDonation).toHaveBeenCalledWith('7');
    await view.user.click(
      within(keyPane(view.container)).getByRole('button', { name: /Manage key #11/ }),
    );
    expect(onOpenDonation).toHaveBeenCalledWith('7', '11');
  });

  it('submits and clears source search through the visible controls', async () => {
    const { requests } = installFetch();
    const view = await renderWithProviders(
      <BrowserHarness role="admin" accountId="admin:one" enabled onOpenDonation={vi.fn()} />,
      { station: 'admin', role: 'admin', route: '/charity' },
    );
    const pane = sourcePane(view.container);
    await within(pane).findByRole('button', {
      name: /Custom endpoint: https:\/\/source-0\.example\.test\/v1/,
    });
    const input = within(view.container).getByRole('searchbox', { name: 'Search sources' });
    await view.user.type(input, 'provider');
    await view.user.click(within(view.container).getByRole('button', { name: 'Search' }));
    await waitFor(() =>
      expect(
        requests.some(
          (request) =>
            request.pathname === '/admin/api/donation-sources' &&
            request.searchParams.get('q') === 'provider',
        ),
      ).toBe(true),
    );
    expect(currentSearch()).toContain('source_q=provider');
    await view.user.click(view.getByRole('button', { name: 'Clear search' }));
    await waitFor(() => expect(currentSearch()).not.toContain('source_q=provider'));
    expect(
      requests.filter(
        (request) =>
          request.pathname === '/admin/api/donation-sources' && !request.searchParams.has('q'),
      ).length,
    ).toBeGreaterThan(0);
  });

  it('accepts 128 supplementary-plane characters within the search limits', async () => {
    const { requests } = installFetch();
    const view = await renderWithProviders(
      <BrowserHarness role="admin" accountId="admin:one" enabled onOpenDonation={vi.fn()} />,
      { station: 'admin', role: 'admin', route: '/charity' },
    );
    await within(sourcePane(view.container)).findByRole('button', {
      name: /Custom endpoint: https:\/\/source-0\.example\.test\/v1/,
    });
    const search = within(view.container).getByRole('searchbox', { name: 'Search sources' });
    const value = '😀'.repeat(128);
    fireEvent.change(search, { target: { value } });
    await view.user.click(within(view.container).getByRole('button', { name: 'Search' }));

    await waitFor(() =>
      expect(
        requests.some(
          (request) =>
            request.pathname === '/admin/api/donation-sources' &&
            request.searchParams.get('q') === value,
        ),
      ).toBe(true),
    );
    expect(Array.from(requests.at(-1)?.searchParams.get('q') ?? '')).toHaveLength(128);
    expect(new TextEncoder().encode(requests.at(-1)?.searchParams.get('q') ?? '')).toHaveLength(
      512,
    );
    expect(currentSearch()).toContain('source_q=');
  });

  it('rejects control characters and isolated surrogates without changing the committed search', async () => {
    const { requests } = installFetch();
    const view = await renderWithProviders(
      <BrowserHarness role="admin" accountId="admin:one" enabled onOpenDonation={vi.fn()} />,
      { station: 'admin', role: 'admin', route: '/charity?source_q=provider' },
    );
    await within(sourcePane(view.container)).findByRole('button', {
      name: /Custom endpoint: https:\/\/source-0\.example\.test\/v1/,
    });
    const search = within(view.container).getByRole('searchbox', { name: 'Search sources' });
    const requestCount = requests.length;
    const invalidValues = ['bad\u0001search', 'bad\u0085search', 'bad\ud800search'];

    for (const value of invalidValues) {
      fireEvent.change(search, { target: { value } });
      await view.user.click(within(view.container).getByRole('button', { name: 'Search' }));
      expect(
        await screen.findByText(
          'Use at most 128 Unicode characters and 512 UTF-8 bytes; control characters are not allowed.',
        ),
      ).toBeVisible();
      expect(requests).toHaveLength(requestCount);
      expect(currentSearch()).toContain('source_q=provider');
    }
  });

  it('keeps source and nested key pagination independent and preserves the selected source', async () => {
    const { requests } = installFetch({ manySources: true, manyKeys: true });
    const view = await renderWithProviders(
      <BrowserHarness role="admin" accountId="admin:one" enabled onOpenDonation={vi.fn()} />,
      { station: 'admin', role: 'admin', route: '/charity' },
    );
    await chooseSource(view, view.container);
    const keys = keyPane(view.container);
    await view.user.click(within(keys).getByRole('button', { name: 'Next' }));
    await within(keys).findByText(/Key · sk-31/);
    const nestedRequest = requests.filter((request) => request.pathname.endsWith('/keys')).at(-1);
    expect(nestedRequest?.searchParams.get('page')).toBe('2');
    expect(currentSearch()).toContain(`source_key=${encodeURIComponent(sourceA)}`);
    expect(currentSearch()).toContain('source_keys_page=2');
    expect(currentSearch()).not.toContain('sources_page=2');
  });

  it('restores the selected source, both page contexts, and nested filters from the URL', async () => {
    const { requests } = installFetch({ manySources: true, manyKeys: true });
    const view = await renderWithProviders(
      <BrowserHarness role="admin" accountId="admin:one" enabled onOpenDonation={vi.fn()} />,
      {
        station: 'admin',
        role: 'admin',
        route: `/charity?sources_page=1&sources_page_size=20&source_keys_page=2&source_keys_page_size=20&source_key=${sourceA}&source_scope=all&source_q=provider&source_key_q=sk&source_handling=pending&source_idle=yes`,
      },
    );
    await within(keyPane(view.container)).findByText(/Key · sk-31/);
    const sourceRequest = requests.find(
      (request) => request.pathname === '/admin/api/donation-sources',
    );
    expect(sourceRequest?.searchParams.get('scope')).toBe('all');
    expect(sourceRequest?.searchParams.get('q')).toBe('provider');
    expect(sourceRequest?.searchParams.get('handling')).toBe('pending');
    expect(sourceRequest?.searchParams.get('page')).toBe('1');
    const keyRequest = requests.find((request) => request.pathname.endsWith('/keys'));
    expect(keyRequest?.searchParams.get('scope')).toBe('all');
    expect(keyRequest?.searchParams.get('q')).toBe('sk');
    expect(keyRequest?.searchParams.get('handling')).toBe('pending');
    expect(keyRequest?.searchParams.get('idle')).toBe('yes');
    expect(keyRequest?.searchParams.get('page')).toBe('2');
    expect(currentSearch()).toContain(`source_key=${encodeURIComponent(sourceA)}`);
    expect(currentSearch()).toContain('source_keys_page=2');
    expect(currentSearch()).toContain('source_scope=all');
  });

  it('normalizes a noncanonical source key URL without requesting its key page', async () => {
    const invalidSourceKey = `dsg_${'A'.repeat(42)}B`;
    const { requests } = installFetch();
    const view = await renderWithProviders(
      <BrowserHarness role="admin" accountId="admin:one" enabled onOpenDonation={vi.fn()} />,
      {
        station: 'admin',
        role: 'admin',
        route: `/charity?source_key=${invalidSourceKey}&source_keys_page=2&source_keys_page_size=50`,
      },
    );

    await within(sourcePane(view.container)).findByRole('button', {
      name: /Custom endpoint: https:\/\/source-0\.example\.test\/v1/,
    });
    await waitFor(() => expect(currentSearch()).not.toContain('source_key='));
    expect(requests.some((request) => request.pathname.endsWith('/keys'))).toBe(false);
    expect(currentSearch()).not.toContain('source_keys_page=');
    expect(currentSearch()).not.toContain('source_keys_page_size=');
  });

  it('keeps the selected source when the outer page changes', async () => {
    const { requests } = installFetch({ manySources: true });
    const view = await renderWithProviders(
      <BrowserHarness role="admin" accountId="admin:one" enabled onOpenDonation={vi.fn()} />,
      { station: 'admin', role: 'admin', route: '/charity' },
    );
    await chooseSource(view, view.container);
    const beforeKeyRequests = requests.filter((request) =>
      request.pathname.endsWith('/keys'),
    ).length;
    await view.user.click(within(sourcePane(view.container)).getByRole('button', { name: 'Next' }));
    expect(
      await within(sourcePane(view.container)).findByRole('button', {
        name: /Custom endpoint: https:\/\/source-1\.example\.test\/v1/,
      }),
    ).toBeVisible();
    expect(
      await within(keyPane(view.container)).findByRole('heading', {
        name: /Custom endpoint: https:\/\/source-0\.example\.test\/v1/,
      }),
    ).toBeVisible();
    expect(within(keyPane(view.container)).getByText(/Key · sk-11/)).toBeVisible();
    expect(currentSearch()).toContain(`source_key=${encodeURIComponent(sourceA)}`);
    expect(requests.filter((request) => request.pathname.endsWith('/keys')).length).toBe(
      beforeKeyRequests,
    );
  });

  it('clears nested context on return while preserving the outer URL context', async () => {
    const { requests } = installFetch({ manySources: true, manyKeys: true });
    const view = await renderWithProviders(
      <BrowserHarness role="admin" accountId="admin:one" enabled onOpenDonation={vi.fn()} />,
      {
        station: 'admin',
        role: 'admin',
        route: `/charity?sources_page=2&sources_page_size=20&source_keys_page=2&source_keys_page_size=20&source_key=${sourceA}&source_scope=all&source_q=provider&source_handling=pending`,
      },
    );
    await within(keyPane(view.container)).findByText(/Key · sk-31/);
    expect(requests.some((request) => request.searchParams.get('page') === '2')).toBe(true);

    await view.user.click(
      within(keyPane(view.container)).getByRole('button', { name: 'Back to sources' }),
    );
    await waitFor(() => expect(currentSearch()).not.toContain('source_key='));
    const search = new URLSearchParams(currentSearch());
    expect(search.get('sources_page')).toBe('2');
    expect(search.get('sources_page_size')).toBe('20');
    expect(search.get('source_scope')).toBe('all');
    expect(search.get('source_q')).toBe('provider');
    expect(search.get('source_handling')).toBe('pending');
    expect(search.has('source_keys_page')).toBe(false);
    expect(search.has('source_keys_page_size')).toBe(false);
    expect(within(keyPane(view.container)).getByText('Select a source')).toBeVisible();
  });

  it('keeps an empty source collection paginated and sends independent filters', async () => {
    const { fetchMock, requests } = installFetch();
    fetchMock.mockImplementationOnce(async (input) => {
      requests.push(requestURL(input));
      return jsonResponse(page([]));
    });
    const view = await renderWithProviders(
      <BrowserHarness role="admin" accountId="admin:one" enabled onOpenDonation={vi.fn()} />,
      { station: 'admin', role: 'admin', route: '/charity?source_scope=all' },
    );
    const pane = sourcePane(view.container);
    expect(await within(pane).findByText('No source groups')).toBeVisible();
    expect(within(pane).getByRole('combobox', { name: 'Items per page' })).toBeVisible();
    expect(within(pane).getByText(/Page 1 of 1/)).toBeVisible();
    expect(requests[0]?.searchParams.get('scope')).toBe('all');
  });

  it('shows a background source error and pauses key actions while stale data is present', async () => {
    const requests: URL[] = [];
    const fetchMock = vi.fn<typeof fetch>(async (input, init) => {
      const url = requestURL(input);
      requests.push(url);
      if (url.pathname === '/admin/api/donation-sources') {
        const pageNumber = url.searchParams.get('page') ?? '1';
        const pageSize = Number(url.searchParams.get('page_size') ?? '20');
        if (pageNumber === '2') return jsonResponse({}, 503);
        return jsonResponse(sourceListForPage(pageNumber, pageSize, true));
      }
      if (
        url.pathname.startsWith('/admin/api/donation-sources/') &&
        url.pathname.endsWith('/keys')
      ) {
        const pageNumber = url.searchParams.get('page') ?? '1';
        const pageSize = Number(url.searchParams.get('page_size') ?? '20');
        return jsonResponse(sourceKeyListForPage(pageNumber, pageSize));
      }
      throw new Error(`Unexpected request: ${init?.method ?? 'GET'} ${url.pathname}${url.search}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const onOpenDonation = vi.fn();
    const view = await renderWithProviders(
      <BrowserHarness role="admin" accountId="admin:one" enabled onOpenDonation={onOpenDonation} />,
      { station: 'admin', role: 'admin', route: '/charity' },
    );

    await chooseSource(view, view.container);
    await view.user.click(within(sourcePane(view.container)).getByRole('button', { name: 'Next' }));
    expect(await within(sourcePane(view.container)).findByRole('alert')).toBeVisible();
    expect(within(sourcePane(view.container)).getByRole('button', { name: 'Retry' })).toBeVisible();
    const manageDonation = await within(keyPane(view.container)).findByRole('button', {
      name: /Manage donation #7/,
    });
    const manageKey = within(keyPane(view.container)).getByRole('button', {
      name: /Manage key #11/,
    });
    expect(manageDonation).toBeDisabled();
    expect(manageKey).toBeDisabled();
    expect(onOpenDonation).not.toHaveBeenCalled();
    expect(requests.some((request) => request.searchParams.get('page') === '2')).toBe(true);
  });

  it('clears the source surface and calls back immediately on forbidden access', async () => {
    const { requests } = installFetch({ keyStatus: 403 });
    const onCapabilityLoss = vi.fn();
    const view = await renderWithProviders(
      <BrowserHarness
        role="admin"
        accountId="admin:one"
        enabled
        onOpenDonation={vi.fn()}
        onCapabilityLoss={onCapabilityLoss}
      />,
      { station: 'admin', role: 'admin', route: '/charity' },
    );
    await view.user.click(await sourceButton(view.container, 0));
    await waitFor(() => expect(onCapabilityLoss).toHaveBeenCalledTimes(1));
    expect(screen.getByRole('alert')).toHaveTextContent(
      'Charity management access is no longer available.',
    );
    expect(
      within(view.container).queryByText('https://source-0.example.test/v1'),
    ).not.toBeInTheDocument();
    expect(requests.some((request) => request.pathname.endsWith('/keys'))).toBe(true);
  });

  it('keeps a source selected and asks the operator to return after a source-key 404', async () => {
    installFetch({ keyStatus: 404 });
    const view = await renderWithProviders(
      <BrowserHarness role="admin" accountId="admin:one" enabled onOpenDonation={vi.fn()} />,
      { station: 'admin', role: 'admin', route: '/charity' },
    );
    await view.user.click(await sourceButton(view.container, 0));
    expect(
      await within(keyPane(view.container)).findByRole('heading', {
        name: 'Source no longer available',
      }),
    ).toBeVisible();
    expect(currentSearch()).toContain(`source_key=${encodeURIComponent(sourceA)}`);
  });

  it('uses the steward source and key endpoints for the second management role', async () => {
    const { requests } = installFetch({ role: 'steward' });
    const view = await renderWithProviders(
      <BrowserHarness role="steward" accountId="member:one" enabled onOpenDonation={vi.fn()} />,
      { station: 'user', role: 'user', route: '/charity' },
    );
    await within(sourcePane(view.container)).findByRole('button', {
      name: /Custom endpoint: https:\/\/source-0\.example\.test\/v1/,
    });
    await chooseSource(view, view.container);
    expect(requests.some((request) => request.pathname === '/api/steward/donation-sources')).toBe(
      true,
    );
    expect(
      requests.some(
        (request) => request.pathname === `/api/steward/donation-sources/${sourceA}/keys`,
      ),
    ).toBe(true);
  });

  it('does not let a late previous-account response replace the current account', async () => {
    const first = (() => {
      let resolve!: (response: Response) => void;
      const promise = new Promise<Response>((next) => {
        resolve = next;
      });
      return { promise, resolve };
    })();
    const requests: string[] = [];
    const fetchMock = vi.fn<typeof fetch>(async (input) => {
      const url = requestURL(input);
      requests.push(url.pathname);
      if (requests.length === 1) return first.promise;
      return jsonResponse(page([sourceSummary(sourceB, 1)]));
    });
    vi.stubGlobal('fetch', fetchMock);
    const onOpenDonation = vi.fn();
    const view = await renderWithProviders(
      <BrowserHarness
        role="admin"
        accountId="admin:first"
        enabled
        onOpenDonation={onOpenDonation}
      />,
      { station: 'admin', role: 'admin', route: '/charity' },
    );
    view.rerender(
      <BrowserHarness
        role="admin"
        accountId="admin:second"
        enabled
        onOpenDonation={onOpenDonation}
      />,
    );
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
    await within(sourcePane(view.container)).findByRole('button', {
      name: /Custom endpoint: https:\/\/source-1\.example\.test\/v1/,
    });
    first.resolve(jsonResponse(page([sourceSummary(sourceA)])));
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(
      within(sourcePane(view.container)).getByRole('button', {
        name: /Custom endpoint: https:\/\/source-1\.example\.test\/v1/,
      }),
    ).toBeVisible();
    expect(
      within(sourcePane(view.container)).queryByRole('button', {
        name: /Custom endpoint: https:\/\/source-0\.example\.test\/v1/,
      }),
    ).not.toBeInTheDocument();
  });
});
