import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, renderHook, waitFor } from '@testing-library/react';
import { type ReactNode } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  useNumberedCatalog,
  useNumberedEndpoints,
  useNumberedEndpointKeys,
} from './numberedQueries';
import type { PageWindow } from './pageTypes';

function fixture(path: string): Record<string, unknown> {
  return JSON.parse(readFileSync(resolve(process.cwd(), '..', path), 'utf8')) as Record<
    string,
    unknown
  >;
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
  });
}

function numbered(
  data: readonly unknown[],
  window: PageWindow = { page: '1', pageSize: 20 },
  totalItems = data.length,
  totalPages = '1',
) {
  return {
    data,
    next_cursor: null,
    pagination: {
      page: window.page,
      page_size: window.pageSize,
      total_items: String(totalItems),
      total_pages: totalPages,
    },
  };
}

function catalogPage(
  manualEntries: readonly unknown[],
  window: PageWindow = { page: '1', pageSize: 20 },
) {
  return {
    evidence: {
      state: 'unknown',
      revision: '1',
      result: null,
      safe_class: 'none',
      observed_at: null,
      count: null,
    },
    automatic_entries: [],
    manual_entries: manualEntries,
    next_cursor: null,
    pagination: {
      page: window.page,
      page_size: window.pageSize,
      total_items: String(manualEntries.length),
      total_pages: '1',
    },
  };
}

function createClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: Number.POSITIVE_INFINITY },
    },
  });
}

function wrapper(client: QueryClient) {
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
}

