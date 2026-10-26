import { useState } from 'react';
import { screen, waitFor, within } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../../test/unit/support';
import { connectionAPI } from '../connections/api';
import { getBindingCandidatesPage, listEndpointKeysPage, listEndpointsPage } from '../pageApi';
import type { NumberedPage, PageWindow } from '../pageTypes';
import type { BindingCandidate, BindingTarget, Endpoint, EndpointKey } from '../types';
import { boundIdentities, candidateIdentity } from './bindingIdentity';
import { PersonalBindingPicker } from './PersonalBindingPicker';

vi.mock('../connections/api', () => ({ connectionAPI: { list: vi.fn(), entries: vi.fn() } }));
vi.mock('../pageApi', () => ({
  getBindingCandidatesPage: vi.fn(),
  listEndpointKeysPage: vi.fn(),
  listEndpointsPage: vi.fn(),
}));

const endpoints: Endpoint[] = ['11', '12'].map((id) => ({
  id,
  connection_id: id === '11' ? '51' : '52',
  note: `Endpoint ${id}`,
  base_url: `https://service-${id}.example/v1`,
  connector_type: 'openai-compatible',
  origin: { kind: 'custom' },
  enabled: true,
  revision: '1',
  key_count: id === '11' ? '2' : '1',
  created_at: 1,
  updated_at: 1,
}));
const keys: EndpointKey[] = ['111', '112', '121'].map((id) => ({
  id,
  endpoint_id: id === '121' ? '12' : '11',
  note: `Key ${id}`,
  display_head: 'sk-',
  display_tail: id,
  enabled: true,
  force_store_false: false,
  max_concurrency: 0,
  max_rpm: 0,
  suspension_state: 'none',
  revision: '1',
  created_at: 1,
  updated_at: 1,
}));
const candidate = (key: string, name: string): BindingCandidate => ({
  endpoint_key_id: key,
  upstream_model_id: name,
  endpoint_base_url: 'https://example.test/v1',
  connector_type: 'openai-compatible',
  endpoint_note: 'Upstream',
  endpoint_key_display_head: 'sk-',
  endpoint_key_display_tail: key,
  endpoint_key_note: `Key ${key}`,
  source_types: ['automatic'],
  native_services: ['chat_completions'],
});
function page<T>(data: T[], window: PageWindow, total = data.length): NumberedPage<T> {
  return {
    data,
    next_cursor: null,
    pagination: {
      page: window.page,
      page_size: window.pageSize,
      total_items: String(total),
      total_pages: String(Math.max(1, Math.ceil(total / window.pageSize))),
    },
  };
}
beforeEach(() => {
  vi.mocked(connectionAPI.list).mockImplementation(async (window) =>
    page(
      ['51', '52'].map((id) => ({
        id,
        name: `Service ${id}`,
        display_name: `Service ${id}`,
        primary_entry: null,
        note: '',
        entry_count: '1',
        credential_count: '2',
        revision: '1',
        created_at: 1,
        updated_at: 1,
      })),
      window,
    ),
  );
  vi.mocked(connectionAPI.entries).mockImplementation(async (id, window) =>
    page(
      endpoints.filter((e) => e.connection_id === id),
      window,
    ),
  );
  vi.mocked(listEndpointsPage).mockImplementation(async (window) => page(endpoints, window));
  vi.mocked(listEndpointKeysPage).mockImplementation(async (id, window) =>
    page(
      keys.filter((k) => k.endpoint_id === id),
      window,
    ),
  );
  vi.mocked(getBindingCandidatesPage).mockImplementation(async (_id, filters, window) =>
    page(
      [
        candidate(
          filters.keyId!,
          filters.query || (window.page === '1' ? 'shared-model' : 'later-model'),
        ),
      ],
      window,
      window.page === '1' && !filters.query ? 21 : 1,
    ),
  );
});
function Picker({ bound = new Set<string>() }: { bound?: ReadonlySet<string> }) {
  const [selected, setSelected] = useState<Set<string>>(new Set());
  return (
    <>
      <PersonalBindingPicker
        accountId="1"
        modelId="101"
        selected={selected}
        bound={bound}
        onToggle={(row) =>
          setSelected((current) => {
            const next = new Set(current);
            const id = candidateIdentity(row);
            if (next.has(id)) next.delete(id);
            else next.add(id);
            return next;
          })
        }
      />
      <output aria-label="Selection size">{selected.size}</output>
    </>
  );
}

