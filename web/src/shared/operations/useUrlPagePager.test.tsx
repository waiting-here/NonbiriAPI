import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { StrictMode, useEffect, useState } from 'react';
import { useLocation, useNavigate, MemoryRouter } from 'react-router';
import { describe, expect, it, vi } from 'vitest';
import type { PageSize } from './pageNumbers';
import { DEFAULT_PAGE, DEFAULT_PAGE_SIZE, type PageStation, usePagePager } from './usePagePager';
import { useUrlPagePager, type UseUrlPagePagerOptions } from './useUrlPagePager';

interface ProbeProps extends UseUrlPagePagerOptions {
  deriveFilterResetKey?: boolean;
}

interface PagerSnapshot {
  page: string;
  pageSize: number;
  scopeReady: boolean;
  search: string;
}

function PagerProbe({ deriveFilterResetKey = false, ...options }: ProbeProps) {
  const location = useLocation();
  const navigate = useNavigate();
  const resetKey = deriveFilterResetKey
    ? (new URLSearchParams(location.search).get('filter') ?? '')
    : options.resetKey;
  const pager = useUrlPagePager({ ...options, resetKey });

  return (
    <>
      <output data-testid="pager-state">
        {JSON.stringify({
          page: pager.page,
          pageSize: pager.pageSize,
          scopeReady: options.scopeReady ?? true,
          search: location.search,
        })}
      </output>
      <button type="button" data-testid="page-3" onClick={() => pager.setPage('3')}>
        page 3
      </button>
      <button type="button" data-testid="size-100" onClick={() => pager.setPageSize(100)}>
        size 100
      </button>
      <button type="button" data-testid="reset" onClick={pager.reset}>
        reset
      </button>
      <button type="button" data-testid="detail" onClick={() => navigate('/detail')}>
        detail
      </button>
      <button type="button" data-testid="list-page-2" onClick={() => navigate('/list?page=2')}>
        list page 2
      </button>
      <button type="button" data-testid="back" onClick={() => navigate(-1)}>
        back
      </button>
      <button type="button" data-testid="forward" onClick={() => navigate(1)}>
        forward
      </button>
    </>
  );
}

interface RouterHarnessProps extends ProbeProps {
  initialEntries: string[];
  initialIndex?: number;
  show?: boolean;
}

function RouterHarness({
  initialEntries,
  initialIndex,
  show = true,
  ...probe
}: RouterHarnessProps) {
  return (
    <StrictMode>
      <MemoryRouter initialEntries={initialEntries} initialIndex={initialIndex}>
        {show ? <PagerProbe {...probe} /> : null}
      </MemoryRouter>
    </StrictMode>
  );
}

function PreferenceWriter({
  station,
  listType,
}: Pick<UseUrlPagePagerOptions, 'station' | 'listType'>) {
  const pager = usePagePager({ station, listType, scopeKey: 'other-view' });
  return (
    <button type="button" data-testid="other-size-100" onClick={() => pager.setPageSize(100)}>
      other view size 100
    </button>
  );
}

interface AsyncAuthorityProbeProps extends Omit<ProbeProps, 'scopeKey' | 'scopeReady'> {
  initialScopeKey?: string;
  resolvedScopeKey?: string;
}

function AsyncAuthorityProbe({
  initialScopeKey = 'anonymous',
  resolvedScopeKey = 'account-a',
  ...probe
}: AsyncAuthorityProbeProps) {
  const [authority, setAuthority] = useState({
    scopeKey: initialScopeKey,
    scopeReady: false,
  });

  useEffect(() => {
    let active = true;
    queueMicrotask(() => {
      if (active) setAuthority({ scopeKey: resolvedScopeKey, scopeReady: true });
    });
    return () => {
      active = false;
    };
  }, [resolvedScopeKey]);

  return (
    <>
      <PagerProbe {...probe} scopeKey={authority.scopeKey} scopeReady={authority.scopeReady} />
      <PreferenceWriter station={probe.station} listType={probe.listType} />
      <button
        type="button"
        data-testid="switch-account"
        onClick={() => setAuthority({ scopeKey: 'account-b', scopeReady: true })}
      >
        switch account
      </button>
    </>
  );
}

interface AsyncAuthorityRouterHarnessProps extends AsyncAuthorityProbeProps {
  initialEntries: string[];
  initialIndex?: number;
}

