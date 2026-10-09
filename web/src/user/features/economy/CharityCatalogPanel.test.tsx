import { act, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { useNavigate, useSearchParams } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { installJsonFetchFixtures, renderWithProviders } from '../../../../test/unit/support';
import { CharityCatalogPanel } from './CharityCatalogPanel';

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
    model_types: ['chat_completions', 'embeddings'],
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
    currently_available: true,
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

function catalogPath(
  page = '1',
  pageSize = 20,
  query = '',
  allowedForMe = 'true',
  allowedLevel = '',
  currentlyAvailable = 'true',
  modelType = '',
): string {
  const params = new URLSearchParams({ view: 'catalog', page, page_size: String(pageSize) });
  if (query) params.set('q', query);
  if (allowedForMe && allowedForMe !== 'all') params.set('allowed_for_me', allowedForMe);
  if (allowedLevel && allowedLevel !== 'all') params.set('allowed_level', allowedLevel);
  if (currentlyAvailable && currentlyAvailable !== 'all') {
    params.set('currently_available', currentlyAvailable);
  }
  if (modelType && modelType !== 'all') params.set('model_type', modelType);
  return `/api/charity/models?${params.toString()}`;
}

function SearchProbe() {
  const [params] = useSearchParams();
  return <output aria-label="Current catalog URL">{params.toString()}</output>;
}

function HistoryProbe() {
  const navigate = useNavigate();
  return (
    <div>
      <button
        type="button"
        onClick={() =>
          navigate(
            '/charity?tab=models&page=1&page_size=50&q=alternate&allowed_for_me=false&allowed_level=3&currently_available=true',
          )
        }
      >
        Open alternate filter view
      </button>
      <button type="button" onClick={() => navigate(-1)}>
        Back to saved catalog
      </button>
    </div>
  );
}

describe('charity catalog panel', () => {
  it('retains a just-selected availability filter when page size changes immediately', async () => {
    const rows = Array.from({ length: 20 }, (_, index) => catalogModel(String(index + 1)));
    const result = (size: number, total: number) =>
      catalogPage(rows.slice(0, size), {
        page: '1',
        page_size: size,
        total_items: String(total),
        total_pages: String(Math.ceil(total / size)),
      });
    installJsonFetchFixtures([
      { method: 'GET', path: catalogPath('1', 20, '', 'all', 'all', 'true'), body: result(20, 22) },
      { method: 'GET', path: catalogPath('1', 20, '', 'all', 'all', 'all'), body: result(20, 26) },
      { method: 'GET', path: catalogPath('1', 10, '', 'all', 'all', 'true'), body: result(10, 22) },
      { method: 'GET', path: catalogPath('1', 10, '', 'all', 'all', 'all'), body: result(10, 26) },
    ]);
    await renderWithProviders(
      <>
        <CharityCatalogPanel accountID="7" />
        <SearchProbe />
      </>,
      {
        station: 'user',
        role: 'user',
        route:
          '/charity?allowed_for_me=all&allowed_level=all&currently_available=true&page=1&page_size=20',
      },
    );
    await screen.findByText('22 items');
    fireEvent.click(screen.getByText(/^Filters/, { selector: 'summary' }));
    act(() => {
      fireEvent.change(screen.getByLabelText('Currently available'), { target: { value: 'all' } });
      fireEvent.change(screen.getByLabelText('Items per page'), { target: { value: '10' } });
    });
    await screen.findByText('26 items');
    expect(screen.getByLabelText('Currently available')).toHaveValue('all');
    expect(screen.getByLabelText('Current catalog URL')).toHaveTextContent(
      'currently_available=all',
    );
  });

  it('renders public descriptions as text with access, availability, and exact prices', async () => {
    const model = catalogModel('1', 'plain', {
      public_description: '<b>plain</b>\nsecond line',
    });
    const fetchMock = installJsonFetchFixtures([
      { method: 'GET', path: catalogPath(), body: catalogPage([model]) },
    ]);
    const rendered = await renderWithProviders(<CharityCatalogPanel accountID="7" />, {
      station: 'user',
      role: 'user',
    });

    expect(await screen.findByText('[公益]provider/plain')).toBeVisible();
    expect(fetchMock.mock.calls[0]?.[0]).toBe(catalogPath());
    expect(screen.getByRole('button', { name: 'Remove access filter' })).toBeVisible();
    expect(screen.getByRole('button', { name: 'Remove availability filter' })).toBeVisible();
    expect(screen.getByText(/<b>plain<\/b>/)).toBeVisible();
    expect(rendered.container.querySelector('.charity-model-preview b')).toBeNull();
    expect(screen.getByText('L1, L3, L5')).toBeVisible();
    expect(screen.getAllByText('Available now').length).toBeGreaterThanOrEqual(1);
    const toggle = screen.getByRole('button', { name: '[公益]provider/plain' });
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
    await rendered.user.click(toggle);
    expect(toggle).toHaveAttribute('aria-expanded', 'true');
    const detail = screen.getByRole('region', { name: '[公益]provider/plain' });
    expect(within(detail).getByText(/Allowed for my current level/)).toBeVisible();
    expect(detail.querySelector('b')).toBeNull();
    const table = within(detail).getByRole('table', { name: 'Charity model prices' });
    expect(within(table).getByLabelText('Original price: 3')).toBeVisible();
    expect(within(table).getByLabelText('Offer price: 2.4')).toBeVisible();
    expect(screen.getByRole('button', { name: 'Copy Model name' })).toBeVisible();
  });

  it('combines each independent filter, resets only the page, and keeps explicit URL state', async () => {
    const initial = catalogModel('1', 'initial');
    const levelFiltered = catalogModel('2', 'level-3', { allowed_levels: [3] });
    const accessFiltered = catalogModel('3', 'denied-level-3', {
      allowed_levels: [3],
      level_allowed: false,
      availability: 'level_denied',
      currently_available: true,
    });
    const unavailable = catalogModel('4', 'unavailable', {
      allowed_levels: [3],
      level_allowed: false,
      availability: 'level_denied',
      currently_available: false,
    });
    const fetchMock = installJsonFetchFixtures([
      { method: 'GET', path: catalogPath(), body: catalogPage([initial]) },
      {
        method: 'GET',
        path: catalogPath('1', 20, '', 'all', 'all', 'all'),
        body: catalogPage([initial]),
      },
      {
        method: 'GET',
        path: catalogPath('1', 20, '', 'true', '3', 'true'),
        body: catalogPage([levelFiltered]),
      },
      {
        method: 'GET',
        path: catalogPath('1', 20, '', 'false', '3', 'true'),
        body: catalogPage([accessFiltered]),
      },
      {
        method: 'GET',
        path: catalogPath('1', 20, '', 'false', '3', 'all'),
        body: catalogPage([unavailable]),
      },
      {
        method: 'GET',
        path: catalogPath('1', 20, '', 'false', 'all', 'all'),
        body: catalogPage([unavailable]),
      },
    ]);
    const rendered = await renderWithProviders(
      <>
        <CharityCatalogPanel accountID="7" />
        <SearchProbe />
      </>,
      { station: 'user', role: 'user' },
    );
    await screen.findByText('[公益]provider/initial');
    await rendered.user.click(screen.getByText(/^Filters/, { selector: 'summary' }));

    await rendered.user.selectOptions(screen.getByLabelText('Allowed for level'), '3');
    expect(await screen.findByText('[公益]provider/level-3')).toBeVisible();
    expect(screen.getByLabelText('Your access')).toHaveValue('true');
    expect(screen.getByLabelText('Currently available')).toHaveValue('true');
    expect(screen.getByLabelText('Current catalog URL')).toHaveTextContent(
      'allowed_for_me=true&allowed_level=3&currently_available=true&model_type=all&page=1',
    );

    await rendered.user.selectOptions(screen.getByLabelText('Your access'), 'false');
    expect(await screen.findByText('[公益]provider/denied-level-3')).toBeVisible();
    expect(screen.getByLabelText('Allowed for level')).toHaveValue('3');
    expect(screen.getByLabelText('Currently available')).toHaveValue('true');
    await rendered.user.click(
      screen.getByRole('button', { name: '[公益]provider/denied-level-3' }),
    );
    expect(screen.getByText(/Resource available now/)).toBeVisible();

    await rendered.user.selectOptions(screen.getByLabelText('Currently available'), 'all');
    expect(await screen.findByText('[公益]provider/unavailable')).toBeVisible();
    expect(screen.getByLabelText('Allowed for level')).toHaveValue('3');
    expect(screen.getByLabelText('Your access')).toHaveValue('false');

    await rendered.user.selectOptions(screen.getByLabelText('Allowed for level'), 'all');
    expect(screen.getByLabelText('Currently available')).toHaveValue('all');
    await rendered.user.click(screen.getByRole('button', { name: 'Clear filters' }));
    expect(screen.getByLabelText('Allowed for level')).toHaveValue('all');
    expect(screen.getByLabelText('Your access')).toHaveValue('all');
    expect(screen.getByLabelText('Currently available')).toHaveValue('all');
    expect(screen.getByLabelText('Current catalog URL')).toHaveTextContent(
      'allowed_for_me=all&allowed_level=all&currently_available=all&model_type=all&page=1',
    );
    expect(fetchMock.mock.calls.map(([input]) => String(input))).toContain(catalogPath());
    expect(fetchMock.mock.calls.map(([input]) => String(input))).toContain(
      catalogPath('1', 20, '', 'false', 'all', 'all'),
    );
    expect(fetchMock.mock.calls.map(([input]) => String(input))).toContain(
      catalogPath('1', 20, '', 'all', 'all', 'all'),
    );
    await screen.findByText('[公益]provider/initial');
    expect(fetchMock.mock.calls.map(([input]) => String(input))).toEqual([
      catalogPath(),
      catalogPath('1', 20, '', 'true', '3', 'true'),
      catalogPath('1', 20, '', 'false', '3', 'true'),
      catalogPath('1', 20, '', 'false', '3', 'all'),
      catalogPath('1', 20, '', 'false', 'all', 'all'),
      catalogPath('1', 20, '', 'all', 'all', 'all'),
    ]);
  });

  it('shows a server page clamp while retaining a requested page for review', async () => {
    const model = catalogModel('1', 'clamped');
    installJsonFetchFixtures([
      {
        method: 'GET',
        path: catalogPath('2'),
        body: catalogPage([model], {
          page: '1',
          page_size: 20,
          total_items: '1',
          total_pages: '1',
        }),
      },
    ]);
    await renderWithProviders(<CharityCatalogPanel accountID="7" />, {
      station: 'user',
      role: 'user',
      route: '/charity?tab=models&page=2',
    });
    expect(await screen.findByText('[公益]provider/clamped')).toBeVisible();
    expect(screen.getByText('That page is no longer available. Showing page 1.')).toBeVisible();
  });

  it('filters model types on the server and resets to the first page', async () => {
    const secondPageModel = catalogModel('21', 'page-two', { model_types: ['chat_completions'] });
    const multiTypeModel = catalogModel('22', 'chat-and-image', {
      model_types: ['chat_completions', 'images_generations'],
    });
    const fetchMock = installJsonFetchFixtures([
      {
        method: 'GET',
        path: catalogPath('2', 20, 'needle'),
        body: catalogPage([secondPageModel], {
          page: '2',
          page_size: 20,
          total_items: '21',
          total_pages: '2',
        }),
      },
      {
        method: 'GET',
        path: catalogPath('1', 20, 'needle', 'true', '', 'true', 'images_generations'),
        body: catalogPage([multiTypeModel]),
      },
    ]);
    const rendered = await renderWithProviders(
      <>
        <CharityCatalogPanel accountID="7" />
        <SearchProbe />
      </>,
      { station: 'user', role: 'user', route: '/charity?tab=models&page=2&q=needle' },
    );
    expect(await screen.findByText('[公益]provider/page-two')).toBeVisible();
    await rendered.user.click(screen.getByText(/^Filters/, { selector: 'summary' }));
    await rendered.user.selectOptions(screen.getByLabelText('Model type'), 'images_generations');

    expect(await screen.findByText('[公益]provider/chat-and-image')).toBeVisible();
    expect(screen.queryByText('[公益]provider/page-two')).toBeNull();
    expect(screen.getByLabelText('Current catalog URL')).toHaveTextContent(
      'q=needle&allowed_for_me=true',
    );
    expect(screen.getByLabelText('Current catalog URL')).toHaveTextContent(
      'model_type=images_generations&page=1',
    );
    expect(fetchMock.mock.calls.map(([input]) => String(input))).toEqual([
      catalogPath('2', 20, 'needle'),
      catalogPath('1', 20, 'needle', 'true', '', 'true', 'images_generations'),
    ]);
  });

  it('clears a model type filter from the empty catalog recovery action', async () => {
    const model = catalogModel('1', 'chat model', { model_types: ['chat_completions'] });
    const fetchMock = installJsonFetchFixtures([
      {
        method: 'GET',
        path: catalogPath('1', 20, '', 'true', '', 'true', 'images_generations'),
        body: catalogPage([]),
      },
      {
        method: 'GET',
        path: catalogPath('1', 20, '', 'all', 'all', 'all'),
        body: catalogPage([model]),
      },
    ]);
    const rendered = await renderWithProviders(<CharityCatalogPanel accountID="7" />, {
      station: 'user',
      role: 'user',
      route: '/charity?model_type=images_generations',
    });

    expect(
      await screen.findByText('Active catalog filters are hiding models. Clear them to see more.'),
    ).toBeVisible();
    await rendered.user.click(screen.getByRole('button', { name: 'Show all models' }));

    expect(await screen.findByText('[公益]provider/chat model')).toBeVisible();
    expect(screen.getByLabelText('Model type')).toHaveValue('all');
    expect(fetchMock.mock.calls.map(([input]) => String(input))).toEqual([
      catalogPath('1', 20, '', 'true', '', 'true', 'images_generations'),
      catalogPath('1', 20, '', 'all', 'all', 'all'),
    ]);
  });

  it('keeps the three filter labels and applied state readable in Chinese', async () => {
    installJsonFetchFixtures([
      { method: 'GET', path: catalogPath(), body: catalogPage([catalogModel('1', '中文')]) },
    ]);
    await renderWithProviders(<CharityCatalogPanel accountID="7" />, {
      station: 'user',
      role: 'user',
      locale: 'zh',
    });
    expect(await screen.findByText('[公益]provider/中文')).toBeVisible();
    expect(screen.getByLabelText('模型类型')).toHaveValue('all');
    expect(screen.getByRole('option', { name: '全部类型' })).toBeInTheDocument();
    expect(screen.getByLabelText('某等级可访问')).toHaveValue('all');
    expect(screen.getByLabelText('本人访问权限')).toHaveValue('true');
    expect(screen.getByLabelText('当前是否可用')).toHaveValue('true');
    expect(screen.getByRole('button', { name: '移除访问权限筛选' })).toBeVisible();
    expect(screen.getByRole('button', { name: '移除当前状态筛选' })).toBeVisible();
  });

  it('restores submitted filters and page through POP navigation and a remount', async () => {
    const savedModel = catalogModel('10', 'saved');
    const alternateModel = catalogModel('11', 'alternate', { currently_available: true });
    const route =
      '/charity?tab=models&page=2&page_size=50&q=saved&allowed_for_me=false&allowed_level=3&currently_available=all';
    const fetchMock = installJsonFetchFixtures([
      {
        method: 'GET',
        path: catalogPath('2', 50, 'saved', 'false', '3', 'all'),
        body: catalogPage([savedModel], {
          page: '2',
          page_size: 50,
          total_items: '51',
          total_pages: '2',
        }),
      },
      {
        method: 'GET',
        path: catalogPath('1', 50, 'alternate', 'false', '3', 'true'),
        body: catalogPage([alternateModel], {
          page: '1',
          page_size: 50,
          total_items: '1',
          total_pages: '1',
        }),
      },
    ]);
    const rendered = await renderWithProviders(
      <>
        <CharityCatalogPanel accountID="7" />
        <SearchProbe />
        <HistoryProbe />
      </>,
      { station: 'user', role: 'user', route },
    );
    expect(await screen.findByText('[公益]provider/saved')).toBeVisible();
    expect(screen.getByLabelText('Allowed for level')).toHaveValue('3');
    expect(screen.getByLabelText('Your access')).toHaveValue('false');
    expect(screen.getByLabelText('Currently available')).toHaveValue('all');
    expect(screen.getByRole('button', { name: 'Previous' })).not.toBeDisabled();

    await rendered.user.click(screen.getByRole('button', { name: 'Open alternate filter view' }));
    expect(await screen.findByText('[公益]provider/alternate')).toBeVisible();
    expect(screen.getByLabelText('Current catalog URL')).toHaveTextContent('q=alternate');

    await rendered.user.click(screen.getByRole('button', { name: 'Back to saved catalog' }));
    expect(await screen.findByText('[公益]provider/saved')).toBeVisible();
    expect(screen.getByLabelText('Allowed for level')).toHaveValue('3');
    expect(screen.getByLabelText('Your access')).toHaveValue('false');
    expect(screen.getByLabelText('Currently available')).toHaveValue('all');
    expect(screen.getByLabelText('Current catalog URL')).toHaveTextContent('page=2');

    rendered.unmount();
    const remounted = await renderWithProviders(
      <>
        <CharityCatalogPanel accountID="7" />
        <SearchProbe />
      </>,
      { station: 'user', role: 'user', route },
    );
    expect(await screen.findByText('[公益]provider/saved')).toBeVisible();
    expect(screen.getByLabelText('Allowed for level')).toHaveValue('3');
    expect(screen.getByLabelText('Your access')).toHaveValue('false');
    expect(screen.getByLabelText('Currently available')).toHaveValue('all');
    expect(screen.getByLabelText('Current catalog URL')).toHaveTextContent('page=2');
    expect(fetchMock.mock.calls.map(([input]) => String(input))).toEqual([
      catalogPath('2', 50, 'saved', 'false', '3', 'all'),
      catalogPath('1', 50, 'alternate', 'false', '3', 'true'),
      catalogPath('2', 50, 'saved', 'false', '3', 'all'),
    ]);
    remounted.unmount();
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
        body: catalogPage([catalogModel('21', 'page-two')], {
          page: '2',
          page_size: 20,
          total_items: '21',
          total_pages: '2',
        }),
      },
      {
        method: 'GET',
        path: catalogPath('1', 50, 'needle'),
        body: catalogPage([searchModel], {
          page: '1',
          page_size: 50,
          total_items: '1',
          total_pages: '1',
        }),
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
      {
        method: 'GET',
        path: catalogPath('1', 50),
        body: catalogPage([...firstPage, catalogModel('21', 'page-two')], {
          page: '1',
          page_size: 50,
          total_items: '21',
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

    await rendered.user.selectOptions(screen.getByLabelText('Items per page'), '50');
    expect(window.localStorage.getItem('nonbiri:user:charity-catalog-page-size:v1')).toBe('50');
    await screen.findByText('[公益]provider/model-1');
    const requestCountBeforeSearch = fetchMock.mock.calls.length;
    const search = screen.getByRole('searchbox');
    fireEvent.change(search, { target: { value: 'needle' } });
    expect(fetchMock).toHaveBeenCalledTimes(requestCountBeforeSearch);
    fireEvent.submit(search.closest('form')!);
    expect(await screen.findByText('[公益]provider/needle')).toBeVisible();
    expect(screen.queryByRole('button', { name: 'Previous' })).toBeNull();
    await rendered.user.click(screen.getByText(/^Filters/, { selector: 'summary' }));

    fireEvent.change(screen.getByLabelText('Your access'), { target: { value: 'false' } });
    expect(await screen.findByText('[公益]provider/denied')).toBeVisible();
    await rendered.user.click(screen.getByRole('button', { name: '[公益]provider/denied' }));
    expect(
      within(screen.getByRole('region', { name: '[公益]provider/denied' })).getByText(
        /Not allowed for my current level/,
      ),
    ).toBeVisible();
    expect(screen.queryByRole('combobox', { name: 'Items per page' })).toBeNull();
    expect(fetchMock.mock.calls.map(([input]) => String(input))).toContain(catalogPath('2'));
    expect(fetchMock.mock.calls.map(([input]) => String(input))).toContain(
      catalogPath('1', 50, 'needle'),
    );
    expect(fetchMock.mock.calls.map(([input]) => String(input))).toContain(
      catalogPath('1', 50, 'needle', 'false'),
    );
    expect(fetchMock.mock.calls.map(([input]) => String(input))).toContain(catalogPath('1', 50));
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

  it('drops a late catalog response after the account scope changes', async () => {
    const resolvers: Array<(response: Response) => void> = [];
    const fetchMock = vi.fn(
      () =>
        new Promise<Response>((resolve) => {
          resolvers.push(resolve);
        }),
    );
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(<CharityCatalogPanel accountID="old-account" />, {
      station: 'user',
      role: 'user',
    });
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));

    rendered.rerender(<CharityCatalogPanel accountID="new-account" />);
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
    resolvers[1]?.(
      new Response(JSON.stringify(catalogPage([catalogModel('8', 'new-account')])), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      }),
    );
    expect(await screen.findByText('[公益]provider/new-account')).toBeVisible();

    resolvers[0]?.(
      new Response(JSON.stringify(catalogPage([catalogModel('9', 'old-account')])), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      }),
    );
    await waitFor(() => expect(screen.queryByText('[公益]provider/old-account')).toBeNull());
    rendered.unmount();
  });
});
