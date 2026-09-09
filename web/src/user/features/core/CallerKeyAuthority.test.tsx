import { act, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { assertNoSensitiveQueryCache, renderWithProviders } from '../../../../test/unit/support';
import { CallerKeyPanel } from '../../pages/KeysPage';
import { coreKeys } from './queries';

const metadata = {
  display: 'nbk_AAAA…AAAA',
  created_at: 1_700_000_000,
  updated_at: 1_700_000_000,
  generation: '1',
};

function response(body: unknown, generation?: string): Response {
  const headers = new Headers({ 'Content-Type': 'application/json' });
  if (generation !== undefined) headers.set('X-Nonbiri-CallerKey-Generation', generation);
  return new Response(JSON.stringify(body), { status: 200, headers });
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((settle) => {
    resolve = settle;
  });
  return { promise, resolve };
}

describe('CallerKey authority response ordering', () => {
  it('reveals the single successful response after a GET observes that same generation', async () => {
    const secret = `nbk_${'A'.repeat(43)}`;
    const post = deferred<Response>();
    let committed = false;
    let posts = 0;
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
        if (String(input) === '/api/caller-key' && (init?.method ?? 'GET') === 'GET') {
          return response(committed ? metadata : null, committed ? '1' : '0');
        }
        if (String(input) === '/api/caller-key/regenerate' && init?.method === 'POST') {
          expect(JSON.parse(String(init.body))).toEqual({ expected_generation: '0' });
          posts += 1;
          committed = true;
          return post.promise;
        }
        throw new Error('Unexpected synthetic request');
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
    await waitFor(() => expect(posts).toBe(1));
    await act(async () => {
      await rendered.queryClient.refetchQueries({ queryKey: coreKeys.callerKey('1'), exact: true });
    });
    expect(await screen.findByText(metadata.display)).toBeVisible();
    expect(screen.getByRole('button', { name: 'Working…' })).toBeDisabled();
    await act(async () => {
      post.resolve(response({ secret, metadata }));
      await post.promise;
    });
    expect(await screen.findByText(secret)).toBeVisible();
    expect(posts).toBe(1);
    expect(rendered.queryClient.getQueryData(coreKeys.callerKey('1'))).toEqual({
      generation: '1',
      metadata,
    });
    expect(assertNoSensitiveQueryCache(rendered.queryClient, [secret]).hitSurfaces).toEqual([]);
  });

  it('does not overwrite a newer cached authority with an older outstanding GET', async () => {
    const oldRead = deferred<Response>();
    let reads = 0;
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
        if (String(input) !== '/api/caller-key' || (init?.method ?? 'GET') !== 'GET') {
          throw new Error('Unexpected synthetic request');
        }
        reads += 1;
        return reads === 1 ? response(null, '0') : oldRead.promise;
      }),
    );
    const rendered = await renderWithProviders(<CallerKeyPanel accountId="1" />, {
      station: 'user',
      role: 'user',
      locale: 'en',
    });
    rendered.queryClient.setQueryData(coreKeys.session, { user: { id: '1' } });
    await screen.findByText('No account API key');
    const pending = rendered.queryClient.refetchQueries({
      queryKey: coreKeys.callerKey('1'),
      exact: true,
    });
    await waitFor(() => expect(reads).toBe(2));
    await act(async () => {
      rendered.queryClient.setQueryData(coreKeys.callerKey('1'), { generation: '1', metadata });
    });
    expect(await screen.findByText(metadata.display)).toBeVisible();
    await act(async () => {
      oldRead.resolve(response(null, '0'));
      await pending;
    });
    expect(rendered.queryClient.getQueryData(coreKeys.callerKey('1'))).toEqual({
      generation: '1',
      metadata,
    });
    expect(screen.getByRole('button', { name: 'Replace API key' })).toBeEnabled();
    expect(screen.queryByText('No account API key')).not.toBeInTheDocument();
  });
});
