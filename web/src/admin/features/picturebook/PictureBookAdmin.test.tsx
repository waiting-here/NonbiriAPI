import { screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { assertNoSensitiveQueryCache, renderWithProviders } from '../../../../test/unit/support';
import {
  decodeAdapter,
  decodeControlRow,
  decodeRefresh,
  getRefresh,
  refreshModels,
  type Upstream,
} from './adminApi';
import { UpstreamForm } from './UpstreamForm';
import { RecoveryPanel } from './RecoveryPanel';

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
function reply(body: unknown) {
  return new Response(JSON.stringify(body), { headers: { 'Content-Type': 'application/json' } });
}
describe('image configuration boundaries', () => {
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
