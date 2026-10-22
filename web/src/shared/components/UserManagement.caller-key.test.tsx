import { act, screen, waitFor, within } from '@testing-library/react';
import { useSearchParams } from 'react-router';
import { afterEach, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { managedUserFixture } from '../../../test/fixtures/managedUsers';
import type { AdminUser, ManagementRole } from '@shared/operations/managedUsers';
import { UserManagement } from './UserManagement';

interface Call {
  path: string;
  method: string;
  body: Record<string, unknown>;
  headers: Headers;
  signal?: AbortSignal | null;
}
const metadata = (id = '7', generation = '1', active = true) => ({
  user_id: id,
  generation,
  active,
  created_at: active ? 1_800_000_000 : null,
  updated_at: 1_800_000_001,
});
const failure = (status: number) =>
  Response.json(
    { error: { code: status === 409 ? 'conflict' : 'service_unavailable', message: 'Try again' } },
    { status },
  );

function install(targets: AdminUser[], handle: (call: Call) => unknown | Promise<unknown>) {
  const calls: Call[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(input), window.location.origin);
      const call: Call = {
        path: url.pathname,
        method: init?.method ?? 'GET',
        body: init?.body ? (JSON.parse(String(init.body)) as Record<string, unknown>) : {},
        headers: new Headers(init?.headers),
        signal: init?.signal,
      };
      calls.push(call);
      let value: unknown;
      if (/\/users$/.test(call.path)) {
        value = {
          data: targets,
          next_cursor: null,
          pagination: {
            page: '1',
            page_size: 20,
            total_items: String(targets.length),
            total_pages: '1',
          },
        };
      } else if (/\/users\/\d+$/.test(call.path)) {
        value = targets.find((user) => call.path.endsWith('/' + user.id));
      } else {
        value = await handle(call);
      }
      return value instanceof Response ? value : Response.json(value);
    }),
  );
  return calls;
}

function Harness({
  role = 'admin',
  trainee = false,
}: {
  role?: ManagementRole;
  trainee?: boolean;
}) {
  const [, setSearch] = useSearchParams();
  return (
    <>
      <button onClick={() => setSearch({ user: '8' })}>Switch target</button>
      <UserManagement role={role} trainee={trainee} account="9" scopeReady sessionError={null} />
    </>
  );
}
async function renderManagement(role: ManagementRole = 'admin', trainee = false, id = '7') {
  const view = await renderWithProviders(<Harness role={role} trainee={trainee} />, {
    station: role === 'admin' ? 'admin' : 'user',
    role: role === 'admin' ? 'admin' : trainee ? 'level5' : 'level6',
    route: (role === 'admin' ? '/users' : '/steward') + '?user=' + id,
    locale: 'en',
  });
  view.queryClient.setQueryData(
    role === 'admin' ? ['admin', 'session'] : ['user', 'session'],
    role === 'admin'
      ? { admin: { username: 'fixture-admin' } }
      : { user: { id: '9', username: 'fixture-steward', effective_level: trainee ? 5 : 6 } },
  );
  return view;
}
async function confirmRevocation(view: Awaited<ReturnType<typeof renderManagement>>) {
  await view.user.click(await screen.findByRole('button', { name: 'Revoke site key' }));
  const dialog = screen.getByRole('alertdialog', { name: 'Revoke this site key?' });
  await view.user.click(within(dialog).getByRole('button', { name: 'Revoke site key' }));
}
afterEach(() => {
  vi.unstubAllGlobals();
  localStorage.clear();
});

it.each([
  { role: 'admin' as const, trainee: false, level: 6 },
  { role: 'steward' as const, trainee: false, level: 5 },
  { role: 'steward' as const, trainee: true, level: 4 },
])(
  'shows key management for $role trainee=$trainee target L$level',
  async ({ role, trainee, level }) => {
    const calls = install([managedUserFixture('7', level)], () => metadata());
    await renderManagement(role, trainee);
    expect(await screen.findByRole('button', { name: 'Revoke site key' })).toBeEnabled();
    expect(calls.filter((call) => call.path.endsWith('/caller-key'))).toHaveLength(1);
    expect(calls.find((call) => call.path.endsWith('/caller-key'))?.path).toBe(
      (role === 'admin' ? '/admin/api' : '/api/steward') + '/users/7/caller-key',
    );
  },
);

