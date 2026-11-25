import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
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
  it.each(['custom', 'mainstream'] as const)(
    'edits a %s service rerank path and restores the original suffix without changing its request format',
    async (source) => {
      let endpoint = {
        ...endpointWire(),
        origin:
          source === 'mainstream'
            ? { kind: 'mainstream', channel_id: 'mch_' + 'A'.repeat(22), name: 'Preset service' }
            : { kind: 'custom' },
        native_services: ['rerank'],
        rerank_format: 'cohere',
        rerank_path: '/saved/reranks',
        rerank_path_configurable: true,
      };
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
                  native_services: ['rerank'],
                  default_native_services: ['rerank'],
                  rerank_formats: ['generic', 'cohere'],
                },
              ],
            });
          if (url.pathname === '/api/endpoints/11') {
            if (init?.method === 'PATCH') {
              const body = JSON.parse(String(init.body));
              writes.push(body);
              endpoint = { ...endpoint, ...body, revision: String(Number(endpoint.revision) + 1) };
            }
            return json(endpoint);
          }
          throw Error('Unexpected ' + url.pathname);
        }),
      );
      const view = await renderWithProviders(
        <EntryScope accountId="1" endpoint={normalizeEndpoint(endpoint)} />,
        { station: 'user' },
      );
      view.queryClient.setQueryData(['user', 'session'], {
        user: { id: '1', username: 'fixture', effective_level: 1 },
      });
      await view.user.click(screen.getByText('Endpoint settings', { exact: true }));
      const path = await screen.findByRole('textbox', { name: /Rerank interface path/ });
      expect(path).toBeVisible();
      await view.user.clear(path);
      await view.user.type(path, '/compatible-api/v1/reranks');
      await view.user.click(screen.getByRole('button', { name: 'Save' }));
      await screen.findByText('Saved');
      await view.user.clear(path);
      await view.user.click(screen.getByRole('button', { name: 'Save' }));
      await waitFor(() => expect(writes).toHaveLength(2));
      expect(writes).toEqual([
        {
          expected_revision: '1',
          native_services: ['rerank'],
          rerank_path: '/compatible-api/v1/reranks',
        },
        { expected_revision: '2', native_services: ['rerank'], rerank_path: '' },
      ]);
      expect(screen.getByRole('combobox', { name: 'Rerank format' })).toHaveValue('cohere');
    },
  );
  it.each([
    ['typesafe-systemone', 'systemone', 'openai'],
    ['anthropic-compatible', 'anthropic_messages', 'anthropic'],
    ['google-gemini', 'gemini_generate_content', 'gemini'],
  ])('uses the %s catalog default in new list settings', async (protocol, service, format) => {
    const view = await renderWithProviders(
      <KeyCredentialScope
        accountId="1"
        endpoint={normalizeEndpoint({
          ...endpointWire(),
          connector_type: protocol,
          configuration_type: protocol,
          native_services: [service],
        })}
        entryKey={normalizeEndpointKey({
          ...keyWire(),
          discovery_source_id: null,
          native_services: [service],
        })}
        defaultOpen
      />,
      { station: 'user', role: 'user' },
    );
    expect(screen.getByRole('combobox', { name: 'Catalog format' })).toHaveValue(format);
    expect(screen.getByRole('textbox', { name: 'Complete model list URL' })).toHaveValue(
      endpointWire().base_url + '/models',
    );
    view.unmount();
  });
  it('keeps an existing System One catalog format ahead of the default', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(async (input) => {
        const url = new URL(String(input), 'https://app.example');
        if (url.pathname !== '/api/discovery-sources/51') throw Error('Unexpected ' + url.pathname);
        return json({
          id: '51',
          connection_id: null,
          name: 'Chosen catalog',
          base_url: 'https://catalog.example/custom',
          models_url: 'https://catalog.example/custom',
          list_format: 'anthropic',
          credential_id: '8',
          credential_version: '2',
          revision: '1',
          discovery_source_revision: '1',
          enabled: true,
          state: 'unknown',
          checked_at: null,
          safe_class: 'none',
          safe_diag: '',
          created_at: 1700000000,
          updated_at: 1700000000,
        });
      }),
    );
    await renderWithProviders(
      <KeyCredentialScope
        accountId="1"
        endpoint={normalizeEndpoint({
          ...endpointWire(),
          connector_type: 'typesafe-systemone',
          configuration_type: 'typesafe-systemone',
          native_services: ['systemone'],
        })}
        entryKey={normalizeEndpointKey({
          ...keyWire(),
          native_services: ['systemone'],
        })}
        defaultOpen
      />,
      { station: 'user', role: 'user' },
    );
    expect(await screen.findByRole('combobox', { name: 'Catalog format' })).toHaveValue(
      'anthropic',
    );
    expect(screen.getByRole('textbox', { name: 'Complete model list URL' })).toHaveValue(
      'https://catalog.example/custom',
    );
  });
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
    await view.user.click(screen.getByText('Endpoint settings', { exact: true }));
    const name = screen.getByRole('textbox', { name: 'Endpoint name' });
    await view.user.clear(name);
    await view.user.type(name, 'Renamed service');
    await view.user.click(screen.getByRole('button', { name: 'Save' }));
    await screen.findByText('Saved');
    expect(writes).toEqual([
      { note: 'Renamed service', native_services: ['chat_completions'], expected_revision: '1' },
    ]);
    expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled();
  });
  it('uses the confirmed entry revision for successive endpoint saves', async () => {
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
                native_services: ['chat_completions', 'responses', 'embeddings'],
                default_native_services: ['chat_completions'],
                rerank_formats: [],
              },
            ],
          });
        if (init?.method === 'PATCH') {
          const body = JSON.parse(String(init.body));
          writes.push(body);
          const previous = endpoint;
          expect(body.expected_revision).toBe(previous.revision);
          const next = {
            ...previous,
            native_services: body.native_services,
            revision: String(Number(previous.revision) + 1),
          };
          endpoint = next;
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
        if (url.pathname === '/api/endpoints/11') return json(endpoint);
        throw Error('Unexpected ' + url.pathname);
      }),
    );
    const initialEndpoint = normalizeEndpoint(endpoint);
    const view = await renderWithProviders(
      <EntryScope accountId="1" endpoint={initialEndpoint} />,
      { station: 'user', role: 'user' },
    );
    view.queryClient.setQueryData(['user', 'session'], {
      user: { id: '1', username: 'owner', effective_level: 1 },
    });
    await view.user.click(
      screen.getByText('Endpoint settings', {
        exact: true,
      }),
    );
    await view.user.type(screen.getByRole('textbox', { name: 'Endpoint name' }), ' first');
    await view.user.click(screen.getByRole('button', { name: 'Save' }));
    await screen.findByText('Saved');
    await view.user.type(screen.getByRole('textbox', { name: 'Endpoint name' }), ' second');
    await view.user.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(writes).toHaveLength(2));
    expect(writes.map((row) => row.expected_revision)).toEqual(['1', '2']);
    expect(writes[1]?.native_services).toEqual(['chat_completions', 'responses', 'embeddings']);
  });

  it('shows the credential controls without an empty protocol save form', async () => {
    const view = await renderWithProviders(
      <KeyCredentialScope
        accountId="1"
        endpoint={normalizeEndpoint(endpointWire())}
        entryKey={normalizeEndpointKey(keyWire())}
      />,
      { station: 'user', role: 'user' },
    );
    expect(view.container.querySelector('details')).toBeInTheDocument();
    expect(screen.queryByText('Protocol access')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Save' })).not.toBeInTheDocument();
  });
});
