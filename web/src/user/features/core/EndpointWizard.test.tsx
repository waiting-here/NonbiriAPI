import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { fireEvent, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { assertNoSensitiveQueryCache, renderWithProviders } from '../../../../test/unit/support';
import { EndpointWizard } from './EndpointWizard';
import { coreKeys } from './queries';

function endpointFixture(): unknown {
  return JSON.parse(
    readFileSync(resolve(process.cwd(), '..', 'internal/resources/testdata/endpoint.json'), 'utf8'),
  ) as unknown;
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(status === 204 ? null : JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function endpointOptionsResponse(): Response {
  return jsonResponse({
    base_connector_types: ['openai-compatible', 'anthropic-compatible'],
    mainstream_channels: [],
  });
}

function storageValues(storage: Storage): string {
  return [...Array(storage.length)]
    .map((_, index) => storage.getItem(storage.key(index) ?? '') ?? '')
    .join('');
}

async function reachEndpointForm(user: Awaited<ReturnType<typeof renderWithProviders>>['user']) {
  await user.click(await screen.findByRole('button', { name: 'Next' }));
  await user.type(screen.getByLabelText('Service address'), 'https://example.com/v1');
}

describe('EndpointWizard secret and exact-replay boundaries', () => {
  it('defaults to a mainstream channel and submits only its strict union fields', async () => {
    const channelID = `mch_${'C'.repeat(21)}A`;
    const onCreated = vi.fn();
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      if (String(input) === '/api/endpoint-create-options') {
        return jsonResponse({
          base_connector_types: ['openai-compatible', 'anthropic-compatible'],
          mainstream_channels: [
            {
              id: channelID,
              name: 'Hosted channel',
              connector_type: 'openai-compatible',
              base_url: 'https://example.com/v1',
            },
          ],
        });
      }
      if (String(input) === '/api/endpoints' && init?.method === 'POST') {
        return jsonResponse(endpointFixture(), 201);
      }
      throw new Error(`Unexpected request: ${init?.method ?? 'GET'} ${String(input)}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(
      <EndpointWizard accountId="1" onClose={vi.fn()} onCreated={onCreated} />,
      { station: 'user', role: 'user', locale: 'en' },
    );
    rendered.queryClient.setQueryData(coreKeys.session, { user: { id: '1' } });

    expect(await screen.findByRole('button', { name: 'Mainstream channel' })).toHaveAttribute(
      'aria-pressed',
      'true',
    );
    await rendered.user.click(screen.getByRole('button', { name: 'Next' }));
    const url = screen.getByLabelText('Service address');
    expect(url).toHaveAttribute('readonly');
    expect(url).toHaveValue('https://example.com/v1');
    await rendered.user.type(screen.getByLabelText('Note'), 'channel note');
    await rendered.user.click(screen.getByRole('button', { name: 'Create endpoint' }));
    await waitFor(() => expect(onCreated).toHaveBeenCalledTimes(1));

    const post = fetchMock.mock.calls.find(([, request]) => request?.method === 'POST');
    expect(JSON.parse(String(post?.[1]?.body))).toEqual({
      source: 'mainstream',
      channel_id: channelID,
      note: 'channel note',
      enabled: true,
    });
  });

  it('keeps a locally invalid secret only in the mounted component and clears it on cancel', async () => {
    const syntheticSecret = 'bad\u0001secret-marker';
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      if (String(input) === '/api/endpoint-create-options') return endpointOptionsResponse();
      if (String(input) === '/api/endpoints' && init?.method === 'POST') {
        return jsonResponse(endpointFixture(), 201);
      }
      throw new Error(`Unexpected request: ${init?.method ?? 'GET'} ${String(input)}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(
      <EndpointWizard accountId="1" onClose={vi.fn()} onCreated={vi.fn()} />,
      { station: 'user', role: 'user', locale: 'en' },
    );
    rendered.queryClient.setQueryData(coreKeys.session, { user: { id: '1' } });
    await reachEndpointForm(rendered.user);
    await rendered.user.click(screen.getByRole('button', { name: 'Create endpoint' }));

    const secret = await screen.findByLabelText('Service key');
    fireEvent.change(secret, { target: { value: syntheticSecret } });
    await rendered.user.type(screen.getByLabelText('Key note'), 'note remains');
    await rendered.user.click(screen.getByLabelText(/I own this credential/));
    await rendered.user.click(screen.getByRole('button', { name: 'Add key' }));

    expect(await screen.findByText(/Other fields were kept/)).toBeVisible();
    expect(screen.getByLabelText('Service key')).toHaveValue(syntheticSecret);
    expect(screen.getByLabelText('Key note')).toHaveValue('note remains');
    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(
      assertNoSensitiveQueryCache(rendered.queryClient, [syntheticSecret]).hitSurfaces,
    ).toEqual([]);
    expect(storageValues(window.localStorage)).not.toContain(syntheticSecret);
    expect(storageValues(window.sessionStorage)).not.toContain(syntheticSecret);

    await rendered.user.click(screen.getByRole('button', { name: 'Cancel' }));
    await waitFor(() => expect(screen.getByLabelText('Service key')).toHaveValue(''));
  });

  it('GET-reconciles first and reuses one request body and Idempotency-Key after response loss', async () => {
    const onCreated = vi.fn();
    let endpointPosts = 0;
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      if (String(input) === '/api/endpoint-create-options') return endpointOptionsResponse();
      if (String(input) === '/api/endpoints' && init?.method === 'POST') {
        endpointPosts += 1;
        return endpointPosts === 1
          ? jsonResponse({ error: { code: 'internal', message: 'safe uncertain response' } }, 503)
          : jsonResponse(endpointFixture(), 201);
      }
      if (String(input) === '/api/endpoints?limit=50' && (init?.method ?? 'GET') === 'GET') {
        return jsonResponse({ data: [], next_cursor: null });
      }
      throw new Error(`Unexpected request: ${init?.method ?? 'GET'} ${String(input)}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(
      <EndpointWizard accountId="1" onClose={vi.fn()} onCreated={onCreated} />,
      { station: 'user', role: 'user', locale: 'en' },
    );
    rendered.queryClient.setQueryData(coreKeys.session, { user: { id: '1' } });
    await reachEndpointForm(rendered.user);
    await rendered.user.type(screen.getByLabelText('Note'), 'endpoint note');
    await rendered.user.click(screen.getByRole('button', { name: 'Create endpoint' }));

    const replay = await screen.findByRole('button', { name: 'Retry the same operation' });
    expect(screen.getByLabelText('Service address')).toBeDisabled();
    await rendered.user.click(replay);
    await waitFor(() => expect(onCreated).toHaveBeenCalledTimes(1));

    const posts = fetchMock.mock.calls.filter(([, init]) => init?.method === 'POST');
    expect(posts).toHaveLength(2);
    const firstHeaders = new Headers(posts[0]?.[1]?.headers);
    const secondHeaders = new Headers(posts[1]?.[1]?.headers);
    expect(firstHeaders.get('Idempotency-Key')).toMatch(/^[A-Za-z0-9_-]{22,128}$/);
    expect(secondHeaders.get('Idempotency-Key')).toBe(firstHeaders.get('Idempotency-Key'));
    expect(
      fetchMock.mock.calls.map(([input, init]) => `${init?.method ?? 'GET'} ${String(input)}`),
    ).toEqual([
      'GET /api/endpoint-create-options',
      'POST /api/endpoints',
      'GET /api/endpoints?limit=50',
      'POST /api/endpoints',
    ]);
    expect(posts.map(([, init]) => JSON.parse(String(init?.body)))).toEqual([
      {
        source: 'custom',
        connector_type: 'openai-compatible',
        base_url: 'https://example.com/v1',
        note: 'endpoint note',
        enabled: true,
      },
      {
        source: 'custom',
        connector_type: 'openai-compatible',
        base_url: 'https://example.com/v1',
        note: 'endpoint note',
        enabled: true,
      },
    ]);
  });
});
