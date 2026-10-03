import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { act, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import { ModelsWorkspace } from './ModelsWorkspace';
import { coreKeys } from './queries';
import type { UserProfile } from './types';

const user = JSON.parse(
  readFileSync(resolve(process.cwd(), '..', 'internal/auth/testdata/user_envelope.json'), 'utf8'),
).user as UserProfile;
const original = {
  ...JSON.parse(
    readFileSync(
      resolve(process.cwd(), '..', 'internal/resources/testdata/manual_update.json'),
      'utf8',
    ),
  ).affected_models[0].model,
  revision: '1',
};
const page = {
  data: [],
  pagination: { page: '1', page_size: 20, total_items: '0', total_pages: '1' },
};
function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}
function fixture(save?: (body: Record<string, unknown>) => Promise<Response>) {
  let current = { ...original, role_policy: { default_action: 'native', rules: {} } };
  const writes: Record<string, unknown>[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(
        input instanceof Request ? input.url : String(input),
        window.location.origin,
      );
      const method = init?.method ?? 'GET';
      if (method === 'GET') {
        if (url.pathname === '/api/models/' + original.id) return json(current);
        if (url.pathname.endsWith('/bindings'))
          return json({ bindings: [], binding_revision: '1' });
        if (url.pathname === '/api/models' || url.pathname === '/api/endpoints') return json(page);
      }
      if ((method === 'PATCH' || method === 'POST') && url.pathname.startsWith('/api/models')) {
        const body = JSON.parse(String(init?.body)) as Record<string, unknown>;
        writes.push(body);
        const wire = { ...body };
        delete wire.expected_revision;
        current = {
          ...current,
          ...wire,
          revision: '2',
          full_name:
            String(body.provider ?? current.provider) + '/' + String(body.model ?? current.model),
        };
        if (save) return save(body);
        return json(current, method === 'POST' ? 201 : 200);
      }
      throw new Error('Unexpected request: ' + method + ' ' + url.pathname);
    }),
  );
  return { writes };
}
async function renderEditor(create = false) {
  const view = await renderWithProviders(<ModelsWorkspace user={user} />, {
    station: 'user',
    route: create ? '/models' : '/models?model_id=' + original.id,
  });
  view.queryClient.setQueryData(coreKeys.session, { user });
  await view.user.click(
    await screen.findByRole('button', {
      name: create ? 'Create model' : 'Edit model',
    }),
  );
  await view.user.click(screen.getByText(/Default:.*role rules/));
  return view;
}
async function addRule(
  view: Awaited<ReturnType<typeof renderEditor>>,
  name: string,
  action: string,
) {
  const editor = screen.getByRole('group', { name: 'Message role handling' });
  await view.user.click(within(editor).getByRole('button', { name: 'Add a role rule' }));
  const inputs = within(editor).getAllByRole('textbox', { name: 'Role name' });
  await view.user.type(inputs.at(-1)!, name);
  await view.user.selectOptions(
    within(editor).getAllByRole('combobox', { name: 'Handling action' }).at(-1)!,
    action,
  );
}
afterEach(() => vi.unstubAllGlobals());

describe('personal model role editor', () => {
  it('creates a model with native handling and no extra role rules', async () => {
    const f = fixture();
    const view = await renderEditor(true);
    const form = screen.getByRole('button', { name: 'Save' }).closest('form')!;
    await view.user.type(within(form).getByRole('textbox', { name: 'Prefix' }), 'personal');
    await view.user.type(screen.getByRole('textbox', { name: 'Model name' }), 'primary');
    expect(screen.getByRole('combobox', { name: 'Default action for unlisted roles' })).toHaveValue(
      'native',
    );
    await view.user.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(f.writes).toHaveLength(1));
    expect(f.writes[0].role_policy).toEqual({ default_action: 'native', rules: {} });
  });

  it('saves custom roles and fallback using the current model revision', async () => {
    const f = fixture();
    const view = await renderEditor();
    await view.user.selectOptions(
      screen.getByRole('combobox', { name: 'Default action for unlisted roles' }),
      'reject',
    );
    await view.user.selectOptions(
      screen.getByRole('combobox', { name: 'Streaming' }),
      'force_stream',
    );
    await addRule(view, 'developer', 'system');
    await addRule(view, 'critic', 'user');
    await view.user.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(f.writes).toHaveLength(1));
    expect(f.writes[0]).toMatchObject({
      transport_rule: 'force_stream',
      expected_revision: original.revision,
      role_policy: { default_action: 'reject', rules: { developer: 'system', critic: 'user' } },
    });
    await waitFor(() =>
      expect(
        screen.queryByRole('group', { name: 'Message role handling' }),
      ).not.toBeInTheDocument(),
    );
    expect(screen.getByText('critic')).toBeInTheDocument();
  });

  it('keeps duplicate rule rows editable and prevents saving until corrected', async () => {
    const f = fixture();
    const view = await renderEditor();
    await addRule(view, 'critic', 'user');
    await addRule(view, 'critic', 'assistant');
    expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled();
    expect(
      screen.getByText('This role is already configured in another row. Keep one rule for it.'),
    ).toBeInTheDocument();
    expect(f.writes).toHaveLength(0);
    await view.user.click(screen.getByRole('button', { name: 'Remove rule 2' }));
    expect(screen.getByRole('button', { name: 'Save' })).toBeEnabled();
  });

  it('preserves the draft and original CAS revision when the cached model advances', async () => {
    const f = fixture();
    const view = await renderEditor();
    await addRule(view, 'critic', 'user');
    await act(async () => {
      view.queryClient.setQueryData(coreKeys.model(user.id, original.id), {
        ...original,
        revision: '3',
        role_policy: { default_action: 'native', rules: {} },
      });
    });
    expect(screen.getByRole('textbox', { name: 'Role name' })).toHaveValue('critic');
    await view.user.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(f.writes).toHaveLength(1));
    expect(f.writes[0]).toMatchObject({
      expected_revision: original.revision,
      role_policy: { default_action: 'native', rules: { critic: 'user' } },
    });
  });

  it('confirms an unknown save by policy contents without a second PATCH', async () => {
    const f = fixture(async () => {
      throw new TypeError('Failed to fetch');
    });
    const view = await renderEditor();
    await addRule(view, 'developer', 'system');
    await view.user.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() =>
      expect(
        screen.queryByRole('group', { name: 'Message role handling' }),
      ).not.toBeInTheDocument(),
    );
    expect(f.writes).toHaveLength(1);
    expect(screen.getByText('developer')).toBeInTheDocument();
  });

  it('drops an old account draft and ignores a late save response', async () => {
    let finish!: (response: Response) => void;
    const pending = new Promise<Response>((resolve) => {
      finish = resolve;
    });
    const f = fixture(() => pending);
    const view = await renderEditor();
    await addRule(view, 'critic', 'user');
    await view.user.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(f.writes).toHaveLength(1));
    view.queryClient.setQueryData(coreKeys.session, null);
    await waitFor(() =>
      expect(
        screen.queryByRole('group', { name: 'Message role handling' }),
      ).not.toBeInTheDocument(),
    );
    await act(async () => {
      finish(json({ ...original, revision: '2', role_policy: f.writes[0].role_policy }));
    });
    await waitFor(() => expect(view.queryClient.getQueryData(coreKeys.session)).toBeNull());
    expect(view.queryClient.getQueryData(coreKeys.model(user.id, original.id))).not.toMatchObject({
      revision: '2',
    });
  });
});
