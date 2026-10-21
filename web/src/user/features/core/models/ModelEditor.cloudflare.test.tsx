import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../../test/unit/support';
import { ModelEditor } from './ModelEditor';
import { normalizeModel } from '../normalizers';
import { coreKeys } from '../queries';
const user = JSON.parse(
  readFileSync(resolve(process.cwd(), '..', 'internal/auth/testdata/user_envelope.json'), 'utf8'),
).user;

const model = normalizeModel({
  ...JSON.parse(
    readFileSync(
      resolve(process.cwd(), '..', 'internal/resources/testdata/manual_update.json'),
      'utf8',
    ),
  ).affected_models[0].model,
  cloudflare_proxy_enabled: false,
  cloudflare_proxy_mode: 'worker',
});
const json = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } });
afterEach(() => vi.unstubAllGlobals());
describe('personal model Cloudflare editor', () => {
  it('defaults a new model on and permits disabling before creation', async () => {
    let body: Record<string, unknown> | undefined;
    const saved = vi.fn();
    vi.stubGlobal(
      'fetch',
      vi.fn(async (_url, init) => {
        body = JSON.parse(String(init?.body));
        return json({ ...model, ...body, full_name: 'personal/test' }, 201);
      }),
    );
    const view = await renderWithProviders(
      <ModelEditor accountId={user.id} onCancel={() => undefined} onSaved={saved} />,
      { station: 'user' },
    );
    view.queryClient.setQueryData(coreKeys.session, { user });
    const toggle = screen.getByRole('switch', { name: 'Cloudflare forwarding' });
    expect(toggle).toBeChecked();
    await view.user.click(toggle);
    await view.user.type(screen.getByRole('textbox', { name: 'Prefix' }), 'personal');
    await view.user.type(screen.getByRole('textbox', { name: 'Model name' }), 'test');
    await view.user.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(saved).toHaveBeenCalled());
    expect(body).toMatchObject({ cloudflare_proxy_enabled: false });
    expect(body).not.toHaveProperty('cloudflare_proxy_mode');
  });
  it('reads the saved bool and retains the changed bool after a conflict', async () => {
    const sent: Record<string, unknown>[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (_url, init) => {
        if (init?.method === 'PATCH') {
          sent.push(JSON.parse(String(init.body)));
          return json({ error: { code: 'conflict', message: 'Changed.' } }, 409);
        }
        return json({ ...model, revision: '2' });
      }),
    );
    const view = await renderWithProviders(
      <ModelEditor
        accountId={user.id}
        initial={model}
        onCancel={() => undefined}
        onSaved={() => undefined}
      />,
      { station: 'user' },
    );
    view.queryClient.setQueryData(coreKeys.session, { user });
    const toggle = screen.getByRole('switch', { name: 'Cloudflare forwarding' });
    expect(toggle).not.toBeChecked();
    await view.user.click(toggle);
    await view.user.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0]).toMatchObject({
      expected_revision: model.revision,
      cloudflare_proxy_enabled: true,
    });
    await waitFor(() => expect(toggle).toBeEnabled());
    expect(toggle).toBeChecked();
    expect(screen.getByRole('textbox', { name: 'Model name' })).toHaveValue(model.model);
  });
});
