import { afterEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { useState } from 'react';
import {
  beginManagementSessionRequest,
  charityManagementKeys,
  noteManagementSessionSuccess,
} from '@shared/charityManagement';
import { charityKeys } from '@shared/operations/charity';
import type { CharitySelection } from './CharityBindingPicker';
import { CharityBindingPicker } from './CharityBindingPicker';
import { disposeTestProviders, renderWithProviders } from '../../../test/unit/support';

const SOURCE_KEY = `dsg_${'_'.repeat(42)}8`;

function sourceKey(index: number): string {
  return `dsg_${index.toString(36).padStart(42, 'A')}A`;
}

function jsonResponse(value: unknown): Response {
  return new Response(JSON.stringify(value), {
    status: 200,
    headers: { 'content-type': 'application/json' },
  });
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, resolve, reject };
}

function sourcePage(data: unknown[], pageNumber = '1', totalItems = data.length, pageSize = 20) {
  const totalPages = totalItems === 0 ? 1 : Math.ceil(totalItems / pageSize);
  return {
    data,
    next_cursor: null,
    pagination: {
      page: pageNumber,
      page_size: pageSize,
      total_items: String(totalItems),
      total_pages: String(totalPages),
    },
  };
}

function source({ sourceKey = SOURCE_KEY, baseURL = 'https://safe.example/v1' } = {}) {
  return {
    source_key: sourceKey,
    safe_source: {
      kind: 'custom',
      connector_type: 'openai-compatible',
      base_url: baseURL,
    },
    donation_count: '1',
    key_count: '1',
    usable_key_count: '1',
    pending_donation_count: '0',
  };
}