it.each([
  { trainee: true, id: '7', level: 5 },
  { trainee: true, id: '7', level: 6 },
  { trainee: false, id: '7', level: 6 },
  { trainee: false, id: '9', level: 4 },
])('does not read keys for a protected target $id/L$level', async ({ trainee, id, level }) => {
  const calls = install([managedUserFixture(id, level)], () => {
    throw new Error('Unexpected key read');
  });
  await renderManagement('steward', trainee, id);
  await screen.findByRole('heading', { name: 'member-' + id });
  expect(screen.queryByRole('heading', { name: 'Site key' })).toBeNull();
  expect(calls.some((call) => call.path.endsWith('/caller-key'))).toBe(false);
});

it('shows an empty key state without offering revocation', async () => {
  install([managedUserFixture()], () => metadata('7', '0', false));
  await renderManagement();
  await screen.findByText('No active site key. The user can create one from their account.');
  expect(screen.queryByRole('button', { name: 'Revoke site key' })).toBeNull();
});

it('cancels confirmation and then revokes only the selected key', async () => {
  let revoked = false;
  const calls = install([managedUserFixture()], (call) => {
    if (call.method === 'POST') {
      revoked = true;
      return { generation: '2', revoked: true };
    }
    return metadata('7', revoked ? '2' : '1', !revoked);
  });
  const view = await renderManagement();
  await view.user.click(await screen.findByRole('button', { name: 'Revoke site key' }));
  const dialog = screen.getByRole('alertdialog');
  expect(dialog).toHaveTextContent('Their account and sign-in remain available');
  await view.user.click(within(dialog).getByRole('button', { name: 'Cancel' }));
  expect(calls.filter((call) => call.method === 'POST')).toHaveLength(0);
  await confirmRevocation(view);
  await screen.findByText('The selected site key was revoked.');
  await screen.findByText('No active site key. The user can create one from their account.');
  const writes = calls.filter((call) => call.method === 'POST');
  expect(writes).toHaveLength(1);
  expect(writes[0].path).toBe('/admin/api/users/7/caller-key/revoke');
  expect(writes[0].body).toEqual({ expected_generation: '1' });
  expect(writes[0].headers.get('Idempotency-Key')).toMatch(/^[A-Za-z0-9_-]{22}$/);
});

it('requires an explicit reread and another confirmation after a key conflict', async () => {
  let generation = '1';
  const calls = install([managedUserFixture()], (call) => {
    if (call.method === 'POST') {
      generation = '2';
      return failure(409);
    }
    return metadata('7', generation);
  });
  const view = await renderManagement();
  await confirmRevocation(view);
  await screen.findByText(
    'The key has changed. Reload its status and check it before deciding whether to revoke it.',
  );
  expect(screen.getByRole('button', { name: 'Revoke site key' })).toBeDisabled();
  expect(calls.filter((call) => call.path.endsWith('/caller-key'))).toHaveLength(1);
  await view.user.click(screen.getByRole('button', { name: 'Load latest' }));
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Revoke site key' })).toBeEnabled(),
  );
  expect(calls.filter((call) => call.method === 'POST')).toHaveLength(1);
  await confirmRevocation(view);
  await waitFor(() => expect(calls.filter((call) => call.method === 'POST')).toHaveLength(2));
  const writes = calls.filter((call) => call.method === 'POST');
  expect(writes[1].body).toEqual({ expected_generation: '2' });
  expect(writes[1].headers.get('Idempotency-Key')).not.toBe(
    writes[0].headers.get('Idempotency-Key'),
  );
});

it('keeps a confirmed receipt when refreshing metadata fails and retries only the read', async () => {
  let reads = 0;
  const calls = install([managedUserFixture()], (call) => {
    if (call.method === 'POST') return { generation: '2', revoked: true };
    reads++;
    if (reads === 2) return failure(503);
    return metadata('7', reads === 1 ? '1' : '2', reads === 1);
  });
  const view = await renderManagement();
  await confirmRevocation(view);
  await screen.findByText('The selected site key was revoked.');
  await screen.findByText('Saved, but the latest version could not be loaded.');
  expect(screen.queryByRole('button', { name: 'Revoke site key' })).toBeNull();
  await view.user.click(screen.getByRole('button', { name: 'Check again' }));
  await screen.findByText('No active site key. The user can create one from their account.');
  expect(screen.getByText('The selected site key was revoked.')).toBeVisible();
  expect(calls.filter((call) => call.method === 'POST')).toHaveLength(1);
});

