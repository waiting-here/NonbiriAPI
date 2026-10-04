import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { act, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { assertNoSensitiveQueryCache, renderWithProviders } from '../../../../test/unit/support';
import { Quickstart } from './Quickstart';
import { coreKeys } from './queries';

function fixture(name: string): Record<string, unknown> {
  return JSON.parse(readFileSync(resolve(process.cwd(), '..', name), 'utf8')) as Record<
    string,
    unknown
  >;
}
function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}
const endpoint = fixture('internal/resources/testdata/endpoint.json');
const endpointKey = fixture('internal/resources/testdata/endpoint_key.json');
const session = fixture('internal/auth/testdata/user_envelope.json');
async function setup() {
  const rendered = await renderWithProviders(<Quickstart accountId="1" onClose={vi.fn()} />, {
    station: 'user',
    role: 'user',
    locale: 'en',
  });
  rendered.queryClient.setQueryData(coreKeys.session, session);
  await rendered.user.type(await screen.findByLabelText('Service URL'), 'https://example.com/v1');
  await rendered.user.click(screen.getByRole('button', { name: 'Next' }));
  await screen.findByLabelText('Key');
  return rendered;
}
afterEach(() => vi.unstubAllGlobals());
describe('Optional resource setup recovery', () => {
  it('checks a missing receipt, clears the plaintext, and asks for the same key only before its original retry', async () => {
    const writes: { body: string; key: string | null }[] = [];
    let refreshes = 0;
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
        const path = String(input);
        if (path === '/api/endpoint-create-options')
          return json({ base_connector_types: ['openai-compatible'], mainstream_channels: [] });
        if (path === '/api/endpoints' && init?.method === 'POST') return json(endpoint, 201);
        if (path === '/api/endpoints/11/keys' && init?.method === 'POST') {
          writes.push({
            body: String(init.body),
            key: new Headers(init.headers).get('Idempotency-Key'),
          });
          return writes.length === 1
            ? json({ error: { code: 'internal', message: 'Lost response' } }, 503)
            : json(endpointKey, 201);
        }
        if (path === '/api/resource-operation-status') return json({ status: 'not_recorded' });
        if (path === '/api/endpoints/11/keys/21/models/refresh') {
          refreshes++;
          return json(
            {
              operation_id: 'op_000000000000000000000A',
              evidence: {
                ...(fixture('internal/resources/testdata/catalog_unknown.json').evidence as Record<
                  string,
                  unknown
                >),
                state: 'checking',
                observed_at: 1700000000,
              },
            },
            202,
          );
        }
        if (path === '/api/endpoints/11/keys/21/models?limit=50')
          return json(fixture('internal/resources/testdata/catalog_succeeded_empty.json'));
        if (path.startsWith('/api/endpoints/11/keys/21/models?'))
          return json({
            ...fixture('internal/resources/testdata/catalog_succeeded_empty.json'),
            pagination: { page: '1', page_size: 20, total_items: '0', total_pages: '1' },
          });
        throw new Error('Unexpected request: ' + path);
      }),
    );
    const rendered = await setup();
    const secret = 'synthetic-transient-key';
    await rendered.user.type(screen.getByLabelText('Key'), secret);
    await rendered.user.click(
      screen.getByLabelText('This is my key, or I have permission to use it'),
    );
    await rendered.user.click(screen.getByRole('button', { name: 'Save and load models' }));
    const retrySecret = await screen.findByLabelText(
      'Enter the same key again to continue saving.',
    );
    expect(screen.queryByLabelText('Key')).not.toBeInTheDocument();
    expect(retrySecret).toHaveValue('');
    assertNoSensitiveQueryCache(rendered.queryClient, [secret]);
    await rendered.user.click(screen.getByRole('button', { name: 'Check result' }));
    expect(writes).toHaveLength(1);
    await rendered.user.type(retrySecret, secret);
    await rendered.user.click(screen.getByRole('button', { name: 'Check result' }));
    await screen.findByText('Found 0 models');
    expect(writes).toHaveLength(2);
    expect(writes[1]).toEqual(writes[0]);
    await waitFor(() => expect(refreshes).toBe(1));
    assertNoSensitiveQueryCache(rendered.queryClient, [secret]);
  });
  it.each(['automatic', 'manual'] as const)(
    'loads models after %s confirmation of a saved key without submitting it again',
    async (confirmation) => {
      let writes = 0;
      let refreshes = 0;
      let statusReads = 0;
      vi.stubGlobal(
        'fetch',
        vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
          const path = String(input);
          if (path === '/api/endpoint-create-options')
            return json({ base_connector_types: ['openai-compatible'], mainstream_channels: [] });
          if (path === '/api/endpoints' && init?.method === 'POST') return json(endpoint, 201);
          if (path === '/api/endpoints/11/keys' && init?.method === 'POST') {
            writes++;
            return json({ error: { code: 'internal', message: 'Lost response' } }, 503);
          }
          if (path === '/api/resource-operation-status') {
            statusReads++;
            return json(
              confirmation === 'manual' && statusReads === 1
                ? { status: 'not_recorded' }
                : {
                    status: 'recorded',
                    stage: 'key',
                    result: { endpoint_id: '11', endpoint_key_id: '21' },
                  },
            );
          }
          if (path.startsWith('/api/endpoints/11/keys?'))
            return json({ data: [endpointKey], next_cursor: null });
          if (path === '/api/endpoints/11/keys/21/models/refresh') {
            refreshes++;
            return json(
              {
                operation_id: 'op_000000000000000000000A',
                evidence: {
                  ...(fixture('internal/resources/testdata/catalog_unknown.json')
                    .evidence as Record<string, unknown>),
                  state: 'checking',
                  observed_at: 1700000000,
                },
              },
              202,
            );
          }
          if (path.startsWith('/api/endpoints/11/keys/21/models?'))
            return json({
              ...fixture('internal/resources/testdata/catalog_succeeded_empty.json'),
              ...(path.includes('page=')
                ? { pagination: { page: '1', page_size: 20, total_items: '0', total_pages: '1' } }
                : {}),
            });
          throw new Error('Unexpected request: ' + path);
        }),
      );
      const rendered = await setup();
      const secret = 'synthetic-confirmed-key';
      await rendered.user.type(screen.getByLabelText('Key'), secret);
      await rendered.user.click(
        screen.getByLabelText('This is my key, or I have permission to use it'),
      );
      await rendered.user.click(screen.getByRole('button', { name: 'Save and load models' }));
      if (confirmation === 'manual') {
        await screen.findByLabelText('Enter the same key again to continue saving.');
        expect(refreshes).toBe(0);
        await rendered.user.click(screen.getByRole('button', { name: 'Check result' }));
      }
      await waitFor(() => expect(refreshes).toBe(1));
      await screen.findByText('Found 0 models');
      expect(writes).toBe(1);
      expect(statusReads).toBe(confirmation === 'manual' ? 2 : 1);
      assertNoSensitiveQueryCache(rendered.queryClient, [secret]);
    },
  );
  it('keeps an expired unconfirmed create closed and directs continuation to saved resources', async () => {
    const close = vi.fn(),
      writes: string[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
        const path = String(input);
        if (path === '/api/endpoint-create-options')
          return json({ base_connector_types: ['openai-compatible'], mainstream_channels: [] });
        if (path === '/api/endpoints' && init?.method === 'POST') {
          writes.push(String(init.body));
          return json({ error: { code: 'internal', message: 'Lost response' } }, 503);
        }
        if (path === '/api/resource-operation-status') return json({ status: 'expired' });
        throw new Error('Unexpected request: ' + path);
      }),
    );
    const rendered = await renderWithProviders(<Quickstart accountId="1" onClose={close} />, {
      station: 'user',
      role: 'user',
      locale: 'en',
    });
    rendered.queryClient.setQueryData(coreKeys.session, session);
    await rendered.user.type(await screen.findByLabelText('Service URL'), 'https://example.com/v1');
    await rendered.user.click(screen.getByRole('button', { name: 'Next' }));
    await screen.findByText(
      'The action record has expired. Look for the saved resource before continuing setup.',
    );
    await rendered.user.click(screen.getByRole('button', { name: 'Check result' }));
    expect(writes).toHaveLength(1);
    expect(screen.queryByRole('button', { name: 'Next' })).not.toBeInTheDocument();
    await rendered.user.click(
      screen.getByRole('button', { name: 'Continue with an existing resource' }),
    );
    await waitFor(() => expect(close).toHaveBeenCalledTimes(1));
  });
});

