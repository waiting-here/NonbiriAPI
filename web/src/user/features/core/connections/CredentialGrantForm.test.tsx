import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { assertNoSensitiveQueryCache, renderWithProviders } from '../../../../../test/unit/support';
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

async function setup(
  lost: boolean | 'retry' | 'expired' = false,
  connector = endpoint.connector_type,
) {
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
        if (lost && writes.length === 1) throw Error('Lost response');
        return json(key, 201);
      }
      if (url.pathname === '/api/resource-operation-status') {
        checks++;
        expect(JSON.parse(String(init?.body)).operation_key).toBe(writes[0]!.identity);
        return json(
          lost === 'expired'
            ? { status: 'expired' }
            : lost === 'retry' || checks === 1
              ? { status: 'not_recorded' }
              : {
                  status: 'recorded',
                  stage: 'key',
                  result: { endpoint_id: '11', endpoint_key_id: '21' },
                },
        );
      }
      if (url.pathname === '/api/endpoints/11/keys/21') return json(key);
      throw Error('Unexpected ' + url.pathname);
    }),
  );
  const view = await renderWithProviders(
    <CredentialGrantForm
      accountId="1"
      connectionId="9"
      endpoint={{ ...endpoint, connector_type: connector }}
    />,
    { station: 'user', role: 'user', locale: 'en' },
  );
  view.queryClient.setQueryData(['user', 'session'], {
    user: { id: '1', username: 'fixture', effective_level: 4 },
  });
  return { ...view, writes };
}
describe('adding a service key', () => {
  it('includes independent queue settings when adding a key', async () => {
    const view = await setup();
    await view.user.type(
      screen.getByLabelText(common.common.connections.secret),
      'synthetic-queued-key',
    );
    await view.user.click(screen.getByText(user.user.services.keySettings, { selector: 'strong' }));
    await view.user.selectOptions(
      screen.getByLabelText(common.common.keyLimits.rpmOverflow),
      'queue',
    );
    await view.user.clear(screen.getByLabelText(common.common.keyLimits.rpmQueueLimit));
    await view.user.type(screen.getByLabelText(common.common.keyLimits.rpmQueueLimit), '7');
    await view.user.click(screen.getByRole('button', { name: user.user.services.addKeyAndFetch }));
    await screen.findByText('Pair model maintenance');
    expect(view.writes[0]!.body).toMatchObject({ rpm_overflow: 'queue', rpm_queue_limit: 7 });
    expect(view.writes[0]!.body).not.toHaveProperty('concurrency_overflow');
  });
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
  it.each(['openai-compatible', 'openai-chat-completions', 'openai-responses'])(
    'only offers the Chat storage policy on supported connectors: %s',
    async (connector) => {
      const view = await setup(false, connector);
      await view.user.click(
        screen.getByText(user.user.services.keySettings, { selector: 'strong' }),
      );
      const toggle = screen.queryByLabelText(user.user.core['endpoints.storePolicy']);
      if (connector === 'openai-responses') expect(toggle).toBeNull();
      else expect(toggle).toBeVisible();
    },
  );

  it('rechecks and replays the frozen key input and identity with re-entered plaintext', async () => {
    const view = await setup('retry');
    const value = 'synthetic-missing-key';
    const original = screen.getByLabelText(common.common.connections.secret);
    await view.user.type(original, value);
    await view.user.click(screen.getByRole('button', { name: user.user.services.addKeyAndFetch }));
    const retrySecret = await screen.findByLabelText(user.user.quickstart.secretAgain);
    expect(original).toHaveValue('');
    expect(original).toBeDisabled();
    assertNoSensitiveQueryCache(view.queryClient, [value]);
    await view.user.type(retrySecret, value);
    await view.user.click(
      screen.getByRole('button', { name: common.common.connections.resumeUnrecorded }),
    );
    await screen.findByText('Pair model maintenance');
    expect(view.writes).toHaveLength(2);
    expect(view.writes[1]).toEqual(view.writes[0]);
    assertNoSensitiveQueryCache(view.queryClient, [value]);
  });

  it('keeps expired creates locked and offers the saved services directory', async () => {
    const view = await setup('expired');
    await view.user.type(
      screen.getByLabelText(common.common.connections.secret),
      'synthetic-expired-key',
    );
    await view.user.click(screen.getByRole('button', { name: user.user.services.addKeyAndFetch }));
    expect(
      await screen.findByRole('link', { name: user.user.quickstart.useExistingRecovery }),
    ).toHaveAttribute('href', '/endpoints');
    expect(screen.getByRole('button', { name: user.user.services.addKeyAndFetch })).toBeDisabled();
    expect(screen.queryByLabelText(user.user.quickstart.secretAgain)).toBeNull();
    expect(view.writes).toHaveLength(1);
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
