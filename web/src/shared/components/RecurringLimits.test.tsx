import type { QueryClient } from '@tanstack/react-query';
import { fireEvent, screen, waitFor } from '@testing-library/react';
import type { ComponentProps, ReactNode } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { useAdminSession } from '../../admin/data';
import {
  beginManagementSessionRequest,
  noteManagementSessionSuccess,
} from '@shared/charityManagement';
import { recurringLimitsKeys } from '@shared/operations/recurringLimits';
import { renderWithProviders } from '../../../test/unit/support';
import { RecurringLimits } from './RecurringLimits';

const RULE_ID = `qlr_${'A'.repeat(21)}Q`;
const U128_MAX = ((1n << 128n) - 1n).toString();
const ZONES = {
  version: 'go1.26.6-zoneinfo',
  zones: ['America/Los_Angeles', 'Europe/Berlin', 'UTC'],
};

type RequestLog = { path: string; init?: RequestInit };

function jsonResponse(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), {
    status,
    headers: { 'content-type': 'application/json' },
  });
}

function rule(overrides: Record<string, unknown> = {}) {
  return {
    id: RULE_ID,
    mode: 'reset',
    interval: '5h',
    alignment: 'first_success',
    time_zone: 'UTC',
    week_starts_on: null,
    metric: 'calls',
    limit: '100',
    used: '90',
    reserved: '5',
    remaining: '5',
    state: 'limited',
    period_start: 1_800_000_000,
    period_end: 1_800_018_000,
    next_transition_at: 1_800_018_000,
    ...overrides,
  };
}

function response(
  overrides: { revision?: string; rules?: unknown[]; donationId?: string; keyId?: string } = {},
) {
  return {
    donation_id: overrides.donationId ?? '7',
    key_id: overrides.keyId ?? '8',
    donation_revision: overrides.revision ?? '9',
    server_now: 1_800_000_000,
    rules: overrides.rules ?? [rule()],
  };
}

type ReadResult = unknown | Response | Error;
type WriteResult = Response | Error;

function seedAdminSession(queryClient: QueryClient, username = 'fixture-admin') {
  const session = { admin: { username } } as const;
  const generation = beginManagementSessionRequest(queryClient, 'admin');
  if (!noteManagementSessionSuccess(queryClient, 'admin', session, generation)) {
    throw new Error('Could not seed the admin station session.');
  }
  queryClient.setQueryData(['admin', 'session'], session);
}

function AdminSessionFixture({ children }: { children: ReactNode }) {
  const session = useAdminSession();
  return session.data ? children : null;
}

function installQuotaFetch({
  role = 'admin',
  reads = [response()],
  zoneReads = [ZONES],
  writes = [jsonResponse({ donation_id: '7', key_id: '8', donation_revision: '10' })],
}: {
  role?: 'admin' | 'owner' | 'steward';
  reads?: ReadResult[];
  zoneReads?: ReadResult[];
  writes?: WriteResult[];
} = {}): { fetchMock: ReturnType<typeof vi.fn>; requests: RequestLog[] } {
  const base = role === 'admin' ? '/admin/api' : role === 'steward' ? '/api/steward' : '/api';
  const endpoint = `${base}/donations/7/keys/8/recurring-limits`;
  let readIndex = 0;
  let zoneReadIndex = 0;
  let writeIndex = 0;
  const requests: RequestLog[] = [];
  const fetchMock = vi.fn<typeof fetch>(async (input, init) => {
    const path = String(input);
    const method = init?.method ?? 'GET';
    requests.push({ path, init });
    if (path === '/admin/api/session' && method === 'GET') {
      return jsonResponse({ admin: { username: 'fixture-admin' } });
    }
    if (path === `${base}/time-zones` && method === 'GET') {
      const value = zoneReads[Math.min(zoneReadIndex++, zoneReads.length - 1)];
      if (value instanceof Response) return value;
      if (value instanceof Error) throw value;
      return jsonResponse(value);
    }
    if (path === endpoint && method === 'GET') {
      const value = reads[Math.min(readIndex++, reads.length - 1)];
      if (value instanceof Response) return value;
      if (value instanceof Error) throw value;
      return jsonResponse(value);
    }
    if (path === endpoint && method === 'PUT') {
      const result = writes[Math.min(writeIndex++, writes.length - 1)];
      if (result instanceof Error) throw result;
      return result;
    }
    throw new Error(`Unexpected request: ${method} ${path}`);
  });
  vi.stubGlobal('fetch', fetchMock);
  return { fetchMock, requests };
}

