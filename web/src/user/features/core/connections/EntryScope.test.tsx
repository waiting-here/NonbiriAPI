import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { useState } from 'react';
import { screen, waitFor } from '@testing-library/react';
import { describe, it, expect, vi } from 'vitest';
import { renderWithProviders } from '../../../../../test/unit/support';
import { normalizeEndpoint, normalizeEndpointKey } from '../normalizers';
import { EntryScope, KeyCredentialScope } from './EntryScope';
const endpointWire = () => ({
  ...JSON.parse(
    readFileSync(resolve(process.cwd(), '../internal/resources/testdata/endpoint.json'), 'utf8'),
  ),
  id: '11',
  connection_id: null,
  native_services: ['chat_completions'],
  rerank_format: '',
  revision: '1',
});
const keyWire = () => ({
  ...JSON.parse(
    readFileSync(
      resolve(process.cwd(), '../internal/resources/testdata/endpoint_key.json'),
      'utf8',
    ),
  ),
  id: '5',
  endpoint_id: '11',
  credential_id: '8',
  credential_version: '2',
  grant_revision: '3',
  discovery_source_id: '51',
  native_services: ['chat_completions'],
  revision: '1',
});
const json = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), { status, headers: { 'content-type': 'application/json' } });
const emptyPage = {
  data: [],
  next_cursor: null,
  pagination: { page: '1', page_size: 20, total_items: '0', total_pages: '1' },
};
describe('persistent entry metadata drafts', () => {
  it('edits the service name with one patch while explicitly authorizing the complete protocol', async () => {
    let endpoint = endpointWire();
    const writes: Record<string, unknown>[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(async (input, init) => {
        const url = new URL(String(input), 'https://app.example');
        if (url.pathname === '/api/endpoint-create-options')
          return json({
            base_connector_types: ['openai-compatible'],
            mainstream_channels: [],
            native_service_options: [
              {
                connector_type: 'openai-compatible',
                configuration_type: 'openai-compatible',
                native_services: ['chat_completions'],
                default_native_services: ['chat_completions'],
                rerank_formats: [],
              },
            ],
          });
        if (init?.method === 'PATCH') {
          const body = JSON.parse(String(init.body));
          writes.push(body);
          endpoint = { ...endpoint, ...body, revision: '2' };
          return json(endpoint);
        }
        if (url.pathname === '/api/endpoints/11') return json(endpoint);
        if (url.pathname === '/api/connections') return json(emptyPage);
        if (url.pathname === '/api/endpoint-create-options')
          return json({
            base_connector_types: ['openai-compatible'],
            mainstream_channels: [],
            native_service_options: [
              {
                connector_type: 'openai-compatible',
                configuration_type: 'openai-compatible',
                default_native_services: ['chat_completions'],
                native_services: ['chat_completions'],
                rerank_formats: [],
              },
            ],
          });
        throw Error('Unexpected ' + url.pathname);
      }),
    );
    const view = await renderWithProviders(
      <EntryScope accountId="1" endpoint={normalizeEndpoint(endpoint)} />,
      { station: 'user', role: 'user' },
    );
    view.queryClient.setQueryData(['user', 'session'], {
      user: { id: '1', username: 'fixture-user', effective_level: 1 },
    });
    await view.user.click(screen.getByText('Edit service', { exact: true }));
    const name = screen.getByRole('textbox', { name: 'Service name' });
    await view.user.clear(name);
    await view.user.type(name, 'Renamed service');
    await view.user.click(screen.getByRole('button', { name: 'Save' }));
    await screen.findByText('Saved');
    expect(writes).toEqual([
      { note: 'Renamed service', native_services: ['chat_completions'], expected_revision: '1' },
    ]);
    expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled();
  });
  it.each(['entry', 'key'] as const)(
    'uses the confirmed %s revision for a second save without adopting unrelated refetches',
    async (kind) => {
      let endpoint = endpointWire();
      let key = keyWire();
      const writes: Record<string, unknown>[] = [];
      vi.stubGlobal(
        'fetch',
        vi.fn<typeof fetch>(async (input, init) => {
          const url = new URL(String(input), 'https://app.example');
          if (url.pathname === '/api/endpoint-create-options')
            return json({
              base_connector_types: ['openai-compatible'],
              mainstream_channels: [],
              native_service_options: [
                {
                  connector_type: 'openai-compatible',
                  configuration_type: 'openai-compatible',
                  native_services: ['chat_completions', 'responses', 'embeddings'],
                  default_native_services: ['chat_completions'],
                  rerank_formats: [],
                },
              ],
            });
          if (init?.method === 'PATCH') {
            const body = JSON.parse(String(init.body));
            writes.push(body);
            const previous = kind === 'entry' ? endpoint : key;
            expect(body.expected_revision).toBe(previous.revision);
            const next = {
              ...previous,
              native_services: body.native_services,
              revision: String(Number(previous.revision) + 1),
            };
            if (kind === 'entry') endpoint = next;
            else key = next;
            return json(next);
          }
          if (url.pathname === '/api/endpoint-create-options')
            return json({
              base_connector_types: ['openai-compatible'],
              mainstream_channels: [],
              native_service_options: [
                {
                  connector_type: 'openai-compatible',
                  configuration_type: 'openai-compatible',
                  default_native_services: ['chat_completions'],
                  native_services: ['chat_completions', 'responses', 'embeddings'],
                  rerank_formats: [],
                },
              ],
            });
          if (url.pathname === '/api/connections') return json(emptyPage);
          if (url.pathname === '/api/endpoints/11/keys')
            return json({ data: [key], next_cursor: null });
          if (url.pathname === '/api/endpoints/11') return json(endpoint);
          throw Error('Unexpected ' + url.pathname);
        }),
      );
      const initialEndpoint = normalizeEndpoint({
        ...endpoint,
        native_services:
          kind === 'entry' ? ['chat_completions'] : ['chat_completions', 'responses', 'embeddings'],
      });
      const view = await renderWithProviders(
        kind === 'entry' ? (
          <EntryScope accountId="1" endpoint={initialEndpoint} />
        ) : (
          <KeyCredentialScope
            accountId="1"
            endpoint={initialEndpoint}
            entryKey={normalizeEndpointKey(key)}
          />
        ),
        { station: 'user', role: 'user' },
      );
      view.queryClient.setQueryData(['user', 'session'], {
        user: { id: '1', username: 'owner', effective_level: 1 },
      });
      await view.user.click(
        screen.getByText(
          kind === 'entry' ? 'Edit service' : 'Credential and service authorization',
          { exact: true },
        ),
      );
      if (kind === 'entry')
        await view.user.type(screen.getByRole('textbox', { name: 'Service name' }), ' first');
      await view.user.click(screen.getByRole('button', { name: 'Save' }));
      await screen.findByText('Saved');
      if (kind === 'entry')
        await view.user.type(screen.getByRole('textbox', { name: 'Service name' }), ' second');
      await view.user.click(screen.getByRole('button', { name: 'Save' }));
      await waitFor(() => expect(writes).toHaveLength(2));
      expect(writes.map((row) => row.expected_revision)).toEqual(['1', '2']);
      expect(writes[1]?.native_services).toEqual(['chat_completions', 'responses', 'embeddings']);
    },
  );

  it('rereads a confirmed save after its metadata read fails, then continues with the new CAS', async () => {
    let current = keyWire();
    let reads = 0;
    const writes: { body: Record<string, unknown>; key: string | null }[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(async (input, init) => {
        const url = new URL(String(input), 'https://app.example');
        if (url.pathname === '/api/endpoint-create-options')
          return json({
            base_connector_types: ['openai-compatible'],
            mainstream_channels: [],
            native_service_options: [
              {
                connector_type: 'openai-compatible',
                configuration_type: 'openai-compatible',
                native_services: ['chat_completions', 'responses', 'embeddings'],
                default_native_services: ['chat_completions'],
                rerank_formats: [],
              },
            ],
          });
        if (url.pathname === '/api/endpoints/11/keys/5' && init?.method === 'PATCH') {
          const body = JSON.parse(String(init.body));
          writes.push({ body, key: new Headers(init.headers).get('Idempotency-Key') });
          expect(body.expected_revision).toBe(current.revision);
          current = {
            ...current,
            native_services: body.native_services,
            revision: String(Number(current.revision) + 1),
          };
          return json(current);
        }
        if (url.pathname === '/api/endpoints/11/keys') {
          reads += 1;
          if (reads === 1)
            return json(
              { error: { code: 'internal_error', message: 'Metadata read unavailable' } },
              500,
            );
          return json({ data: [current], next_cursor: null });
        }
        throw Error('Unexpected ' + (init?.method ?? 'GET') + ' ' + url.pathname);
      }),
    );
    const view = await renderWithProviders(
      <KeyCredentialScope
        accountId="1"
        endpoint={normalizeEndpoint({
          ...endpointWire(),
          native_services: ['chat_completions', 'responses', 'embeddings'],
        })}
        entryKey={normalizeEndpointKey(current)}
      />,
      { station: 'user', role: 'user' },
    );
    view.queryClient.setQueryData(['user', 'session'], {
      user: { id: '1', username: 'owner', effective_level: 1 },
    });
    await view.user.click(
      screen.getByText('Credential and service authorization', { exact: true }),
    );
    await view.user.click(screen.getByRole('button', { name: 'Save' }));
    const reread = await screen.findByRole('button', { name: 'Retry' });
    expect(writes).toHaveLength(1);
    expect(reads).toBe(1);
    expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled();
    expect(screen.queryByRole('checkbox', { name: 'Responses' })).not.toBeInTheDocument();
    await view.user.click(reread);
    await screen.findByText('Saved');
    expect(writes).toHaveLength(1);
    expect(reads).toBe(2);
    expect(screen.queryByRole('checkbox', { name: 'Responses' })).not.toBeInTheDocument();
    await view.user.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(writes).toHaveLength(2));
    expect(writes.map((row) => row.body.expected_revision)).toEqual(['1', '2']);
    expect(writes[1]?.key).not.toBe(writes[0]?.key);
    expect(writes[1]?.body).toEqual({
      native_services: ['chat_completions', 'responses', 'embeddings'],
      expected_revision: '2',
    });
  });

  it('retains the current catalog when resolving a service-only key conflict', async () => {
    let current = keyWire();
    let adoptSnapshot: () => void = () => undefined;
    const writes: Record<string, unknown>[] = [];
    function Host() {
      const [key, setKey] = useState(normalizeEndpointKey(current));
      adoptSnapshot = () => setKey(normalizeEndpointKey(current));
      return (
        <KeyCredentialScope
          accountId="1"
          endpoint={normalizeEndpoint({
            ...endpointWire(),
            native_services: ['chat_completions', 'responses'],
          })}
          entryKey={key}
        />
      );
    }
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(async (input, init) => {
        const url = new URL(String(input), 'https://app.example');
        if (url.pathname === '/api/endpoint-create-options')
          return json({
            base_connector_types: ['openai-compatible'],
            mainstream_channels: [],
            native_service_options: [
              {
                connector_type: 'openai-compatible',
                configuration_type: 'openai-compatible',
                native_services: ['chat_completions', 'responses', 'embeddings'],
                default_native_services: ['chat_completions'],
                rerank_formats: [],
              },
            ],
          });
        if (init?.method === 'PATCH') {
          const body = JSON.parse(String(init.body));
          writes.push(body);
          if (writes.length === 1) {
            current = { ...current, revision: '2' };
            adoptSnapshot();
            return json({ error: { code: 'conflict', message: 'Entry changed' } }, 409);
          }
          expect(body.expected_revision).toBe('2');
          current = { ...current, native_services: body.native_services, revision: '3' };
          return json(current);
        }
        if (url.pathname === '/api/endpoints/11/keys')
          return json({ data: [current], next_cursor: null });
        throw Error('Unexpected ' + url.pathname);
      }),
    );
    const view = await renderWithProviders(<Host />, { station: 'user', role: 'user' });
    view.queryClient.setQueryData(['user', 'session'], {
      user: { id: '1', username: 'owner', effective_level: 1 },
    });
    await view.user.click(
      screen.getByText('Credential and service authorization', { exact: true }),
    );
    await view.user.click(screen.getByRole('button', { name: 'Save' }));
    await screen.findByText('The data changed. Review the latest values and try again.');
    expect(screen.queryByRole('checkbox', { name: 'Responses' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled();
    await view.user.click(screen.getByRole('button', { name: 'Continue with current revision' }));
    await view.user.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(writes).toHaveLength(2));
    expect(writes[1]).toEqual({
      native_services: ['chat_completions', 'responses', 'embeddings'],
      expected_revision: '2',
    });
    expect(writes[1]).not.toHaveProperty('discovery_source_id');
    expect(current.discovery_source_id).toBe('51');
  });
});
