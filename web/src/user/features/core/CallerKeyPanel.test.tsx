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
