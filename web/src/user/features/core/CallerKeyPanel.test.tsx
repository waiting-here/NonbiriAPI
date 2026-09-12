import { act, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { assertNoSensitiveQueryCache, renderWithProviders } from '../../../../test/unit/support';
import { CallerKeyPanel } from '../../pages/KeysPage';
import { coreKeys } from './queries';

function response(body: unknown, status = 200, generation?: string): Response {
  const headers = new Headers({ 'Content-Type': 'application/json' });
  if (generation !== undefined) headers.set('X-Nonbiri-CallerKey-Generation', generation);
  return new Response(JSON.stringify(body), { status, headers });
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((settle) => {
    resolve = settle;
  });
  return { promise, resolve };
}

function installCallerKeyFetch(secret: string) {
  let generation = '0';
  let metadata: {
    display: string;
    created_at: number;
    updated_at: number;
    generation: string;
  } | null = null;
  const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
    const path = String(input);
    const method = init?.method ?? 'GET';
    if (path === '/api/caller-key' && method === 'GET') return response(metadata, 200, generation);
    if (path === '/api/caller-key/regenerate' && method === 'POST') {
      expect(new Headers(init?.headers).has('Idempotency-Key')).toBe(false);
      expect(JSON.parse(String(init?.body))).toEqual({ expected_generation: generation });
      generation = String(BigInt(generation) + 1n);
      metadata = {
        display: 'nbk_AAAA…AAAA',
        created_at: 1_700_000_000,
        updated_at: 1_700_000_000,
        generation,
      };
      return response({ secret, metadata });
    }
    throw new Error(`Unexpected request: ${method} ${path}`);
  });
  vi.stubGlobal('fetch', fetchMock);
  return fetchMock;
}

