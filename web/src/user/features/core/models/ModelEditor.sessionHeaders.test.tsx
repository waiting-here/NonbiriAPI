import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../../test/unit/support';
import { ModelEditor } from './ModelEditor';
import { normalizeModel } from '../normalizers';
import { coreKeys } from '../queries';
import type { Model } from '../types';
const user = JSON.parse(
  readFileSync(resolve(process.cwd(), '..', 'internal/auth/testdata/user_envelope.json'), 'utf8'),
).user;
const model = normalizeModel(
  JSON.parse(
    readFileSync(
      resolve(process.cwd(), '..', 'internal/resources/testdata/manual_update.json'),
      'utf8',
    ),
  ).affected_models[0].model,
);
const configured: Model = {
  ...model,
  session_headers: [{ name: 'x-session-id', idle_seconds: 300, format: 'hex_32' }],
};
const json = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } });
async function editor(initial?: Model, onSaved = vi.fn()) {
  const view = await renderWithProviders(
    <ModelEditor
      accountId={user.id}
      initial={initial}
      onCancel={() => undefined}
      onSaved={onSaved}
    />,
    { station: 'user' },
  );
  view.queryClient.setQueryData(coreKeys.session, { user });
  return { ...view, saved: onSaved };
}
afterEach(() => vi.unstubAllGlobals());
describe('personal managed session headers', () => {
  it('creates with zero expiry and an OpenCode format', async () => {
    let sent: Record<string, unknown> | undefined;
    vi.stubGlobal(
      'fetch',
      vi.fn(async (_url, init) => {
        sent = JSON.parse(String(init?.body));
        return json({ ...model, ...sent, full_name: `${sent?.provider}/${sent?.model}` }, 201);
      }),
    );
    const view = await editor();
    await view.user.type(screen.getByRole('textbox', { name: 'Prefix' }), 'personal');
    await view.user.type(screen.getByRole('textbox', { name: 'Model name' }), 'test');
    await view.user.click(screen.getByText('Managed session headers', { selector: 'strong' }));
    await view.user.click(screen.getByRole('button', { name: 'Add managed header' }));
    await view.user.type(screen.getByLabelText('Header 1 name'), 'X-Session-ID');
    await view.user.clear(screen.getByLabelText('Idle expiry (seconds)'));
    await view.user.type(screen.getByLabelText('Idle expiry (seconds)'), '0');
    await view.user.selectOptions(screen.getByLabelText('Random value format'), 'opencode_session');
    await view.user.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(view.saved).toHaveBeenCalled());
    expect(sent?.session_headers).toEqual([
      { name: 'X-Session-ID', idle_seconds: 0, format: 'opencode_session' },
    ]);
  });
  it('retains an invalid-request draft, then clears saved headers explicitly', async () => {
    const sent: Record<string, unknown>[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (_url, init) => {
        if (init?.method === 'PATCH') {
          const body = JSON.parse(String(init.body));
          sent.push(body);
          return sent.length === 1
            ? json({ error: { code: 'invalid_request', message: 'Invalid header.' } }, 400)
            : json({ ...configured, ...body });
        }
        return json(configured);
      }),
    );
    const view = await editor(configured);
    await view.user.clear(screen.getByLabelText('Idle expiry (seconds)'));
    await view.user.type(screen.getByLabelText('Idle expiry (seconds)'), '0');
    await view.user.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(screen.getByLabelText('Idle expiry (seconds)')).toHaveValue(0);
    expect(screen.getByLabelText('Header 1 name')).toHaveValue('x-session-id');
    await view.user.click(screen.getByRole('button', { name: 'Remove header 1' }));
    await view.user.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(view.saved).toHaveBeenCalled());
    expect(sent[1].session_headers).toEqual([]);
  });

  it.each([true, false])(
    'reconciles an unknown PATCH only when header contents match (%s)',
    async (match) => {
      vi.stubGlobal(
        'fetch',
        vi.fn(async (_url, init) => {
          if (init?.method === 'PATCH') throw new TypeError('Failed to fetch');
          return json({
            ...configured,
            revision: '3',
            session_headers: [
              {
                name: 'X-SESSION-ID',
                idle_seconds: 0,
                format: match ? 'opencode_part' : 'opencode_message',
              },
            ],
          });
        }),
      );
      const view = await editor(configured);
      await view.user.clear(screen.getByLabelText('Idle expiry (seconds)'));
      await view.user.type(screen.getByLabelText('Idle expiry (seconds)'), '0');
      await view.user.selectOptions(screen.getByLabelText('Random value format'), 'opencode_part');
      await view.user.click(screen.getByRole('button', { name: 'Save' }));
      if (match) await waitFor(() => expect(view.saved).toHaveBeenCalled());
      else {
        await waitFor(() => expect(screen.getByLabelText('Header 1 name')).toBeDisabled());
        expect(screen.getByLabelText('Idle expiry (seconds)')).toHaveValue(0);
        expect(screen.getByLabelText('Random value format')).toHaveValue('opencode_part');
        expect(view.saved).not.toHaveBeenCalled();
      }
    },
  );
  it('retries an unresolved save using the retained payload and idempotency identity', async () => {
    const calls: RequestInit[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (_url, init) => {
        if (init?.method === 'PATCH') {
          calls.push(init);
          if (calls.length === 1) throw new TypeError('Failed to fetch');
          return json({ ...configured, ...JSON.parse(String(init.body)), revision: '2' });
        }
        return json(configured);
      }),
    );
    const view = await editor(configured);
    await view.user.clear(screen.getByLabelText('Idle expiry (seconds)'));
    await view.user.type(screen.getByLabelText('Idle expiry (seconds)'), '0');
    await view.user.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(screen.getByLabelText('Header 1 name')).toBeDisabled());
    await view.user.click(screen.getByRole('button', { name: 'Check result' }));
    await waitFor(() => expect(view.saved).toHaveBeenCalled());
    expect(calls).toHaveLength(2);
    expect(calls[1].body).toBe(calls[0].body);
    expect(new Headers(calls[1].headers).get('Idempotency-Key')).toBe(
      new Headers(calls[0].headers).get('Idempotency-Key'),
    );
  });
  it('reads the saved session configuration from an unknown-create receipt', async () => {
    let sent: Record<string, unknown> | undefined;
    let identity: string | null = null;
    let receiptKey: unknown;
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url, init) => {
        if (String(url) === '/api/resource-operation-status') {
          receiptKey = JSON.parse(String(init?.body)).operation_key;
          return json({ status: 'recorded', stage: 'model', result: { model_id: model.id } });
        }
        if (init?.method === 'POST') {
          sent = JSON.parse(String(init.body));
          identity = new Headers(init.headers).get('Idempotency-Key');
          throw new TypeError('Failed to fetch');
        }
        return json({ ...model, ...sent, full_name: 'personal/test' });
      }),
    );
    const view = await editor();
    await view.user.type(screen.getByRole('textbox', { name: 'Prefix' }), 'personal');
    await view.user.type(screen.getByRole('textbox', { name: 'Model name' }), 'test');
    await view.user.click(screen.getByText('Managed session headers', { selector: 'strong' }));
    await view.user.click(screen.getByRole('button', { name: 'Add managed header' }));
    await view.user.type(screen.getByLabelText('Header 1 name'), 'x-session-id');
    await view.user.clear(screen.getByLabelText('Idle expiry (seconds)'));
    await view.user.type(screen.getByLabelText('Idle expiry (seconds)'), '0');
    await view.user.selectOptions(screen.getByLabelText('Random value format'), 'opencode_message');
    await view.user.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(view.saved).toHaveBeenCalled());
    expect(view.saved.mock.calls[0][0].session_headers).toEqual([
      { name: 'x-session-id', idle_seconds: 0, format: 'opencode_message' },
    ]);
    expect(receiptKey).toBe(identity);
  });
});
