import { screen, waitFor, fireEvent, within } from '@testing-library/react';
import type { ReactNode } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { useAdminSession } from '../../admin/data';
import { renderWithProviders } from '../../../test/unit/support';
import en from '../i18n/common/en.json';
import {
  defaultStewardPermissions,
  type StewardPermissions,
} from '../operations/stewardPermissions';
import { StewardPermissionEditor } from './StewardPermissionEditor';

const copy = en.management.permissions;
const path = '/admin/api/users/51/steward-permissions';

function Session({ children }: { children: ReactNode }) {
  const session = useAdminSession();
  return session.data ? children : null;
}

function installServer(loseFirstResponse = false, initial = defaultStewardPermissions(5)) {
  let policy = initial;
  const writes: { key: string | null; body: Record<string, unknown> }[] = [];
  const response = (value: unknown) =>
    new Response(JSON.stringify(value), {
      headers: { 'Content-Type': 'application/json', 'Cache-Control': 'no-store' },
    });
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, options?: RequestInit) => {
      const url = new URL(input instanceof Request ? input.url : String(input), location.origin);
      if (url.pathname === '/admin/api/session')
        return response({ admin: { username: 'fixture-admin' } });
      if (url.pathname !== path) throw new Error(`Unexpected request: ${url.pathname}`);
      if (options?.method === 'PUT') {
        const body = JSON.parse(String(options.body)) as Record<string, unknown>;
        writes.push({ key: new Headers(options.headers).get('Idempotency-Key'), body });
        if (loseFirstResponse && writes.length === 1) throw new TypeError('Connection interrupted');
        policy = {
          revision: '1',
          donation_scope: body.donation_scope,
          permissions: body.permissions,
          limits: body.limits,
        } as StewardPermissions;
      }
      return response(policy);
    }),
  );
  return writes;
}

describe('steward permission editor', () => {
  it('browses by responsibility and preserves custom grants until an explicit preset change', async () => {
    const initial = defaultStewardPermissions(5);
    initial.permissions['users.caller_keys.read'] = false;
    initial.permissions['users.caller_keys.revoke'] = true;
    initial.permissions['ai.memory.edit'] = true;
    const writes = installServer(false, initial);
    const { user } = await renderWithProviders(
      <Session>
        <StewardPermissionEditor role="admin" account="fixture-admin" userId="51" />
      </Session>,
      { station: 'admin', locale: 'en' },
    );
    await user.click(
      await screen.findByRole('button', { name: new RegExp(copy.groups.safety.title) }),
    );
    expect(
      screen.queryByRole('region', { name: copy.groups.models.title }),
    ).not.toBeInTheDocument();
    const pair = screen.getByRole('radiogroup', { name: copy.pairs.callerKeys });
    expect(within(pair).getByRole('radio', { name: copy.modes.custom })).toBeChecked();
    expect(writes).toHaveLength(0);
    await user.click(within(pair).getByRole('radio', { name: copy.modes.read }));
    await user.click(screen.getByRole('button', { name: copy.save }));
    await waitFor(() => expect(writes).toHaveLength(1));
    expect(writes[0].body.permissions).toEqual({
      ...initial.permissions,
      'users.caller_keys.read': true,
      'users.caller_keys.revoke': false,
    });
    expect(writes[0].body.limits).toEqual(initial.limits);
    expect(writes[0].body.donation_scope).toBe(initial.donation_scope);
  });

  it('keeps presets as an editable draft and saves the exact fractional allowance', async () => {
    const writes = installServer();
    const { user } = await renderWithProviders(
      <Session>
        <StewardPermissionEditor role="admin" account="fixture-admin" userId="51" />
      </Session>,
      { station: 'admin', locale: 'en' },
    );
    await user.click(await screen.findByRole('button', { name: copy.none }));
    expect(writes).toHaveLength(0);
    expect(screen.getByRole('slider', { name: copy.scope.title })).toHaveValue('0');
    await user.click(screen.getByRole('button', { name: copy.all }));
    expect(screen.getByRole('slider', { name: copy.scope.title })).toHaveValue('3');
    const amount = screen.getByRole('spinbutton', {
      name: copy.limits.users_balance_adjustment_max_milli,
    });
    fireEvent.change(amount, { target: { value: '12.345' } });
    await user.click(screen.getByRole('button', { name: copy.save }));
    await waitFor(() => expect(writes).toHaveLength(1));
    expect(writes[0].key).toBeTruthy();
    expect(writes[0].body).toMatchObject({
      expected_revision: '0',
      donation_scope: 'full',
      permissions: { 'users.balance': true },
      limits: { 'users.balance_adjustment_max_milli': '12345' },
    });
    await screen.findByText(en.common.outcome.saved);
  });

  it('retains an uncertain write and retries the same intent with the same key', async () => {
    const writes = installServer(true);
    const { user } = await renderWithProviders(
      <Session>
        <StewardPermissionEditor role="admin" account="fixture-admin" userId="51" />
      </Session>,
      { station: 'admin', locale: 'en' },
    );
    await user.click(await screen.findByRole('button', { name: copy.none }));
    await user.click(screen.getByRole('button', { name: copy.save }));
    await screen.findByText(en.common.outcome.unknownTitle);
    expect(screen.getByRole('button', { name: copy.all })).toBeDisabled();
    await user.click(screen.getByRole('button', { name: en.common.outcome.recheck }));
    await waitFor(() => expect(writes).toHaveLength(2));
    expect(writes[1]).toEqual(writes[0]);
    await screen.findByText(en.common.outcome.saved);
  });
});