const endpoint = fixture('internal/resources/testdata/endpoint.json');
const endpointKey = fixture('internal/resources/testdata/endpoint_key.json');
const manualEntry = {
  id: '41',
  source_type: 'manual',
  upstream_model_id: 'Vendor/Manual',
  provider: 'Vendor',
  source_revision: '1',
  pair_revision: '1',
  created_at: 1_700_000_000,
  updated_at: 1_700_000_000,
};

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('numbered core query hooks', () => {
  it('cancels superseded filters without retaining another filter or account as placeholder', async () => {
    const pending: Array<{
      resolve: (value: Response) => void;
      signal?: AbortSignal | null;
      path: string;
    }> = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(
        (input: string | URL | Request, init?: RequestInit) =>
          new Promise<Response>((resolve) => {
            pending.push({ resolve, signal: init?.signal, path: String(input) });
          }),
      ),
    );
    const client = createClient();
    const rendered = renderHook(
      ({ account, search }) =>
        useNumberedEndpoints(account, { page: '1', pageSize: 20 }, true, {
          q: search,
          source: 'custom',
        }),
      {
        initialProps: { account: 'a', search: 'first' },
        wrapper: wrapper(client),
      },
    );
    await waitFor(() => expect(pending).toHaveLength(1));
    act(() => pending[0].resolve(jsonResponse(numbered([endpoint]))));
    await waitFor(() => expect(rendered.result.current.data?.data).toHaveLength(1));
    rendered.rerender({ account: 'a', search: 'second' });
    expect(rendered.result.current.data).toBeUndefined();
    await waitFor(() => expect(pending).toHaveLength(2));
    rendered.rerender({ account: 'a', search: 'third' });
    await waitFor(() => expect(pending).toHaveLength(3));
    expect(pending[1].signal?.aborted).toBe(true);
    act(() => pending[1].resolve(jsonResponse(numbered([endpoint]))));
    expect(rendered.result.current.data).toBeUndefined();
    act(() => pending[2].resolve(jsonResponse(numbered([]))));
    await waitFor(() => expect(rendered.result.current.data?.data).toEqual([]));
    rendered.rerender({ account: 'b', search: 'third' });
    expect(rendered.result.current.data).toBeUndefined();
    await waitFor(() => expect(pending).toHaveLength(4));
    expect(pending[3].path).toContain('q=third&source=custom');
    rendered.unmount();
    expect(pending[3].signal?.aborted).toBe(true);
  });

  it('keeps every page query disabled when the account scope is empty', () => {
    const fetchMock = vi.fn<typeof fetch>();
    vi.stubGlobal('fetch', fetchMock);
    const client = createClient();
    const rendered = renderHook(() => useNumberedEndpoints('', { page: '1', pageSize: 20 }, true), {
      wrapper: wrapper(client),
    });

    expect(rendered.result.current.fetchStatus).toBe('idle');
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('uses page keys and forwards an AbortSignal while retaining the same-account page', async () => {
    const requests: Array<{ path: string; signal: AbortSignal | null | undefined }> = [];
    let resolveSecond!: (response: Response) => void;
    const second = new Promise<Response>((resolve) => {
      resolveSecond = resolve;
    });
    const fetchMock = vi.fn((input: string | URL | Request, init?: RequestInit) => {
      const path = String(input);
      requests.push({ path, signal: init?.signal });
      if (requests.length === 1) return Promise.resolve(jsonResponse(numbered([endpoint])));
      return second;
    });
    vi.stubGlobal('fetch', fetchMock);
    const client = createClient();
    const rendered = renderHook(
      ({ page }: { page: string }) => useNumberedEndpoints('account-a', { page, pageSize: 20 }),
      { initialProps: { page: '1' }, wrapper: wrapper(client) },
    );

    await waitFor(() => expect(rendered.result.current.data?.data[0]?.id).toBe('11'));
    expect(requests[0]?.path).toBe('/api/endpoints?page=1&page_size=20');
    expect(requests[0]?.signal).toBeInstanceOf(AbortSignal);

    await act(async () => {
      rendered.rerender({ page: '2' });
    });
    expect(rendered.result.current.data?.data[0]?.id).toBe('11');
    expect(rendered.result.current.isFetching).toBe(true);
    expect(requests[1]?.path).toBe('/api/endpoints?page=2&page_size=20');
    resolveSecond(jsonResponse(numbered([endpoint], { page: '2', pageSize: 20 }, 21, '2')));
    await waitFor(() => expect(rendered.result.current.data?.pagination.page).toBe('2'));
  });

  it('clears placeholder data when the account or key parent changes', async () => {
    let resolveNext!: (response: Response) => void;
    const next = new Promise<Response>((resolve) => {
      resolveNext = resolve;
    });
    let calls = 0;
    const fetchMock = vi.fn<typeof fetch>();
    fetchMock.mockImplementation(async () => {
      calls += 1;
      if (calls === 1) return Promise.resolve(jsonResponse(numbered([endpointKey])));
      return next;
    });
    vi.stubGlobal('fetch', fetchMock);
    const client = createClient();
    const rendered = renderHook(
      ({ accountId, endpointId }: { accountId: string; endpointId: string }) =>
        useNumberedEndpointKeys(accountId, endpointId, { page: '1', pageSize: 20 }),
      {
        initialProps: { accountId: 'account-a', endpointId: '11' },
        wrapper: wrapper(client),
      },
    );

    await waitFor(() => expect(rendered.result.current.data?.data[0]?.id).toBe('21'));
    await act(async () => {
      rendered.rerender({ accountId: 'account-b', endpointId: '12' });
    });
    expect(rendered.result.current.data).toBeUndefined();
    expect(rendered.result.current.isFetching).toBe(true);
    expect(String(fetchMock.mock.calls[1]?.[0])).toBe('/api/endpoints/12/keys?page=1&page_size=20');
    resolveNext(jsonResponse(numbered([{ ...endpointKey, endpoint_id: '12' }])));
    await waitFor(() => expect(rendered.result.current.data?.data[0]?.id).toBe('21'));
  });

  it('isolates catalog results by source and search and sends the active filters', async () => {
    let reply!: (response: Response) => void;
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(jsonResponse(catalogPage([manualEntry])))
      .mockImplementation(() => new Promise<Response>((resolve) => (reply = resolve)));
    vi.stubGlobal('fetch', fetchMock);
    const client = createClient();
    const rendered = renderHook(
      ({ source, search }: { source: 'manual' | 'automatic'; search: string }) =>
        useNumberedCatalog(
          'account-a',
          '11',
          '21',
          source,
          { page: '1', pageSize: 20 },
          true,
          search,
        ),
      { initialProps: { source: 'manual', search: '' }, wrapper: wrapper(client) },
    );
    const path = '/api/endpoints/11/keys/21/models?page=1&page_size=20';
    const scope = ['user', 'core', 'account', 'account-a', 'catalog', '11', '21', 'page'];

    await waitFor(() => expect(rendered.result.current.data?.manual_entries[0]?.id).toBe('41'));
    expect(fetchMock.mock.calls[0]?.[0]).toBe(path + '&source=manual');
    rendered.rerender({ source: 'manual', search: 'Vendor/Manual' });
    expect(rendered.result.current.data).toBeUndefined();
    await waitFor(() =>
      expect(fetchMock.mock.calls[1]?.[0]).toBe(path + '&source=manual&q=Vendor%2FManual'),
    );
    act(() => reply(jsonResponse(catalogPage([manualEntry]))));
    await waitFor(() => expect(rendered.result.current.data?.manual_entries[0]?.id).toBe('41'));

    rendered.rerender({ source: 'automatic', search: 'Vendor/Manual' });
    expect(rendered.result.current.data).toBeUndefined();
    await waitFor(() =>
      expect(fetchMock.mock.calls[2]?.[0]).toBe(path + '&source=automatic&q=Vendor%2FManual'),
    );
    act(() => reply(jsonResponse(catalogPage([]))));
    await waitFor(() => expect(rendered.result.current.data?.manual_entries).toEqual([]));
    expect(
      client
        .getQueryCache()
        .getAll()
        .map((query) => query.queryKey),
    ).toEqual([
      [...scope, 'manual', '', '1', 20],
      [...scope, 'manual', 'Vendor/Manual', '1', 20],
      [...scope, 'automatic', 'Vendor/Manual', '1', 20],
    ]);
  });
});
