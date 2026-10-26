import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../../test/unit/support';
import { CredentialGrantForm } from './CredentialGrantForm';
import type { Credential } from './types';
import common from '@shared/i18n/common/en.json';
import user from '../../../i18n/en.json';

const credential = {
  id: '8',
  connection_id: '9',
  note: 'Service key',
  enabled: true,
} as Credential;
const endpoint = JSON.parse(
  readFileSync(resolve('../internal/resources/testdata/endpoint.json'), 'utf8'),
);
const key = JSON.parse(
  readFileSync(resolve('../internal/resources/testdata/endpoint_key.json'), 'utf8'),
);
const json = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } });
vi.mock('./CredentialPicker', () => ({
  credentialLabel: (value: Credential) => value.note,
  CredentialPicker: ({
    connectionId,
    onPick,
  }: {
    connectionId: string;
    onPick: (value: Credential) => void;
  }) => <button onClick={() => onPick(credential)}>Pick service {connectionId} key</button>,
}));
vi.mock('./ServiceModelSetup', () => ({ ServiceModelSetup: () => <p>Pair model maintenance</p> }));

async function setup(lost = false) {
  const writes: { body: Record<string, unknown>; identity: string | null }[] = [];
  let checks = 0;
  vi.stubGlobal(
    'fetch',
    vi.fn<typeof fetch>(async (input, init) => {
      const url = new URL(String(input), 'https://app.example');
      if (url.pathname.endsWith('/keys') && init?.method === 'POST') {
        writes.push({
          body: JSON.parse(String(init.body)),
          identity: new Headers(init.headers).get('Idempotency-Key'),
        });
        if (lost) throw Error('Lost response');
        return json(key, 201);
      }
      if (url.pathname === '/api/resource-operation-status') {
        checks++;
        expect(JSON.parse(String(init?.body)).operation_key).toBe(writes[0]!.identity);
        return json(
          checks === 1
            ? { status: 'not_recorded' }
            : {
                status: 'recorded',
                stage: 'key',
                result: { endpoint_id: '11', endpoint_key_id: '21' },
              },
        );
      }
      if (url.pathname === '/api/endpoints/11/keys')
        return json({ data: [key], next_cursor: null });
      throw Error('Unexpected ' + url.pathname);
    }),
  );
  const view = await renderWithProviders(
    <CredentialGrantForm accountId="1" connectionId="9" endpoint={endpoint} />,
    { station: 'user', role: 'user', locale: 'en' },
  );
  view.queryClient.setQueryData(['user', 'session'], {
    user: { id: '1', username: 'fixture', effective_level: 4 },
  });
  return { ...view, writes };
}
describe('adding a service key', () => {
  it('saves a new secret in one submission and opens pair model maintenance', async () => {
    const view = await setup();
    await view.user.type(
      screen.getByLabelText(common.common.connections.secret),
      'synthetic-service-key',
    );
    await view.user.click(screen.getByRole('button', { name: user.user.services.addKeyAndFetch }));
    await screen.findByText('Pair model maintenance');
    expect(view.writes).toHaveLength(1);
    expect(view.writes[0]!.body).toMatchObject({
      secret: 'synthetic-service-key',
      ownership_confirmed: true,
    });
    expect(view.writes[0]!.body).not.toHaveProperty('discovery_source_id');
    expect(
      JSON.stringify(
        view.queryClient
          .getMutationCache()
          .getAll()
          .map((row) => row.state.variables),
      ),
    ).not.toContain('synthetic-service-key');
  });
  it('uses an existing key from the current service without another credential or catalog save', async () => {
    const view = await setup();
    await view.user.click(
      screen.getByRole('radio', { name: common.common.connections.existingCredential }),
    );
    await view.user.click(screen.getByRole('button', { name: 'Pick service 9 key' }));
    await view.user.click(screen.getByRole('button', { name: user.user.services.addKeyAndFetch }));
    await screen.findByText('Pair model maintenance');
    expect(view.writes[0]!.body).toMatchObject({ credential_id: '8' });
    expect(view.writes[0]!.body).not.toHaveProperty('secret');
    expect(view.writes[0]!.body).not.toHaveProperty('discovery_source_id');
  });
  it('checks a lost key response without submitting the secret a second time', async () => {
    const view = await setup(true);
    await view.user.type(
      screen.getByLabelText(common.common.connections.secret),
      'synthetic-lost-key',
    );
    await view.user.click(screen.getByRole('button', { name: user.user.services.addKeyAndFetch }));
    const check = await screen.findByRole('button', {
      name: common.common.connections.checkResult,
    });
    await view.user.click(check);
    await waitFor(() => expect(screen.getByText('Pair model maintenance')).toBeVisible());
    expect(view.writes).toHaveLength(1);
  });
});
