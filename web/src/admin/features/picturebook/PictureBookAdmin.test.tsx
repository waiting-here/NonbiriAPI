import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { assertNoSensitiveQueryCache, renderWithProviders } from '../../../../test/unit/support';
import {
  decodeAdapter,
  decodeControlRow,
  decodeRefresh,
  getRefresh,
  getCatalog,
  refreshModels,
  saveModelsBatch,
  decodeAdminModel,
  type Upstream,
} from './adminApi';
import { modelFixture } from '@shared/picturebook/fixtures';
import { UpstreamForm } from './UpstreamForm';
import { RecoveryPanel } from './RecoveryPanel';
import { ModelEditor } from './ModelEditor';
import { CapabilityProfilePanel } from './CapabilityProfilePanel';

const adapter = {
  discovery: { method: 'GET', path: '/v1/models', items_pointer: '/data', id_pointer: '/id' },
  submit: {
    method: 'POST',
    path: '/v1/images/generations',
    mapping: { model_pointer: '/model', parameters: { prompt: '/prompt' }, constants: [] },
  },
  response: {
    images_pointer: '/data',
    base64_pointer: '/b64_json',
    working_states: [],
    success_states: [],
    failure_states: [],
  },
};
const upstream = (): Upstream => ({
  revision: '1',
  configured: true,
  base_url: 'https://images.example.invalid',
  secret_set: true,
  rpm: 30,
  concurrency: 2,
  per_user_limit: 1,
  global_limit: 100,
  queue_timeout_seconds: 1800,
  execution_timeout_seconds: 1800,
  memory_budget_mib: 512,
  image_origins: [],
  adapter: decodeAdapter(adapter),
  control: null,
});
const adminModelFixture = () => {
  const publicModel = modelFixture();
  return {
    ...publicModel,
    parameter_capabilities: publicModel.parameters.map((rule) => ({
      key: rule.key,
      source: 'profile',
      support: rule.supported ? 'supported' : 'unsupported',
      overridden: false,
      conflict: false,
    })),
  };
};
function reply(body: unknown) {
  return new Response(JSON.stringify(body), { headers: { 'Content-Type': 'application/json' } });
}
describe('image configuration boundaries', () => {
  it('decodes numbered full-catalog results and structured all-or-nothing batch issues', async () => {
    const model = {
      ...adminModelFixture(),
      upstream_model_id: 'Synthetic image',
      metadata: {},
      configured: true,
      enabled: true,
      mapping: { model_pointer: '', parameters: {}, constants: [] },
      capability_revision: '2',
      capability_readiness: 'ready',
      catalog_type: 'image',
      missing: false,
    };
    const requests: { path: string; method: string | undefined; body: unknown }[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (path: unknown, init?: RequestInit) => {
        requests.push({
          path: String(path),
          method: init?.method,
          body: init?.body && JSON.parse(String(init.body)),
        });
        if (init?.method === 'POST')
          return reply({
            applied: false,
            receipts: [],
            issues: [
              {
                model_id: model.id,
                field_path: 'input.expected_revision',
                code: 'revision_conflict',
                safe_message: 'Reload this model before saving.',
              },
            ],
          });
        return reply({
          data: [model],
          total: 151,
          revision: 'ics_AAAAAAAAAAAAAAAAAAAAAA',
          page: 8,
          page_size: 20,
        });
      }),
    );
    const catalog = await getCatalog({
      q: 'synthetic',
      type: 'all',
      configured: 'all',
      enabled: 'all',
      page: 8,
      page_size: 20,
    });
    expect(catalog.total).toBe(151);
    expect(catalog.data[0].catalog_type).toBe('image');
    expect(requests[0].path).toContain('page=8');
    expect(requests[0].path).toContain('q=synthetic');
    expect(() => decodeAdminModel({ ...model, upstream_secret: 'private' })).toThrow();
    expect(() => decodeAdminModel({ ...model, parameter_capabilities: [] })).toThrow();
    const result = await saveModelsBatch(
      {
        models: [
          {
            id: model.id,
            input: {
              expected_revision: '2',
              display_name: model.display_name,
              description: model.description,
              enabled: true,
              price: model.price,
              pricing: model.pricing,
              parameters: model.parameters,
              combinations: model.combinations,
              mapping: model.mapping,
            },
          },
        ],
      },
      'AAAAAAAAAAAAAAAAAAAAAA',
    );
    expect(result.applied).toBe(false);
    expect(result.issues[0].field_path).toBe('input.expected_revision');
    expect(requests[1].method).toBe('POST');
    expect(requests[1].body).toHaveProperty('models');
  });
  it('checks an unsaved model draft locally without a task or generation request', async () => {
    const requests: { path: string; body: unknown }[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (path: unknown, init?: RequestInit) => {
        requests.push({ path: String(path), body: JSON.parse(String(init?.body)) });
        return reply({
          valid: true,
          issues: [],
          effective_parameters: { prompt: 'Synthetic sample', n: 1 },
          effective_selection: { values: {}, selection: {} },
          quote: {
            unit: { paper: '2', brush: '1' },
            total: { paper: '2', brush: '1' },
            basis: 'default',
            price_key: '',
          },
        });
      }),
    );
    const value = decodeAdminModel({
      ...adminModelFixture(),
      parameter_capabilities: adminModelFixture().parameter_capabilities.map((entry, index) => ({
        ...entry,
        source: index === 0 ? 'profile' : index === 1 ? 'discovered' : 'manual',
        overridden: index === 2,
        conflict: index === 2,
      })),
      upstream_model_id: 'synthetic-image',
      metadata: {},
      configured: true,
      enabled: true,
      mapping: { model_pointer: '', parameters: {}, constants: [] },
      capability_revision: '2',
      capability_readiness: 'ready',
      catalog_type: 'image',
    });
    const view = await renderWithProviders(
      <ModelEditor value={value} onSaved={() => undefined} onLocked={() => undefined} />,
      { station: 'admin', role: 'admin' },
    );
    const origins = within(screen.getByRole('region', { name: 'Accepted parameter sources' }));
    expect(origins.getByText(/Connection profile/)).toBeInTheDocument();
    expect(origins.getByText(/Discovered model metadata/)).toBeInTheDocument();
    expect(origins.getAllByText(/Manual override/).length).toBeGreaterThan(0);
    expect(origins.getByText(/Override conflicts with the accepted source/)).toBeInTheDocument();
    expect(origins.getAllByText('View effective constraints')).toHaveLength(
      value.parameters.length,
    );
    await view.user.type(screen.getByLabelText(/^Prompt/), 'Synthetic sample');
    await view.user.clear(screen.getByLabelText('Image count'));
    await view.user.type(screen.getByLabelText('Image count'), '2');
    await view.user.click(screen.getByText('Advanced parameters'));
    await view.user.selectOptions(screen.getByLabelText('Quality'), 'high');
    expect(screen.getByText(/Local estimated total.*4 \/ 2/)).toBeInTheDocument();
    await view.user.click(screen.getByRole('button', { name: 'Check draft' }));
    await screen.findByText(/Local check passed/);
    expect(requests).toHaveLength(1);
    expect(requests[0].path).toContain('/models/check');
    expect(requests[0].body).toHaveProperty('draft');
    expect(requests[0].body).toHaveProperty('parameters.prompt', 'Synthetic sample');
    expect(requests[0].body).toHaveProperty('parameters.n', 2);
    expect(requests[0].body).toHaveProperty('parameters.quality', 'high');
    expect(requests.every((request) => !/\/quote|\/tasks/.test(request.path))).toBe(true);
  });
  it('round-trips the closed capability profile through form controls and the saved JSON', async () => {
    const starting = {
      version: 1,
      fields: [
        {
          rule: {
            key: 'prompt',
            type: 'string',
            supported: true,
            required: true,
            length_unit: 'utf8_bytes',
            min_length: 1,
            max_length: 65536,
          },
        },
      ],
    };
    const writes: unknown[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (path: unknown, init?: RequestInit) => {
        if (init?.method === 'PUT') {
          const input = JSON.parse(String(init.body)) as { profile: unknown };
          writes.push(input.profile);
          return reply({ revision: '2', profile: input.profile });
        }
        if (String(path).endsWith('/upstream/capability-profile'))
          return reply({ revision: '1', profile: starting });
        throw new Error('unexpected request: ' + String(path));
      }),
    );
    const view = await renderWithProviders(
      <CapabilityProfilePanel account="fixture-admin" onDirty={() => undefined} />,
      { station: 'admin', role: 'admin' },
    );
    view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'fixture-admin' } });
    await view.user.click(await screen.findByRole('button', { name: 'Retry' }));
    await view.user.type(await screen.findByLabelText('catalog_type_pointer'), '/kind');
    expect(screen.getByLabelText('Image classification value 1')).toHaveValue('image');
    await view.user.click(screen.getByLabelText('Extract size capability'));
    await view.user.selectOptions(screen.getByLabelText('Size converter mode'), 'width_height');
    await view.user.type(screen.getByLabelText('combinations_pointer'), '/sizes');
    await view.user.type(screen.getByLabelText('auto_pointer'), '/auto');
    await view.user.click(screen.getByRole('button', { name: 'Add size combination' }));
    await view.user.selectOptions(screen.getByLabelText('Add parameter'), 'size');
    await view.user.click(screen.getByRole('button', { name: 'Add parameter' }));
    await view.user.click(screen.getByLabelText('Parse width and height'));
    const json = JSON.parse(
      (screen.getByLabelText(/Complete declarative profile JSON/) as HTMLTextAreaElement).value,
    );
    expect(json).toMatchObject({
      catalog_type_pointer: '/kind',
      image_values: ['image'],
      size: {
        combinations_pointer: '/sizes',
        auto_pointer: '/auto',
        capability: { mode: 'width_height', combinations: [{ width: 1, height: 1 }] },
      },
    });
    expect(
      json.fields.find((field: { rule: { key: string } }) => field.rule.key === 'size').rule
        .dimensions,
    ).toMatchObject({ format: 'width_height', width: { minimum: 1, maximum: 1024, step: 1 } });
    await view.user.click(screen.getByRole('button', { name: 'Save capability profile' }));
    await waitFor(() => expect(writes).toEqual([json]));
  });
  it('reads bare discovery status and the wrapped start receipt from their actual API helpers', async () => {
    const operation = {
      id: 'op_AAAAAAAAAAAAAAAAAAAAAA',
      state: 'succeeded',
      created_at: 1700000000,
      completed_at: 1700000001,
      model_count: 2,
      error_code: null,
    };
    vi.stubGlobal(
      'fetch',
      vi.fn(async (_path: unknown, init?: RequestInit) =>
        reply(init?.method === 'POST' ? { operation } : operation),
      ),
    );
    await expect(getRefresh(operation.id)).resolves.toEqual(operation);
    await expect(refreshModels({}, 'AAAAAAAAAAAAAAAAAAAAAA')).resolves.toEqual(operation);
    expect(() => decodeRefresh({ operation })).toThrow();
  });
  it('rejects executable adapter declarations and accepts only known state/control fields', () => {
    expect(() => decodeAdapter({ ...adapter, script: 'return request' })).toThrow();
    expect(() =>
      decodeAdapter({ ...adapter, submit: { ...adapter.submit, headers: { secret: 'no' } } }),
    ).toThrow();
    expect(() =>
      decodeControlRow({
        id: 'iup_AAAAAAAAAAAAAAAAAAAAAA',
        paused: true,
        reason: 'receipt_unknown',
        revision: '2',
        uncertain_slots: 1,
        current: false,
        queued: 2,
        running: 1,
        identity_hash: 'private',
      }),
    ).toThrow();
  });
  it('accepts bounded declarative receipts only with polling and a task identity', () => {
    const value = {
      ...adapter,
      submit: {
        ...adapter.submit,
        receipt: { indicator_pointer: '/accepted', indicator_value: true },
      },
      poll: { method: 'GET', path: '/progress/{task_id}' },
      response: {
        ...adapter.response,
        task_id_pointer: '/ticket/ref',
        state_pointer: '/stage',
        working_states: ['waiting'],
        success_states: ['ready'],
        failure_states: ['rejected'],
      },
    };
    expect(decodeAdapter(value).submit.receipt).toEqual(value.submit.receipt);
    expect(
      decodeAdapter({
        ...value,
        submit: {
          ...value.submit,
          receipt: { indicator_pointer: '/accepted', indicator_value: 'queued' },
        },
      }).submit.receipt?.indicator_value,
    ).toBe('queued');
    for (const indicator_value of ['', 1, {}, null])
      expect(() =>
        decodeAdapter({
          ...value,
          submit: { ...value.submit, receipt: { indicator_pointer: '/accepted', indicator_value } },
        }),
      ).toThrow();
    expect(() => decodeAdapter({ ...adapter, submit: value.submit })).toThrow();
  });
  it('uses one exact retry for a lost compact save receipt without caching the replacement secret', async () => {
    const writes: { body: string; key: string | null }[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (_input: unknown, init?: RequestInit) => {
        writes.push({
          body: String(init?.body),
          key: new Headers(init?.headers).get('Idempotency-Key'),
        });
        if (writes.length === 1) throw new TypeError('lost');
        return reply({ revision: '2' });
      }),
    );
    const saved = vi.fn();
    const view = await renderWithProviders(
      <UpstreamForm value={upstream()} onSaved={saved} onReload={() => undefined} />,
      { station: 'admin', role: 'admin' },
    );
    view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'fixture-admin' } });
    await view.user.click(screen.getByLabelText('Replace stored key'));
    await view.user.type(screen.getByLabelText('Activity key'), 'synthetic-private-key-marker');
    await view.user.click(screen.getByRole('button', { name: 'Save service settings' }));
    await screen.findByText(/save result is unconfirmed/i);
    expect(screen.getByLabelText('Activity key')).toBeDisabled();
    assertNoSensitiveQueryCache(view.queryClient, ['synthetic-private-key-marker']);
    await view.user.click(screen.getByRole('button', { name: 'Retry the same save' }));
    await waitFor(() => expect(saved).toHaveBeenCalledWith({ revision: '2' }));
    expect(writes[0]).toEqual(writes[1]);
    expect(JSON.parse(writes[0].body).secret).toEqual({
      mode: 'replace',
      value: 'synthetic-private-key-marker',
    });
    expect(screen.getByLabelText('Activity key')).toHaveValue('');
    assertNoSensitiveQueryCache(view.queryClient, ['synthetic-private-key-marker']);
  });
  it('requires a reason and deliberate confirmation before resuming a previous service identity', async () => {
    const control = {
      id: 'iup_AAAAAAAAAAAAAAAAAAAAAA',
      paused: true,
      reason: 'receipt_unknown',
      revision: '8',
      uncertain_slots: 1,
      current: false,
      queued: 1,
      running: 0,
    };
    const writes: unknown[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (path: unknown, init?: RequestInit) => {
        if (String(path).endsWith('/resume')) {
          writes.push(JSON.parse(String(init?.body)));
          return reply({
            control: {
              id: control.id,
              paused: false,
              reason: '',
              revision: '9',
              uncertain_slots: 0,
            },
          });
        }
        return reply({ data: [control], next_cursor: null });
      }),
    );
    const view = await renderWithProviders(<RecoveryPanel account="fixture-admin" />, {
      station: 'admin',
      role: 'admin',
    });
    view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'fixture-admin' } });
    await view.user.click(await screen.findByRole('button', { name: 'Retry' }));
    await screen.findByText('Previous service');
    expect(screen.getByRole('button', { name: 'Confirm resume' })).toBeDisabled();
    await view.user.type(
      screen.getByLabelText('Reason for resuming'),
      'Checked the synthetic request.',
    );
    expect(screen.getByRole('button', { name: 'Confirm resume' })).toBeDisabled();
    await view.user.click(
      screen.getByLabelText('I checked outstanding requests and confirm resuming dispatch'),
    );
    await view.user.click(screen.getByRole('button', { name: 'Confirm resume' }));
    await waitFor(() =>
      expect(writes).toEqual([
        {
          control_id: control.id,
          expected_revision: '8',
          reason: 'Checked the synthetic request.',
        },
      ]),
    );
  });
});
