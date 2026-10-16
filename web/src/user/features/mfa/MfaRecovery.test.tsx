import { useState } from 'react';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { act, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import {
  beginManagementSessionRequest,
  noteManagementSessionSuccess,
} from '@shared/charityManagement';
import { apiFetch } from '@shared/query/http';
import { coreRawRequest } from '../core/request';
import { MfaRecovery } from './MfaRecovery';

const envelope = JSON.parse(
  readFileSync(resolve(process.cwd(), '../internal/auth/testdata/user_envelope.json'), 'utf8'),
);
const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'content-type': 'application/json' } });
const required = () =>
  json({ error: { code: 'mfa_required', message: 'Verify authenticator.' } }, 403);
const status = { enrolled: true, required: false, verified: false, verified_until: null };

function Draft({ core = false }: { core?: boolean }) {
  const [draft, setDraft] = useState('');
  return (
    <>
      <label>
        Draft
        <input value={draft} onChange={(event) => setDraft(event.target.value)} />
      </label>
      <button
        onClick={() => {
          void (
            core
              ? coreRawRequest('/api/draft', { method: 'POST', json: { draft } })
              : apiFetch('/api/draft', { method: 'POST', json: { draft } })
          ).catch(() => undefined);
        }}
      >
        Save draft
      </button>
    </>
  );
}
async function setup(core = false) {
  const view = await renderWithProviders(<span />, { station: 'user' });
  act(() => {
    noteManagementSessionSuccess(
      view.queryClient,
      'steward',
      envelope,
      beginManagementSessionRequest(view.queryClient, 'steward'),
    );
    view.queryClient.setQueryData(['user', 'session'], envelope);
  });
  view.rerender(
    <MfaRecovery>
      <Draft core={core} />
    </MfaRecovery>,
  );
  return view;
}

afterEach(() => vi.unstubAllGlobals());
describe('account MFA recovery', () => {
  it.each([false, true])(
    'keeps inputs and does not replay failed writes, core=%s',
    async (core) => {
      let writes = 0;
      vi.stubGlobal(
        'fetch',
        vi.fn(async (input: string) => {
          if (input === '/api/draft') {
            writes++;
            return required();
          }
          if (input.endsWith('/verify'))
            return json({ ...status, verified: true, verified_until: 1800000000 });
          if (input === '/api/session') return json(envelope);
          return json(status);
        }),
      );
      const view = await setup(core);
      await view.user.type(screen.getByLabelText('Draft'), 'Keep this draft');
      const original = screen.getByLabelText('Draft');
      await view.user.click(screen.getByRole('button', { name: 'Save draft' }));
      await screen.findByRole('alertdialog', { name: 'Verify your authenticator' });
      expect(screen.getByLabelText('Draft')).toBe(original);
      await view.user.type(await screen.findByLabelText('Authenticator code'), '123456');
      await view.user.click(screen.getByRole('button', { name: 'Verify and continue' }));
      await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument());
      expect(screen.getByLabelText('Draft')).toHaveValue('Keep this draft');
      expect(writes).toBe(1);
      await screen.findByText(
        'Verification complete. Review your input and submit the action again.',
      );
    },
  );

  it('ignores a late MFA failure from the previous account', async () => {
    let resolveResponse!: (value: Response) => void;
    vi.stubGlobal(
      'fetch',
      vi.fn(
        () =>
          new Promise<Response>((done) => {
            resolveResponse = done;
          }),
      ),
    );
    const view = await setup();
    await view.user.click(screen.getByRole('button', { name: 'Save draft' }));
    act(() => {
      const next = { user: { ...envelope.user, id: '876', username: 'next-fixture' } };
      noteManagementSessionSuccess(
        view.queryClient,
        'steward',
        next,
        beginManagementSessionRequest(view.queryClient, 'steward'),
      );
      view.queryClient.setQueryData(['user', 'session'], next);
    });
    await act(async () => {
      resolveResponse(required());
    });
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument();
  });
});
