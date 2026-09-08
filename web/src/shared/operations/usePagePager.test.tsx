import { act, renderHook } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { PAGE_SIZES, type PageSize } from './pageNumbers';
import { DEFAULT_PAGE, DEFAULT_PAGE_SIZE, usePagePager } from './usePagePager';

interface PagerProps {
  station: 'admin' | 'user';
  listType: string;
  scopeKey: string;
  resetKey?: string;
}

function props(overrides: Partial<PagerProps> = {}): PagerProps {
  return {
    station: 'user',
    listType: 'catalog',
    scopeKey: 'account-a',
    ...overrides,
  };
}

describe('usePagePager', () => {
  it('starts at page one, restores a valid list preference, and persists only page size', () => {
    window.localStorage.setItem('nonbiri:user:catalog-page-size:v1', '50');

    const { result } = renderHook((options: PagerProps) => usePagePager(options), {
      initialProps: props(),
    });

    expect(result.current.page).toBe(DEFAULT_PAGE);
    expect(result.current.pageSize).toBe(50);

    act(() => result.current.setPage('12'));
    expect(result.current.page).toBe('12');

    act(() => result.current.setPageSize(100));
    expect(result.current.page).toBe(DEFAULT_PAGE);
    expect(result.current.pageSize).toBe(100);
    expect(window.localStorage.getItem('nonbiri:user:catalog-page-size:v1')).toBe('100');
    expect(Object.keys(window.localStorage)).toEqual(['nonbiri:user:catalog-page-size:v1']);
  });

  it.each(['', '0', '15', '020', '20.0', '1e2'])('ignores an invalid stored size: %s', (stored) => {
    window.localStorage.setItem('nonbiri:user:catalog-page-size:v1', stored);

    const { result } = renderHook((options: PagerProps) => usePagePager(options), {
      initialProps: props(),
    });

    expect(result.current.pageSize).toBe(DEFAULT_PAGE_SIZE);
  });

  it('keeps list types and stations independent while sharing size across scopes', () => {
    const user = renderHook((options: PagerProps) => usePagePager(options), {
      initialProps: props(),
    });
    const admin = renderHook((options: PagerProps) => usePagePager(options), {
      initialProps: props({ station: 'admin', scopeKey: 'admin-session' }),
    });
    const otherList = renderHook((options: PagerProps) => usePagePager(options), {
      initialProps: props({ listType: 'endpoints' }),
    });

    act(() => user.result.current.setPageSize(50));
    act(() => user.result.current.setPage('4'));

    expect(user.result.current).toMatchObject({ page: '4', pageSize: 50 });
    expect(admin.result.current).toMatchObject({ page: DEFAULT_PAGE, pageSize: DEFAULT_PAGE_SIZE });
    expect(otherList.result.current).toMatchObject({
      page: DEFAULT_PAGE,
      pageSize: DEFAULT_PAGE_SIZE,
    });

    user.rerender(props({ scopeKey: 'account-b' }));
    expect(user.result.current).toMatchObject({ page: DEFAULT_PAGE, pageSize: 50 });
    user.rerender(props({ scopeKey: 'account-a' }));
    expect(user.result.current).toMatchObject({ page: DEFAULT_PAGE, pageSize: 50 });
  });

  it('returns page one immediately when scope or committed filters change', () => {
    const { result, rerender } = renderHook((options: PagerProps) => usePagePager(options), {
      initialProps: props({ resetKey: 'status=all' }),
    });

    act(() => result.current.setPage('8'));
    expect(result.current.page).toBe('8');

    rerender(props({ scopeKey: 'account-b', resetKey: 'status=all' }));
    expect(result.current.page).toBe(DEFAULT_PAGE);
    act(() => result.current.setPage('6'));
    rerender(props({ scopeKey: 'account-b', resetKey: 'status=closed' }));
    expect(result.current.page).toBe(DEFAULT_PAGE);
    expect(result.current.pageSize).toBe(DEFAULT_PAGE_SIZE);
  });

  it('resets page while preserving size and ignores invalid page and size values', () => {
    const { result } = renderHook((options: PagerProps) => usePagePager(options), {
      initialProps: props(),
    });

    act(() => result.current.setPageSize(10));
    act(() => result.current.setPage('2147483647'));
    expect(result.current).toMatchObject({ page: '2147483647', pageSize: 10 });

    for (const invalidPage of ['0', '01', '2147483648', '1.0', '1e2', '']) {
      act(() => result.current.setPage(invalidPage));
    }
    expect(result.current.page).toBe('2147483647');

    for (const invalidSize of [0, 15, 20.5, 101] as unknown as PageSize[]) {
      act(() => result.current.setPageSize(invalidSize));
    }
    expect(result.current.pageSize).toBe(10);
    expect(window.localStorage.getItem('nonbiri:user:catalog-page-size:v1')).toBe('10');

    act(() => result.current.reset());
    expect(result.current).toMatchObject({ page: DEFAULT_PAGE, pageSize: 10 });
    expect(PAGE_SIZES).toEqual([10, 20, 50, 100]);
  });

  it('falls back to the current session when storage cannot be read or written', () => {
    const getItem = vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('storage unavailable');
    });
    const setItem = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('storage unavailable');
    });

    const { result, unmount } = renderHook((options: PagerProps) => usePagePager(options), {
      initialProps: props({ listType: 'issues' }),
    });

    expect(result.current.pageSize).toBe(DEFAULT_PAGE_SIZE);
    act(() => result.current.setPageSize(100));
    act(() => result.current.setPage('3'));
    expect(result.current).toMatchObject({ page: '3', pageSize: 100 });
    unmount();

    const remounted = renderHook((options: PagerProps) => usePagePager(options), {
      initialProps: props({ listType: 'issues', scopeKey: 'account-b' }),
    });
    expect(remounted.result.current).toMatchObject({ page: DEFAULT_PAGE, pageSize: 100 });
    expect(getItem).toHaveBeenCalled();
    expect(setItem).toHaveBeenCalledWith('nonbiri:user:issues-page-size:v1', '100');
  });

  it('shares an unavailable-storage preference across mounts and list switches', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('storage unavailable');
    });
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('storage unavailable');
    });

    const first = renderHook((options: PagerProps) => usePagePager(options), {
      initialProps: props({ listType: 'offline-catalog', scopeKey: 'account-a' }),
    });
    act(() => first.result.current.setPageSize(50));

    const second = renderHook((options: PagerProps) => usePagePager(options), {
      initialProps: props({ listType: 'offline-catalog', scopeKey: 'account-b' }),
    });
    expect(second.result.current.pageSize).toBe(50);

    second.rerender(props({ listType: 'offline-endpoints', scopeKey: 'account-b' }));
    expect(second.result.current).toMatchObject({
      page: DEFAULT_PAGE,
      pageSize: DEFAULT_PAGE_SIZE,
    });
    act(() => second.result.current.setPageSize(100));

    second.rerender(props({ listType: 'offline-catalog', scopeKey: 'account-b' }));
    expect(second.result.current).toMatchObject({ page: DEFAULT_PAGE, pageSize: 50 });

    first.unmount();
    second.unmount();
    const remounted = renderHook((options: PagerProps) => usePagePager(options), {
      initialProps: props({ listType: 'offline-catalog', scopeKey: 'account-c' }),
    });
    expect(remounted.result.current.pageSize).toBe(50);
  });

  it('uses a usable store as the source of truth after a value is cleared or invalidated', () => {
    const listType = 'clearable-catalog';
    const storageKey = `nonbiri:user:${listType}-page-size:v1`;
    window.localStorage.setItem(storageKey, '50');

    const first = renderHook((options: PagerProps) => usePagePager(options), {
      initialProps: props({ listType }),
    });
    expect(first.result.current.pageSize).toBe(50);
    first.unmount();

    window.localStorage.removeItem(storageKey);
    const cleared = renderHook((options: PagerProps) => usePagePager(options), {
      initialProps: props({ listType, scopeKey: 'account-b' }),
    });
    expect(cleared.result.current.pageSize).toBe(DEFAULT_PAGE_SIZE);
    act(() => cleared.result.current.setPageSize(100));
    window.localStorage.setItem(storageKey, 'not-a-page-size');
    cleared.unmount();

    const invalid = renderHook((options: PagerProps) => usePagePager(options), {
      initialProps: props({ listType, scopeKey: 'account-c' }),
    });
    expect(invalid.result.current.pageSize).toBe(DEFAULT_PAGE_SIZE);
  });

  it('keeps a preference across remounts when storage reads work but writes fail', () => {
    const setItem = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('storage is read-only');
    });
    const listType = 'read-only-catalog';

    const first = renderHook((options: PagerProps) => usePagePager(options), {
      initialProps: props({ listType }),
    });
    act(() => first.result.current.setPageSize(50));
    first.unmount();

    const remounted = renderHook((options: PagerProps) => usePagePager(options), {
      initialProps: props({ listType, scopeKey: 'account-b' }),
    });
    expect(remounted.result.current.pageSize).toBe(50);
    expect(setItem).toHaveBeenCalledWith(`nonbiri:user:${listType}-page-size:v1`, '50');
  });
});
