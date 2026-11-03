import { useState } from 'react';
import { QueryObserver, type QueryClient } from '@tanstack/react-query';
import { act, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '@shared/query/http';
import { Sheet } from '@shared/components/ui/Sheet';
import { renderWithProviders } from '../../../../../test/unit/support';
import { ServiceCreateForm } from './ServiceCreateForm';
import type { ConnectionIntent } from './resourceOperation';
import type { Endpoint, EndpointKey } from '../types';
import type { Connection, Credential } from './types';
import common from '@shared/i18n/common/en.json';
import userEn from '../../../i18n/en.json';
import { coreKeys } from '../queries';

const mocks = vi.hoisted(() => ({ execute: vi.fn(), status: vi.fn(), read: vi.fn() }));
const resourceObservers: (() => void)[] = [];
afterEach(() => {
  for (const unsubscribe of resourceObservers.splice(0)) unsubscribe();
});

function observeResourceReads(client: QueryClient) {
  const queryKey = coreKeys.models('7');
  client.setQueryData(queryKey, { version: 1 });
  const read = vi.fn(async () => ({ version: 2 }));
  resourceObservers.push(
    new QueryObserver(client, { queryKey, queryFn: read, staleTime: Infinity }).subscribe(
      () => undefined,
    ),
  );
  return read;
}
const options = {
  base_connector_types: ['openai-compatible', 'typesafe-systemone'],
  native_service_options: [
    {
      connector_type: 'typesafe-systemone',
      configuration_type: 'typesafe-systemone',
      native_services: ['systemone'],
      default_native_services: ['systemone'],
      rerank_formats: [],
    },
    {
      connector_type: 'openai-compatible',
      configuration_type: 'openai-compatible',
      native_services: ['chat_completions', 'responses'],
      default_native_services: ['chat_completions'],
      rerank_formats: [],
    },
  ],
  mainstream_channels: [
    {
      id: 'mch_' + 'A'.repeat(22),
      name: 'Example preset',
      revision: '1',
      connector_type: 'openai-compatible',
      base_url: 'https://preset.example/v1',
      entries: [
        {
          id: '1',
          name: 'Preset chat',
          enabled: true,
          connector_type: 'openai-compatible',
          base_url: 'https://preset.example/v1',
          native_services: ['chat_completions'],
          rerank_format: '',
        },
        {
          id: '2',
          name: 'Preset alternate',
          enabled: true,
          connector_type: 'openai-compatible',
          base_url: 'https://alternate.example/v1',
          native_services: ['chat_completions'],
          rerank_format: '',
        },
        {
          id: '3',
          name: 'Preset no catalog',
          enabled: true,
          connector_type: 'openai-compatible',
          base_url: 'https://manual.example/v1',
          native_services: ['chat_completions'],
          rerank_format: '',
        },
      ],
      discovery_source: { base_url: 'https://preset.example/v1', list_format: 'openai' },
    },
  ],
};
const credential = { id: '21', note: 'Shared credential', credential_version: '1' } as Credential;
vi.mock('../queries', async (original) => ({
  ...(await original<typeof import('../queries')>()),
  useEndpointCreateOptions: () => ({ data: options, isPending: false, error: null }),
}));
vi.mock('./resourceOperation', async (original) => ({
  ...(await original<typeof import('./resourceOperation')>()),
  executeConnection: mocks.execute,
  readConnectionResult: mocks.read,
}));
vi.mock('../resourceOperation', async (original) => ({
  ...(await original<typeof import('../resourceOperation')>()),
  resourceStatus: mocks.status,
}));
vi.mock('./CredentialPicker', () => ({
  credentialLabel: (value: Credential) => value.note,
  CredentialPicker: ({ onPick }: { onPick: (value: Credential) => void }) => (
    <button type="button" onClick={() => onPick(credential)}>
      Choose shared credential
    </button>
  ),
}));
vi.mock('./ServiceModelSetup', () => ({
  ServiceModelSetup: ({ entryKey, source }: { entryKey: EndpointKey; source?: { id: string } }) => (
    <div>
      <p>Models use grant {entryKey.id}</p>
      <p>Catalog {source?.id ?? 'none'}</p>
    </div>
  ),
}));

beforeEach(() => {
  mocks.status.mockReset();
  mocks.read.mockReset();
  mocks.execute.mockReset();
  mocks.execute.mockImplementation(async (intent: ConnectionIntent) => {
    if (intent.kind === 'connection')
      return { kind: 'connection', connection: { id: '1', ...intent.input } };
    if (intent.kind === 'endpoint')
      return {
        kind: 'endpoint',
        endpoint: {
          id:
            (intent.input.source === 'mainstream' ? intent.input.channel_entry_id : undefined) ===
            '2'
              ? '12'
              : '11',
          note: 'Service',
          base_url: 'https://service.example/v1',
          native_services: intent.input.native_services,
        } as Endpoint,
      };
    if (intent.kind === 'credential') return { kind: 'credential', credential };
    if (intent.kind === 'discovery_source')
      return {
        kind: 'discovery_source',
        source: {
          id: String(
            40 + mocks.execute.mock.calls.filter(([row]) => row.kind === 'discovery_source').length,
          ),
          credential_id: credential.id,
          credential_version: credential.credential_version,
          base_url: intent.input.base_url,
          list_format: intent.input.list_format,
        },
      };
    if (intent.kind === 'key')
      return {
        kind: 'key',
        key: { id: '31', native_services: intent.input.native_services },
        keyId: '31',
        endpointId: '11',
      };
    throw new Error('Unexpected stage');
  });
});
function SetupSheet({ connection }: { connection?: Connection }) {
  const [open, setOpen] = useState(true);
  return (
    <>
      <button type="button" onClick={() => setOpen(true)}>
        Resume service setup
      </button>
      <Sheet
        open={open}
        keepMounted
        title="Service setup"
        closeLabel="Close"
        onClose={() => setOpen(false)}
      >
        <ServiceCreateForm accountId="7" connection={connection} />
      </Sheet>
    </>
  );
}

async function fillCustom(
  connection?: Connection,
  { retained = false, onManage }: { retained?: boolean; onManage?: () => void } = {},
) {
  const view = await renderWithProviders(
    retained ? (
      <SetupSheet connection={connection} />
    ) : (
      <ServiceCreateForm accountId="7" connection={connection} onManage={onManage} />
    ),
    {
      station: 'user',
      role: 'user',
    },
  );
  view.queryClient.setQueryData(['user', 'session'], {
    user: { id: '7', username: 'fixture-user', effective_level: 1 },
  });
  if (connection)
    await view.user.click(
      screen.getByRole('radio', { name: common.common.connections.newCredential }),
    );
  await view.user.type(
    screen.getByLabelText(userEn.user.services.serviceAddress),
    'https://service.example/v1',
  );
  await view.user.type(
    screen.getByLabelText(common.common.connections.secret),
    'synthetic-upstream-secret',
  );
  return view;
}
describe('service setup as one task', () => {
  async function systemOneForm() {
    const view = await fillCustom();
    await view.user.click(
      screen.getByText(userEn.user.services.advancedSetup, { selector: 'strong' }),
    );
    await view.user.selectOptions(
      screen.getByRole('combobox', { name: common.common.connections.protocol }),
      'typesafe-systemone',
    );
    await view.user.click(
      screen.getByText(common.common.connections.discoveryOptional, { selector: 'strong' }),
    );
    return view;
  }
  it('creates System One keys with the OpenAI catalog default and no extra source save', async () => {
    const view = await systemOneForm();
    expect(
      screen.getByRole('checkbox', { name: userEn.user.workflows.discoverModels }),
    ).toBeChecked();
    expect(
      screen.getByRole('combobox', { name: common.common.connections.listFormat }),
    ).toHaveValue('openai');
    await view.user.click(screen.getByRole('button', { name: userEn.user.services.addAnotherKey }));
    await view.user.type(
      screen.getByLabelText(userEn.user.services.additionalKey.replace('{{number}}', '2')),
      'synthetic-second-systemone-key',
    );
    await view.user.click(screen.getByRole('button', { name: userEn.user.services.addAndFetch }));
    await screen.findByText(userEn.user.workflows.serviceReady);
    const grants = mocks.execute.mock.calls.filter(([intent]) => intent.kind === 'key');
    expect(grants).toHaveLength(2);
    for (const [intent] of grants) {
      expect(intent.input.native_services).toEqual(['systemone']);
      expect(intent.input).not.toHaveProperty('discovery_source_id');
    }
    expect(mocks.execute.mock.calls.some(([intent]) => intent.kind === 'discovery_source')).toBe(
      false,
    );
  });
  it('preserves explicitly disabled discovery on a new System One key', async () => {
    const view = await systemOneForm();
    await view.user.click(
      screen.getByRole('checkbox', { name: userEn.user.workflows.discoverModels }),
    );
    await view.user.click(screen.getByRole('button', { name: userEn.user.services.addAndFetch }));
    await screen.findByText(userEn.user.workflows.serviceReady);
    expect(
      mocks.execute.mock.calls.find(([intent]) => intent.kind === 'key')![0].input
        .discovery_source_id,
    ).toBeNull();
  });
  it('keeps an explicitly chosen System One catalog protocol', async () => {
    const view = await systemOneForm();
    await view.user.selectOptions(
      screen.getByRole('combobox', { name: common.common.connections.listFormat }),
      'anthropic',
    );
    await view.user.click(screen.getByRole('button', { name: userEn.user.services.addAndFetch }));
    await screen.findByText(userEn.user.workflows.serviceReady);
    expect(
      mocks.execute.mock.calls.find(([intent]) => intent.kind === 'discovery_source')![0].input,
    ).toMatchObject({
      list_format: 'anthropic',
      base_url: 'https://service.example/v1',
    });
    expect(
      mocks.execute.mock.calls.find(([intent]) => intent.kind === 'key')![0].input
        .discovery_source_id,
    ).toBe('41');
  });
  it('creates a custom service, credential and grant with one main submission', async () => {
    const onManage = vi.fn();
    const view = await fillCustom(undefined, { onManage });
    const reads = observeResourceReads(view.queryClient);
    const original = mocks.execute.getMockImplementation()!;
    mocks.execute.mockImplementation(async (...args) => {
      expect(reads).not.toHaveBeenCalled();
      return original(...args);
    });
    expect(screen.getByLabelText(userEn.user.workflows.serviceName)).toHaveValue('service.example');
    await view.user.click(screen.getByRole('button', { name: userEn.user.services.addAndFetch }));
    await screen.findByText(userEn.user.workflows.serviceReady);
    await waitFor(() => expect(reads).toHaveBeenCalledTimes(1));
    expect(mocks.execute.mock.calls.map(([intent]) => intent.kind)).toEqual([
      'connection',
      'endpoint',
      'credential',
      'key',
    ]);
    expect(mocks.execute.mock.calls[2]![1]).toBe('synthetic-upstream-secret');
    expect(new Set(mocks.execute.mock.calls.map((call) => call[2])).size).toBe(4);
    expect(screen.getByText('Models use grant 31')).toBeVisible();
    expect(screen.getByText('Catalog none')).toBeVisible();
    expect(
      mocks.execute.mock.calls.find(([intent]) => intent.kind === 'key')![0].input,
    ).not.toHaveProperty('discovery_source_id');
    expect(screen.getByRole('link', { name: userEn.user.workflows.donateService })).toHaveAttribute(
      'href',
      '/charity?tab=donate&endpoint_id=11',
    );
    const manage = screen.getByRole('link', { name: common.common.connections.manage });
    expect(manage).toHaveAttribute(
      'href',
      '/endpoints?connection_id=1&connection_view=entries&endpoint_id=11',
    );
    await view.user.click(manage);
    expect(onManage).toHaveBeenCalledTimes(1);
  });
  it('adds multiple secrets in one task without sharing their internal catalogs', async () => {
    const original = mocks.execute.getMockImplementation()!;
    let credentialNumber = 20;
    mocks.execute.mockImplementation(async (...args) =>
      args[0].kind === 'credential'
        ? { kind: 'credential', credential: { ...credential, id: String(++credentialNumber) } }
        : original(...args),
    );
    const view = await fillCustom();
    await view.user.click(screen.getByRole('button', { name: userEn.user.services.addAnotherKey }));
    await view.user.type(
      screen.getByLabelText(userEn.user.services.additionalKey.replace('{{number}}', '2')),
      'synthetic-second-key',
    );
    await view.user.click(screen.getByRole('button', { name: userEn.user.services.addAndFetch }));
    await screen.findByText(userEn.user.workflows.serviceReady);
    expect(mocks.execute.mock.calls.map(([intent]) => intent.kind)).toEqual([
      'connection',
      'endpoint',
      'credential',
      'key',
      'credential',
      'key',
    ]);
    const grants = mocks.execute.mock.calls.filter(([intent]) => intent.kind === 'key');
    expect(grants.map(([intent]) => intent.input.credential_id)).toEqual(['21', '22']);
    expect(grants.every(([intent]) => !('discovery_source_id' in intent.input))).toBe(true);
    expect(
      mocks.execute.mock.calls
        .filter(([intent]) => intent.kind === 'credential')
        .map((call) => call[1]),
    ).toEqual(['synthetic-upstream-secret', 'synthetic-second-key']);
  });
  it('removes extra drafts and discards them when switching to an existing service key', async () => {
    const view = await fillCustom({ id: '1', credential_count: '1' } as Connection);
    const add = screen.getByRole('button', { name: userEn.user.services.addAnotherKey });
    const extraLabel = userEn.user.services.additionalKey.replace('{{number}}', '2');
    await view.user.click(add);
    await view.user.type(screen.getByLabelText(extraLabel), 'synthetic-unused-extra');
    await view.user.click(
      screen.getByRole('button', { name: userEn.user.services.removeAdditionalKey }),
    );
    expect(screen.queryByLabelText(extraLabel)).not.toBeInTheDocument();
    await view.user.click(add);
    await view.user.click(
      screen.getByRole('radio', { name: common.common.connections.existingCredential }),
    );
    await view.user.click(screen.getByRole('button', { name: 'Choose shared credential' }));
    const save = screen.getByRole('button', { name: userEn.user.services.addAndFetch });
    expect(save).toBeEnabled();
    await view.user.click(save);
    await screen.findByText(userEn.user.workflows.serviceReady);
    expect(mocks.execute.mock.calls.map(([intent]) => intent.kind)).toEqual(['endpoint', 'key']);
  });
  it('creates the chosen preset without asking for separate entry and grant saves', async () => {
    const view = await renderWithProviders(<ServiceCreateForm accountId="7" />, {
      station: 'user',
      role: 'user',
    });
    view.queryClient.setQueryData(['user', 'session'], {
      user: { id: '7', username: 'fixture-user', effective_level: 1 },
    });
    await view.user.click(
      screen.getByRole('radio', { name: common.common.connections.mainstream }),
    );
    await view.user.selectOptions(
      screen.getByRole('combobox', { name: common.common.connections.mainstream }),
      options.mainstream_channels[0]!.id,
    );
    await view.user.click(
      screen.getByText(common.common.connections.entries, { selector: 'strong' }),
    );
    const noCatalog = screen
      .getByLabelText('Preset no catalog')
      .closest<HTMLElement>('.nb-stack--sm')!;
    await view.user.click(
      within(noCatalog).getByRole('checkbox', {
        name: common.common.connections.useSuggestedCatalog,
      }),
    );
    await view.user.type(
      screen.getByLabelText(common.common.connections.secret),
      'synthetic-preset-secret',
    );
    await view.user.click(screen.getByRole('button', { name: userEn.user.services.addAndFetch }));
    await screen.findByText(userEn.user.workflows.serviceReady);
    expect(mocks.execute.mock.calls[1]![0]).toMatchObject({
      kind: 'endpoint',
      input: {
        source: 'mainstream',
        channel_entry_id: '1',
        native_services: ['chat_completions', 'responses'],
      },
    });
    const sources = mocks.execute.mock.calls.filter(
      ([intent]) => intent.kind === 'discovery_source',
    );
    const grants = mocks.execute.mock.calls.filter(([intent]) => intent.kind === 'key');
    expect(sources).toHaveLength(2);
    expect(grants).toHaveLength(3);
    expect(grants.map(([intent]) => intent.input.discovery_source_id)).toEqual(['41', '42', null]);
  });
  it('reuses a selected credential and retries only an unfinished grant', async () => {
    const original = mocks.execute.getMockImplementation()!;
    let fail = true;
    mocks.execute.mockImplementation(async (...args) => {
      if (args[0].kind === 'key' && fail) {
        fail = false;
        throw new ApiError('invalid_request', 'Synthetic grant failure', 400);
      }
      return original(...args);
    });
    const view = await fillCustom({ id: '1', credential_count: '1' } as Connection);
    await view.user.click(
      screen.getByRole('radio', { name: common.common.connections.existingCredential }),
    );
    await view.user.click(screen.getByRole('button', { name: 'Choose shared credential' }));
    await view.user.click(screen.getByRole('button', { name: userEn.user.services.addAndFetch }));
    await screen.findByText(userEn.user.workflows.continueSetup);
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: userEn.user.workflows.continueService }),
      ).toBeEnabled(),
    );
    await view.user.click(
      screen.getByRole('button', { name: userEn.user.workflows.continueService }),
    );
    await screen.findByText(userEn.user.workflows.serviceReady);
    expect(mocks.execute.mock.calls.map(([intent]) => intent.kind)).toEqual([
      'endpoint',
      'key',
      'key',
    ]);
  });
  it('refreshes partial setup once after a rejected grant and retains confirmed resources on retry', async () => {
    const original = mocks.execute.getMockImplementation()!;
    let fail = true;
    mocks.execute.mockImplementation(async (...args) => {
      if (args[0].kind === 'key' && fail) {
        fail = false;
        throw new ApiError('invalid_request', 'Synthetic grant failure', 400);
      }
      return original(...args);
    });
    const view = await fillCustom();
    const reads = observeResourceReads(view.queryClient);
    await view.user.click(screen.getByRole('button', { name: userEn.user.services.addAndFetch }));
    const resume = await screen.findByRole('button', {
      name: userEn.user.workflows.continueService,
    });
    await waitFor(() => expect(resume).toBeEnabled());
    expect(reads).toHaveBeenCalledTimes(1);
    expect(mocks.execute.mock.calls.map(([intent]) => intent.kind)).toEqual([
      'connection',
      'endpoint',
      'credential',
      'key',
    ]);
    await view.user.click(resume);
    await screen.findByText(userEn.user.workflows.serviceReady);
    await waitFor(() => expect(reads).toHaveBeenCalledTimes(2));
    expect(mocks.execute.mock.calls.map(([intent]) => intent.kind)).toEqual([
      'connection',
      'endpoint',
      'credential',
      'key',
      'key',
    ]);
  });
  it('retains a dismissed in-flight task and checks its unknown receipt without recreating resources', async () => {
    const original = mocks.execute.getMockImplementation()!;
    let rejectCredential!: (error: Error) => void;
    mocks.execute.mockImplementation(async (...args) => {
      if (args[0].kind === 'credential')
        return new Promise((_resolve, reject) => {
          rejectCredential = reject;
        });
      return original(...args);
    });
    mocks.status.mockResolvedValueOnce({ status: 'not_recorded' }).mockResolvedValueOnce({
      status: 'recorded',
      stage: 'credential',
      result: { credential_id: '21' },
    });
    mocks.read
      .mockResolvedValueOnce(null)
      .mockResolvedValueOnce({ kind: 'credential', credential });
    const view = await fillCustom(undefined, { retained: true });
    const reads = observeResourceReads(view.queryClient);
    await view.user.click(screen.getByRole('button', { name: userEn.user.services.addAndFetch }));
    await waitFor(() =>
      expect(mocks.execute.mock.calls.map(([intent]) => intent.kind)).toEqual([
        'connection',
        'endpoint',
        'credential',
      ]),
    );
    const signal = mocks.execute.mock.calls[2]![3] as AbortSignal;
    await view.user.click(screen.getByRole('button', { name: 'Close' }));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(signal.aborted).toBe(false);
    await act(async () => {
      rejectCredential(new ApiError('network_error', 'Synthetic lost response', 0));
    });
    await waitFor(() => expect(mocks.status).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(reads).toHaveBeenCalledTimes(1));
    await view.user.click(screen.getByRole('button', { name: 'Resume service setup' }));
    const check = await screen.findByRole('button', {
      name: common.common.connections.checkResult,
    });
    await view.user.click(check);
    await waitFor(() => expect(reads).toHaveBeenCalledTimes(2));
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: userEn.user.workflows.continueService }),
      ).toBeEnabled(),
    );
    await view.user.click(
      screen.getByRole('button', { name: userEn.user.workflows.continueService }),
    );
    await screen.findByText(userEn.user.workflows.serviceReady);
    await waitFor(() => expect(reads).toHaveBeenCalledTimes(3));
    expect(mocks.execute.mock.calls.map(([intent]) => intent.kind)).toEqual([
      'connection',
      'endpoint',
      'credential',
      'key',
    ]);
    expect(mocks.status.mock.calls[0]![0]).toBe(mocks.status.mock.calls[1]![0]);
    expect(
      JSON.stringify(
        view.queryClient
          .getMutationCache()
          .getAll()
          .map((row) => row.state.variables),
      ),
    ).not.toContain('synthetic-upstream-secret');
  });
  it('does not continue later stages after the initiating session changes', async () => {
    let resolve!: (value: unknown) => void;
    mocks.execute.mockReturnValue(
      new Promise((done) => {
        resolve = done;
      }),
    );
    const view = await fillCustom();
    const reads = observeResourceReads(view.queryClient);
    await view.user.click(screen.getByRole('button', { name: userEn.user.services.addAnotherKey }));
    const extraLabel = userEn.user.services.additionalKey.replace('{{number}}', '2');
    await view.user.type(screen.getByLabelText(extraLabel), 'synthetic-session-extra');
    await view.user.click(screen.getByRole('button', { name: userEn.user.services.addAndFetch }));
    await waitFor(() => expect(mocks.execute).toHaveBeenCalledTimes(1));
    await act(async () => {
      view.queryClient.setQueryData(['user', 'session'], {
        user: { id: '8', username: 'other-user', effective_level: 1 },
      });
      resolve({ kind: 'connection', connection: { id: '1' } });
    });
    expect(mocks.execute).toHaveBeenCalledTimes(1);
    expect(reads).not.toHaveBeenCalled();
    expect(screen.getByLabelText(common.common.connections.secret)).toHaveValue('');
    expect(screen.getByLabelText(extraLabel)).toHaveValue('');
  });
});
