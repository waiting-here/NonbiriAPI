import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, renderHook, waitFor } from '@testing-library/react';
import type { ReactNode } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { coreKeys } from './queries';
import { listKeyBindingsPage, useKeyBindingsPage } from './browseData';
import {
  normalizeEndpoint,
  normalizeEndpointKey,
  normalizeKeyBindingView,
  normalizeModel,
} from './normalizers';

const BASE_BINDING = {
  id: '51',
  model_id: '41',
  model_full_name: 'logical/primary',
  endpoint_id: '11',
  endpoint_key_id: '21',
  endpoint_base_url: 'https://example.com/v1',
  connector_type: 'openai-compatible',
  endpoint_note: 'endpoint note',
  display_head: 'head',
  display_tail: 'tail',
  key_note: 'key note',
  upstream_model_id: 'Vendor/Model',
  ord: 0,
  max_concurrency: 0,
  max_rpm: 0,
  state: 'available',
} as const;

const DISCOVERY = {
  state: 'unknown',
  revision: '1',
  result: null,
  safe_class: 'none',
  observed_at: null,
  count: null,
} as const;

const OLD_ENDPOINT = {
  id: '11',
  connector_type: 'openai-compatible',
  base_url: 'https://example.com/v1',
  origin: { kind: 'custom' },
  note: 'endpoint note',
  enabled: true,
  revision: '3',
  key_count: '2',
  created_at: 1_700_000_000,
  updated_at: 1_700_000_001,
};

const OLD_KEY = {
  id: '21',
  endpoint_id: '11',
  display_head: 'head',
  display_tail: 'tail',
  note: 'key note',
  enabled: true,
  force_store_false: false,
  max_concurrency: 0,
  max_rpm: 0,
  suspension_state: 'none',
  revision: '4',
  created_at: 1_700_000_000,
  updated_at: 1_700_000_001,
};

const OLD_MODEL = {
  id: '41',
  provider: 'logical',
  model: 'primary',
  full_name: 'logical/primary',
  route_strategy: 'ordered',
  silent_retry: true,
  flatten_tool_calls: false,
  revision: '5',
  binding_revision: '6',
  binding_count: '1',
  created_at: 1_700_000_000,
  updated_at: 1_700_000_001,
};

function endpointBrowse() {
  return {
    model_count: '1',
    available_key_count: '1',
    state: 'available',
  };
}

function keyBrowse(preview: unknown[] = [BASE_BINDING]) {
  return {
    model_count: '1',
    binding_count: '1',
    donation_eligibility: 'eligible',
    available_binding_count: '1',
    discovery: DISCOVERY,
    preview,
  };
}

function modelBrowse(preview: unknown[] = [BASE_BINDING]) {
  return { available_binding_count: '1', preview };
}

function binding(index: number, upstreamModel = `Vendor/Model${index}`) {
  return {
    ...BASE_BINDING,
    id: String(51 + index),
    model_id: String(41 + index),
    upstream_model_id: upstreamModel,
    ord: index,
  };
}