it('loads candidates only after choosing a key and retains cross-key, endpoint, page and filter selections', async () => {
  const view = await renderWithProviders(<Picker />, { station: 'user', role: 'user' });
  expect(await screen.findByRole('combobox', { name: 'Filter by service' })).toBeVisible();
  expect(getBindingCandidatesPage).not.toHaveBeenCalled();
  await view.user.click(await screen.findByRole('button', { name: /Endpoint 11/ }));
  expect(getBindingCandidatesPage).not.toHaveBeenCalled();
  await view.user.click(await screen.findByRole('button', { name: 'Key 111 · sk-…111' }));
  await view.user.click(await screen.findByRole('checkbox', { name: /shared-model/ }));
  const models = screen.getByRole('region', { name: 'Models' });
  await view.user.click(within(models).getByRole('button', { name: 'Next' }));
  await view.user.click(await screen.findByRole('checkbox', { name: /later-model/ }));
  await view.user.click(screen.getByRole('button', { name: 'Key 112 · sk-…112' }));
  await view.user.click(await screen.findByRole('checkbox', { name: /shared-model/ }));
  await view.user.selectOptions(screen.getByRole('combobox', { name: 'Filter by service' }), '52');
  // This service's only endpoint and key are selected without extra steps.
  await view.user.click(await screen.findByRole('checkbox', { name: /shared-model/ }));
  expect(screen.getByLabelText('Selection size')).toHaveTextContent('4');
  await view.user.type(
    screen.getByRole('searchbox', { name: "Search this key's models" }),
    'filtered-model',
  );
  await waitFor(() =>
    expect(screen.getByRole('checkbox', { name: /filtered-model/ })).toBeVisible(),
  );
  expect(screen.getByLabelText('Selection size')).toHaveTextContent('4');
  await view.user.selectOptions(screen.getByRole('combobox', { name: 'Filter by service' }), '51');
  await view.user.click(await screen.findByRole('button', { name: 'Key 111 · sk-…111' }));
  expect(await screen.findByRole('checkbox', { name: /shared-model/ })).toBeChecked();
  expect(
    vi
      .mocked(getBindingCandidatesPage)
      .mock.calls.every(([, filters]) => Boolean(filters.keyId && filters.endpointId)),
  ).toBe(true);
});

it('marks every target of an existing multi-target binding as bound', async () => {
  const first = candidate('111', 'first-model');
  const second = candidate('112', 'shared-model');
  const targets: BindingTarget[] = [first, second].map((row, index) => ({
    id: String(index + 10),
    revision: '1',
    endpoint_id: '11',
    endpoint_key_id: row.endpoint_key_id,
    upstream_model_id: row.upstream_model_id,
    connector_type: 'openai-compatible',
    configuration_type: 'openai-compatible',
    base_url: row.endpoint_base_url,
    credential_id: '21',
    credential_version: '1',
    grant_revision: '1',
    native_services: ['chat_completions'],
    model_types: ['chat_completions'],
    service_routes: [
      { operation: 'chat_completions', native_service: 'chat_completions', mode: 'native' },
    ],
    capabilities: { can_bind: true, unavailable_reason: null },
  }));
  const bound = boundIdentities([{ ...first, id: '2', ord: 0, targets }]);
  const view = await renderWithProviders(<Picker bound={bound} />, {
    station: 'user',
    role: 'user',
  });
  await view.user.click(await screen.findByRole('button', { name: /Endpoint 11/ }));
  await view.user.click(await screen.findByRole('button', { name: 'Key 112 · sk-…112' }));
  expect(
    await screen.findByRole('checkbox', { name: /shared-model.*Already bound/ }),
  ).toBeDisabled();
});
