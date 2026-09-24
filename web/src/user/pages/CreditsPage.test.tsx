import { screen, waitFor, within } from '@testing-library/react';
import { type ReactNode } from 'react';
import { useNavigate, useLocation } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { writePageSizePreference } from '@shared/operations/usePagePager';
import { renderWithProviders } from '../../../test/unit/support';
import { CreditsPage } from './CreditsPage';

const testSession = vi.hoisted(() => ({ accountID: 'account-1' }));

vi.mock('../components/UserPageGate', () => ({
  UserPageGate: ({ children }: { children: ReactNode }) => <>{children}</>,
}));
vi.mock('../data', () => ({
  useUserSession: () => ({
    data: { user: { id: testSession.accountID } },
    isPending: false,
    error: null,
    refetch: vi.fn(),
  }),
}));
vi.mock('../features/core/queries', () => ({
  coreSessionMatchesAccount: () => true,
}));

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
  });
}

function requestPath(input: string | URL | Request): string {
  const raw = input instanceof Request ? input.url : String(input);
  const url = new URL(raw, window.location.origin);
  return `${url.pathname}${url.search}`;
}

function entry(id: number, kind = 'checkin_award', delta = '1') {
  return {
    asset_type: 'general',
    operation_id: `op_${String(id).padStart(21, '0')}A`,
    line: 1,
    kind,
    delta,
    created_at: 1_800_000_000,
    request_id: null,
  };
}

function historyPage(
  data: readonly Record<string, unknown>[],
  page: string,
  pageSize: number,
  total: number,
  anchor: string | null = typeof data[0]?.operation_id === 'string' ? data[0].operation_id : null,
) {
  return {
    data,
    page,
    page_size: pageSize,
    total: String(total),
    total_pages: String(Math.max(1, Math.ceil(total / pageSize))),
    anchor,
    game_balance: '0',
    current_balance: '100',
    server_now: 1_800_000_001,
  };
}

function LocationProbe() {
  const location = useLocation();
  return <output data-testid="location">{`${location.pathname}${location.search}`}</output>;
}

function NavigationTools() {
  const navigate = useNavigate();
  return (
    <div>
      <button
        type="button"
        onClick={() => navigate('/credits?page=1&page_size=20&category=charity')}
      >
        Open charity filter
      </button>
      <button
        type="button"
        onClick={() => navigate('/credits?page=1&page_size=20&category=donation')}
      >
        Open donation filter
      </button>
      <button type="button" onClick={() => navigate(-1)}>
        Go back
      </button>
    </div>
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  window.localStorage.removeItem('nonbiri:user:credit-history-page-size:v1');
  testSession.accountID = 'account-1';
});