function putRequests(requests: readonly RequestLog[]): RequestLog[] {
  return requests.filter(({ init }) => init?.method === 'PUT');
}

function getDetailRequests(requests: readonly RequestLog[]): RequestLog[] {
  return requests.filter(
    ({ path, init }) => path.endsWith('/recurring-limits') && (init?.method ?? 'GET') === 'GET',
  );
}

async function renderAdmin(
  props: Partial<ComponentProps<typeof RecurringLimits>> = {},
  options: { locale?: 'en' | 'zh' } = {},
) {
  const rendered = await renderWithProviders(
    <AdminSessionFixture>
      <RecurringLimits role="admin" donationId="7" keyId="8" accountId="account-a" {...props} />
    </AdminSessionFixture>,
    { station: 'admin', role: 'admin', locale: options.locale ?? 'en' },
  );
  return rendered;
}

afterEach(() => vi.unstubAllGlobals());

describe('RecurringLimits', () => {
  it('renders U128 usage and limit strings without numeric truncation', async () => {
    installQuotaFetch({
      role: 'owner',
      reads: [
        response({
          rules: [
            rule({
              limit: U128_MAX,
              used: U128_MAX,
              reserved: '0',
              remaining: '0',
            }),
          ],
        }),
      ],
    });

    await renderWithProviders(
      <RecurringLimits role="owner" donationId="7" keyId="8" accountId="account-a" />,
      { station: 'user', role: 'user', locale: 'en' },
    );

    await screen.findByRole('heading', { name: 'Recurring charity limits' });
    await waitFor(() => expect(screen.getAllByTitle(U128_MAX)).toHaveLength(2));
    expect(
      screen.getAllByText(`${U128_MAX.replace(/\B(?=(\d{3})+(?!\d))/g, ',')} calls`),
    ).toHaveLength(2);
    expect(screen.queryByRole('button', { name: 'Save recurring limits' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Move up' })).not.toBeInTheDocument();
  });

  it('keeps owner views read-only and selects the Chinese copy when requested', async () => {
    installQuotaFetch({ role: 'owner' });

    await renderWithProviders(
      <RecurringLimits role="owner" donationId="7" keyId="8" accountId="account-a" />,
      { station: 'user', role: 'user', locale: 'zh' },
    );

    expect(await screen.findByRole('heading', { name: '公益循环限量' })).toBeVisible();
    expect(screen.getByText('当前密钥所有者只能查看。')).toBeVisible();
    expect(screen.getByText(/上限: 100次/)).toBeVisible();
    expect(screen.getByText(/已用: 90次/)).toBeVisible();
    expect(screen.getByText(/在途预留: 5次/)).toBeVisible();
    expect(screen.getByText(/剩余: 5次/)).toBeVisible();
    expect(screen.getByText(/此周期从所选时区内首次成功公益调用开始/)).toBeVisible();
    expect(
      screen.queryByText('所选时区决定此规则的业务时间；同时显示浏览器当地时间供参考。'),
    ).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '保存循环限量' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '添加循环规则' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '删除规则' })).not.toBeInTheDocument();
  });

  it('keeps terminal or expired admin views on the role-specific read path', async () => {
    const { requests } = installQuotaFetch();

    await renderAdmin({ readOnly: true });

    expect(await screen.findByRole('heading', { name: 'Recurring charity limits' })).toBeVisible();
    expect(
      screen.getByText(
        'This donation key is terminal or expired; recurring limits can no longer be changed.',
      ),
    ).toBeVisible();
    expect(screen.queryByRole('button', { name: 'Save recurring limits' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Add recurring rule' })).not.toBeInTheDocument();
    expect(getDetailRequests(requests)).toHaveLength(1);
    expect(getDetailRequests(requests)[0]?.path).toBe(
      '/admin/api/donations/7/keys/8/recurring-limits',
    );
    expect(requests.some(({ path }) => path.endsWith('/time-zones'))).toBe(false);
  });

  it('labels unsaved rules and empty comparison collections clearly', async () => {
    installQuotaFetch({
      reads: [response({ rules: [] }), response({ revision: '10', rules: [] })],
      writes: [jsonResponse({ error: { code: 'conflict', message: 'stale revision' } }, 409)],
    });
    const rendered = await renderAdmin();
    await screen.findByRole('heading', { name: 'Recurring charity limits' });
    await rendered.user.click(screen.getByRole('button', { name: 'Add recurring rule' }));
    expect(screen.getByText('Unsaved new rule')).toBeVisible();
    const timeZone = screen.getByLabelText('Time zone');
    await rendered.user.clear(timeZone);
    await rendered.user.type(timeZone, 'UTC');
    await rendered.user.type(screen.getByLabelText('Limit'), '100');
    await rendered.user.click(screen.getByRole('button', { name: 'Save recurring limits' }));

    expect(await screen.findByText('The rules changed while you were editing.')).toBeVisible();
    expect(screen.getByText('No recurring rules are configured for this key.')).toBeVisible();
    expect(screen.getAllByText(/Unsaved new rule/).length).toBeGreaterThan(0);
  });

  it('uses the actual calendar alignment copy and marks unsupported offsets explicitly', async () => {
    installQuotaFetch({
      role: 'owner',
      reads: [
        response({
          rules: [
            rule({
              interval: 'day',
              alignment: 'calendar',
              time_zone: 'Mars/Phobos',
              week_starts_on: null,
            }),
          ],
        }),
      ],
    });

    await renderWithProviders(
      <RecurringLimits role="owner" donationId="7" keyId="8" accountId="account-a" />,
      { station: 'user', role: 'user', locale: 'en' },
    );

    expect(await screen.findByRole('heading', { name: 'Recurring charity limits' })).toBeVisible();
    expect(
      screen.getByText(/The selected time zone defines this rule's business time/),
    ).toBeVisible();
    expect(
      screen.queryByText(/This period starts at the first successful charity call/),
    ).not.toBeInTheDocument();
    expect(screen.getAllByText(/offset unavailable/).length).toBeGreaterThan(0);
  });

  it('shows the read error and retry when the initial single-key GET fails', async () => {
    installQuotaFetch({
      reads: [
        jsonResponse({ error: { code: 'internal', message: 'temporary read failure' } }, 500),
        response(),
      ],
    });

    const rendered = await renderAdmin();
    expect(await screen.findByRole('heading', { name: 'Something went wrong' })).toBeVisible();
    expect(screen.getByRole('button', { name: 'Retry' })).toBeEnabled();
    await rendered.user.click(screen.getByRole('button', { name: 'Retry' }));
    expect(await screen.findByRole('heading', { name: 'Recurring charity limits' })).toBeVisible();
    expect(await screen.findByLabelText('Limit')).toHaveValue('100');
  });

  it('keeps the access-loss state visible after an owner GET revokes access', async () => {
    installQuotaFetch({
      role: 'owner',
      reads: [jsonResponse({ error: { code: 'unauthorized', message: 'signed out' } }, 401)],
    });

    await renderWithProviders(
      <RecurringLimits role="owner" donationId="7" keyId="8" accountId="account-a" />,
      { station: 'user', role: 'user', locale: 'en' },
    );

    expect(
      await screen.findByText(
        'Your session no longer has access to these recurring limits. Sign in again to continue.',
      ),
    ).toBeVisible();
  });

  it('offers a real retry when the editable time-zone registry GET fails', async () => {
    const { requests } = installQuotaFetch({
      zoneReads: [new Error('time-zone read failed'), ZONES],
    });
    const rendered = await renderAdmin();
    await screen.findByRole('heading', { name: 'Recurring charity limits' });
    expect(
      await screen.findByText('The time-zone registry is unavailable. Retry before saving.'),
    ).toBeVisible();
    await rendered.user.click(screen.getByRole('button', { name: 'Retry time-zone registry' }));
    await waitFor(() =>
      expect(
        screen.queryByText('The time-zone registry is unavailable. Retry before saving.'),
      ).not.toBeInTheDocument(),
    );
    expect(requests.filter(({ path }) => path === '/admin/api/time-zones')).toHaveLength(2);
  });

  it('shows only legal mode combinations and the structural impact before saving', async () => {
    installQuotaFetch();
    const rendered = await renderAdmin();
    await screen.findByRole('heading', { name: 'Recurring charity limits' });
    const mode = await screen.findByLabelText('Mode');
    const interval = screen.getByLabelText('Period');

    await rendered.user.selectOptions(mode, 'sliding');
    expect(screen.queryByLabelText('Starts at')).not.toBeInTheDocument();
    expect(
      screen.getByText(/Rule 1 changes structure and starts counting again from this save\./),
    ).toBeVisible();

    await rendered.user.selectOptions(mode, 'reset');
    await rendered.user.selectOptions(interval, 'week');
    await rendered.user.selectOptions(screen.getByLabelText('Starts at'), 'calendar');
    expect(screen.getByLabelText('Week starts on')).toHaveValue('1');
    expect(
      screen.getByText(/Rule 1 changes structure and starts counting again from this save\./),
    ).toBeVisible();
    expect(screen.getByRole('button', { name: 'Save recurring limits' })).toBeEnabled();

    const limit = screen.getByLabelText('Limit');
    await rendered.user.clear(limit);
    await rendered.user.type(limit, '150');
    expect(
      screen.getByText('Changing a limit keeps recorded usage and in-flight reservations.'),
    ).toBeVisible();
    expect(
      screen.getByText(/Calls already sent finish under the rules captured when sent\./),
    ).toBeVisible();
  });

  it('retains the exact payload and idempotency key for an unknown-result retry', async () => {
    const { requests } = installQuotaFetch({
      reads: [response(), response({ revision: '10', rules: [rule({ limit: '101' })] })],
      writes: [
        new Error('response lost'),
        jsonResponse({ donation_id: '7', key_id: '8', donation_revision: '10' }),
      ],
    });
    const rendered = await renderAdmin();
    await screen.findByRole('heading', { name: 'Recurring charity limits' });
    const limit = await screen.findByLabelText('Limit');
    await rendered.user.clear(limit);
    await rendered.user.type(limit, '101');
    await rendered.user.click(screen.getByRole('button', { name: 'Save recurring limits' }));

    expect(await screen.findByText('The save result is unknown.')).toBeVisible();
    expect(getDetailRequests(requests)).toHaveLength(1);
    expect(screen.getByLabelText('Limit')).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Save recurring limits' })).toBeDisabled();
    fireEvent.submit(
      screen.getByRole('button', { name: 'Save recurring limits' }).closest('form')!,
    );
    expect(putRequests(requests)).toHaveLength(1);
    await rendered.user.click(screen.getByRole('button', { name: 'Retry the same save' }));
    await waitFor(() => expect(screen.getByText('Recurring limits saved.')).toBeVisible());

    const puts = putRequests(requests);
    expect(puts).toHaveLength(2);
    expect(new Headers(puts[0]?.init?.headers).get('Idempotency-Key')).toMatch(
      /^[A-Za-z0-9_-]{22}$/,
    );
    expect(new Headers(puts[1]?.init?.headers).get('Idempotency-Key')).toBe(
      new Headers(puts[0]?.init?.headers).get('Idempotency-Key'),
    );
    expect(puts.map(({ init }) => init?.body)).toEqual([
      JSON.stringify({
        expected_revision: '9',
        rules: [
          {
            id: RULE_ID,
            mode: 'reset',
            interval: '5h',
            alignment: 'first_success',
            time_zone: 'UTC',
            week_starts_on: null,
            metric: 'calls',
            limit: '101',
          },
        ],
      }),
      JSON.stringify({
        expected_revision: '9',
        rules: [
          {
            id: RULE_ID,
            mode: 'reset',
            interval: '5h',
            alignment: 'first_success',
            time_zone: 'UTC',
            week_starts_on: null,
            metric: 'calls',
            limit: '101',
          },
        ],
      }),
    ]);
  });

  it('keeps the success fact while requiring authority resynchronization after a GET failure', async () => {
    const { requests } = installQuotaFetch({
      reads: [
        response(),
        jsonResponse({ error: { code: 'internal', message: 'temporary read failure' } }, 502),
        response({ revision: '10', rules: [rule({ limit: '101' })] }),
        response({ revision: '11', rules: [rule({ limit: '102' })] }),
      ],
      writes: [
        jsonResponse({ donation_id: '7', key_id: '8', donation_revision: '10' }),
        jsonResponse({ donation_id: '7', key_id: '8', donation_revision: '11' }),
      ],
    });
    const onSaved = vi.fn();
    const rendered = await renderAdmin({ onSaved });
    await screen.findByRole('heading', { name: 'Recurring charity limits' });
    const limit = await screen.findByLabelText('Limit');
    await rendered.user.clear(limit);
    await rendered.user.type(limit, '101');
    await rendered.user.click(screen.getByRole('button', { name: 'Save recurring limits' }));

    expect(await screen.findByText('Recurring limits saved.')).toBeVisible();
    expect(await screen.findByText(/latest server state could not be confirmed/)).toBeVisible();
    expect(screen.getByRole('button', { name: 'Save recurring limits' })).toBeDisabled();
    expect(putRequests(requests)).toHaveLength(1);
    expect(getDetailRequests(requests)).toHaveLength(2);
    expect(onSaved).toHaveBeenCalledTimes(1);

    await rendered.user.click(screen.getByRole('button', { name: 'Reload current server rules' }));
    await waitFor(() => expect(getDetailRequests(requests)).toHaveLength(3));
    await waitFor(() => expect(screen.getByLabelText('Limit')).toHaveValue('101'));
    expect(
      screen.queryByText(/latest server state could not be confirmed/),
    ).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Save recurring limits' })).toBeDisabled();

    await rendered.user.clear(screen.getByLabelText('Limit'));
    await rendered.user.type(screen.getByLabelText('Limit'), '102');
    expect(screen.getByRole('button', { name: 'Save recurring limits' })).toBeEnabled();
    await rendered.user.click(screen.getByRole('button', { name: 'Save recurring limits' }));
    await waitFor(() => expect(putRequests(requests)).toHaveLength(2));
    expect(getDetailRequests(requests)).toHaveLength(4);
    expect(onSaved).toHaveBeenCalledTimes(2);
  });

  it('loads authority on 409 while preserving the local draft for review', async () => {
    const { requests } = installQuotaFetch({
      reads: [
        response(),
        response({
          revision: '10',
          rules: [
            rule({
              limit: '140',
              interval: 'week',
              alignment: 'calendar',
              time_zone: 'Europe/Berlin',
              week_starts_on: 3,
            }),
          ],
        }),
      ],
      writes: [jsonResponse({ error: { code: 'conflict', message: 'stale revision' } }, 409)],
    });
    const rendered = await renderAdmin();
    await screen.findByRole('heading', { name: 'Recurring charity limits' });
    const limit = await screen.findByLabelText('Limit');
    await rendered.user.clear(limit);
    await rendered.user.type(limit, '120');
    await rendered.user.click(screen.getByRole('button', { name: 'Save recurring limits' }));

    expect(await screen.findByText('The rules changed while you were editing.')).toBeVisible();
    expect(screen.getAllByText(/140 calls/).length).toBeGreaterThan(0);
    expect(screen.getAllByText(/120 calls/).length).toBeGreaterThan(0);
    expect(screen.getByText(/Starts at: Calendar boundary/)).toBeVisible();
    expect(screen.getByText(/Time zone: Europe\/Berlin/)).toBeVisible();
    expect(screen.getByText(/Week starts on: Wednesday/)).toBeVisible();
    expect(limit).toHaveValue('120');
    expect(getDetailRequests(requests)).toHaveLength(2);
    expect(putRequests(requests)).toHaveLength(1);
    expect(screen.getByRole('button', { name: 'Save recurring limits' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Discard changes' })).toBeDisabled();
    fireEvent.submit(
      screen.getByRole('button', { name: 'Save recurring limits' }).closest('form')!,
    );
    expect(putRequests(requests)).toHaveLength(1);

    await rendered.user.click(screen.getByRole('button', { name: 'Keep my draft' }));
    expect(screen.queryByText('The rules changed while you were editing.')).not.toBeInTheDocument();
    expect(screen.getByLabelText('Limit')).toHaveValue('120');
    expect(screen.getByRole('button', { name: 'Save recurring limits' })).toBeEnabled();
  });

  it('anchors a kept draft to the latest authority revision', async () => {
    installQuotaFetch({
      reads: [response(), response({ revision: '10', rules: [rule({ limit: '140' })] })],
      writes: [jsonResponse({ error: { code: 'conflict', message: 'stale revision' } }, 409)],
    });
    const rendered = await renderAdmin();
    await screen.findByRole('heading', { name: 'Recurring charity limits' });
    const limit = await screen.findByLabelText('Limit');
    await rendered.user.clear(limit);
    await rendered.user.type(limit, '120');
    await rendered.user.click(screen.getByRole('button', { name: 'Save recurring limits' }));
    await screen.findByText('The rules changed while you were editing.');
    await rendered.user.click(screen.getByRole('button', { name: 'Keep my draft' }));

    await rendered.user.clear(screen.getByLabelText('Limit'));
    await rendered.user.type(screen.getByLabelText('Limit'), '140');
    expect(screen.getByRole('button', { name: 'Save recurring limits' })).toBeDisabled();
  });

  it('can discard a normal draft and restores the current baseline', async () => {
    installQuotaFetch();
    const rendered = await renderAdmin();
    await screen.findByRole('heading', { name: 'Recurring charity limits' });
    const limit = await screen.findByLabelText('Limit');
    await rendered.user.clear(limit);
    await rendered.user.type(limit, '120');
    expect(screen.getByRole('button', { name: 'Discard changes' })).toBeEnabled();
    await rendered.user.click(screen.getByRole('button', { name: 'Discard changes' }));

    expect(screen.getByLabelText('Limit')).toHaveValue('100');
    expect(screen.getByRole('button', { name: 'Save recurring limits' })).toBeDisabled();
    expect(screen.queryByRole('button', { name: 'Discard changes' })).not.toBeInTheDocument();
  });

  it('blocks saving until a failed conflict read is retried and explicitly resolved', async () => {
    const { requests } = installQuotaFetch({
      reads: [
        response(),
        jsonResponse({ error: { code: 'internal', message: 'temporary read failure' } }, 500),
        response({ revision: '10', rules: [rule({ limit: '140' })] }),
      ],
      writes: [jsonResponse({ error: { code: 'conflict', message: 'stale revision' } }, 409)],
    });
    const rendered = await renderAdmin();
    await screen.findByRole('heading', { name: 'Recurring charity limits' });
    const limit = await screen.findByLabelText('Limit');
    await rendered.user.clear(limit);
    await rendered.user.type(limit, '120');
    await rendered.user.click(screen.getByRole('button', { name: 'Save recurring limits' }));

    expect(await screen.findByText(/The latest server rules could not be loaded/)).toBeVisible();
    expect(screen.getByRole('button', { name: 'Save recurring limits' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Reload current server rules' })).toBeEnabled();
    fireEvent.submit(
      screen.getByRole('button', { name: 'Save recurring limits' }).closest('form')!,
    );
    expect(putRequests(requests)).toHaveLength(1);

    await rendered.user.click(screen.getByRole('button', { name: 'Reload current server rules' }));
    expect(await screen.findByText('The rules changed while you were editing.')).toBeVisible();
    expect(screen.getByRole('button', { name: 'Save recurring limits' })).toBeDisabled();
    expect(getDetailRequests(requests)).toHaveLength(3);
    await rendered.user.click(screen.getByRole('button', { name: 'Keep my draft' }));
    expect(screen.getByRole('button', { name: 'Save recurring limits' })).toBeEnabled();
    expect(screen.getByLabelText('Limit')).toHaveValue('120');
  });

  it('isolates cached data and draft state when the account scope changes', async () => {
    const { requests } = installQuotaFetch({
      reads: [
        response({ rules: [rule({ limit: '100' })] }),
        response({ rules: [rule({ limit: '200' })] }),
      ],
    });
    const rendered = await renderAdmin();
    await screen.findByRole('heading', { name: 'Recurring charity limits' });
    await waitFor(() => expect(screen.getByLabelText('Limit')).toHaveValue('100'));
    const oldKey = recurringLimitsKeys.detail('admin', 'account-a', '7', '8');
    expect(rendered.queryClient.getQueryData(oldKey)).toBeDefined();

    seedAdminSession(rendered.queryClient, 'fixture-admin-b');
    rendered.rerender(
      <RecurringLimits role="admin" donationId="7" keyId="8" accountId="account-b" />,
    );
    await waitFor(() => expect(screen.getByLabelText('Limit')).toHaveValue('200'));
    expect(rendered.queryClient.getQueryData(oldKey)).toBeUndefined();
    expect(getDetailRequests(requests)).toHaveLength(2);
  });

  it('does not let a late GET from the previous account replace the new draft', async () => {
    const endpoint = '/admin/api/donations/7/keys/8/recurring-limits';
    let resolveAccountA!: (value: Response) => void;
    let resolveAccountB!: (value: Response) => void;
    const accountA = new Promise<Response>((resolve) => {
      resolveAccountA = resolve;
    });
    const accountB = new Promise<Response>((resolve) => {
      resolveAccountB = resolve;
    });
    let detailCalls = 0;
    const fetchMock = vi.fn<typeof fetch>(async (input, init) => {
      const path = String(input);
      const method = init?.method ?? 'GET';
      if (path === '/admin/api/session' && method === 'GET') {
        return jsonResponse({ admin: { username: 'fixture-admin' } });
      }
      if (path === '/admin/api/time-zones' && method === 'GET') return jsonResponse(ZONES);
      if (path === endpoint && method === 'GET') {
        detailCalls += 1;
        return detailCalls === 1 ? accountA : accountB;
      }
      throw new Error(`Unexpected request: ${method} ${path}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    const rendered = await renderAdmin();
    await waitFor(() => expect(detailCalls).toBe(1));
    seedAdminSession(rendered.queryClient, 'fixture-admin-b');
    rendered.rerender(
      <RecurringLimits role="admin" donationId="7" keyId="8" accountId="account-b" />,
    );
    await waitFor(() => expect(detailCalls).toBe(2));
    resolveAccountA(jsonResponse(response({ rules: [rule({ limit: '111' })] })));
    resolveAccountB(jsonResponse(response({ rules: [rule({ limit: '222' })] })));

    await waitFor(() => expect(screen.getByLabelText('Limit')).toHaveValue('222'));
    expect(screen.getByLabelText('Limit')).not.toHaveValue('111');
  });

  it('ignores late reconcile and saved callbacks after switching account scope', async () => {
    const endpoint = '/admin/api/donations/7/keys/8/recurring-limits';
    let resolveWrite!: (value: Response) => void;
    const pendingWrite = new Promise<Response>((resolve) => {
      resolveWrite = resolve;
    });
    let detailCalls = 0;
    let writeCalls = 0;
    const requests: RequestLog[] = [];
    const fetchMock = vi.fn<typeof fetch>(async (input, init) => {
      const path = String(input);
      const method = init?.method ?? 'GET';
      requests.push({ path, init });
      if (path === '/admin/api/session' && method === 'GET') {
        return jsonResponse({ admin: { username: 'fixture-admin' } });
      }
      if (path === '/admin/api/time-zones' && method === 'GET') return jsonResponse(ZONES);
      if (path === endpoint && method === 'GET') {
        detailCalls += 1;
        if (detailCalls === 1) return jsonResponse(response());
        if (detailCalls === 2) return jsonResponse(response({ rules: [rule({ limit: '200' })] }));
        return jsonResponse(response({ rules: [rule({ limit: '333' })] }));
      }
      if (path === endpoint && method === 'PUT') {
        writeCalls += 1;
        return pendingWrite;
      }
      throw new Error(`Unexpected request: ${method} ${path}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const onSavedA = vi.fn();
    const onSavedB = vi.fn();

    const rendered = await renderAdmin({ onSaved: onSavedA });
    await screen.findByRole('heading', { name: 'Recurring charity limits' });
    const oldLimit = await screen.findByLabelText('Limit');
    await rendered.user.clear(oldLimit);
    await rendered.user.type(oldLimit, '101');
    await rendered.user.click(screen.getByRole('button', { name: 'Save recurring limits' }));
    await waitFor(() => expect(writeCalls).toBe(1));

    seedAdminSession(rendered.queryClient, 'fixture-admin-b');
    rendered.rerender(
      <RecurringLimits
        role="admin"
        donationId="7"
        keyId="8"
        accountId="account-b"
        onSaved={onSavedB}
      />,
    );
    await waitFor(() => expect(screen.getByLabelText('Limit')).toHaveValue('200'));
    const newLimit = screen.getByLabelText('Limit');
    await rendered.user.clear(newLimit);
    await rendered.user.type(newLimit, '201');

    resolveWrite(jsonResponse({ donation_id: '7', key_id: '8', donation_revision: '10' }));
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(screen.getByLabelText('Limit')).toHaveValue('201');
    expect(detailCalls).toBe(2);
    expect(onSavedA).not.toHaveBeenCalled();
    expect(onSavedB).not.toHaveBeenCalled();
  });

  it('keeps malformed numeric drafts out of the request path', async () => {
    const { requests } = installQuotaFetch();
    const rendered = await renderAdmin();
    await screen.findByRole('heading', { name: 'Recurring charity limits' });
    const limit = await screen.findByLabelText('Limit');
    await rendered.user.clear(limit);
    await rendered.user.type(limit, '001');
    expect(screen.getByText(/canonical non-negative integer/)).toBeVisible();
    expect(screen.getByRole('button', { name: 'Save recurring limits' })).toBeDisabled();
    fireEvent.submit(
      screen.getByRole('button', { name: 'Save recurring limits' }).closest('form')!,
    );
    expect(putRequests(requests)).toHaveLength(0);
  });
});
