import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { act, screen, waitFor } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../../test/unit/support';
import type { MatrixTarget } from './TargetMatrix';
import { BindingTargets } from './BindingTargets';

vi.mock('./TargetMatrix', () => ({
  TargetMatrix: ({
    value,
    onChange,
    disabled,
  }: {
    value: MatrixTarget[];
    onChange: (value: MatrixTarget[]) => void;
    disabled: boolean;
  }) => (
    <input
      aria-label="Target draft"
      disabled={disabled}
      value={value[0]?.upstream_model_id ?? ''}
      onChange={(event) =>
        onChange(
          value.map((row, i) =>
            i === 0 ? { ...row, upstream_model_id: event.target.value } : row,
          ),
        )
      }
    />
  ),
}));
const json = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), {
    status,
    headers: { 'content-type': 'application/json' },
  });

it('loads bindings once, keeps a folded draft, and adopts only a refreshed conflict revision', async () => {
  const model = {
    ...JSON.parse(
      readFileSync(
        resolve(process.cwd(), '../internal/resources/testdata/manual_update.json'),
        'utf8',
      ),
    ).affected_models[0].model,
    binding_revision: '1',
  };
  let revision = '1';
  let reads = 0;
  let pendingRead: (() => void) | undefined;
  let blockConflictRead = false;
  const writes: Record<string, unknown>[] = [];
  const target = {
    id: '20',
    revision: '1',
    endpoint_key_id: '2',
    endpoint_id: '1',
    upstream_model_id: 'original-model',
    connector_type: 'openai-compatible',
    configuration_type: 'openai-compatible',
    base_url: 'https://upstream.example/v1',
    credential_id: '3',
    credential_version: '1',
    grant_revision: '1',
    native_services: ['chat_completions'],
    model_types: ['chat_completions'],
    service_routes: [
      { operation: 'chat_completions', native_service: 'chat_completions', mode: 'native' },
    ],
    capabilities: { can_bind: true, unavailable_reason: null },
  };
  vi.stubGlobal(
    'fetch',
    vi.fn<typeof fetch>(async (input, init) => {
      const path = new URL(String(input), 'https://app.example').pathname;
      if (path === '/api/models/' + model.id + '/bindings/10/targets' && init?.method === 'PUT') {
        const body = JSON.parse(String(init.body));
        writes.push(body);
        if (writes.length === 1) {
          revision = '2';
          blockConflictRead = true;
          return json({ error: { code: 'conflict', message: 'Bindings changed' } }, 409);
        }
        expect(body.expected_binding_revision).toBe(revision);
        revision = String(Number(revision) + 1);
        return json({ binding_revision: revision });
      }
      if (path === '/api/models/' + model.id + '/bindings') {
        reads += 1;
        if (blockConflictRead) {
          blockConflictRead = false;
          await new Promise<void>((resolve) => {
            pendingRead = resolve;
          });
        }
        return json({
          binding_revision: revision,
          bindings: [
            {
              id: '10',
              endpoint_key_id: '2',
              upstream_model_id: 'original-model',
              endpoint_base_url: target.base_url,
              connector_type: 'openai-compatible',
              endpoint_note: '',
              endpoint_key_display_head: 'head',
              endpoint_key_display_tail: 'tail',
              endpoint_key_note: '',
              ord: 0,
              targets: [target],
            },
          ],
        });
      }
      throw Error('Unexpected ' + (init?.method ?? 'GET') + ' ' + path);
    }),
  );
  const view = await renderWithProviders(<BindingTargets accountId="1" model={model} />, {
    station: 'user',
    role: 'user',
  });
  view.queryClient.setQueryData(['user', 'session'], {
    user: { id: '1', username: 'owner', effective_level: 1 },
  });
  await view.user.click(
    await screen.findByText('Configure sources by call type', { selector: 'strong' }),
  );
  const group = await screen.findByText('Call combination 1', { selector: 'strong' });
  expect(screen.queryByLabelText('Target draft')).toBeNull();
  await view.user.click(group);
  const draft = await screen.findByLabelText('Target draft');
  await view.user.clear(draft);
  await view.user.type(draft, 'draft-model');
  await view.user.click(group);
  await view.user.click(group);
  expect(draft).toHaveValue('draft-model');
  expect(reads).toBe(1);
  await view.user.click(screen.getByRole('button', { name: 'Save call combination' }));
  const adopt = await screen.findByRole('button', { name: 'Continue with current revision' });
  await waitFor(() => expect(pendingRead).toBeDefined());
  expect(adopt).toBeDisabled();
  await act(async () => {
    pendingRead!();
  });
  await waitFor(() => expect(adopt).toBeEnabled());
  expect(draft).toHaveValue('draft-model');
  await view.user.click(adopt);
  await view.user.click(screen.getByRole('button', { name: 'Save call combination' }));
  await screen.findByText('Saved');
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Save call combination' })).toBeEnabled(),
  );
  await view.user.click(screen.getByRole('button', { name: 'Save call combination' }));
  await waitFor(() => expect(writes).toHaveLength(3));
  expect(writes.map((row) => row.expected_binding_revision)).toEqual(['1', '2', '3']);
  expect(writes[2]?.targets).toEqual([
    {
      endpoint_key_id: '2',
      upstream_model_id: 'draft-model',
      native_services: ['chat_completions'],
    },
  ]);
});
