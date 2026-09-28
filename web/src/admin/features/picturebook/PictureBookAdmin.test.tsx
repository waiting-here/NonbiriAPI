import { screen, waitFor } from '@testing-library/react';
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
import automaticModel from './fixtures/automatic-model.json';

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
    capability_issues: [],
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
  it('decodes the complete synthetic automatic model response and its real parameter and price shapes', () => {
    const value = decodeAdminModel(automaticModel);
    expect(value.capability_issues).toEqual([]);
    expect(value.parameter_capabilities.every((entry) => entry.source === 'discovered')).toBe(true);
    expect(value.parameters.find((entry) => entry.key === 'prompt')).toMatchObject({
      max_length: 128,
      length_unit: 'utf16_units',
    });
    expect(value.parameters.find((entry) => entry.key === 'quality')).toMatchObject({
      default: 'low',
    });
    expect(value.pricing?.sizes).toEqual([{ width: 512, height: 512, paper: '5', brush: '0' }]);
    expect(value.size_capability?.combinations?.[0]).toMatchObject({
      resolution: 'compact',
      tier: 'compact',
    });
  });
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
              enabled: true,
              price: model.price,
              pricing: model.pricing,
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
    expect(screen.queryByLabelText(/JSON/)).not.toBeInTheDocument();
    expect(screen.queryByLabelText('Public display name')).not.toBeInTheDocument();
    await view.user.click(screen.getByText('Try parameters and prices (no charge)'));
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
  it('preserves complete prices including older tiers and exact-size orientation without manual configuration', async () => {
    const pricing = {
      default: { paper: '2', brush: '1' },
      fallback: 'unavailable',
      tiers: [{ tier: 'retained', paper: '9', brush: '2' }],
      sizes: [
        { width: 512, height: 768, paper: '7', brush: '0' },
        { width: 768, height: 512, paper: '8', brush: '0' },
      ],
    };
    const value = decodeAdminModel({
      ...adminModelFixture(),
      pricing,
      upstream_model_id: 'synthetic-image',
      metadata: {},
      configured: true,
      enabled: true,
      mapping: { model_pointer: '', parameters: {}, constants: [] },
      capability_readiness: 'ready',
      size_capability: {
        mode: 'resolution_ratio_grid',
        combinations: [
          { ratio: '1:1', resolution: 'compact', width: 512, height: 512, tier: 'compact' },
        ],
      },
    });
    const writes: unknown[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (_path: unknown, init?: RequestInit) => {
        writes.push(JSON.parse(String(init?.body)));
        return reply({ id: value.id, revision: '3' });
      }),
    );
    const view = await renderWithProviders(
      <ModelEditor value={value} onSaved={() => undefined} onLocked={() => undefined} />,
      { station: 'admin', role: 'admin' },
    );
    view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'fixture-admin' } });
    expect(screen.getByLabelText('Price tier 1')).toHaveValue('retained');
    expect(screen.getByLabelText('Price width 1')).toHaveValue(512);
    expect(screen.getByLabelText('Price height 2')).toHaveValue(512);
    await view.user.click(screen.getByRole('button', { name: 'Save model settings' }));
    await waitFor(() =>
      expect(writes).toEqual([
        {
          expected_revision: value.revision,
          enabled: true,
          price: value.price,
          pricing,
        },
      ]),
    );
  });

  it('edits tier and exact-size prices with controls and rejects duplicates and zero prices', async () => {
    const value = decodeAdminModel({
      ...adminModelFixture(),
      upstream_model_id: 'synthetic-image',
      metadata: {},
      configured: false,
      revision: '0',
      capability_revision: '0',
      pricing_revision: '0',
      enabled: false,
      capability_readiness: 'ready',
      catalog_type: 'image',
      mapping: { model_pointer: '', parameters: {}, constants: [] },
      size_capability: {
        mode: 'resolution_ratio_grid',
        combinations: [
          { ratio: '1:1', resolution: 'compact', width: 512, height: 512, tier: 'compact' },
        ],
      },
    });
    const writes: unknown[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (_path: unknown, init?: RequestInit) => {
        writes.push(JSON.parse(String(init?.body)));
        return reply({ id: value.id, revision: '1' });
      }),
    );
    const view = await renderWithProviders(
      <ModelEditor value={value} onSaved={() => undefined} onLocked={() => undefined} />,
      { station: 'admin', role: 'admin' },
    );
    view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'fixture-admin' } });
    await view.user.click(screen.getByLabelText('Make this model available'));
    await view.user.click(screen.getByRole('button', { name: 'Add tier price' }));
    await view.user.clear(screen.getByLabelText('Tier sketch paper 1'));
    await view.user.type(screen.getByLabelText('Tier sketch paper 1'), '3');
    await view.user.click(screen.getByRole('button', { name: 'Add size price' }));
    await view.user.clear(screen.getByLabelText('Size sketch paper 1'));
    await view.user.type(screen.getByLabelText('Size sketch paper 1'), '7');
    await view.user.click(screen.getByRole('button', { name: 'Add size price' }));
    await view.user.type(screen.getByLabelText('Price width 2'), '512');
    await view.user.type(screen.getByLabelText('Price height 2'), '512');
    await view.user.click(screen.getByRole('button', { name: 'Save model settings' }));
    await screen.findByText(/sizes or tiers cannot repeat/);
    expect(writes).toHaveLength(0);
    await view.user.click(screen.getByRole('button', { name: 'Remove size price 2' }));
    await view.user.clear(screen.getByLabelText('Size sketch paper 1'));
    await view.user.type(screen.getByLabelText('Size sketch paper 1'), '0');
    await view.user.clear(screen.getByLabelText('Size brushes 1'));
    await view.user.type(screen.getByLabelText('Size brushes 1'), '0');
    await view.user.click(screen.getByRole('button', { name: 'Save model settings' }));
    expect(writes).toHaveLength(0);
    await view.user.clear(screen.getByLabelText('Size sketch paper 1'));
    await view.user.type(screen.getByLabelText('Size sketch paper 1'), '7');
    await view.user.selectOptions(
      screen.getByLabelText('When no size price matches'),
      'unavailable',
    );
    await view.user.click(screen.getByRole('button', { name: 'Save model settings' }));
    await waitFor(() =>
      expect(writes).toEqual([
        {
          expected_revision: '0',
          enabled: true,
          price: { paper: '2', brush: '1' },
          pricing: {
            default: { paper: '2', brush: '1' },
            fallback: 'unavailable',
            tiers: [{ tier: 'compact', paper: '3', brush: '1' }],
            sizes: [{ width: 512, height: 512, paper: '7', brush: '0' }],
          },
        },
      ]),
    );
  });

  it('explains unavailable automatic metadata while preserving an already enabled model price', async () => {
    const issue = {
      model_id: modelFixture().id,
      field_path: 'parameters.size',
      code: 'unsupported_metadata',
      safe_message: 'Synthetic unsupported model metadata.',
    };
    const value = decodeAdminModel({
      ...adminModelFixture(),
      upstream_model_id: 'synthetic-image',
      metadata: {},
      configured: true,
      enabled: true,
      capability_readiness: 'pending',
      capability_issues: [issue],
      mapping: { model_pointer: '', parameters: {}, constants: [] },
    });
    expect(() => decodeAdminModel({ ...value, capability_issues: {} })).toThrow();
    expect(() =>
      decodeAdminModel({ ...value, capability_issues: [{ ...issue, code: 1 }] }),
    ).toThrow();
    const writes: unknown[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (_path: unknown, init?: RequestInit) => {
        writes.push(JSON.parse(String(init?.body)));
        return reply({ id: value.id, revision: '3' });
      }),
    );
    const view = await renderWithProviders(
      <ModelEditor value={value} onSaved={() => undefined} onLocked={() => undefined} />,
      { station: 'admin', role: 'admin' },
    );
    view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'fixture-admin' } });
    expect(screen.getByText(/This model is currently unavailable/)).toBeInTheDocument();
    expect(screen.getByText(/catalog information.*incomplete/)).toBeInTheDocument();
    expect(screen.queryByLabelText(/JSON/)).not.toBeInTheDocument();
    expect(screen.getByLabelText('Make this model available')).toBeChecked();
    await view.user.clear(screen.getByLabelText('Sketch paper per image'));
    await view.user.type(screen.getByLabelText('Sketch paper per image'), '4');
    await view.user.click(screen.getByRole('button', { name: 'Save model settings' }));
    await waitFor(() => expect(writes[0]).toHaveProperty('enabled', true));
    expect(writes[0]).toHaveProperty('price.paper', '4');
  });
  it('allows disabling a pending model but prevents re-enabling it before metadata is ready', async () => {
    const value = decodeAdminModel({ ...automaticModel, capability_readiness: 'pending' });
    const view = await renderWithProviders(
      <ModelEditor value={value} onSaved={() => undefined} onLocked={() => undefined} />,
      { station: 'admin', role: 'admin' },
    );
    const enabled = screen.getByLabelText('Make this model available');
    expect(enabled).toBeEnabled();
    await view.user.click(enabled);
    expect(enabled).not.toBeChecked();
    expect(enabled).toBeDisabled();
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
    expect(Object.keys(JSON.parse(writes[0].body)).sort()).toEqual([
      'base_url',
      'expected_revision',
      'secret',
    ]);
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
