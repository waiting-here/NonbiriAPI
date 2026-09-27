import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import i18next from 'i18next';
import { I18nextProvider } from 'react-i18next';
import { createMemoryRouter, Link, RouterProvider } from 'react-router';
import { describe, expect, it, vi } from 'vitest';
import { modelFixture } from '@shared/picturebook/fixtures';
import { AdminContent } from './PictureBookAdmin';

const publicModel = modelFixture();
const model = {
  ...publicModel,
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
async function setup(failModelSaves = 0) {
  let failures = failModelSaves;
  const writes: unknown[] = [];
  const profileWrites: unknown[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (path: unknown, init?: RequestInit) => {
      const url = String(path);
      if (url.endsWith('/upstream')) return json(upstream);
      if (url.endsWith('/upstream/capability-profile')) {
        const profile =
          init?.method === 'PUT'
            ? (JSON.parse(String(init.body)) as { profile: unknown }).profile
            : {
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
        if (init?.method === 'PUT') profileWrites.push(profile);
        return json({ revision: init?.method === 'PUT' ? '2' : '1', profile });
      }
      if (url.includes('/upstream/controls')) return json({ data: [], next_cursor: null });
      if (url.includes('/models/') && init?.method === 'PUT') {
        writes.push(JSON.parse(String(init.body)));
        if (failures-- > 0)
          return json({ code: 'invalid_request', message: 'Synthetic rejection' }, 400);
        return json({ id: model.id, revision: '3' });
      }
      if (url.includes('/models/')) return json(model);
      if (url.includes('/models?'))
        return json({ data: [model], total: 1, revision: '0', page: 1, page_size: 20 });
      throw new Error('Unexpected picture book request: ' + url);
    }),
  );
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  client.setQueryData(['admin', 'session'], { admin: { username: 'fixture-admin' } });
  const i18n = i18next.createInstance();
  await i18n.init({
    lng: 'en',
    fallbackLng: 'en',
    resources: { en: { translation: {} } },
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
  return { ...view, router, writes, profileWrites, user: userEvent.setup(), client };
}

describe('picture book draft navigation', () => {
  it('blocks SPA navigation and browser close, preserves failed saves, then leaves after a receipt', async () => {
    const view = await setup(1);
    await screen.findByRole('option', { name: /synthetic-image/ });
    await view.user.selectOptions(
      await screen.findByLabelText('Choose a model to configure'),
      model.id,
    );
    await view.user.clear(screen.getByLabelText('Public display name'));
    await view.user.type(screen.getByLabelText('Public display name'), 'Edited name');
    await view.user.click(screen.getByRole('link', { name: 'Leave page' }));
    await screen.findByRole('button', { name: 'Save and leave' });
    expect(view.router.state.location.pathname).toBe('/picture');
    const beforeUnload = new Event('beforeunload', { cancelable: true });
    act(() => {
      window.dispatchEvent(beforeUnload);
    });
    expect(beforeUnload.defaultPrevented).toBe(true);
    await view.user.click(screen.getByRole('button', { name: 'Continue editing' }));
    expect(screen.getByLabelText('Public display name')).toHaveValue('Edited name');
    await view.user.click(screen.getByRole('link', { name: 'Leave page' }));
    await view.user.click(screen.getByRole('button', { name: 'Save and leave' }));
    await waitFor(() => expect(view.writes).toHaveLength(1));
    expect(view.router.state.location.pathname).toBe('/picture');
    expect(screen.getByLabelText('Public display name')).toHaveValue('Edited name');
    await view.user.click(screen.getByRole('button', { name: 'Save and leave' }));
    await screen.findByText('Other page');
    expect(view.writes).toHaveLength(2);
    expect(view.writes[0]).toHaveProperty('display_name', 'Edited name');
    expect(view.writes[1]).toHaveProperty('display_name', 'Edited name');
    view.client.clear();
  });

  it('discards an unsaved model switch, including switching to no selection, and can discard on leave', async () => {
    const view = await setup();
    await screen.findByRole('option', { name: /synthetic-image/ });
    await view.user.selectOptions(
      await screen.findByLabelText('Choose a model to configure'),
      model.id,
    );
    await view.user.clear(screen.getByLabelText('Public display name'));
    await view.user.type(screen.getByLabelText('Public display name'), 'Unsent edit');
    await view.user.selectOptions(screen.getByLabelText('Choose a model to configure'), '');
    await screen.findByRole('button', { name: 'Discard draft and switch' });
    expect(screen.getByLabelText('Choose a model to configure')).toHaveValue(model.id);
    await view.user.click(screen.getByRole('button', { name: 'Continue editing' }));
    await view.user.selectOptions(screen.getByLabelText('Choose a model to configure'), '');
    await view.user.click(screen.getByRole('button', { name: 'Discard draft and switch' }));
    expect(screen.getByLabelText('Choose a model to configure')).toHaveValue('');
    await view.user.selectOptions(screen.getByLabelText('Choose a model to configure'), model.id);
    expect(screen.getByLabelText('Public display name')).toHaveValue(model.display_name);
    await view.user.clear(screen.getByLabelText('Public display name'));
    await view.user.type(screen.getByLabelText('Public display name'), 'Second unsent edit');
    await view.user.click(screen.getByRole('link', { name: 'Leave page' }));
    await view.user.click(await screen.findByRole('button', { name: 'Discard drafts and leave' }));
    await screen.findByText('Other page');
    expect(view.writes).toHaveLength(0);
    view.client.clear();
  });

  it('protects a private capability profile draft on SPA leave', async () => {
    const view = await setup();
    await view.user.type(await screen.findByLabelText('catalog_type_pointer'), '/kind');
    await view.user.click(screen.getByRole('link', { name: 'Leave page' }));
    await screen.findByRole('button', { name: 'Save and leave' });
    expect(view.router.state.location.pathname).toBe('/picture');
    await view.user.click(screen.getByRole('button', { name: 'Continue editing' }));
    expect(screen.getByLabelText('catalog_type_pointer')).toHaveValue('/kind');
    await view.user.click(screen.getByRole('link', { name: 'Leave page' }));
    await view.user.click(screen.getByRole('button', { name: 'Save and leave' }));
    await screen.findByText('Other page');
    expect(view.profileWrites).toHaveLength(1);
    expect(view.profileWrites[0]).toHaveProperty('catalog_type_pointer', '/kind');
    view.client.clear();
  });

  it('saves a model before switching to no selection', async () => {
    const view = await setup();
    await screen.findByRole('option', { name: /synthetic-image/ });
    await view.user.selectOptions(screen.getByLabelText('Choose a model to configure'), model.id);
    await view.user.clear(screen.getByLabelText('Public display name'));
    await view.user.type(screen.getByLabelText('Public display name'), 'Saved switch');
    await view.user.selectOptions(screen.getByLabelText('Choose a model to configure'), '');
    await view.user.click(await screen.findByRole('button', { name: 'Save and switch' }));
    await waitFor(() => expect(view.writes).toHaveLength(1));
    expect(screen.getByLabelText('Choose a model to configure')).toHaveValue('');
    view.client.clear();
  });
});
