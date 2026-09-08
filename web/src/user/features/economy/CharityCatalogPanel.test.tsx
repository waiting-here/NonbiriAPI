import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { CharityCatalogPanel } from './CharityCatalogPanel';
import { installJsonFetchFixtures, renderWithProviders } from '../../../../test/unit/support';

afterEach(() => {
  vi.unstubAllGlobals();
  window.localStorage.removeItem('nonbiri:user:charity-catalog-page-size:v1');
});

const SERVER_NOW = 1_800_000_000;

function catalogModel(
  id: string,
  modelName = `model-${id}`,
  overrides: Record<string, unknown> = {},
) {
  return {
    id,
    provider: 'provider',
    model: modelName,
    full_name: `[公益]provider/${modelName}`,
    pricing: {
      mode: 'per_request',
      user_price_milli: '3000',
      discounted_user_price_milli: '2400',
      user_prices_milli: null,
      discounted_user_prices_milli: null,
    },
    discount: {
      enabled: true,
      percent: 80,
      start_at: SERVER_NOW - 10,
      end_at: SERVER_NOW + 100,
    },
    public_description: `Description for ${modelName}`,
    enabled: true,
    allowed_levels: [1, 3, 5],
    level_allowed: true,
    availability: 'available',
    ...overrides,
  };
}

function catalogPage(
  models: unknown[],
  pagination: Record<string, unknown> = {
    page: '1',
    page_size: 20,
    total_items: String(models.length),
    total_pages: '1',
  },
) {
  return {
    models,
    pagination,
    donation_intake: 'open',
    server_now: SERVER_NOW,
  };
}

function catalogPath(page = '1', pageSize = 20, query = '', allowedForMe = ''): string {
  const params = new URLSearchParams({ view: 'catalog', page, page_size: String(pageSize) });
  if (query) params.set('q', query);
  if (allowedForMe) params.set('allowed_for_me', allowedForMe);
  return `/api/charity/models?${params.toString()}`;
}

