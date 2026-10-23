import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../../test/unit/support';
import { CredentialGrantForm } from './CredentialGrantForm';
import type { Credential } from './types';
import common from '@shared/i18n/common/en.json';

const credential: Credential = {
  id: '8',
  connection_id: '9',
  note: 'Shared credential',
  enabled: true,
  revision: '1',
  credential_version: '2',
  display_head: '',
  display_tail: '',
  created_at: 1,
  updated_at: 1,
};
const preset = { base_url: 'https://catalog.example/v1', list_format: 'openai' as const };
const source = {
  id: '17',
  connection_id: '9',
  name: 'Existing catalog',
  ...preset,
  credential_id: '8',
  credential_version: '2',
  revision: '1',
  discovery_source_revision: '1',
  models_url: 'https://catalog.example/v1/models',
  state: 'unknown',
  enabled: true,
  checked_at: null,
  safe_class: 'none',
  safe_diag: '',
  created_at: 1,
  updated_at: 1,
};
const endpoint = JSON.parse(
  readFileSync(resolve(process.cwd(), '../internal/resources/testdata/endpoint.json'), 'utf8'),
);
const key = JSON.parse(
  readFileSync(resolve(process.cwd(), '../internal/resources/testdata/endpoint_key.json'), 'utf8'),
);
const json = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } });
vi.mock('./CredentialPicker', () => ({
  credentialLabel: (value: Credential) => value.note,
  CredentialPicker: ({ onPick }: { onPick: (value: Credential) => void }) => (
    <div>
      <button type="button" onClick={() => onPick(credential)}>
        Pick version 2
      </button>
      <button type="button" onClick={() => onPick({ ...credential, credential_version: '3' })}>
        Pick version 3
      </button>
    </div>
  ),
}));

async function setup({
  suggested = true,
  existing = false,
  lost = false,
  keyFailure = false,
} = {}) {
  const catalogs = existing ? [source] : [];
  let currentVersion = '2';
  let failures = keyFailure ? 1 : 0;
  let receiptReads = 0;
  const sourceWrites: string[] = [];
  const keyWrites: Record<string, unknown>[] = [];
  const fetch = vi.fn<typeof globalThis.fetch>(async (input, init) => {
    const url = new URL(String(input), 'https://app.example');
    if (url.pathname === '/api/credentials/8')
      return json({ ...credential, credential_version: currentVersion });
    if (url.pathname === '/api/resource-operation-status') {
      receiptReads++;
      expect(JSON.parse(String(init?.body)).operation_key).toBe(sourceWrites[0]);
      return json(
        receiptReads === 1
          ? { status: 'not_recorded' }
          : {
              status: 'recorded',
              stage: 'discovery_source',
              result: { discovery_source_id: '17' },
            },
      );
    }
    if (url.pathname === '/api/discovery-sources/17') return json(source);
    if (url.pathname.endsWith('/discovery-sources')) {
      if (init?.method === 'POST') {
        sourceWrites.push(new Headers(init.headers).get('Idempotency-Key')!);
        expect(JSON.parse(String(init.body))).toEqual({
          ...preset,
          credential_id: '8',
          enabled: true,
        });
        const catalog = {
          ...source,
          id: String(17 + catalogs.length),
          credential_version: currentVersion,
        };
        catalogs.push(catalog);
        if (lost) throw Error('Lost response');
        return json(catalog, 201);
      }
      return json({
        data: catalogs,
        next_cursor: null,
        pagination: {
          page: '1',
          page_size: Number(url.searchParams.get('page_size')),
          total_items: String(catalogs.length),
          total_pages: '1',
        },
      });
    }
    if (url.pathname.endsWith('/keys') && init?.method === 'POST') {
      const body = JSON.parse(String(init.body));
      keyWrites.push(body);
      if (failures-- > 0)
        return json({ error: { code: 'conflict', message: 'Changed limits' } }, 409);
      return json(
        {
          ...key,
          credential_id: '8',
          credential_version: currentVersion,
          discovery_source_id: body.discovery_source_id,
        },
        201,
      );
    }
    throw Error('Unexpected ' + url.pathname);
  });
  vi.stubGlobal('fetch', fetch);
  const view = await renderWithProviders(
    <CredentialGrantForm
      accountId="1"
      connectionId="9"
      endpoint={endpoint}
      discoveryPreset={suggested ? preset : undefined}
    />,
    { station: 'user', locale: 'en', role: 'user' },
  );
  view.queryClient.setQueryData(['user', 'session'], {
    user: { id: '1', username: 'reader', effective_level: 4 },
  });
  await view.user.click(screen.getByRole('button', { name: 'Pick version 2' }));
  return {
    ...view,
    sourceWrites,
    keyWrites,
    fetch,
    rotate: () => {
      currentVersion = '3';
    },
  };
}

