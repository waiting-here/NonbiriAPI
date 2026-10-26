import { act, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { renderWithProviders, scanQueryClientForTokens } from '../../../../../test/unit/support';
import { CredentialCreateForm } from './CredentialForm';
import type { ConnectionResult } from './resourceOperation';
const mocks = vi.hoisted(() => ({ execute: vi.fn() }));
vi.mock('./resourceOperation', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./resourceOperation')>()),
  executeConnection: mocks.execute,
}));
const credential = {
  id: '8',
  connection_id: '2',
  note: 'Shared',
  enabled: true,
  revision: '1',
  credential_version: '1',
  display_head: 'head',
  display_tail: 'tail',
  created_at: 1700000000,
  updated_at: 1700000000,
};
describe('credential entry privacy', () => {
  it('clears the secret before outbound work and keeps only safe intent in the mutation cache', async () => {
    let finish!: (result: ConnectionResult) => void;
    mocks.execute.mockImplementation(
      () =>
        new Promise<ConnectionResult>((resolve) => {
          finish = resolve;
        }),
    );
    const onSaved = vi.fn();
    const view = await renderWithProviders(
      <CredentialCreateForm accountId="1" connectionId="2" onSaved={onSaved} />,
      { station: 'user', locale: 'en', role: 'user' },
    );
    view.queryClient.setQueryData(['user', 'session'], {
      user: { id: '1', username: 'reader', effective_level: 4 },
    });
    const token = 'synthetic-private-credential-123';
    const input = screen.getByLabelText('API key', { exact: true });
    await view.user.type(input, token);
    await view.user.click(
      screen.getByRole('checkbox', { name: 'I am authorized to use and grant this credential' }),
    );
    await view.user.click(screen.getByRole('button', { name: 'Save key' }));
    await waitFor(() => expect(mocks.execute).toHaveBeenCalledTimes(1));
    expect(input).toHaveValue('');
    expect(mocks.execute.mock.calls[0]?.[1]).toBe(token);
    const scan = scanQueryClientForTokens(view.queryClient, [token]);
    expect(scan.hitSurfaces).toEqual([]);
    expect(scan.overflow).toBe(false);
    const variables = view.queryClient
      .getMutationCache()
      .getAll()
      .map((row) => row.state.variables);
    expect(JSON.stringify(variables)).not.toContain(token);
    expect(variables[0]).toMatchObject({
      variables: {
        kind: 'credential',
        connectionId: '2',
        input: { ownership_confirmed: true },
      },
    });
    await act(async () => finish({ kind: 'credential', credential }));
    await waitFor(() => expect(onSaved).toHaveBeenCalledWith(credential));
  });
});