function AsyncAuthorityRouterHarness({
  initialEntries,
  initialIndex,
  ...probe
}: AsyncAuthorityRouterHarnessProps) {
  return (
    <StrictMode>
      <MemoryRouter initialEntries={initialEntries} initialIndex={initialIndex}>
        <AsyncAuthorityProbe {...probe} />
      </MemoryRouter>
    </StrictMode>
  );
}

function snapshot(): PagerSnapshot {
  return JSON.parse(screen.getByTestId('pager-state').textContent ?? '{}') as PagerSnapshot;
}

function options(overrides: Partial<ProbeProps> = {}): ProbeProps {
  return {
    station: 'user' as PageStation,
    listType: 'url-catalog',
    scopeKey: 'account-a',
    resetKey: 'filter=all',
    ...overrides,
  };
}

describe('useUrlPagePager', () => {
  it.each([10, 20, 50, 100] as const)(
    'accepts the canonical %d page-size option',
    (pageSize: PageSize) => {
      render(
        <RouterHarness
          {...options({ listType: `url-four-size-${pageSize}` })}
          initialEntries={[`/list?page=2&page_size=${pageSize}`]}
        />,
      );

      expect(snapshot()).toMatchObject({ page: '2', pageSize });
    },
  );

  it('restores URL page state, reuses the list preference, preserves other query values, and resets explicitly', async () => {
    window.localStorage.setItem('nonbiri:user:url-catalog-page-size:v1', '50');
    const user = userEvent.setup();
    render(
      <RouterHarness
        {...options()}
        initialEntries={['/list?page=2&filter=active&tag=one&tag=two']}
      />,
    );

    expect(snapshot()).toMatchObject({ page: '2', pageSize: 50 });

    await user.click(screen.getByTestId('page-3'));
    expect(snapshot()).toMatchObject({ page: '3', pageSize: 50 });
    let params = new URLSearchParams(snapshot().search);
    expect(params.get('page_size')).toBe('50');
    expect(params.get('filter')).toBe('active');
    expect(params.getAll('tag')).toEqual(['one', 'two']);

    await user.click(screen.getByTestId('reset'));
    expect(snapshot()).toMatchObject({ page: DEFAULT_PAGE, pageSize: 50 });
    params = new URLSearchParams(snapshot().search);
    expect(params.get('filter')).toBe('active');
    expect(params.getAll('tag')).toEqual(['one', 'two']);

    await user.click(screen.getByTestId('size-100'));
    expect(snapshot()).toMatchObject({ page: DEFAULT_PAGE, pageSize: 100 });
    expect(window.localStorage.getItem('nonbiri:user:url-catalog-page-size:v1')).toBe('100');
    params = new URLSearchParams(snapshot().search);
    expect(params.get('page')).toBe(DEFAULT_PAGE);
    expect(params.get('page_size')).toBe('100');
    expect(params.get('filter')).toBe('active');
  });

  it('keeps a pushed page through detail navigation and restores it on POP and remount', async () => {
    const user = userEvent.setup();
    const view = render(
      <RouterHarness
        {...options({ listType: 'url-detail-return' })}
        initialEntries={['/list?page=2&page_size=50&filter=all']}
      />,
    );

    await user.click(screen.getByTestId('page-3'));
    expect(snapshot()).toMatchObject({ page: '3', pageSize: 50 });
    await user.click(screen.getByTestId('detail'));
    await user.click(screen.getByTestId('back'));
    expect(snapshot()).toMatchObject({ page: '3', pageSize: 50 });

    await user.click(screen.getByTestId('forward'));
    await user.click(screen.getByTestId('back'));
    expect(snapshot()).toMatchObject({ page: '3', pageSize: 50 });

    view.rerender(
      <RouterHarness
        {...options({ listType: 'url-detail-return' })}
        initialEntries={['/list?page=2&page_size=50&filter=all']}
        show={false}
      />,
    );
    view.rerender(
      <RouterHarness
        {...options({ listType: 'url-detail-return' })}
        initialEntries={['/list?page=2&page_size=50&filter=all']}
      />,
    );
    expect(snapshot()).toMatchObject({ page: '3', pageSize: 50 });
  });

  it('preserves the URL page through async authority confirmation and pins its size across a return', async () => {
    const listType = 'url-async-authority-return';
    const storageKey = `nonbiri:user:${listType}-page-size:v1`;
    window.localStorage.setItem(storageKey, '50');
    const user = userEvent.setup();
    const view = render(
      <AsyncAuthorityRouterHarness
        {...options({ listType })}
        initialEntries={['/list?page=2&filter=active&tag=one']}
      />,
    );

    expect(snapshot()).toMatchObject({ page: '2', pageSize: 50, scopeReady: false });
    await waitFor(() =>
      expect(snapshot()).toMatchObject({ page: '2', pageSize: 50, scopeReady: true }),
    );

    await user.click(screen.getByTestId('page-3'));
    expect(snapshot()).toMatchObject({ page: '3', pageSize: 50, scopeReady: true });
    expect(new URLSearchParams(snapshot().search).get('page_size')).toBe('50');

    await user.click(screen.getByTestId('detail'));
    await user.click(screen.getByTestId('other-size-100'));
    expect(window.localStorage.getItem(storageKey)).toBe('100');
    await user.click(screen.getByTestId('back'));
    expect(snapshot()).toMatchObject({ page: '3', pageSize: 50, scopeReady: true });

    const returnedSearch = snapshot().search;
    view.unmount();
    render(
      <AsyncAuthorityRouterHarness
        {...options({ listType })}
        initialEntries={[`/list${returnedSearch}`]}
      />,
    );
    await waitFor(() =>
      expect(snapshot()).toMatchObject({ page: '3', pageSize: 50, scopeReady: true }),
    );
    await user.click(screen.getByTestId('switch-account'));
    await waitFor(() => expect(snapshot()).toMatchObject({ page: DEFAULT_PAGE, scopeReady: true }));
    expect(new URLSearchParams(snapshot().search).get('page')).toBe(DEFAULT_PAGE);
  });

  it('normalizes page immediately when scope or committed filters change', async () => {
    const user = userEvent.setup();
    const view = render(
      <RouterHarness
        {...options({ listType: 'url-reset-boundary' })}
        initialEntries={['/list?page=5&page_size=50&filter=all']}
      />,
    );

    view.rerender(
      <RouterHarness
        {...options({ listType: 'url-reset-boundary', scopeKey: 'account-b' })}
        initialEntries={['/list?page=5&page_size=50&filter=all']}
      />,
    );
    await waitFor(() => expect(snapshot().page).toBe(DEFAULT_PAGE));
    expect(new URLSearchParams(snapshot().search).get('page')).toBe(DEFAULT_PAGE);

    await user.click(screen.getByTestId('page-3'));
    view.rerender(
      <RouterHarness
        {...options({
          listType: 'url-reset-boundary',
          scopeKey: 'account-b',
          resetKey: 'filter=closed',
        })}
        initialEntries={['/list?page=5&page_size=50&filter=all']}
      />,
    );
    await waitFor(() => expect(snapshot().page).toBe(DEFAULT_PAGE));
    expect(new URLSearchParams(snapshot().search).get('page')).toBe(DEFAULT_PAGE);
  });

  it('supports nested parameter names without touching outer pagination values', async () => {
    const user = userEvent.setup();
    render(
      <RouterHarness
        {...options({
          listType: 'url-nested',
          pageParam: 'filters.page',
          pageSizeParam: 'filters.page_size',
        })}
        initialEntries={[
          '/list?page=9&page_size=100&filters.page=2&filters.page_size=50&other=keep',
        ]}
      />,
    );

    expect(snapshot()).toMatchObject({ page: '2', pageSize: 50 });
    await user.click(screen.getByTestId('page-3'));
    const params = new URLSearchParams(snapshot().search);
    expect(params.get('page')).toBe('9');
    expect(params.get('page_size')).toBe('100');
    expect(params.get('filters.page')).toBe('3');
    expect(params.get('filters.page_size')).toBe('50');
    expect(params.get('other')).toBe('keep');
  });

  it('rejects invalid, repeated, and oversized URL values and replaces them with safe values', async () => {
    window.localStorage.setItem('nonbiri:user:url-invalid-page-size-page-size:v1', '100');
    render(
      <RouterHarness
        {...options({ listType: 'url-invalid-page-size' })}
        initialEntries={['/list?page=99999999999&page_size=15&page_size=50&keep=1']}
      />,
    );

    expect(snapshot()).toMatchObject({ page: DEFAULT_PAGE, pageSize: 100 });
    await waitFor(() => {
      const params = new URLSearchParams(snapshot().search);
      expect(params.getAll('page')).toEqual([DEFAULT_PAGE]);
      expect(params.getAll('page_size')).toEqual(['100']);
    });
    expect(new URLSearchParams(snapshot().search).get('keep')).toBe('1');
  });

  it('retains the shared session preference when storage is disabled', async () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('storage unavailable');
    });
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('storage unavailable');
    });
    const user = userEvent.setup();
    const view = render(
      <RouterHarness
        {...options({ listType: 'url-storage-disabled' })}
        initialEntries={['/list?page=2']}
      />,
    );

    expect(snapshot()).toMatchObject({ page: '2', pageSize: DEFAULT_PAGE_SIZE });
    await user.click(screen.getByTestId('size-100'));
    expect(snapshot()).toMatchObject({ page: DEFAULT_PAGE, pageSize: 100 });

    await user.click(screen.getByTestId('detail'));
    await user.click(screen.getByTestId('list-page-2'));
    expect(snapshot()).toMatchObject({ page: '2', pageSize: 100 });

    view.rerender(
      <RouterHarness
        {...options({ listType: 'url-storage-disabled' })}
        initialEntries={['/list?page=2']}
        show={false}
      />,
    );
    view.rerender(
      <RouterHarness
        {...options({ listType: 'url-storage-disabled' })}
        initialEntries={['/list?page=2']}
      />,
    );
    expect(snapshot()).toMatchObject({ page: '2', pageSize: 100 });
  });

  it('resets the page when switching lists while keeping each list preference independent', async () => {
    window.localStorage.setItem('nonbiri:user:url-list-a-page-size:v1', '50');
    window.localStorage.setItem('nonbiri:user:url-list-b-page-size:v1', '100');
    const view = render(
      <RouterHarness {...options({ listType: 'url-list-a' })} initialEntries={['/list?page=4']} />,
    );

    expect(snapshot()).toMatchObject({ page: '4', pageSize: 50 });
    view.rerender(
      <RouterHarness {...options({ listType: 'url-list-b' })} initialEntries={['/list?page=4']} />,
    );
    await waitFor(() => expect(snapshot()).toMatchObject({ page: DEFAULT_PAGE, pageSize: 100 }));

    view.rerender(
      <RouterHarness {...options({ listType: 'url-list-a' })} initialEntries={['/list?page=4']} />,
    );
    await waitFor(() => expect(snapshot()).toMatchObject({ page: DEFAULT_PAGE, pageSize: 50 }));
  });

  it('does not treat POP filter restoration as a new filter reset', async () => {
    const user = userEvent.setup();
    render(
      <RouterHarness
        {...options({ listType: 'url-pop-filter', deriveFilterResetKey: true })}
        initialEntries={[
          '/list?page=2&page_size=50&filter=old',
          '/list?page=3&page_size=50&filter=new',
        ]}
        initialIndex={1}
      />,
    );

    expect(snapshot()).toMatchObject({ page: '3', pageSize: 50 });
    await user.click(screen.getByTestId('back'));
    expect(snapshot()).toMatchObject({ page: '2', pageSize: 50 });
    await user.click(screen.getByTestId('forward'));
    expect(snapshot()).toMatchObject({ page: '3', pageSize: 50 });
  });

  it('does not import an explicit URL size from a different list kind', async () => {
    window.localStorage.setItem('nonbiri:user:url-explicit-list-b-page-size:v1', '100');
    const view = render(
      <RouterHarness
        {...options({ listType: 'url-explicit-list-a' })}
        initialEntries={['/list?page=4&page_size=20']}
      />,
    );
    expect(snapshot()).toMatchObject({ page: '4', pageSize: 20 });
    view.rerender(
      <RouterHarness
        {...options({ listType: 'url-explicit-list-b' })}
        initialEntries={['/list?page=4&page_size=20']}
      />,
    );
    expect(snapshot()).toMatchObject({ page: '1', pageSize: 100 });
    await waitFor(() =>
      expect(new URLSearchParams(snapshot().search).get('page_size')).toBe('100'),
    );
  });
});