function jsonResponse(payload: unknown, status = 200): Response {
  return new Response(JSON.stringify(payload), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function pageResponse(
  data: readonly unknown[],
  page: string,
  pageSize: number,
  totalItems = data.length,
) {
  const totalPages = totalItems === 0 ? 1 : Math.ceil(totalItems / pageSize);
  const actualPage = Math.min(Number(page), totalPages);
  return {
    data,
    next_cursor: null,
    pagination: {
      page: String(actualPage),
      page_size: pageSize,
      total_items: String(totalItems),
      total_pages: String(totalPages),
    },
  };
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('resource browse summary normalizers', () => {
  it('keeps old endpoint, key, and model DTOs readable without a browse field', () => {
    expect(normalizeEndpoint(OLD_ENDPOINT)).not.toHaveProperty('browse');
    expect(normalizeEndpointKey(OLD_KEY)).not.toHaveProperty('browse');
    expect(normalizeModel(OLD_MODEL)).not.toHaveProperty('browse');
  });

  it('decodes endpoint, key, and model browse projections with the shared binding view', () => {
    const endpoint = normalizeEndpoint({ ...OLD_ENDPOINT, browse: endpointBrowse() });
    const key = normalizeEndpointKey({ ...OLD_KEY, browse: keyBrowse() });
    const model = normalizeModel({ ...OLD_MODEL, browse: modelBrowse() });

    expect(endpoint.browse).toEqual(endpointBrowse());
    expect(key.browse).toEqual({ ...keyBrowse(), preview: [BASE_BINDING] });
    expect(model.browse).toEqual({ ...modelBrowse(), preview: [BASE_BINDING] });
  });

  it('rejects unknown/private browse fields and invalid binding view state', () => {
    expect(() =>
      normalizeEndpoint({ ...OLD_ENDPOINT, browse: { ...endpointBrowse(), private: true } }),
    ).toThrow(/endpoint browse summary/i);
    expect(() =>
      normalizeEndpointKey({
        ...OLD_KEY,
        browse: { ...keyBrowse(), preview: [{ ...BASE_BINDING, secret: 'nope' }] },
      }),
    ).toThrow(/key binding view/i);
    expect(() => normalizeKeyBindingView({ ...BASE_BINDING, state: 'other' })).toThrow(
      /key binding state/i,
    );
  });

  it('keeps different logical models connected to the same key and upstream model', () => {
    const preview = [
      BASE_BINDING,
      { ...BASE_BINDING, id: '52', model_id: '42', model_full_name: 'logical/second' },
    ];
    const result = normalizeEndpointKey({
      ...OLD_KEY,
      browse: {
        ...keyBrowse(preview),
        model_count: '2',
        binding_count: '2',
        available_binding_count: '2',
      },
    });
    expect(result.browse?.preview.map((item) => item.model_id)).toEqual(['41', '42']);
    expect(() => normalizeEndpointKey({ ...OLD_KEY, browse: { ...keyBrowse([]) } })).toThrow(
      /browse preview/i,
    );
  });

  it('enforces counts, parent IDs, duplicate previews, and limits', () => {
    expect(() =>
      normalizeEndpoint({
        ...OLD_ENDPOINT,
        browse: { ...endpointBrowse(), available_key_count: '3' },
      }),
    ).toThrow(/available key count/i);
    expect(() =>
      normalizeEndpoint({
        ...OLD_ENDPOINT,
        browse: { ...endpointBrowse(), state: 'no_keys' },
      }),
    ).toThrow(/endpoint browse state/i);
    expect(() =>
      normalizeEndpointKey({
        ...OLD_KEY,
        browse: { ...keyBrowse(), model_count: '2' },
      }),
    ).toThrow(/endpoint key browse counts/i);
    expect(() =>
      normalizeEndpointKey({
        ...OLD_KEY,
        browse: {
          ...keyBrowse(),
          binding_count: '2',
          preview: [BASE_BINDING, { ...BASE_BINDING, id: '52' }],
        },
      }),
    ).toThrow(/endpoint key browse parent|preview/i);
    expect(() =>
      normalizeEndpointKey({
        ...OLD_KEY,
        browse: { ...keyBrowse(), preview: [{ ...BASE_BINDING, endpoint_key_id: '99' }] },
      }),
    ).toThrow(/endpoint key browse parent/i);
    expect(() =>
      normalizeModel({
        ...OLD_MODEL,
        binding_count: '257',
        browse: modelBrowse([]),
      }),
    ).toThrow(/model binding count/i);
    expect(() =>
      normalizeModel({
        ...OLD_MODEL,
        browse: { ...modelBrowse(), available_binding_count: '2' },
      }),
    ).toThrow(/model browse available binding count/i);
    expect(() =>
      normalizeModel({
        ...OLD_MODEL,
        browse: { ...modelBrowse(), preview: [{ ...BASE_BINDING, model_id: '99' }] },
      }),
    ).toThrow(/model browse parent/i);
    expect(() => normalizeKeyBindingView({ ...BASE_BINDING, max_rpm: 2_147_483_648 })).toThrow(
      /key binding max RPM/i,
    );
    expect(() => normalizeKeyBindingView({ ...BASE_BINDING, max_concurrency: -1 })).toThrow(
      /key binding max concurrency/i,
    );
    expect(() =>
      normalizeKeyBindingView({ ...BASE_BINDING, endpoint_note: 'bad\u0085note' }),
    ).toThrow(/key binding endpoint note/i);
    expect(() => normalizeKeyBindingView({ ...BASE_BINDING, upstream_model_id: '\ud800' })).toThrow(
      /upstream model/i,
    );
  });
});

describe('resource key binding page adapter', () => {
  it('accepts the same exact pair on separate logical models while rejecting duplicate model connections', async () => {
    const rows = [binding(0, 'shared-model'), binding(1, 'shared-model')];
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(async () => jsonResponse(pageResponse(rows, '1', 20))),
    );
    expect(
      (await listKeyBindingsPage('11', '21', { page: '1', pageSize: 20 }, 'shared-model')).data,
    ).toHaveLength(2);
    rows[1].model_id = rows[0].model_id;
    await expect(
      listKeyBindingsPage('11', '21', { page: '1', pageSize: 20 }, 'shared-model'),
    ).rejects.toThrow(/key binding pairs/i);
  });
  it.each([10, 20, 50, 100] as const)('accepts page size %d', async (pageSize) => {
    const rows = Array.from({ length: pageSize }, (_, index) => binding(index));
    const fetchMock = vi.fn<typeof fetch>(async () =>
      jsonResponse(pageResponse(rows, '1', pageSize)),
    );
    vi.stubGlobal('fetch', fetchMock);

    const result = await listKeyBindingsPage('11', '21', { page: '1', pageSize });

    expect(result.data).toHaveLength(pageSize);
    expect(result.pagination).toMatchObject({ page: '1', page_size: pageSize });
  });

  it('honors an out-of-range page clamp and the empty page contract', async () => {
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(jsonResponse(pageResponse([binding(20)], '9', 20, 21)))
      .mockResolvedValueOnce(jsonResponse(pageResponse([], '1', 50, 0)));
    vi.stubGlobal('fetch', fetchMock);

    const clamped = await listKeyBindingsPage('11', '21', { page: '9', pageSize: 20 });
    const empty = await listKeyBindingsPage('11', '21', { page: '1', pageSize: 50 });

    expect(clamped.pagination).toMatchObject({ page: '2', total_items: '21', total_pages: '2' });
    expect(clamped.data).toHaveLength(1);
    expect(empty.data).toEqual([]);
    expect(empty.pagination).toMatchObject({ page: '1', total_items: '0', total_pages: '1' });
  });

  it('encodes and exactly enforces the full upstream model filter', async () => {
    const upstreamModel = 'Vendor/Model?x&界';
    const fetchMock = vi.fn<typeof fetch>(async () =>
      jsonResponse(pageResponse([binding(0, upstreamModel)], '1', 10)),
    );
    vi.stubGlobal('fetch', fetchMock);

    await listKeyBindingsPage('11', '21', { page: '1', pageSize: 10 }, upstreamModel);

    const requestURL = new URL(String(fetchMock.mock.calls[0]?.[0]), 'http://user.test');
    expect(requestURL.pathname).toBe('/api/endpoints/11/keys/21/bindings');
    expect(requestURL.searchParams.get('upstream_model_id')).toBe(upstreamModel);
    expect(requestURL.searchParams.get('page')).toBe('1');
    expect(requestURL.searchParams.get('page_size')).toBe('10');

    const mismatch = vi.fn<typeof fetch>(async () =>
      jsonResponse(pageResponse([binding(0, `${upstreamModel}Extra`)], '1', 10)),
    );
    vi.stubGlobal('fetch', mismatch);
    await expect(
      listKeyBindingsPage('11', '21', { page: '1', pageSize: 10 }, upstreamModel),
    ).rejects.toThrow(/parent or filter/i);
  });

  it('rejects invalid requests and responses outside the endpoint-key scope', async () => {
    const fetchMock = vi.fn<typeof fetch>();
    vi.stubGlobal('fetch', fetchMock);
    await expect(listKeyBindingsPage('0', '21', { page: '1', pageSize: 10 })).rejects.toThrow(
      /invalid endpoint id/i,
    );
    await expect(listKeyBindingsPage('11', '21', { page: '0', pageSize: 10 })).rejects.toThrow(
      /invalid page/i,
    );
    await expect(listKeyBindingsPage('11', '21', { page: '1', pageSize: 10 }, '')).rejects.toThrow(
      /invalid upstream model id/i,
    );
    expect(fetchMock).not.toHaveBeenCalled();

    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(async () =>
        jsonResponse(pageResponse([{ ...BASE_BINDING, endpoint_id: '99' }], '1', 10)),
      ),
    );
    await expect(listKeyBindingsPage('11', '21', { page: '1', pageSize: 10 })).rejects.toThrow(
      /parent or filter/i,
    );
  });
});

describe('useKeyBindingsPage query isolation', () => {
  function makeWrapper(queryClient: QueryClient) {
    return function Wrapper({ children }: { children: ReactNode }) {
      return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
    };
  }

  it('scopes keys under endpoint routing and aborts old account/filter requests without placeholders', async () => {
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false, gcTime: Number.POSITIVE_INFINITY } },
    });
    const requests: Array<{
      signal: AbortSignal | null | undefined;
      resolve: (response: Response) => void;
    }> = [];
    const fetchMock = vi.fn<typeof fetch>((_input, init) => {
      return new Promise<Response>((resolve, reject) => {
        const signal = init?.signal;
        const onAbort = () => reject(new DOMException('aborted', 'AbortError'));
        signal?.addEventListener('abort', onAbort, { once: true });
        requests.push({
          signal,
          resolve: (response) => {
            signal?.removeEventListener('abort', onAbort);
            resolve(response);
          },
        });
      });
    });
    vi.stubGlobal('fetch', fetchMock);

    const rendered = renderHook(
      ({
        accountId,
        upstreamModel,
        page,
      }: {
        accountId: string;
        upstreamModel: string;
        page: string;
      }) => useKeyBindingsPage(accountId, '11', '21', { page, pageSize: 20 }, upstreamModel),
      {
        initialProps: { accountId: '7', upstreamModel: 'Vendor/First', page: '1' },
        wrapper: makeWrapper(queryClient),
      },
    );
    await waitFor(() => expect(requests).toHaveLength(1));

    rendered.rerender({ accountId: '8', upstreamModel: 'Vendor/Second', page: '2' });
    await waitFor(() => expect(requests).toHaveLength(2));
    expect(requests[0]?.signal?.aborted).toBe(true);

    act(() => {
      requests[1]?.resolve(jsonResponse(pageResponse([binding(0, 'Vendor/Second')], '2', 20, 21)));
    });
    await waitFor(() =>
      expect(rendered.result.current.data?.data[0]?.upstream_model_id).toBe('Vendor/Second'),
    );
    expect(rendered.result.current.data?.data[0]?.upstream_model_id).not.toBe('Vendor/First');

    const expectedKey = [
      ...coreKeys.endpointRouting('8', '11', ['21']),
      'key-bindings',
      'Vendor/Second',
      '2',
      20,
    ];
    expect(queryClient.getQueryCache().find({ queryKey: expectedKey })?.queryKey).toEqual(
      expectedKey,
    );
    rendered.unmount();
    queryClient.clear();
  });

  it('does not request while account identity is empty and does not retry authorization failures', async () => {
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false, gcTime: Number.POSITIVE_INFINITY } },
    });
    const fetchMock = vi.fn<typeof fetch>(async () =>
      jsonResponse({ error: { code: 'forbidden', message: 'safe' } }, 403),
    );
    vi.stubGlobal('fetch', fetchMock);

    const rendered = renderHook(
      ({ accountId }: { accountId: string }) =>
        useKeyBindingsPage(accountId, '11', '21', { page: '1', pageSize: 10 }),
      { initialProps: { accountId: '' }, wrapper: makeWrapper(queryClient) },
    );
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(fetchMock).not.toHaveBeenCalled();

    rendered.rerender({ accountId: '7' });
    await waitFor(() => expect(rendered.result.current.isError).toBe(true));
    expect(fetchMock).toHaveBeenCalledTimes(1);
    rendered.unmount();
    queryClient.clear();
  });
});