function key({
  keyID = '21',
  donationID = '7',
  safeNote = 'safe key note',
  charityState = 'available',
  endedReason = null,
  baseURL = 'https://safe.example/v1',
} = {}) {
  return {
    id: keyID,
    key_id: keyID,
    donation_id: donationID,
    donation_revision: '1',
    endpoint_key_id: keyID,
    display_head: 'head',
    display_tail: 'tail',
    safe_source: {
      kind: 'custom',
      connector_type: 'openai-compatible',
      base_url: baseURL,
    },
    physical_enabled: true,
    charity_state: charityState,
    limits: { price: null, calls: null, tokens: null },
    usage: {
      price_used: '0',
      price_inflight: '0',
      calls_used: '0',
      calls_inflight: '0',
      tokens_used: '0',
      tokens_inflight: '0',
    },
    token_reserve: 0,
    expires_at: null,
    authorized_expires_at: null,
    failure_disable_threshold: '10',
    streak: { generation: '1', count: '0', failure_disabled: false },
    ended_reason: endedReason,
    safe_note: safeNote,
    max_concurrency: 2,
    max_rpm: 60,
    binding_count: '0',
    idle: true,
    rule_count: '0',
    rules: [],
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

function candidate({
  keyID = '21',
  donationID = '7',
  modelID = 'model-a',
  sourceTypes = ['automatic'],
} = {}) {
  return {
    donation_key_id: keyID,
    donation_id: donationID,
    source: {
      connector_type: 'openai-compatible',
      canonical_base_url: 'https://safe.example/v1',
      display_head: 'head',
      display_tail: 'tail',
    },
    upstream_model_id: modelID,
    source_types: sourceTypes,
  };
}

function authorize(
  queryClient: Parameters<typeof beginManagementSessionRequest>[0],
  username = 'root',
) {
  const generation = beginManagementSessionRequest(queryClient, 'admin');
  expect(
    noteManagementSessionSuccess(queryClient, 'admin', { admin: { username } }, generation),
  ).toBe(true);
  queryClient.setQueryData(['admin', 'session'], { admin: { username } });
  queryClient.setQueryData(charityManagementKeys.capability('admin'), false);
}

function PickerHarness({
  onReadStateChange,
  locked = false,
  modelId = '31',
}: {
  onReadStateChange?: (blocked: boolean) => void;
  locked?: boolean;
  modelId?: string;
} = {}) {
  const [selected, setSelected] = useState<Record<string, CharitySelection>>({});
  return (
    <CharityBindingPicker
      role="admin"
      modelId={modelId}
      selected={selected}
      onChange={setSelected}
      locked={locked}
      onReadStateChange={onReadStateChange}
    />
  );
}

function modelCheckbox(modelID: string): HTMLElement {
  const checkbox = screen
    .getAllByRole('checkbox')
    .find(
      (entry) => entry.parentElement?.textContent?.replace(/\s+/g, '') === `${modelID}Automatic`,
    );
  if (!checkbox) throw new Error(`Missing checkbox for ${modelID}`);
  return checkbox;
}

afterEach(async () => {
  await disposeTestProviders();
  vi.unstubAllGlobals();
});

describe('CharityBindingPicker numbered source flow', () => {
  it('reads source, key, and candidate pages only after an administrator session is authorized', async () => {
    const requests: string[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(async (input) => {
        const url = new URL(String(input), 'https://example.test');
        requests.push(`${url.pathname}${url.search}`);
        if (url.pathname === '/admin/api/donation-sources')
          return jsonResponse(sourcePage([source()]));
        if (url.pathname === `/admin/api/donation-sources/${encodeURIComponent(SOURCE_KEY)}/keys`) {
          return jsonResponse(sourcePage([key()]));
        }
        if (url.pathname === '/admin/api/charity-models/31/binding-candidates') {
          return jsonResponse(sourcePage([candidate()]));
        }
        throw new Error(`Unexpected request: ${url.pathname}`);
      }),
    );
    const view = await renderWithProviders(<PickerHarness />, {
      station: 'admin',
      role: 'admin',
    });
    expect(requests).toEqual([]);
    authorize(view.queryClient);

    await screen.findByRole('button', { name: /Custom endpoint/ });
    expect(requests[0]).toBe('/admin/api/donation-sources?scope=active&page=1&page_size=20');
    await view.user.click(screen.getByRole('button', { name: /Custom endpoint/ }));
    await view.user.click(await screen.findByRole('button', { name: /safe key note/ }));
    await view.user.click(await screen.findByRole('checkbox', { name: /model-a/ }));

    expect(requests).toContain(
      `/admin/api/donation-sources/${encodeURIComponent(SOURCE_KEY)}/keys?scope=active&page=1&page_size=20`,
    );
    expect(requests).toContain(
      '/admin/api/charity-models/31/binding-candidates?donation_id=7&donation_key_id=21&page=1&page_size=20',
    );
    expect(screen.getByRole('heading', { name: '1 service connection(s) selected' })).toBeVisible();
  });

  it('keeps every source choice disabled while the parent operation is locked', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(async () => jsonResponse(sourcePage([source()]))),
    );
    const view = await renderWithProviders(
      <CharityBindingPicker role="admin" modelId="31" selected={{}} onChange={vi.fn()} locked />,
      { station: 'admin', role: 'admin' },
    );
    authorize(view.queryClient);
    const sourceButton = await screen.findByRole('button', { name: /Custom endpoint/ });
    expect(sourceButton).toBeDisabled();
    expect(screen.getByRole('combobox', { name: 'Source scope' })).toBeDisabled();
  });

  it('fails closed and notifies the owner when a page is forbidden', async () => {
    const onCapabilityLoss = vi.fn();
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(async () => new Response('', { status: 403 })),
    );
    const view = await renderWithProviders(
      <CharityBindingPicker
        role="admin"
        modelId="31"
        selected={{}}
        onChange={vi.fn()}
        locked={false}
        onCapabilityLoss={onCapabilityLoss}
      />,
      { station: 'admin', role: 'admin' },
    );
    authorize(view.queryClient);
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Charity management access is no longer available.',
    );
    await waitFor(() => expect(onCapabilityLoss).toHaveBeenCalledTimes(1));
  });

  it('clears selected rows when the administrator session subject changes', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(async (input) => {
        const url = new URL(String(input), 'https://example.test');
        if (url.pathname === '/admin/api/donation-sources')
          return jsonResponse(sourcePage([source()]));
        if (url.pathname === `/admin/api/donation-sources/${encodeURIComponent(SOURCE_KEY)}/keys`) {
          return jsonResponse(sourcePage([key()]));
        }
        if (url.pathname === '/admin/api/charity-models/31/binding-candidates') {
          return jsonResponse(sourcePage([candidate()]));
        }
        throw new Error(`Unexpected request: ${url.pathname}`);
      }),
    );
    const view = await renderWithProviders(<PickerHarness />, {
      station: 'admin',
      role: 'admin',
    });
    authorize(view.queryClient);
    await view.user.click(await screen.findByRole('button', { name: /Custom endpoint/ }));
    await view.user.click(await screen.findByRole('button', { name: /safe key note/ }));
    await view.user.click(await screen.findByRole('checkbox', { name: /model-a/ }));
    expect(screen.getByRole('heading', { name: '1 service connection(s) selected' })).toBeVisible();

    authorize(view.queryClient, 'next-admin');
    await waitFor(() =>
      expect(
        screen.getByRole('heading', { name: '0 service connection(s) selected' }),
      ).toBeVisible(),
    );
  });

  it('keeps source, key, candidate, and selected context across real second pages', async () => {
    const sourcesPageOne = Array.from({ length: 20 }, (_, index) =>
      source({ sourceKey: sourceKey(index), baseURL: `https://safe.example/source-${index + 1}` }),
    );
    const sourcesPageTwo = [
      source({ sourceKey: sourceKey(20), baseURL: 'https://safe.example/source-21' }),
    ];
    const keysPageOne = Array.from({ length: 20 }, (_, index) =>
      key({
        keyID: String(index + 1),
        donationID: String(index + 1),
        safeNote: `safe key ${index + 1}`,
        baseURL: 'https://safe.example/source-21',
      }),
    );
    const keysPageTwo = [
      key({
        keyID: '22',
        donationID: '22',
        safeNote: 'safe key 22',
        baseURL: 'https://safe.example/source-21',
      }),
    ];
    const candidatesPageOne = Array.from({ length: 20 }, (_, index) =>
      candidate({ keyID: '22', donationID: '22', modelID: `model-${index + 1}` }),
    );
    const candidatesPageTwo = [candidate({ keyID: '22', donationID: '22', modelID: 'model-21' })];
    const requests: string[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(async (input) => {
        const url = new URL(String(input), 'https://example.test');
        requests.push(`${url.pathname}${url.search}`);
        const pageNumber = url.searchParams.get('page');
        if (url.pathname === '/admin/api/donation-sources') {
          return jsonResponse(
            sourcePage(pageNumber === '2' ? sourcesPageTwo : sourcesPageOne, pageNumber ?? '1', 21),
          );
        }
        if (
          url.pathname === `/admin/api/donation-sources/${encodeURIComponent(sourceKey(20))}/keys`
        ) {
          return jsonResponse(
            sourcePage(pageNumber === '2' ? keysPageTwo : keysPageOne, pageNumber ?? '1', 21),
          );
        }
        if (url.pathname === '/admin/api/charity-models/31/binding-candidates') {
          return jsonResponse(
            sourcePage(
              pageNumber === '2' ? candidatesPageTwo : candidatesPageOne,
              pageNumber ?? '1',
              21,
            ),
          );
        }
        throw new Error(`Unexpected request: ${url.pathname}${url.search}`);
      }),
    );
    const view = await renderWithProviders(<PickerHarness />, {
      station: 'admin',
      role: 'admin',
    });
    authorize(view.queryClient);

    await screen.findByText('https://safe.example/source-1');
    let pager = screen.getAllByRole('navigation', { name: 'Pagination' })[0];
    await view.user.click(within(pager).getByRole('button', { name: 'Next' }));
    await screen.findByRole('button', { name: /source-21/ });
    await view.user.click(screen.getByRole('button', { name: /source-21/ }));

    await screen.findByText('safe key 1', { selector: 'strong' });
    pager = screen.getAllByRole('navigation', { name: 'Pagination' })[0];
    await view.user.click(within(pager).getByRole('button', { name: 'Next' }));
    await screen.findByRole('button', { name: /safe key 22/ });
    await view.user.click(screen.getByText('safe key 22', { selector: 'strong' }));

    await waitFor(() => expect(modelCheckbox('model-1')).toBeVisible());
    await view.user.click(modelCheckbox('model-1'));
    pager = screen.getAllByRole('navigation', { name: 'Pagination' })[0];
    await view.user.click(within(pager).getByRole('button', { name: 'Next' }));
    await waitFor(() => expect(modelCheckbox('model-21')).toBeVisible());
    await view.user.click(modelCheckbox('model-21'));
    expect(screen.getByRole('heading', { name: '2 service connection(s) selected' })).toBeVisible();

    await view.user.click(screen.getByRole('button', { name: /Choose key/ }));
    expect(await screen.findByRole('button', { name: /safe key 22/ })).toBeVisible();
    await view.user.click(screen.getByRole('button', { name: /Choose source/ }));
    expect(await screen.findByRole('button', { name: /source-21/ })).toBeVisible();
    expect(requests).toContain(
      '/admin/api/charity-models/31/binding-candidates?donation_id=22&donation_key_id=22&page=2&page_size=20',
    );
  });

  it('does not reuse old source rows after a committed filter change', async () => {
    const nextPage = deferred<Response>();
    const fetchMock = vi.fn<typeof fetch>(async (input) => {
      const url = new URL(String(input), 'https://example.test');
      if (url.pathname === '/admin/api/donation-sources' && url.searchParams.get('q') === 'new') {
        return nextPage.promise;
      }
      if (url.pathname === '/admin/api/donation-sources') {
        return jsonResponse(sourcePage([source({ baseURL: 'https://safe.example/old' })]));
      }
      throw new Error(`Unexpected request: ${url.pathname}${url.search}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const view = await renderWithProviders(<PickerHarness />, {
      station: 'admin',
      role: 'admin',
    });
    authorize(view.queryClient);
    await screen.findByRole('button', { name: /https:\/\/safe\.example\/old/ });
    const search = screen.getByRole('searchbox', { name: 'Search sources' });
    await view.user.type(search, 'new');
    await view.user.click(screen.getByRole('button', { name: 'Search' }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
    expect(
      screen.queryByRole('button', { name: /https:\/\/safe\.example\/old/ }),
    ).not.toBeInTheDocument();
    nextPage.resolve(jsonResponse(sourcePage([source({ baseURL: 'https://safe.example/new' })])));
    await screen.findByRole('button', { name: /https:\/\/safe\.example\/new/ });
  });

  it('rejects invalid source search input without issuing a request', async () => {
    const fetchMock = vi.fn<typeof fetch>(async (input) => {
      const url = new URL(String(input), 'https://example.test');
      if (url.pathname === '/admin/api/donation-sources')
        return jsonResponse(sourcePage([source()]));
      throw new Error(`Unexpected request: ${url.pathname}${url.search}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const view = await renderWithProviders(<PickerHarness />, {
      station: 'admin',
      role: 'admin',
    });
    authorize(view.queryClient);
    await screen.findByRole('button', { name: /Custom endpoint/ });
    const search = screen.getByRole('searchbox', { name: 'Search sources' });
    fireEvent.change(search, { target: { value: '\u0001' } });
    expect(search).toHaveAttribute('aria-invalid', 'true');
    expect(screen.getByRole('button', { name: 'Search' })).toBeDisabled();
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it('accepts a non-BMP source search at the 128-scalar and 512-byte boundary', async () => {
    const query = '😀'.repeat(128);
    const requests: string[] = [];
    const fetchMock = vi.fn<typeof fetch>(async (input) => {
      const url = new URL(String(input), 'https://example.test');
      requests.push(`${url.pathname}${url.search}`);
      return jsonResponse(sourcePage([]));
    });
    vi.stubGlobal('fetch', fetchMock);
    const view = await renderWithProviders(<PickerHarness />, {
      station: 'admin',
      role: 'admin',
    });
    authorize(view.queryClient);
    const search = await screen.findByRole('searchbox', { name: 'Search sources' });
    fireEvent.change(search, { target: { value: query } });
    expect(search).toHaveAttribute('aria-invalid', 'false');
    expect(screen.getByRole('button', { name: 'Search' })).not.toBeDisabled();
    await view.user.click(screen.getByRole('button', { name: 'Search' }));
    await waitFor(() =>
      expect(requests).toContain(
        `/admin/api/donation-sources?q=${encodeURIComponent(query)}&scope=active&page=1&page_size=20`,
      ),
    );
  });

  it('offers source retry while the key pane is open after a background source failure', async () => {
    let sourceReads = 0;
    const fetchMock = vi.fn<typeof fetch>(async (input) => {
      const url = new URL(String(input), 'https://example.test');
      if (url.pathname === '/admin/api/donation-sources') {
        sourceReads += 1;
        return sourceReads === 2
          ? new Response('', { status: 503 })
          : jsonResponse(sourcePage([source()]));
      }
      if (url.pathname.endsWith('/keys')) return jsonResponse(sourcePage([key()]));
      throw new Error(`Unexpected request: ${url.pathname}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const view = await renderWithProviders(<PickerHarness />, { station: 'admin', role: 'admin' });
    authorize(view.queryClient);
    await view.user.click(await screen.findByRole('button', { name: /Custom endpoint/ }));
    await screen.findByRole('button', { name: /safe key note/ });
    const sourceQuery = view.queryClient.getQueryCache().findAll({
      queryKey: [...charityKeys.root('admin'), 'binding-picker', 'root', '31', 'sources'],
    })[0];
    expect(sourceQuery).toBeDefined();
    await view.queryClient.refetchQueries({ queryKey: sourceQuery!.queryKey, exact: true });
    expect(await screen.findByText('The service is temporarily unavailable.')).toBeVisible();
    expect(screen.getByRole('button', { name: /safe key note/ })).toBeDisabled();
    await view.user.click(screen.getByRole('button', { name: 'Retry' }));
    await waitFor(() =>
      expect(screen.getByRole('button', { name: /safe key note/ })).toBeEnabled(),
    );
    expect(sourceReads).toBe(3);
    expect(
      fetchMock.mock.calls.filter(([input]) => String(input).includes('binding-candidates')),
    ).toHaveLength(0);
  });

  it.each([200, 403])(
    'ignores a late %s source response after the account changes',
    async (status) => {
      const oldRead = deferred<Response>();
      let reads = 0;
      vi.stubGlobal(
        'fetch',
        vi.fn<typeof fetch>(async () => {
          reads += 1;
          if (reads === 1) return oldRead.promise;
          return jsonResponse(
            sourcePage([source({ baseURL: 'https://safe.example/current-account' })]),
          );
        }),
      );
      const view = await renderWithProviders(<PickerHarness />, {
        station: 'admin',
        role: 'admin',
      });
      authorize(view.queryClient);
      await waitFor(() => expect(reads).toBe(1));
      authorize(view.queryClient, 'next-admin');
      await screen.findByRole('button', { name: /current-account/ });
      await act(async () => {
        oldRead.resolve(
          status === 200
            ? jsonResponse(
                sourcePage([source({ baseURL: 'https://safe.example/previous-account' })]),
              )
            : new Response(
                JSON.stringify({ error: { code: 'forbidden', message: 'old request' } }),
                { status: 403, headers: { 'content-type': 'application/json' } },
              ),
        );
      });
      expect(view.queryClient.getQueryData(['admin', 'session'])).toEqual({
        admin: { username: 'next-admin' },
      });
      expect(screen.getByRole('button', { name: /current-account/ })).toBeEnabled();
      expect(screen.queryByRole('button', { name: /previous-account/ })).not.toBeInTheDocument();
    },
  );

  it('reports background key reads and keeps candidate controls paused until retry succeeds', async () => {
    let sourceKeyReads = 0;
    const readStates: boolean[] = [];
    const fetchMock = vi.fn<typeof fetch>(async (input) => {
      const url = new URL(String(input), 'https://example.test');
      if (url.pathname === '/admin/api/donation-sources')
        return jsonResponse(sourcePage([source()]));
      if (url.pathname === `/admin/api/donation-sources/${encodeURIComponent(SOURCE_KEY)}/keys`) {
        sourceKeyReads += 1;
        if (sourceKeyReads === 2) return new Response('', { status: 503 });
        return jsonResponse(sourcePage([key()]));
      }
      if (url.pathname === '/admin/api/charity-models/31/binding-candidates') {
        return jsonResponse(sourcePage([candidate()]));
      }
      throw new Error(`Unexpected request: ${url.pathname}${url.search}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const view = await renderWithProviders(
      <PickerHarness onReadStateChange={(blocked) => readStates.push(blocked)} />,
      { station: 'admin', role: 'admin' },
    );
    authorize(view.queryClient);
    await view.user.click(await screen.findByRole('button', { name: /Custom endpoint/ }));
    await view.user.click(await screen.findByRole('button', { name: /safe key note/ }));
    await screen.findByRole('checkbox', { name: /model-a/ });
    const sourceKeyQuery = view.queryClient
      .getQueryCache()
      .findAll({
        queryKey: [...charityKeys.root('admin'), 'binding-picker', 'root', '31', 'source-keys'],
        exact: false,
      })
      .find((query) => query.queryKey[7] === SOURCE_KEY);
    expect(sourceKeyQuery).toBeDefined();
    await view.queryClient.refetchQueries({ queryKey: sourceKeyQuery!.queryKey, exact: true });
    expect(await screen.findByText('The service is temporarily unavailable.')).toBeVisible();
    expect(readStates).toContain(true);
    expect(screen.getByRole('checkbox', { name: /model-a/ })).toBeDisabled();
    await view.user.click(screen.getByRole('button', { name: 'Retry' }));
    await waitFor(() => expect(readStates.at(-1)).toBe(false));
    expect(screen.getByRole('checkbox', { name: /model-a/ })).not.toBeDisabled();
  });

  it('keeps terminal keys in the all-scope view but never opens candidates', async () => {
    const fetchMock = vi.fn<typeof fetch>(async (input) => {
      const url = new URL(String(input), 'https://example.test');
      if (url.pathname === '/admin/api/donation-sources')
        return jsonResponse(sourcePage([source()]));
      if (url.pathname === `/admin/api/donation-sources/${encodeURIComponent(SOURCE_KEY)}/keys`) {
        return jsonResponse(
          sourcePage([{ ...key(), charity_state: 'ended', ended_reason: 'withdrawn' }]),
        );
      }
      if (url.pathname === '/admin/api/charity-models/31/binding-candidates') {
        return jsonResponse(sourcePage([candidate()]));
      }
      throw new Error(`Unexpected request: ${url.pathname}${url.search}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const view = await renderWithProviders(<PickerHarness />, {
      station: 'admin',
      role: 'admin',
    });
    authorize(view.queryClient);
    await screen.findByRole('button', { name: /Custom endpoint/ });
    await view.user.selectOptions(screen.getByRole('combobox', { name: 'Source scope' }), 'all');
    await view.user.click(await screen.findByRole('button', { name: /Custom endpoint/ }));
    const terminal = await screen.findByRole('button', { name: /safe key note/ });
    expect(terminal).toBeDisabled();
    expect(
      fetchMock.mock.calls.some(([input]) => String(input).includes('binding-candidates')),
    ).toBe(false);
  });
});