it('retries an uncertain result with the original request and never targets a newer key automatically', async () => {
  let writes = 0;
  const calls = install([managedUserFixture()], (call) => {
    if (call.method === 'POST') {
      writes++;
      if (writes === 1) throw new TypeError('Connection lost');
      return { generation: '2', revoked: true };
    }
    return metadata('7', writes > 0 ? '3' : '1');
  });
  const view = await renderManagement();
  await confirmRevocation(view);
  await screen.findByText("Couldn't confirm the save");
  expect(screen.getByRole('button', { name: 'Revoke site key' })).toBeDisabled();
  await view.user.click(screen.getByRole('button', { name: 'Check again' }));
  await screen.findByText('The selected site key was revoked.');
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Revoke site key' })).toBeEnabled(),
  );
  const requests = calls.filter((call) => call.method === 'POST');
  expect(requests).toHaveLength(2);
  expect(requests.map((call) => call.body)).toEqual([
    { expected_generation: '1' },
    { expected_generation: '1' },
  ]);
  expect(requests[0].headers.get('Idempotency-Key')).toBe(
    requests[1].headers.get('Idempotency-Key'),
  );
});

it.each(['read', 'confirm', 'write'] as const)(
  'discards the old target during %s',
  async (phase) => {
    let release!: (value: unknown) => void;
    const pending = new Promise<unknown>((resolve) => {
      release = resolve;
    });
    const calls = install([managedUserFixture('7'), managedUserFixture('8')], (call) => {
      if (call.path.includes('/users/8/')) return metadata('8', '0', false);
      if (call.method === 'POST') return pending;
      if (phase === 'read') return pending;
      return metadata();
    });
    const view = await renderManagement();
    if (phase === 'confirm') {
      await view.user.click(await screen.findByRole('button', { name: 'Revoke site key' }));
    } else if (phase === 'write') {
      await confirmRevocation(view);
      await waitFor(() => expect(calls.some((call) => call.method === 'POST')).toBe(true));
    } else {
      await waitFor(() =>
        expect(calls.some((call) => call.path.endsWith('/caller-key'))).toBe(true),
      );
    }
    await view.user.click(screen.getByRole('button', { name: 'Switch target' }));
    await screen.findByRole('heading', { name: 'member-8' });
    await screen.findByText('No active site key. The user can create one from their account.');
    await act(async () =>
      release(phase === 'read' ? metadata() : { generation: '2', revoked: true }),
    );
    expect(screen.queryByRole('alertdialog')).toBeNull();
    expect(screen.queryByText('The selected site key was revoked.')).toBeNull();
    expect(screen.queryByRole('button', { name: 'Revoke site key' })).toBeNull();
    expect(calls.some((call) => call.path === '/admin/api/users/8/caller-key/revoke')).toBe(false);
    const oldRequest = calls.find((call) =>
      phase === 'write' ? call.method === 'POST' : call.path === '/admin/api/users/7/caller-key',
    );
    if (phase !== 'confirm') expect(oldRequest?.signal?.aborted).toBe(true);
  },
);

it('closes a pending confirmation when the steward no longer outranks the target', async () => {
  const calls = install([managedUserFixture('7', 5)], () => metadata());
  const view = await renderManagement('steward');
  await view.user.click(await screen.findByRole('button', { name: 'Revoke site key' }));
  await act(async () => {
    view.queryClient.setQueryData(['user', 'session'], {
      user: { id: '9', username: 'fixture-steward', effective_level: 5 },
    });
  });
  view.rerender(<Harness role="steward" trainee />);
  expect(screen.queryByRole('alertdialog')).toBeNull();
  expect(screen.queryByRole('heading', { name: 'Site key' })).toBeNull();
  expect(calls.filter((call) => call.method === 'POST')).toHaveLength(0);
});