describe('charity catalog panel', () => {
  it('renders public descriptions as text with access, availability, and exact prices', async () => {
    const model = catalogModel('1', 'plain', {
      public_description: '<b>plain</b>\nsecond line',
    });
    installJsonFetchFixtures([{ method: 'GET', path: catalogPath(), body: catalogPage([model]) }]);
    const rendered = await renderWithProviders(<CharityCatalogPanel accountID="7" />, {
      station: 'user',
      role: 'user',
    });

    expect(await screen.findByText('[公益]provider/plain')).toBeVisible();
    expect(screen.getByText(/<b>plain<\/b>/)).toBeVisible();
    expect(screen.getByText('L1, L3, L5')).toBeVisible();
    expect(screen.getByText('Allowed for me', { selector: 'dd' })).toBeVisible();
    expect(screen.getByText('Available now')).toBeVisible();
    const table = screen.getByRole('table', { name: 'Charity model prices' });
    expect(within(table).getByLabelText('Original price: 3')).toBeVisible();
    expect(within(table).getByLabelText('Offer price: 2.4')).toBeVisible();
    const toggle = screen.getByRole('button', { name: 'Show full description' });
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
    await rendered.user.click(toggle);
    expect(toggle).toHaveAttribute('aria-expanded', 'true');
    expect(screen.getByRole('button', { name: 'Copy Model name' })).toBeVisible();
  });

  it('requests server pages and resets to page one for search, access, and page size', async () => {
    const firstPage = Array.from({ length: 20 }, (_, index) => catalogModel(String(index + 1)));
    const searchModel = catalogModel('21', 'needle', {
      public_description: 'needle description',
      allowed_levels: [2, 4],
    });
    const deniedModel = catalogModel('22', 'denied', {
      public_description: 'denied description',
      level_allowed: false,
      availability: 'level_denied',
    });
    const fetchMock = installJsonFetchFixtures([
      {
        method: 'GET',
        path: catalogPath(),
        body: catalogPage(firstPage, {
          page: '1',
          page_size: 20,
          total_items: '21',
          total_pages: '2',
        }),
      },
      {
        method: 'GET',
        path: catalogPath('2'),
        body: catalogPage([catalogModel('20', 'page-two')], {
          page: '2',
          page_size: 20,
          total_items: '21',
          total_pages: '2',
        }),
      },
      {
        method: 'GET',
        path: catalogPath('1', 20, 'needle'),
        body: catalogPage([searchModel]),
      },
      {
        method: 'GET',
        path: catalogPath('1', 20, 'needle', 'false'),
        body: catalogPage([deniedModel]),
      },
      {
        method: 'GET',
        path: catalogPath('1', 50, 'needle', 'false'),
        body: catalogPage([deniedModel], {
          page: '1',
          page_size: 50,
          total_items: '1',
          total_pages: '1',
        }),
      },
    ]);
    const rendered = await renderWithProviders(<CharityCatalogPanel accountID="7" />, {
      station: 'user',
      role: 'user',
    });
    await screen.findByText('[公益]provider/model-1');

    await rendered.user.click(screen.getByRole('button', { name: 'Next' }));
    expect(await screen.findByText('[公益]provider/page-two')).toBeVisible();

    const requestCountBeforeSearch = fetchMock.mock.calls.length;
    const search = screen.getByRole('searchbox');
    fireEvent.change(search, { target: { value: 'needle' } });
    expect(fetchMock).toHaveBeenCalledTimes(requestCountBeforeSearch);
    fireEvent.submit(search.closest('form')!);
    expect(await screen.findByText('[公益]provider/needle')).toBeVisible();
    expect(screen.getByRole('button', { name: 'Previous' })).toBeDisabled();

    fireEvent.change(screen.getByLabelText('Your access'), { target: { value: 'false' } });
    expect(await screen.findByText('[公益]provider/denied')).toBeVisible();
    expect(screen.getByText('Not allowed for me', { selector: 'dd' })).toBeVisible();

    fireEvent.change(screen.getByLabelText('Items per page'), { target: { value: '50' } });
    await waitFor(() =>
      expect(screen.getByRole('combobox', { name: 'Items per page' })).toHaveValue('50'),
    );
    expect(window.localStorage.getItem('nonbiri:user:charity-catalog-page-size:v1')).toBe('50');
    expect(fetchMock.mock.calls.map(([input]) => String(input))).toContain(catalogPath('2'));
    expect(fetchMock.mock.calls.map(([input]) => String(input))).toContain(
      catalogPath('1', 20, 'needle'),
    );
    expect(fetchMock.mock.calls.map(([input]) => String(input))).toContain(
      catalogPath('1', 20, 'needle', 'false'),
    );
    expect(fetchMock.mock.calls.map(([input]) => String(input))).toContain(
      catalogPath('1', 50, 'needle', 'false'),
    );
  });

  it('keeps the current account query key and supports a failed read retry', async () => {
    let calls = 0;
    const fetchMock = vi.fn(async () => {
      calls += 1;
      if (calls === 1) {
        return new Response(JSON.stringify({}), {
          status: 503,
          headers: { 'content-type': 'application/json' },
        });
      }
      return new Response(JSON.stringify(catalogPage([catalogModel('7', 'retried')])), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      });
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(<CharityCatalogPanel accountID="account-7" />, {
      station: 'user',
      role: 'user',
    });

    expect(await screen.findByRole('alert')).toHaveTextContent(
      'The service is temporarily unavailable.',
    );
    expect(
      rendered.queryClient
        .getQueryCache()
        .getAll()
        .some((query) => query.queryKey.includes('account-7')),
    ).toBe(true);
    await rendered.user.click(screen.getByRole('button', { name: 'Retry' }));
    expect(await screen.findByText('[公益]provider/retried')).toBeVisible();
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it('does not render a previous page when the next account-scoped read is forbidden', async () => {
    let calls = 0;
    const fetchMock = vi.fn(async (input: string) => {
      calls += 1;
      if (calls === 1) {
        return new Response(JSON.stringify(catalogPage([catalogModel('1', 'old')])), {
          status: 200,
          headers: { 'content-type': 'application/json' },
        });
      }
      expect(input).toContain('q=private');
      return new Response(JSON.stringify({}), {
        status: 403,
        headers: { 'content-type': 'application/json' },
      });
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(<CharityCatalogPanel accountID="7" />, {
      station: 'user',
      role: 'user',
    });
    expect(await screen.findByText('[公益]provider/old')).toBeVisible();
    const search = screen.getByRole('searchbox');
    fireEvent.change(search, { target: { value: 'private' } });
    fireEvent.submit(search.closest('form')!);
    await waitFor(() => expect(screen.queryByText('[公益]provider/old')).toBeNull());
    expect(rendered.queryClient.getQueryData(['user', 'session'])).toBeNull();
  });
});
