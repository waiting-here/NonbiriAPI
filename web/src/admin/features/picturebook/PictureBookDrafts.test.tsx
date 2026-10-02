import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import i18next from 'i18next';
import { I18nextProvider } from 'react-i18next';
import { createMemoryRouter, Link, RouterProvider } from 'react-router';
import { describe, expect, it, vi } from 'vitest';
import { modelFixture } from '@shared/picturebook/fixtures';
import adminEnglish from '../../i18n/en.json';
import { AdminContent } from './PictureBookAdmin';

const publicModel = modelFixture();
const model = {
  ...publicModel,
  capability_issues: [],
  parameter_capabilities: publicModel.parameters.map((rule) => ({
    key: rule.key,
    source: 'profile',
    support: rule.supported ? 'supported' : 'unsupported',
    overridden: false,
    conflict: false,
  })),
  upstream_model_id: 'synthetic-image',
  metadata: {},
  configured: true,
  enabled: true,
  mapping: { model_pointer: '', parameters: {}, constants: [] },
  capability_revision: '2',
  capability_readiness: 'ready',
  catalog_type: 'image',
  missing: false,
};
const upstream = {
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
  adapter: {
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
  },
  control: null,
};
function json(value: unknown, status = 200) {
  return new Response(JSON.stringify(value), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}
async function setup(
  failModelSaves = 0,
  lostReceipt = false,
  failBatchSaves = 0,
  failRefreshes = 0,
  beforeModelResponse?: () => Promise<void>,
  beforeUpstreamReload?: () => Promise<void>,
) {
  let failures = failModelSaves;
  const writes: unknown[] = [];
  const upstreamWrites: unknown[] = [];
  const writeKeys: (string | null)[] = [];
  const batchWrites: unknown[] = [],
    refreshWrites: unknown[] = [];
  const batchKeys: (string | null)[] = [],
    refreshKeys: (string | null)[] = [];
  const liveUpstream = structuredClone(upstream);
  let liveModel = structuredClone(model);
  let upstreamReads = 0;
  let modelReads = 0;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (path: unknown, init?: RequestInit) => {
      const url = String(path);
      if (url.endsWith('/upstream')) {
        if (init?.method === 'PUT') {
          const input = JSON.parse(String(init.body));
          expect(input.expected_revision).toBe(liveUpstream.revision);
          upstreamWrites.push(input);
          liveUpstream.base_url = input.base_url;
          liveUpstream.revision = String(Number(liveUpstream.revision) + 1);
          return json({ revision: liveUpstream.revision });
        }
        upstreamReads++;
        if (upstreamReads > 1) await beforeUpstreamReload?.();
        return json(liveUpstream);
      }
      if (url.endsWith('/models/refresh')) {
        refreshWrites.push(JSON.parse(String(init?.body)));
        refreshKeys.push(new Headers(init?.headers).get('Idempotency-Key'));
        if (failRefreshes-- > 0) throw new TypeError('Synthetic lost refresh receipt');
        liveUpstream.revision = '5';
        liveModel = {
          ...liveModel,
          revision: '4',
          pricing_revision: '4',
          capability_revision: '4',
        };
        return json({
          operation: {
            id: 'op_AAAAAAAAAAAAAAAAAAAAAA',
            state: 'succeeded',
            created_at: 1700000000,
            completed_at: 1700000001,
            model_count: 1,
            error_code: null,
          },
        });
      }
      if (url.endsWith('/models/batch')) {
        const input = JSON.parse(String(init?.body));
        batchWrites.push(input);
        batchKeys.push(new Headers(init?.headers).get('Idempotency-Key'));
        if (failBatchSaves-- > 0) throw new TypeError('Synthetic lost batch receipt');
        const change = input.models[0].input;
        liveModel = { ...liveModel, revision: '3', price: change.price, pricing: change.pricing };
        liveUpstream.revision = '2';
        return json({
          applied: true,
          receipts: [
            { id: model.id, revision: '3', capability_revision: '3', pricing_revision: '3' },
          ],
          issues: [],
        });
      }
      if (url.includes('/upstream/controls')) return json({ data: [], next_cursor: null });
      if (url.includes('/models/') && init?.method === 'PUT') {
        const input = JSON.parse(String(init.body));
        writes.push(input);
        writeKeys.push(new Headers(init.headers).get('Idempotency-Key'));
        await beforeModelResponse?.();
        if (failures-- > 0) {
          if (lostReceipt) throw new TypeError('Synthetic lost receipt');
          return json({ code: 'invalid_request', message: 'Synthetic rejection' }, 400);
        }
        const revision = String(Number(liveModel.revision) + 1);
        liveModel = {
          ...liveModel,
          revision,
          enabled: input.enabled,
          price: input.price,
          pricing: input.pricing,
        };
        liveUpstream.revision = String(Number(liveUpstream.revision) + 1);
        return json({ id: model.id, revision });
      }
      if (url.includes('/models/')) {
        modelReads++;
        return json(liveModel);
      }
      if (url.includes('/models?'))
        return json({ data: [liveModel], total: 1, revision: '0', page: 1, page_size: 20 });
      throw new Error('Unexpected picture book request: ' + url);
    }),
  );
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  client.setQueryData(['admin', 'session'], { admin: { username: 'fixture-admin' } });
  const i18n = i18next.createInstance();
  await i18n.init({
    lng: 'en',
    fallbackLng: 'en',
    resources: { en: { translation: adminEnglish } },
    initAsync: false,
  });
  const router = createMemoryRouter(
    [
      {
        path: '/picture',
        element: (
          <>
            <Link to="/other">Leave page</Link>
            <AdminContent account="fixture-admin" />
          </>
        ),
      },
      { path: '/other', element: <p>Other page</p> },
    ],
    { initialEntries: ['/picture'] },
  );
  const view = render(
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={client}>
        <RouterProvider router={router} />
      </QueryClientProvider>
    </I18nextProvider>,
  );
  return {
    ...view,
    router,
    writes,
    upstreamWrites,
    writeKeys,
    batchWrites,
    batchKeys,
    refreshWrites,
    refreshKeys,
    reads: () => ({ upstreamReads, modelReads }),
    user: userEvent.setup(),
    client,
  };
}