describe('granting a new mainstream entry', () => {
  it('defaults to the suggested catalog and creates it only when authorizing', async () => {
    const view = await setup();
    expect(
      screen.getByRole('checkbox', { name: common.common.connections.useSuggestedCatalog }),
    ).toBeChecked();
    expect(view.sourceWrites).toHaveLength(0);
    await view.user.click(
      screen.getByRole('button', { name: common.common.connections.authorizeEntry }),
    );
    await screen.findByText(common.common.connections.grantSaved);
    expect(view.sourceWrites).toHaveLength(1);
    expect(view.keyWrites[0]).toMatchObject({ credential_id: '8', discovery_source_id: '17' });
  });
  it('reuses an existing exact catalog instead of creating another', async () => {
    const view = await setup({ existing: true });
    await view.user.click(
      screen.getByRole('button', { name: common.common.connections.authorizeEntry }),
    );
    await screen.findByText(common.common.connections.grantSaved);
    expect(view.sourceWrites).toHaveLength(0);
    expect(view.keyWrites[0]?.discovery_source_id).toBe('17');
  });

  it('keeps an explicit opt-out when changing credentials and leaves custom entries unchanged', async () => {
    const view = await setup();
    await view.user.click(
      screen.getByRole('checkbox', { name: common.common.connections.useSuggestedCatalog }),
    );
    await view.user.click(
      screen.getByRole('button', { name: common.common.connections.changeCredential }),
    );
    await view.user.click(screen.getByRole('button', { name: 'Pick version 2' }));
    expect(
      screen.getByRole('checkbox', { name: common.common.connections.useSuggestedCatalog }),
    ).not.toBeChecked();
    await view.user.click(
      screen.getByRole('button', { name: common.common.connections.authorizeEntry }),
    );
    await screen.findByText(common.common.connections.grantSaved);
    expect(view.sourceWrites).toHaveLength(0);
    expect(view.keyWrites[0]?.discovery_source_id).toBeNull();
  });
  it('does not apply any catalog default when no suggestion is supplied', async () => {
    const view = await setup({ suggested: false });
    expect(
      screen.queryByRole('checkbox', { name: common.common.connections.useSuggestedCatalog }),
    ).not.toBeInTheDocument();
    await view.user.click(
      screen.getByRole('button', { name: common.common.connections.authorizeEntry }),
    );
    await screen.findByText(common.common.connections.grantSaved);
    expect(view.sourceWrites).toHaveLength(0);
    expect(view.keyWrites[0]?.discovery_source_id).toBeNull();
  });
  it('keeps an explicitly selected catalog instead of replacing it with the preset', async () => {
    const view = await setup({ existing: true });
    await view.user.click(
      screen.getByText(common.common.connections.discoveryOptional, { exact: true }),
    );
    await view.user.click(
      await screen.findByRole('button', { name: common.common.connections.useSource }),
    );
    expect(
      screen.getByRole('checkbox', { name: common.common.connections.useSuggestedCatalog }),
    ).not.toBeChecked();
    await view.user.click(
      screen.getByRole('button', { name: common.common.connections.authorizeEntry }),
    );
    await screen.findByText(common.common.connections.grantSaved);
    expect(view.sourceWrites).toHaveLength(0);
    expect(view.fetch.mock.calls.some(([url]) => String(url).startsWith('/api/credentials/'))).toBe(
      false,
    );
    expect(view.keyWrites[0]?.discovery_source_id).toBe('17');
  });
  it('requires a fresh credential selection after rotation and creates the catalog for that exact version', async () => {
    const view = await setup({ existing: true });
    view.rotate();
    const save = screen.getByRole('button', { name: common.common.connections.authorizeEntry });
    await view.user.click(save);
    await screen.findByRole('alert');
    expect(screen.getByText(common.common.connections.credentialChangedForCatalog)).toBeVisible();
    expect(view.sourceWrites).toHaveLength(0);
    expect(view.keyWrites).toHaveLength(0);
    await view.user.click(
      screen.getByRole('button', { name: common.common.connections.changeCredential }),
    );
    await view.user.click(screen.getByRole('button', { name: 'Pick version 3' }));
    await view.user.click(
      screen.getByRole('button', { name: common.common.connections.authorizeEntry }),
    );
    await screen.findByText(common.common.connections.grantSaved);
    expect(view.sourceWrites).toHaveLength(1);
    expect(view.keyWrites[0]?.discovery_source_id).toBe('18');
  });

  it('recovers a lost creation response with the original identity and never creates a duplicate', async () => {
    const view = await setup({ lost: true });
    const save = screen.getByRole('button', { name: common.common.connections.authorizeEntry });
    await view.user.click(save);
    const check = await screen.findByRole('button', {
      name: common.common.connections.checkResult,
    });
    expect(save).toBeDisabled();
    expect(view.keyWrites).toHaveLength(0);
    await view.user.click(check);
    await waitFor(() => expect(save).toBeEnabled());
    await view.user.click(save);
    await screen.findByText(common.common.connections.grantSaved);
    expect(view.sourceWrites).toHaveLength(1);
    expect(view.keyWrites).toHaveLength(1);
    expect(view.keyWrites[0]?.discovery_source_id).toBe('17');
  });
  it('reuses a successfully created catalog when the following authorization fails', async () => {
    const view = await setup({ keyFailure: true });
    const save = screen.getByRole('button', { name: common.common.connections.authorizeEntry });
    await view.user.click(save);
    await screen.findByRole('alert');
    await waitFor(() => expect(save).toBeEnabled());
    await view.user.click(save);
    await screen.findByText(common.common.connections.grantSaved);
    expect(view.sourceWrites).toHaveLength(1);
    expect(view.keyWrites).toHaveLength(2);
  });
});
