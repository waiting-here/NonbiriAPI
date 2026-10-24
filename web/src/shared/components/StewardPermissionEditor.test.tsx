import { screen, waitFor, fireEvent } from '@testing-library/react';
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

function installServer(loseFirstResponse = false) {
  let policy = defaultStewardPermissions(5);
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
    await user.click(screen.getByRole('button', { name: copy.all }));
    const amount = screen.getByRole('spinbutton', {
      name: copy.limits.users_balance_adjustment_max_milli,
    });
    fireEvent.change(amount, { target: { value: '12.345' } });
    await user.click(screen.getByRole('button', { name: copy.save }));
    await waitFor(() => expect(writes).toHaveLength(1));
    expect(writes[0].key).toBeTruthy();
    expect(writes[0].body).toMatchObject({
      expected_revision: '0',
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