it('does not resume a stopped connection when its earlier lookup returns late', async () => {
  let release!: (value: Response) => void;
  const lookupStarted = vi.fn();
  const modelWrites = vi.fn();
  const page = { page: '1', page_size: 20, total_items: '1', total_pages: '1' };
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const path = String(input);
      if (path === '/api/endpoint-create-options')
        return json({ base_connector_types: ['openai-compatible'], mainstream_channels: [] });
      if (path === '/api/endpoints' && init?.method === 'POST') return json(endpoint, 201);
      if (path === '/api/endpoints/11/keys' && init?.method === 'POST')
        return json(endpointKey, 201);
      if (path === '/api/endpoints/11/keys/21/models/refresh')
        return json(
          {
            operation_id: 'op_000000000000000000000A',
            evidence: {
              ...(fixture('internal/resources/testdata/catalog_unknown.json').evidence as Record<
                string,
                unknown
              >),
              state: 'checking',
              observed_at: 1700000000,
            },
          },
          202,
        );
      if (path === '/api/endpoints/11/keys/21/models?limit=50')
        return json(fixture('internal/resources/testdata/catalog_succeeded_empty.json'));
      if (path.startsWith('/api/endpoints/11/keys/21/models?'))
        return json({
          ...fixture('internal/resources/testdata/catalog_succeeded_empty.json'),
          manual_entries: [
            {
              id: '31',
              source_type: 'manual',
              upstream_model_id: 'sample-model',
              provider: 'vendor',
              source_revision: '2',
              pair_revision: '2',
              created_at: 1700000003,
              updated_at: 1700000013,
            },
          ],
          pagination: page,
        });
      if (path.startsWith('/api/models?')) {
        lookupStarted();
        return new Promise<Response>((resolve) => {
          release = resolve;
        });
      }
      if (path === '/api/models' && init?.method === 'POST') {
        modelWrites();
        return json({ error: { code: 'internal', message: 'Unexpected old flow write' } }, 503);
      }
      throw new Error('Unexpected request: ' + path);
    }),
  );
  const rendered = await setup();
  await rendered.user.type(screen.getByLabelText('Key'), 'synthetic-transient-key');
  await rendered.user.click(
    screen.getByLabelText('This is my key, or I have permission to use it'),
  );
  await rendered.user.click(screen.getByRole('button', { name: 'Save and load models' }));
  await rendered.user.type(await screen.findByLabelText('Model name prefix'), 'vendor');
  await rendered.user.click(await screen.findByLabelText('sample-model'));
  await rendered.user.click(screen.getByRole('button', { name: 'Add 1 models' }));
  await waitFor(() => expect(lookupStarted).toHaveBeenCalledOnce());
  await rendered.user.click(screen.getByRole('button', { name: /Back to My services/ }));
  await rendered.user.click(screen.getByRole('button', { name: 'Continue setup' }));
  await act(async () =>
    release(json({ data: [], pagination: { ...page, page_size: 50, total_items: '0' } })),
  );
  expect(modelWrites).not.toHaveBeenCalled();
  expect(screen.getByRole('button', { name: 'Add 1 models' })).toBeEnabled();
});