describe('picture book draft navigation', () => {
  it('blocks SPA navigation and browser close, preserves failed saves, then leaves after a receipt', async () => {
    const view = await setup(1);
    await screen.findByRole('option', { name: /synthetic-image/ });
    await view.user.selectOptions(
      await screen.findByLabelText('Choose a model to configure'),
      model.id,
    );
    await view.user.clear(screen.getByLabelText('Sketch paper per image'));
    await view.user.type(screen.getByLabelText('Sketch paper per image'), '5');
    await view.user.click(screen.getByRole('link', { name: 'Leave page' }));
    await screen.findByRole('button', { name: 'Save and leave' });
    expect(view.router.state.location.pathname).toBe('/picture');
    const beforeUnload = new Event('beforeunload', { cancelable: true });
    act(() => {
      window.dispatchEvent(beforeUnload);
    });
    expect(beforeUnload.defaultPrevented).toBe(true);
    await view.user.click(screen.getByRole('button', { name: 'Continue editing' }));
    expect(screen.getByLabelText('Sketch paper per image')).toHaveValue('5');
    await view.user.click(screen.getByRole('link', { name: 'Leave page' }));
    await view.user.click(screen.getByRole('button', { name: 'Save and leave' }));
    await waitFor(() => expect(view.writes).toHaveLength(1));
    expect(view.router.state.location.pathname).toBe('/picture');
    expect(screen.getByLabelText('Sketch paper per image')).toHaveValue('5');
    await view.user.click(screen.getByRole('button', { name: 'Save and leave' }));
    await screen.findByText('Other page');
    expect(view.writes).toHaveLength(2);
    expect(view.writes[0]).toHaveProperty('price.paper', '5');
    expect(view.writes[1]).toHaveProperty('price.paper', '5');
    view.client.clear();
  });

  it('discards an unsaved model switch, including switching to no selection, and can discard on leave', async () => {
    const view = await setup();
    await screen.findByRole('option', { name: /synthetic-image/ });
    await view.user.selectOptions(
      await screen.findByLabelText('Choose a model to configure'),
      model.id,
    );
    await view.user.clear(screen.getByLabelText('Sketch paper per image'));
    await view.user.type(screen.getByLabelText('Sketch paper per image'), '6');
    await view.user.selectOptions(screen.getByLabelText('Choose a model to configure'), '');
    await screen.findByRole('button', { name: 'Discard draft and switch' });
    expect(screen.getByLabelText('Choose a model to configure')).toHaveValue(model.id);
    await view.user.click(screen.getByRole('button', { name: 'Continue editing' }));
    await view.user.selectOptions(screen.getByLabelText('Choose a model to configure'), '');
    await view.user.click(screen.getByRole('button', { name: 'Discard draft and switch' }));
    expect(screen.getByLabelText('Choose a model to configure')).toHaveValue('');
    await view.user.selectOptions(screen.getByLabelText('Choose a model to configure'), model.id);
    expect(screen.getByLabelText('Sketch paper per image')).toHaveValue(model.price.paper);
    await view.user.clear(screen.getByLabelText('Sketch paper per image'));
    await view.user.type(screen.getByLabelText('Sketch paper per image'), '7');
    await view.user.click(screen.getByRole('link', { name: 'Leave page' }));
    await view.user.click(await screen.findByRole('button', { name: 'Discard drafts and leave' }));
    await screen.findByText('Other page');
    expect(view.writes).toHaveLength(0);
    view.client.clear();
  });

  it('protects endpoint and replacement-key drafts on SPA leave without caching the key', async () => {
    const view = await setup();
    await view.user.clear(await screen.findByLabelText('Service base URL'));
    await view.user.type(
      screen.getByLabelText('Service base URL'),
      'https://images.example.invalid/v1',
    );
    await view.user.click(screen.getByLabelText('Replace stored key'));
    await view.user.type(screen.getByLabelText('Activity key'), 'synthetic-private-key');
    expect(screen.getByRole('button', { name: 'Refresh model catalog' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Reload saved settings' })).toBeDisabled();
    await view.user.click(screen.getByRole('link', { name: 'Leave page' }));
    await screen.findByRole('button', { name: 'Save and leave' });
    expect(view.router.state.location.pathname).toBe('/picture');
    await view.user.click(screen.getByRole('button', { name: 'Continue editing' }));
    expect(screen.getByLabelText('Activity key')).toHaveValue('synthetic-private-key');
    expect(
      JSON.stringify(
        view.client
          .getQueryCache()
          .getAll()
          .map((query) => query.state.data),
      ),
    ).not.toContain('synthetic-private-key');
    await view.user.click(screen.getByRole('link', { name: 'Leave page' }));
    await view.user.click(screen.getByRole('button', { name: 'Save and leave' }));
    await screen.findByText('Other page');
    expect(view.upstreamWrites).toEqual([
      {
        expected_revision: '1',
        base_url: 'https://images.example.invalid/v1',
        secret: { mode: 'replace', value: 'synthetic-private-key' },
      },
    ]);
    view.client.clear();
  });

  it('saves a model before switching to no selection', async () => {
    const view = await setup();
    await screen.findByRole('option', { name: /synthetic-image/ });
    await view.user.selectOptions(screen.getByLabelText('Choose a model to configure'), model.id);
    await view.user.clear(screen.getByLabelText('Sketch paper per image'));
    await view.user.type(screen.getByLabelText('Sketch paper per image'), '8');
    await view.user.selectOptions(screen.getByLabelText('Choose a model to configure'), '');
    await view.user.click(await screen.findByRole('button', { name: 'Save and switch' }));
    await waitFor(() => expect(view.writes).toHaveLength(1));
    expect(screen.getByLabelText('Choose a model to configure')).toHaveValue('');
    view.client.clear();
  });
  it('keeps the exact uncertain model save and blocks discard, refresh and other configuration writes', async () => {
    const view = await setup(1, true);
    await screen.findByRole('option', { name: /synthetic-image/ });
    await view.user.selectOptions(
      await screen.findByLabelText('Choose a model to configure'),
      model.id,
    );
    await view.user.clear(screen.getByLabelText('Sketch paper per image'));
    await view.user.type(screen.getByLabelText('Sketch paper per image'), '6');
    await view.user.click(screen.getByRole('button', { name: 'Save model settings' }));
    await screen.findByText(/save result is unconfirmed/i);
    expect(screen.getByLabelText('Sketch paper per image')).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Refresh model catalog' })).toBeDisabled();
    expect(screen.getByLabelText('Service base URL')).toBeDisabled();
    expect(screen.getByLabelText('Choose a model to configure')).toBeDisabled();
    await view.user.click(screen.getByRole('link', { name: 'Leave page' }));
    expect(await screen.findByRole('button', { name: 'Discard drafts and leave' })).toBeDisabled();
    await view.user.click(screen.getByRole('button', { name: 'Continue editing' }));
    await view.user.click(screen.getByRole('button', { name: 'Retry the same save' }));
    await waitFor(() => expect(view.writes).toHaveLength(2));
    expect(view.writes[0]).toEqual(view.writes[1]);
    expect(view.writeKeys[0]).toEqual(view.writeKeys[1]);
    await waitFor(() => expect(screen.getByLabelText('Sketch paper per image')).toHaveValue('6'));
    view.client.clear();
  });

  it('cannot discard a pending or uncertain save from the model-switch prompt and preserves its retry identity', async () => {
    let release!: () => void;
    const responseGate = new Promise<void>((resolve) => { release = resolve; });
    const view = await setup(1, true, 0, 0, () => responseGate);
    await screen.findByRole('option', { name: /synthetic-image/ });
    await view.user.selectOptions(screen.getByLabelText('Choose a model to configure'), model.id);
    await view.user.clear(screen.getByLabelText('Sketch paper per image'));
    await view.user.type(screen.getByLabelText('Sketch paper per image'), '9');
    await view.user.selectOptions(screen.getByLabelText('Choose a model to configure'), '');
    await view.user.click(await screen.findByRole('button', { name: 'Save and switch' }));
    await waitFor(() => expect(view.writes).toHaveLength(1));
    const discard = screen.getByRole('button', { name: 'Discard draft and switch' });
    expect(discard).toBeDisabled();
    await view.user.click(discard);
    expect(screen.getByLabelText('Choose a model to configure')).toHaveValue(model.id);
    await act(async () => release());
    await screen.findByText(/save result is unconfirmed/i);
    expect(discard).toBeDisabled();
    await view.user.click(discard);
    expect(screen.getByLabelText('Sketch paper per image')).toHaveValue('9');
    await view.user.click(screen.getByRole('button', { name: 'Retry the same save' }));
    await waitFor(() => expect(view.writes).toHaveLength(2));
    expect(view.writes[0]).toEqual(view.writes[1]);
    expect(view.writeKeys[0]).toEqual(view.writeKeys[1]);
    await waitFor(() => expect(screen.getByLabelText('Choose a model to configure')).toHaveValue(''));
    view.client.clear();
  });

  it('locks endpoint and key editing during an explicit authoritative reload', async () => {
    let release!: () => void;
    const readGate = new Promise<void>((resolve) => { release = resolve; });
    const view = await setup(0, false, 0, 0, undefined, () => readGate);
    await screen.findByLabelText('Service base URL');
    await view.user.click(screen.getByRole('button', { name: 'Reload saved settings' }));
    await waitFor(() => expect(view.reads().upstreamReads).toBeGreaterThan(1));
    expect(screen.getByLabelText('Service base URL')).toBeDisabled();
    expect(screen.getByLabelText('Replace stored key')).toBeDisabled();
    await act(async () => release());
    await waitFor(() => expect(screen.getByLabelText('Service base URL')).toBeEnabled());
    expect(view.upstreamWrites).toHaveLength(0);
    view.client.clear();
  });

  it('locks old model configuration until the saved service has been authoritatively reloaded', async () => {
    let release!: () => void;
    const readGate = new Promise<void>((resolve) => { release = resolve; });
    const view = await setup(0, false, 0, 0, undefined, () => readGate);
    await screen.findByRole('option', { name: /synthetic-image/ });
    await view.user.selectOptions(screen.getByLabelText('Choose a model to configure'), model.id);
    await view.user.clear(screen.getByLabelText('Service base URL'));
    await view.user.type(screen.getByLabelText('Service base URL'), 'https://replacement.example.invalid/v1');
    await view.user.click(screen.getByRole('button', { name: 'Save service settings' }));
    await waitFor(() => expect(view.upstreamWrites).toHaveLength(1));
    await waitFor(() => expect(view.reads().upstreamReads).toBeGreaterThan(1));
    expect(screen.getByLabelText('Sketch paper per image')).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Save model settings' })).toBeDisabled();
    expect(screen.getByLabelText('Choose a model to configure')).toBeDisabled();
    await act(async () => release());
    await waitFor(() => expect(screen.getByLabelText('Sketch paper per image')).toBeEnabled());
    expect(screen.getByLabelText('Service base URL')).toHaveValue('https://replacement.example.invalid/v1');
    expect(view.writes).toHaveLength(0);
    view.client.clear();
  });

  it('reloads upstream and opened model revisions after refresh before a subsequent save', async () => {
    const view = await setup();
    await screen.findByRole('option', { name: /synthetic-image/ });
    await view.user.selectOptions(
      await screen.findByLabelText('Choose a model to configure'),
      model.id,
    );
    await view.user.click(screen.getByRole('button', { name: 'Refresh model catalog' }));
    await screen.findByText(/Catalog updated/);
    await waitFor(() => expect(view.reads().upstreamReads).toBeGreaterThan(1));
    await waitFor(() => expect(view.reads().modelReads).toBeGreaterThan(0));
    await waitFor(() => expect(screen.getByLabelText('Sketch paper per image')).toBeEnabled());
    await view.user.clear(screen.getByLabelText('Sketch paper per image'));
    await view.user.type(screen.getByLabelText('Sketch paper per image'), '6');
    await view.user.click(screen.getByRole('button', { name: 'Save model settings' }));
    await waitFor(() => expect(view.writes[0]).toHaveProperty('expected_revision', '4'));
    await waitFor(() => expect(screen.getByLabelText('Service base URL')).toBeEnabled());
    await view.user.clear(screen.getByLabelText('Service base URL'));
    await view.user.type(
      screen.getByLabelText('Service base URL'),
      'https://images.example.invalid/v1',
    );
    await view.user.click(screen.getByRole('button', { name: 'Save service settings' }));
    await waitFor(() => expect(view.upstreamWrites[0]).toHaveProperty('expected_revision', '6'));
    expect(view.upstreamWrites[0]).toHaveProperty('secret.mode', 'keep');
    view.client.clear();
  });
  it('retains an uncertain atomic batch and reads the new service revision before later edits', async () => {
    const view = await setup(0, false, 1);
    await screen.findByRole('option', { name: /synthetic-image/ });
    await view.user.selectOptions(screen.getByLabelText('Choose a model to configure'), model.id);
    await view.user.click(screen.getByLabelText('synthetic-image'));
    await view.user.clear(screen.getByLabelText('Sketch paper per image'));
    await view.user.type(screen.getByLabelText('Sketch paper per image'), '7');
    await view.user.click(screen.getByRole('button', { name: 'Save selected models' }));
    await screen.findByRole('button', { name: 'Retry the same batch save' });
    expect(screen.getByLabelText('Sketch paper per image')).toBeDisabled();
    expect(screen.getByLabelText('Service base URL')).toBeDisabled();
    await view.user.click(screen.getByRole('link', { name: 'Leave page' }));
    expect(await screen.findByRole('button', { name: 'Discard drafts and leave' })).toBeDisabled();
    await view.user.click(screen.getByRole('button', { name: 'Save and leave' }));
    expect(view.router.state.location.pathname).toBe('/picture');
    await view.user.click(screen.getByRole('button', { name: 'Continue editing' }));
    await view.user.click(screen.getByRole('button', { name: 'Retry the same batch save' }));
    await waitFor(() => expect(screen.getByLabelText('Service base URL')).toBeEnabled());
    expect(view.batchWrites).toHaveLength(2);
    expect(view.batchWrites[0]).toEqual(view.batchWrites[1]);
    expect(view.batchKeys[0]).toEqual(view.batchKeys[1]);
    expect(view.batchWrites[1]).toHaveProperty('models.0.input.pricing.default.paper', '7');
    await view.user.click(screen.getByRole('button', { name: 'Save service settings' }));
    await waitFor(() => expect(view.upstreamWrites[0]).toHaveProperty('expected_revision', '2'));
    view.client.clear();
  });

  it('keeps uncertain catalog refresh identity and prevents other writes or discarding it on leave', async () => {
    const view = await setup(0, false, 0, 1);
    await screen.findByRole('option', { name: /synthetic-image/ });
    await view.user.click(screen.getByRole('button', { name: 'Refresh model catalog' }));
    await screen.findByRole('button', { name: 'Retry the same model refresh' });
    expect(screen.getByLabelText('Service base URL')).toBeDisabled();
    expect(screen.getByLabelText('Choose a model to configure')).toBeDisabled();
    await view.user.click(screen.getByRole('link', { name: 'Leave page' }));
    expect(await screen.findByRole('button', { name: 'Discard drafts and leave' })).toBeDisabled();
    await view.user.click(screen.getByRole('button', { name: 'Continue editing' }));
    await view.user.click(screen.getByRole('button', { name: 'Retry the same model refresh' }));
    await waitFor(() => expect(screen.getByLabelText('Service base URL')).toBeEnabled());
    expect(view.refreshWrites).toEqual([{}, {}]);
    expect(view.refreshKeys[0]).toEqual(view.refreshKeys[1]);
    await view.user.click(screen.getByRole('button', { name: 'Save service settings' }));
    await waitFor(() => expect(view.upstreamWrites[0]).toHaveProperty('expected_revision', '5'));
    view.client.clear();
  });
});