describe('credit history numbered page controls', () => {
  it.each(['en', 'zh'] as const)(
    'shows four independent assets and filters activity entries in %s',
    async (locale) => {
      const requests: string[] = [];
      const exchange = entry(601, 'activity_exchange', '-2000');
      const paper = { ...exchange, asset_type: 'sketch_paper', line: 2, delta: '2' };
      const reserve = { ...entry(602, 'image_reserve', '-3'), asset_type: 'sketch_brush' };
      const refund = { ...entry(603, 'image_refund', '3'), asset_type: 'sketch_brush' };
      const decay = entry(604, 'inactivity_decay', '-0.125');
      const game = { ...entry(605, 'checkin_award', '0.007'), asset_type: 'game' };
      const all = [exchange, paper, reserve, refund, decay, game];
      vi.stubGlobal(
        'fetch',
        vi.fn(async (input: string | URL | Request) => {
          const path = requestPath(input);
          requests.push(path);
          if (path === '/api/time-zones')
            return jsonResponse({ version: 'go1.26.6-zoneinfo', zones: ['UTC'] });
          if (path === '/api/credits/history?asset_type=all&page=1&page_size=20')
            return jsonResponse(historyPage(all, '1', 20, all.length));
          if (
            path ===
            '/api/credits/history?asset_type=sketch_paper&page=1&page_size=20&category=picture_book'
          )
            return jsonResponse(historyPage([paper], '1', 20, 1));
          if (
            path ===
            '/api/credits/history?asset_type=sketch_brush&page=1&page_size=20&category=picture_book'
          )
            return jsonResponse(historyPage([reserve, refund], '1', 20, 2));
          throw new Error(`Unexpected request: ${path}`);
        }),
      );
      const view = await renderWithProviders(<CreditsPage />, {
        station: 'user',
        role: 'user',
        locale,
        route: '/credits',
      });
      const table = within(await screen.findByRole('table'));
      const labels =
        locale === 'zh'
          ? {
              general: '通用积分',
              game: '游戏积分',
              paper: '草稿纸',
              brush: '画笔',
              exchange: '活动币兑换',
              reserve: '图像生成：预扣',
              refund: '图像生成：退款',
              decay: '低活跃积分衰减',
              asset: '积分类型',
              category: '变化原因',
              apply: '筛选',
            }
          : {
              general: 'General credits',
              game: 'Game credits',
              paper: 'Sketch paper',
              brush: 'Paint brushes',
              exchange: 'Activity currency exchange',
              reserve: 'Image generation: funds reserved',
              refund: 'Image generation: refund',
              decay: 'Inactivity credit decay',
              asset: 'Credit type',
              category: 'Reason',
              apply: 'Apply filters',
            };
      for (const [delta, asset, reason] of [
        ['-2000', labels.general, labels.exchange],
        ['+2', labels.paper, labels.exchange],
        ['-3', labels.brush, labels.reserve],
        ['+3', labels.brush, labels.refund],
        ['-0.125', labels.general, labels.decay],
        ['+0.007', labels.game, locale === 'zh' ? '签到奖励' : 'Check-in reward'],
      ]) {
        const row = table.getByText(delta).closest('tr')!;
        expect(within(row).getByText(asset)).toBeVisible();
        expect(within(row).getByText(reason)).toBeVisible();
      }
      const assets = screen.getByRole('combobox', { name: labels.asset });
      expect(Array.from(assets.querySelectorAll('option')).map((option) => option.value)).toEqual([
        'general',
        'game',
        'sketch_paper',
        'sketch_brush',
        'all',
      ]);
      await view.user.selectOptions(assets, 'sketch_paper');
      await view.user.selectOptions(
        screen.getByRole('combobox', { name: labels.category }),
        'picture_book',
      );
      await view.user.click(screen.getByRole('button', { name: labels.apply }));
      await waitFor(() => expect(table.queryByText('-2000')).not.toBeInTheDocument());
      expect(table.getByText('+2')).toBeVisible();
      await view.user.selectOptions(assets, 'sketch_brush');
      await view.user.click(screen.getByRole('button', { name: labels.apply }));
      await waitFor(() => expect(table.queryByText('+2')).not.toBeInTheDocument());
      expect(table.getByText('-3')).toBeVisible();
      expect(table.getByText('+3')).toBeVisible();
      expect(requests).toContain(
        '/api/credits/history?asset_type=sketch_paper&page=1&page_size=20&category=picture_book',
      );
      expect(requests).toContain(
        '/api/credits/history?asset_type=sketch_brush&page=1&page_size=20&category=picture_book',
      );
    },
  );

  it('shows both asset lines and resets the page and anchor when the asset changes', async () => {
    const requests: string[] = [];
    const general = entry(501, 'rps_queue_reserve', '-1');
    const game = { ...general, asset_type: 'game', line: 2, delta: '-2' };
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: string | URL | Request) => {
        const path = requestPath(input);
        requests.push(path);
        if (path === '/api/time-zones')
          return jsonResponse({ version: 'go1.26.6-zoneinfo', zones: ['UTC'] });
        if (path === '/api/credits/history?asset_type=game&page=1&page_size=20')
          return jsonResponse(historyPage([game], '1', 20, 1));
        return jsonResponse(historyPage([general, game], '2', 20, 22));
      }),
    );
    const view = await renderWithProviders(<CreditsPage />, {
      station: 'user',
      role: 'user',
      route: `/credits?page=2&anchor=${general.operation_id}`,
    });
    expect(await screen.findByText('-1')).toBeVisible();
    expect(screen.getByText('-2')).toBeVisible();
    await view.user.selectOptions(screen.getByRole('combobox', { name: 'Credit type' }), 'game');
    await view.user.click(screen.getByRole('button', { name: 'Apply filters' }));
    await waitFor(() =>
      expect(requests).toContain('/api/credits/history?asset_type=game&page=1&page_size=20'),
    );
    await waitFor(() => expect(screen.queryByText('-1')).not.toBeInTheDocument());
    expect(screen.getByText('-2')).toBeVisible();
  });

  it('uses all four page sizes, remembers ten rows, carries anchor, and refreshes latest', async () => {
    const first20 = Array.from({ length: 20 }, (_, index) => entry(index + 1));
    const first10 = first20.slice(0, 10);
    const second10 = Array.from({ length: 10 }, (_, index) => entry(index + 21));
    const anchor = first20[0].operation_id;
    const requests: string[] = [];
    const fetchMock = vi.fn((input: string | URL | Request) => {
      const path = requestPath(input);
      requests.push(path);
      if (path === '/api/time-zones') {
        return Promise.resolve(jsonResponse({ version: 'go1.26.6-zoneinfo', zones: ['UTC'] }));
      }
      if (path === '/api/credits/history?asset_type=all&page=1&page_size=20') {
        return Promise.resolve(jsonResponse(historyPage(first20, '1', 20, 21, anchor)));
      }
      if (path === `/api/credits/history?asset_type=all&page=1&page_size=10&anchor=${anchor}`) {
        return Promise.resolve(jsonResponse(historyPage(first10, '1', 10, 21, anchor)));
      }
      if (path === '/api/credits/history?asset_type=all&page=1&page_size=10') {
        return Promise.resolve(jsonResponse(historyPage(first10, '1', 10, 21, anchor)));
      }
      if (path === `/api/credits/history?asset_type=all&page=2&page_size=10&anchor=${anchor}`) {
        return Promise.resolve(jsonResponse(historyPage(second10, '2', 10, 21, anchor)));
      }
      throw new Error(`Unexpected request: ${path}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    const view = await renderWithProviders(
      <>
        <LocationProbe />
        <CreditsPage />
      </>,
      { station: 'user', role: 'user', route: '/credits' },
    );

    expect((await screen.findAllByText('+1')).length).toBeGreaterThan(0);
    expect(screen.getByText('Page 1 of 2 · Total: 21')).toBeVisible();
    const size = screen.getByRole('combobox', { name: 'Items per page' });
    expect(Array.from(size.querySelectorAll('option')).map((option) => option.value)).toEqual([
      '10',
      '20',
      '50',
      '100',
    ]);
    expect(size).toHaveValue('20');

    await view.user.selectOptions(size, '10');
    await waitFor(() =>
      expect(requests).toContain(
        `/api/credits/history?asset_type=all&page=1&page_size=10&anchor=${anchor}`,
      ),
    );
    expect(size).toHaveValue('10');
    await view.user.click(screen.getByRole('button', { name: 'Next' }));
    expect(await screen.findByText('Page 2 of 3 · Total: 21')).toBeVisible();
    expect(requests).toContain(
      `/api/credits/history?asset_type=all&page=2&page_size=10&anchor=${anchor}`,
    );
    expect(screen.getByTestId('location')).toHaveTextContent(
      `/credits?page=2&page_size=10&anchor=${anchor}`,
    );

    await view.user.click(screen.getByRole('button', { name: 'Refresh' }));
    await waitFor(() =>
      expect(
        requests.filter(
          (path) => path === '/api/credits/history?asset_type=all&page=1&page_size=10',
        ),
      ).toHaveLength(1),
    );
    expect(screen.getByTestId('location')).toHaveTextContent('/credits?page=1&page_size=10');

    view.unmount();
    const remounted = await renderWithProviders(
      <>
        <LocationProbe />
        <CreditsPage />
      </>,
      { station: 'user', role: 'user', route: '/credits' },
    );
    expect(await screen.findByText('Page 1 of 3 · Total: 21')).toBeVisible();
    expect(
      requests.filter((path) => path === '/api/credits/history?asset_type=all&page=1&page_size=10'),
    ).toHaveLength(2);
    remounted.unmount();
  });

  it('canonicalizes invalid URL values and applies category and direction filters from page one', async () => {
    const baseEntry = entry(1);
    const filteredEntry = entry(2, 'charity_reserve', '-1');
    const requests: string[] = [];
    const fetchMock = vi.fn((input: string | URL | Request) => {
      const path = requestPath(input);
      requests.push(path);
      if (path === '/api/time-zones') {
        return Promise.resolve(jsonResponse({ version: 'go1.26.6-zoneinfo', zones: ['UTC'] }));
      }
      if (path === '/api/credits/history?asset_type=all&page=1&page_size=20') {
        return Promise.resolve(jsonResponse(historyPage([baseEntry], '1', 20, 1)));
      }
      if (
        path ===
        '/api/credits/history?asset_type=all&page=1&page_size=20&category=charity&direction=expense'
      ) {
        return Promise.resolve(jsonResponse(historyPage([filteredEntry], '1', 20, 1)));
      }
      throw new Error(`Unexpected request: ${path}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    const view = await renderWithProviders(
      <>
        <LocationProbe />
        <CreditsPage />
      </>,
      {
        station: 'user',
        role: 'user',
        route:
          '/credits?page=0&page=01&page_size=30&anchor=bad&category=charity&category=donation&unknown=x',
      },
    );

    expect((await screen.findAllByText('+1')).length).toBeGreaterThan(0);
    await waitFor(() => expect(screen.getByTestId('location')).toHaveTextContent('/credits'));
    expect(requests).not.toContain(
      '/api/credits/history?asset_type=all&page=0&page=01&page_size=30&anchor=bad',
    );

    await view.user.selectOptions(screen.getByRole('combobox', { name: 'Reason' }), 'charity');
    await view.user.selectOptions(
      screen.getByRole('combobox', { name: 'Money in / out' }),
      'expense',
    );
    await view.user.click(screen.getByRole('button', { name: 'Apply filters' }));
    expect(await screen.findByText('-1')).toBeVisible();
    expect(requests).toContain(
      '/api/credits/history?asset_type=all&page=1&page_size=20&category=charity&direction=expense',
    );
    expect(screen.getByTestId('location')).toHaveTextContent(
      '/credits?asset_type=all&page=1&page_size=20&category=charity&direction=expense',
    );
  });

  it('accepts a legal int64 URL page and shows the server clamp without rewriting it', async () => {
    const requested = '9223372036854775807';
    const value = entry(1);
    const requests: string[] = [];
    const fetchMock = vi.fn((input: string | URL | Request) => {
      const path = requestPath(input);
      requests.push(path);
      if (path === '/api/time-zones') {
        return Promise.resolve(jsonResponse({ version: 'go1.26.6-zoneinfo', zones: ['UTC'] }));
      }
      if (path === `/api/credits/history?asset_type=all&page=${requested}&page_size=10`) {
        return Promise.resolve(jsonResponse(historyPage([value], '1', 10, 1)));
      }
      throw new Error(`Unexpected request: ${path}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    await renderWithProviders(
      <>
        <LocationProbe />
        <CreditsPage />
      </>,
      {
        station: 'user',
        role: 'user',
        route: `/credits?page=${requested}&page_size=10`,
      },
    );

    expect(
      await screen.findByText('That page is no longer available. Showing page 1.'),
    ).toBeVisible();
    expect(requests).toContain(
      `/api/credits/history?asset_type=all&page=${requested}&page_size=10`,
    );
    expect(screen.getByTestId('location')).toHaveTextContent(
      `/credits?page=${requested}&page_size=10`,
    );
  });

  it('keeps newer filter data over a slow response and restores a POP URL', async () => {
    const initialPage = Array.from({ length: 20 }, (_, index) => entry(index + 1));
    const initialEntry = initialPage[0];
    const secondPageEntry = entry(2);
    const charityEntry = entry(3, 'charity_reserve', '-1');
    const donationEntry = entry(4, 'donor_reward', '2');
    const anchor = initialEntry.operation_id;
    let resolveCharity!: (response: Response) => void;
    const charityResponse = new Promise<Response>((resolve) => {
      resolveCharity = resolve;
    });
    const requests: string[] = [];
    const fetchMock = vi.fn((input: string | URL | Request) => {
      const path = requestPath(input);
      requests.push(path);
      if (path === '/api/time-zones') {
        return Promise.resolve(jsonResponse({ version: 'go1.26.6-zoneinfo', zones: ['UTC'] }));
      }
      if (path === '/api/credits/history?asset_type=all&page=1&page_size=20') {
        return Promise.resolve(jsonResponse(historyPage(initialPage, '1', 20, 21, anchor)));
      }
      if (path === `/api/credits/history?asset_type=all&page=2&page_size=20&anchor=${anchor}`) {
        return Promise.resolve(jsonResponse(historyPage([secondPageEntry], '2', 20, 21, anchor)));
      }
      if (path === '/api/credits/history?asset_type=all&page=1&page_size=20&category=charity') {
        return charityResponse;
      }
      if (path === '/api/credits/history?asset_type=all&page=1&page_size=20&category=donation') {
        return Promise.resolve(jsonResponse(historyPage([donationEntry], '1', 20, 1)));
      }
      throw new Error(`Unexpected request: ${path}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    const view = await renderWithProviders(
      <>
        <LocationProbe />
        <NavigationTools />
        <CreditsPage />
      </>,
      { station: 'user', role: 'user', route: '/credits' },
    );
    expect((await screen.findAllByText('+1')).length).toBeGreaterThan(0);
    await view.user.click(screen.getByRole('button', { name: 'Next' }));
    expect(await screen.findByText('Page 2 of 2 · Total: 21')).toBeVisible();
    await view.user.click(screen.getByRole('button', { name: 'Go back' }));
    await waitFor(() => expect(screen.getByTestId('location')).toHaveTextContent('/credits'));
    expect((await screen.findAllByText('+1')).length).toBeGreaterThan(0);

    await view.user.click(screen.getByRole('button', { name: 'Open charity filter' }));
    await view.user.click(screen.getByRole('button', { name: 'Open donation filter' }));
    expect(await screen.findByText('+2')).toBeVisible();
    await waitFor(() =>
      expect(requests).toContain(
        '/api/credits/history?asset_type=all&page=1&page_size=20&category=donation',
      ),
    );
    resolveCharity(jsonResponse(historyPage([charityEntry], '1', 20, 1)));
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(screen.getByText('+2')).toBeVisible();
    expect(screen.queryByText('-1')).not.toBeInTheDocument();
  });

  it('remounts a changed account without its old anchor and hides private rows on 401/403', async () => {
    const oldEntry = entry(5, 'checkin_award', '1');
    const newEntry = entry(6, 'checkin_award', '2');
    const oldAnchor = oldEntry.operation_id;
    let status = 200;
    const requests: string[] = [];
    const fetchMock = vi.fn((input: string | URL | Request) => {
      const path = requestPath(input);
      requests.push(path);
      if (path === '/api/time-zones') {
        return Promise.resolve(jsonResponse({ version: 'go1.26.6-zoneinfo', zones: ['UTC'] }));
      }
      if (path === `/api/credits/history?asset_type=all&page=2&page_size=20&anchor=${oldAnchor}`) {
        return Promise.resolve(jsonResponse(historyPage([oldEntry], '1', 20, 1)));
      }
      if (
        path === '/api/credits/history?asset_type=all&page=1&page_size=20&category=charity' ||
        path === '/api/credits/history?asset_type=all&page=1&page_size=20&category=donation'
      ) {
        return Promise.resolve(new Response(null, { status }));
      }
      if (path === '/api/credits/history?asset_type=all&page=1&page_size=20') {
        return status === 200
          ? Promise.resolve(jsonResponse(historyPage([newEntry], '1', 20, 1)))
          : Promise.resolve(new Response(null, { status }));
      }
      throw new Error(`Unexpected request: ${path}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    const view = await renderWithProviders(
      <>
        <LocationProbe />
        <NavigationTools />
        <CreditsPage />
      </>,
      {
        station: 'user',
        role: 'user',
        route: `/credits?page=2&page_size=20&anchor=${oldAnchor}`,
      },
    );
    expect((await screen.findAllByText('+1')).length).toBeGreaterThan(0);

    testSession.accountID = 'account-2';
    view.rerender(
      <>
        <LocationProbe />
        <NavigationTools />
        <CreditsPage />
      </>,
    );
    await waitFor(() =>
      expect(
        requests.filter(
          (path) => path === '/api/credits/history?asset_type=all&page=1&page_size=20',
        ),
      ).toHaveLength(1),
    );
    expect(screen.getByTestId('location')).toHaveTextContent('/credits');
    expect(screen.queryByText('+2')).toBeInTheDocument();
    expect(requests).not.toContain(
      `/api/credits/history?asset_type=all&page=1&page_size=20&anchor=${oldAnchor}`,
    );

    status = 403;
    await view.user.click(screen.getByRole('button', { name: 'Open charity filter' }));
    expect(await screen.findByRole('alert')).toBeVisible();
    expect(screen.queryByText('+2')).not.toBeInTheDocument();
    status = 401;
    await view.user.click(screen.getByRole('button', { name: 'Open donation filter' }));
    expect(await screen.findByRole('alert')).toBeVisible();
    expect(screen.queryByText('+2')).not.toBeInTheDocument();
  });

  it('keeps pagination controls and totals for an empty result page', async () => {
    const requests: string[] = [];
    const fetchMock = vi.fn((input: string | URL | Request) => {
      const path = requestPath(input);
      requests.push(path);
      if (path === '/api/time-zones') {
        return Promise.resolve(jsonResponse({ version: 'go1.26.6-zoneinfo', zones: ['UTC'] }));
      }
      if (path === '/api/credits/history?asset_type=all&page=1&page_size=20') {
        return Promise.resolve(jsonResponse(historyPage([], '1', 20, 0, null)));
      }
      throw new Error(`Unexpected request: ${path}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    const view = await renderWithProviders(<CreditsPage />, {
      station: 'user',
      role: 'user',
      route: '/credits',
    });

    expect(await screen.findByText('No credit changes found')).toBeVisible();
    expect(screen.getByText('Page 1 of 1 · Total: 0')).toBeVisible();
    expect(screen.getByRole('combobox', { name: 'Items per page' })).toHaveValue('20');
    expect(screen.getByRole('button', { name: 'Previous' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Next' })).toBeDisabled();
    expect(requests).toEqual([
      '/api/time-zones',
      '/api/credits/history?asset_type=all&page=1&page_size=20',
    ]);
    view.unmount();
  });

  it('retains the selected page size across remounts when storage writes fail', async () => {
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('storage unavailable');
    });
    const anchor = entry(7).operation_id;
    const requests: string[] = [];
    const fetchMock = vi.fn((input: string | URL | Request) => {
      const path = requestPath(input);
      requests.push(path);
      if (path === '/api/time-zones') {
        return Promise.resolve(jsonResponse({ version: 'go1.26.6-zoneinfo', zones: ['UTC'] }));
      }
      if (path === '/api/credits/history?asset_type=all&page=1&page_size=20') {
        return Promise.resolve(jsonResponse(historyPage([entry(7)], '1', 20, 1, anchor)));
      }
      if (path === `/api/credits/history?asset_type=all&page=1&page_size=10&anchor=${anchor}`) {
        return Promise.resolve(jsonResponse(historyPage([entry(7)], '1', 10, 1, anchor)));
      }
      if (path === '/api/credits/history?asset_type=all&page=1&page_size=10') {
        return Promise.resolve(jsonResponse(historyPage([entry(7)], '1', 10, 1, anchor)));
      }
      throw new Error(`Unexpected request: ${path}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    const view = await renderWithProviders(<CreditsPage />, {
      station: 'user',
      role: 'user',
      route: '/credits',
    });
    await screen.findByText('Page 1 of 1 · Total: 1');
    const size = screen.getByRole('combobox', { name: 'Items per page' });
    await view.user.selectOptions(size, '10');
    await waitFor(() => expect(size).toHaveValue('10'));
    view.unmount();

    const remounted = await renderWithProviders(<CreditsPage />, {
      station: 'user',
      role: 'user',
      route: '/credits',
    });
    expect(await screen.findByText('Page 1 of 1 · Total: 1')).toBeVisible();
    expect(screen.getByRole('combobox', { name: 'Items per page' })).toHaveValue('10');
    expect(requests).toContain('/api/credits/history?asset_type=all&page=1&page_size=10');
    remounted.unmount();

    vi.restoreAllMocks();
    writePageSizePreference('user', 'credit-history', 20);
  });
});