describe('CallerKeyPanel one-time plaintext boundary', () => {
  it('moves through loading, empty, success, and close without caching or storing plaintext', async () => {
    const secret = `nbk_${'A'.repeat(43)}`;
    installCallerKeyFetch(secret);
    const rendered = await renderWithProviders(<CallerKeyPanel accountId="1" />, {
      station: 'user',
      role: 'user',
      locale: 'en',
    });
    rendered.queryClient.setQueryData(coreKeys.session, { user: { id: '1' } });

    expect(screen.getByText('Loading…')).toBeInTheDocument();
    await screen.findByText('No account API key');
    await rendered.user.click(screen.getByRole('button', { name: 'Create API key' }));
    expect(await screen.findByText(secret)).toBeInTheDocument();
    expect(screen.getByText('Key identifier (cannot be used for calls)')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /copy key identifier/i })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Replace API key' })).toBeDisabled();
    expect(assertNoSensitiveQueryCache(rendered.queryClient, [secret]).hitSurfaces).toEqual([]);
    expect(
      [...Array(window.localStorage.length)]
        .map((_, index) => window.localStorage.getItem(window.localStorage.key(index) ?? ''))
        .join(''),
    ).not.toContain(secret);
    expect(
      [...Array(window.sessionStorage.length)]
        .map((_, index) => window.sessionStorage.getItem(window.sessionStorage.key(index) ?? ''))
        .join(''),
    ).not.toContain(secret);

    await rendered.user.click(screen.getByRole('button', { name: 'I have saved it — close' }));
    expect(screen.queryByText(secret)).not.toBeInTheDocument();
  });

  it('copies a valid one-time value only after the Clipboard promise resolves', async () => {
    const secret = `nbk_${'H'.repeat(42)}Q`;
    const clipboardResult = deferred<void>();
    const writeText = vi.fn(() => clipboardResult.promise);
    const previousClipboard = Object.getOwnPropertyDescriptor(window.navigator, 'clipboard');
    installCallerKeyFetch(secret);
    const rendered = await renderWithProviders(<CallerKeyPanel accountId="1" />, {
      station: 'user',
      role: 'user',
      locale: 'en',
    });
    Object.defineProperty(window.navigator, 'clipboard', {
      configurable: true,
      value: { writeText },
    });
    rendered.queryClient.setQueryData(coreKeys.session, { user: { id: '1' } });

    await screen.findByText('No account API key');
    await rendered.user.click(screen.getByRole('button', { name: 'Create API key' }));
    await screen.findByText(secret);
    await rendered.user.click(screen.getByRole('button', { name: 'Copy' }));

    expect(screen.getByRole('button', { name: 'Copy' })).toBeVisible();
    await act(async () => {
      clipboardResult.resolve(undefined);
      await clipboardResult.promise;
    });
    expect(await screen.findByRole('button', { name: 'Copied' })).toBeVisible();
    expect(writeText).toHaveBeenCalledWith(secret);
    if (previousClipboard) {
      Object.defineProperty(window.navigator, 'clipboard', previousClipboard);
    } else {
      Reflect.deleteProperty(window.navigator, 'clipboard');
    }
  });

  it('confirms replacement of an existing key and reveals only the replacement response', async () => {
    const secret = `nbk_${'I'.repeat(42)}8`;
    let generation = '3';
    let metadata = {
      display: 'nbk_IIII…IIII',
      created_at: 1_700_000_000,
      updated_at: 1_700_000_000,
      generation,
    };
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      if (String(input) === '/api/caller-key' && (init?.method ?? 'GET') === 'GET') {
        return response(metadata, 200, generation);
      }
      if (String(input) === '/api/caller-key/regenerate' && init?.method === 'POST') {
        expect(JSON.parse(String(init?.body))).toEqual({ expected_generation: '3' });
        generation = '4';
        metadata = {
          display: 'nbk_IIII…IIIA',
          created_at: 1_700_000_000,
          updated_at: 1_700_000_004,
          generation,
        };
        return response({ secret, metadata });
      }
      throw new Error(`Unexpected request: ${init?.method ?? 'GET'} ${String(input)}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(<CallerKeyPanel accountId="1" />, {
      station: 'user',
      role: 'user',
      locale: 'en',
    });
    rendered.queryClient.setQueryData(coreKeys.session, { user: { id: '1' } });

    await screen.findByText('Key identifier (cannot be used for calls)');
    await rendered.user.click(screen.getByRole('button', { name: 'Replace API key' }));
    expect(screen.getByRole('alertdialog')).toBeVisible();
    await rendered.user.click(
      screen
        .getByRole('alertdialog')
        .querySelector<HTMLButtonElement>('button:not([disabled]):last-child')!,
    );

    expect(await screen.findByText(secret)).toBeVisible();
    expect(screen.getByRole('button', { name: 'Replace API key' })).toBeDisabled();
    expect(fetchMock.mock.calls.filter(([, init]) => init?.method === 'POST')).toHaveLength(1);
  });

  it('drops a late account reveal at the account boundary', async () => {
    const secret = `nbk_${'A'.repeat(43)}`;
    const lateResponse = deferred<Response>();
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      if (String(input) === '/api/caller-key' && (init?.method ?? 'GET') === 'GET') {
        return response(null, 200, '0');
      }
      if (String(input) === '/api/caller-key/regenerate' && init?.method === 'POST') {
        return lateResponse.promise;
      }
      throw new Error(`Unexpected request: ${init?.method ?? 'GET'} ${String(input)}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(<CallerKeyPanel accountId="1" />, {
      station: 'user',
      role: 'user',
      locale: 'en',
    });
    rendered.queryClient.setQueryData(coreKeys.session, { user: { id: '1' } });
    await screen.findByText('No account API key');
    await rendered.user.click(screen.getByRole('button', { name: 'Create API key' }));
    await waitFor(() =>
      expect(fetchMock.mock.calls.some(([, init]) => init?.method === 'POST')).toBe(true),
    );

    rendered.rerender(<CallerKeyPanel accountId="2" />);
    rendered.queryClient.setQueryData(coreKeys.session, { user: { id: '2' } });
    await act(async () => {
      lateResponse.resolve(
        response({
          secret,
          metadata: {
            display: 'nbk_AAAA…AAAA',
            created_at: 1_700_000_000,
            updated_at: 1_700_000_000,
            generation: '1',
          },
        }),
      );
      await lateResponse.promise;
    });
    await waitFor(() => expect(screen.queryByText(secret)).not.toBeInTheDocument());
    expect(
      rendered.queryClient.getQueryData(['user', 'core', 'account', '2', 'caller-key']),
    ).toEqual({ generation: '0', metadata: null });
  });

  it('keeps plaintext visible when only the follow-up metadata refresh fails', async () => {
    const secret = `nbk_${'B'.repeat(42)}Q`;
    let reads = 0;
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
        if (String(input) === '/api/caller-key' && (init?.method ?? 'GET') === 'GET') {
          reads += 1;
          return reads === 1
            ? response(null, 200, '0')
            : response({ error: { code: 'internal', message: 'safe refresh failure' } }, 500);
        }
        if (String(input) === '/api/caller-key/regenerate' && init?.method === 'POST') {
          return response({
            secret,
            metadata: {
              display: 'nbk_BBBB…BBBQ',
              created_at: 1_700_000_000,
              updated_at: 1_700_000_001,
              generation: '1',
            },
          });
        }
        throw new Error(`Unexpected request: ${init?.method ?? 'GET'} ${String(input)}`);
      }),
    );
    const rendered = await renderWithProviders(<CallerKeyPanel accountId="1" />, {
      station: 'user',
      role: 'user',
      locale: 'en',
    });
    rendered.queryClient.setQueryData(coreKeys.session, { user: { id: '1' } });

    await screen.findByText('No account API key');
    await rendered.user.click(screen.getByRole('button', { name: 'Create API key' }));

    expect(await screen.findByText(secret)).toBeVisible();
    expect(await screen.findByText(/Key details could not be refreshed/)).toBeVisible();
    expect(screen.getByText(secret)).toBeVisible();
  });

  it('keeps plaintext visible while the follow-up metadata refresh is delayed', async () => {
    const secret = `nbk_${'L'.repeat(42)}Q`;
    const refresh = deferred<Response>();
    let reads = 0;
    const metadata = {
      display: 'nbk_LLLL…LLLQ',
      created_at: 1_700_000_000,
      updated_at: 1_700_000_001,
      generation: '1',
    };
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      if (String(input) === '/api/caller-key' && (init?.method ?? 'GET') === 'GET') {
        reads += 1;
        return reads === 1 ? response(null, 200, '0') : refresh.promise;
      }
      if (String(input) === '/api/caller-key/regenerate' && init?.method === 'POST') {
        return response({ secret, metadata });
      }
      throw new Error(`Unexpected request: ${init?.method ?? 'GET'} ${String(input)}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(<CallerKeyPanel accountId="1" />, {
      station: 'user',
      role: 'user',
      locale: 'en',
    });
    rendered.queryClient.setQueryData(coreKeys.session, { user: { id: '1' } });

    await screen.findByText('No account API key');
    await rendered.user.click(screen.getByRole('button', { name: 'Create API key' }));
    await screen.findByText(secret);
    await waitFor(() => expect(reads).toBe(2));
    expect(screen.getByText(secret)).toBeVisible();

    await act(async () => {
      refresh.resolve(response(metadata, 200, '1'));
      await refresh.promise;
    });
    expect(screen.getByText(secret)).toBeVisible();
  });

  it('does not rebuild the previous account cache when session switches before success handling', async () => {
    const secret = `nbk_${'C'.repeat(42)}Q`;
    const lateResponse = deferred<Response>();
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      if (String(input) === '/api/caller-key' && (init?.method ?? 'GET') === 'GET') {
        return response(null, 200, '0');
      }
      if (String(input) === '/api/caller-key/regenerate' && init?.method === 'POST') {
        return lateResponse.promise;
      }
      throw new Error(`Unexpected request: ${init?.method ?? 'GET'} ${String(input)}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(<CallerKeyPanel accountId="1" />, {
      station: 'user',
      role: 'user',
      locale: 'en',
    });
    rendered.queryClient.setQueryData(coreKeys.session, { user: { id: '1' } });
    await screen.findByText('No account API key');
    await rendered.user.click(screen.getByRole('button', { name: 'Create API key' }));
    await waitFor(() =>
      expect(fetchMock.mock.calls.some(([, init]) => init?.method === 'POST')).toBe(true),
    );

    rendered.queryClient.setQueryData(coreKeys.session, { user: { id: '2' } });
    await rendered.queryClient.cancelQueries({ queryKey: coreKeys.callerKey('1'), exact: true });
    rendered.queryClient.removeQueries({ queryKey: coreKeys.callerKey('1'), exact: true });
    const cacheWriteSpy = vi.spyOn(rendered.queryClient, 'setQueryData');
    await act(async () => {
      lateResponse.resolve(
        response({
          secret,
          metadata: {
            display: 'nbk_CCCC…CCCQ',
            created_at: 1_700_000_000,
            updated_at: 1_700_000_001,
            generation: '1',
          },
        }),
      );
      await lateResponse.promise;
    });

    expect(cacheWriteSpy).not.toHaveBeenCalledWith(
      coreKeys.callerKey('1'),
      expect.objectContaining({ generation: '1' }),
    );
    expect(rendered.queryClient.getQueryData(coreKeys.callerKey('1'))).not.toEqual(
      expect.objectContaining({ generation: '1' }),
    );
    expect(screen.queryByText(secret)).not.toBeInTheDocument();
    expect(assertNoSensitiveQueryCache(rendered.queryClient, [secret]).hitSurfaces).toEqual([]);
    cacheWriteSpy.mockRestore();
  });

  it('reports Clipboard rejection while keeping the complete value selectable', async () => {
    const secret = `nbk_${'D'.repeat(42)}Q`;
    const writeText = vi.fn().mockRejectedValue(new Error('clipboard permission denied'));
    const previousClipboard = Object.getOwnPropertyDescriptor(window.navigator, 'clipboard');
    installCallerKeyFetch(secret);
    const rendered = await renderWithProviders(<CallerKeyPanel accountId="1" />, {
      station: 'user',
      role: 'user',
      locale: 'en',
    });
    Object.defineProperty(window.navigator, 'clipboard', {
      configurable: true,
      value: { writeText },
    });
    expect(navigator.clipboard?.writeText).toBe(writeText);
    rendered.queryClient.setQueryData(coreKeys.session, { user: { id: '1' } });

    await screen.findByText('No account API key');
    await rendered.user.click(screen.getByRole('button', { name: 'Create API key' }));
    await screen.findByText(secret);
    await rendered.user.click(screen.getByRole('button', { name: 'Copy' }));

    expect(
      await screen.findByText('Copy failed. Select and copy the value manually.'),
    ).toBeVisible();
    expect(writeText).toHaveBeenCalledWith(secret);
    expect(screen.getByText(secret)).toBeVisible();
    expect(screen.getByRole('button', { name: 'Copy' })).toBeVisible();
    if (previousClipboard) {
      Object.defineProperty(window.navigator, 'clipboard', previousClipboard);
    } else {
      Reflect.deleteProperty(window.navigator, 'clipboard');
    }
  });

  it('does not submit a second generation while the first operation is pending', async () => {
    const secret = `nbk_${'E'.repeat(42)}Q`;
    const lateResponse = deferred<Response>();
    let posts = 0;
    let generation = '0';
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      if (String(input) === '/api/caller-key' && (init?.method ?? 'GET') === 'GET') {
        return response(
          generation === '0'
            ? null
            : {
                display: 'nbk_EEEE…EEEE',
                created_at: 1_700_000_000,
                updated_at: 1_700_000_000,
                generation,
              },
          200,
          generation,
        );
      }
      if (String(input) === '/api/caller-key/regenerate' && init?.method === 'POST') {
        posts += 1;
        generation = '1';
        return lateResponse.promise;
      }
      throw new Error(`Unexpected request: ${init?.method ?? 'GET'} ${String(input)}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(<CallerKeyPanel accountId="1" />, {
      station: 'user',
      role: 'user',
      locale: 'en',
    });
    rendered.queryClient.setQueryData(coreKeys.session, { user: { id: '1' } });

    await screen.findByText('No account API key');
    await rendered.user.click(screen.getByRole('button', { name: 'Create API key' }));
    await waitFor(() => expect(posts).toBe(1));
    expect(screen.getByRole('button', { name: 'Working…' })).toBeDisabled();

    await rendered.user.click(screen.getByRole('button', { name: 'Working…' }));
    expect(posts).toBe(1);

    await act(async () => {
      lateResponse.resolve(
        response({
          secret,
          metadata: {
            display: 'nbk_EEEE…EEEE',
            created_at: 1_700_000_000,
            updated_at: 1_700_000_000,
            generation: '1',
          },
        }),
      );
      await lateResponse.promise;
    });
    expect(await screen.findByText(secret)).toBeVisible();
  });

  it('does not reveal a response after another page has advanced the generation', async () => {
    const secret = `nbk_${'F'.repeat(42)}Q`;
    const lateResponse = deferred<Response>();
    let generation = '0';
    let metadata: {
      display: string;
      created_at: number;
      updated_at: number;
      generation: string;
    } | null = null;
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      if (String(input) === '/api/caller-key' && (init?.method ?? 'GET') === 'GET') {
        return response(metadata, 200, generation);
      }
      if (String(input) === '/api/caller-key/regenerate' && init?.method === 'POST') {
        return lateResponse.promise;
      }
      throw new Error(`Unexpected request: ${init?.method ?? 'GET'} ${String(input)}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(<CallerKeyPanel accountId="1" />, {
      station: 'user',
      role: 'user',
      locale: 'en',
    });
    rendered.queryClient.setQueryData(coreKeys.session, { user: { id: '1' } });

    await screen.findByText('No account API key');
    await rendered.user.click(screen.getByRole('button', { name: 'Create API key' }));
    await waitFor(() =>
      expect(fetchMock.mock.calls.some(([, init]) => init?.method === 'POST')).toBe(true),
    );

    generation = '2';
    metadata = {
      display: 'nbk_FFFF…FFFF',
      created_at: 1_700_000_000,
      updated_at: 1_700_000_002,
      generation,
    };
    rendered.queryClient.setQueryData(coreKeys.callerKey('1'), { generation, metadata });
    await act(async () => {
      lateResponse.resolve(
        response({
          secret,
          metadata: {
            display: 'nbk_FFFF…FFFA',
            created_at: 1_700_000_000,
            updated_at: 1_700_000_001,
            generation: '1',
          },
        }),
      );
      await lateResponse.promise;
    });

    await waitFor(() => expect(screen.queryByText(secret)).not.toBeInTheDocument());
    expect(
      await screen.findByText('The data changed. Review the latest values and try again.'),
    ).toBeVisible();
    expect(rendered.queryClient.getQueryData(coreKeys.callerKey('1'))).toEqual({
      generation: '2',
      metadata,
    });
    expect(assertNoSensitiveQueryCache(rendered.queryClient, [secret]).hitSurfaces).toEqual([]);
  });

  it('explains a lost generation response and refreshes authority without revealing a value', async () => {
    const secret = `nbk_${'G'.repeat(42)}Q`;
    let reads = 0;
    const refreshedMetadata = {
      display: 'nbk_GGGG…GGGG',
      created_at: 1_700_000_000,
      updated_at: 1_700_000_003,
      generation: '1',
    };
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      if (String(input) === '/api/caller-key' && (init?.method ?? 'GET') === 'GET') {
        reads += 1;
        return reads === 1 ? response(null, 200, '0') : response(refreshedMetadata, 200, '1');
      }
      if (String(input) === '/api/caller-key/regenerate' && init?.method === 'POST') {
        return response({ error: { code: 'internal', message: 'response unavailable' } }, 500);
      }
      throw new Error(`Unexpected request: ${init?.method ?? 'GET'} ${String(input)}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(<CallerKeyPanel accountId="1" />, {
      station: 'user',
      role: 'user',
      locale: 'en',
    });
    rendered.queryClient.setQueryData(coreKeys.session, { user: { id: '1' } });

    await screen.findByText('No account API key');
    await rendered.user.click(screen.getByRole('button', { name: 'Create API key' }));

    expect(
      await screen.findByText(
        'The response was lost. The page is checking the latest status and will not resend the action.',
      ),
    ).toBeVisible();
    expect(screen.queryByText(secret)).not.toBeInTheDocument();
    expect(await screen.findByText('Key identifier (cannot be used for calls)')).toBeVisible();
    expect(rendered.queryClient.getQueryData(coreKeys.callerKey('1'))).toEqual({
      generation: '1',
      metadata: refreshedMetadata,
    });
  });

  it('renders a bounded error state when the authority read fails', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => response({ error: { code: 'internal', message: 'safe failure' } }, 500)),
    );
    await renderWithProviders(<CallerKeyPanel accountId="1" />, {
      station: 'user',
      role: 'user',
      locale: 'en',
    });
    expect(await screen.findByText('Could not load this section')).toBeInTheDocument();
    expect(screen.getByText('safe failure')).toBeInTheDocument();
  });
});
