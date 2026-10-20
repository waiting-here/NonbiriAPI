import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { EndpointPolicies } from './EndpointPolicies';
import type { EndpointBackoffPolicy } from '@shared/operations/endpointPolicies';
vi.mock('./AsyncLeaveDialog', () => ({ AsyncLeaveDialog: () => null }));
const identity = { connector_type: 'openai-compatible', base_url: 'https://provider.example/v1' };
const initial: EndpointBackoffPolicy = {
  ...identity,
  revision: '2',
  rules: [{ kind: 'code', value: 'quota_empty', period: 'auto' }],
};
const json = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), { status, headers: { 'content-type': 'application/json' } });
function fixture(
  write?: (body: Record<string, unknown>, init: RequestInit) => Promise<Response>,
  readDetail?: (url: URL, init?: RequestInit) => Promise<Response>,
) {
  const requests: string[] = [];
  const writes: RequestInit[] = [];
  let policy = initial;
  let failList = false;
  let routeCount = 1;
  vi.stubGlobal(
    'fetch',
    vi.fn<typeof fetch>(async (input, init) => {
      const url = new URL(String(input), 'https://example.test');
      requests.push(url.pathname + url.search);
      if (init?.method === 'PUT') {
        writes.push(init);
        if (write) return write(JSON.parse(String(init.body)), init);
        policy = { ...policy, ...JSON.parse(String(init.body)), revision: '3' };
        return json(policy);
      }
      if (url.pathname.endsWith('/endpoint-backoff-options'))
        return json({ connector_types: ['openai-compatible', 'google-gemini'] });
      if (url.pathname.endsWith('/egress/cloudflare-routes'))
        return json({
          enabled: true,
          routes: Array.from({ length: routeCount }, (_, index) => ({
            source_base_url:
              index === 0 ? identity.base_url : `https://provider-${index}.example/v1`,
            gateway_origin: 'https://gateway.example',
            credential_header: 'X-Api-Key',
            credential_query_parameter: 'key',
            retain_credential_header: true,
          })),
        });
      if (url.pathname.endsWith('/endpoint-backoff-policy'))
        if (readDetail) return readDetail(url, init);
      if (url.pathname.endsWith('/endpoint-backoff-policy'))
        return json({
          ...policy,
          connector_type: url.searchParams.get('connector_type'),
          base_url: url.searchParams.get('base_url'),
        });
      if (failList) return json({ error: { code: 'unavailable', message: 'Try again' } }, 503);
      const size = Number(url.searchParams.get('page_size') || 20);
      const pages = Math.ceil(91 / size);
      const page = Math.min(Number(url.searchParams.get('page') || 1), pages);
      return json({
        data: [
          {
            ...identity,
            revision: policy.revision,
            rule_count: String(policy.rules.length),
            key_count: '21',
          },
        ],
        next_cursor: null,
        pagination: {
          page: String(page),
          page_size: size,
          total_items: '91',
          total_pages: String(pages),
        },
      });
    }),
  );
  return {
    requests,
    writes,
    update: (next: EndpointBackoffPolicy) => {
      policy = next;
    },
    failList: (value: boolean) => {
      failList = value;
    },
    routeCount: (value: number) => {
      routeCount = value;
    },
  };
}
async function open(
  route = '/endpoint-policies?page=2&page_size=10&q=provider',
  role: 'admin' | 'steward' = 'admin',
) {
  const view = await renderWithProviders(
    <EndpointPolicies role={role} accountId="fixture-admin" />,
    { station: role === 'admin' ? 'admin' : 'user', route },
  );
  view.queryClient.setQueryDefaults(['admin', 'session'], {
    queryFn: async () => ({ admin: { username: 'fixture-admin' } }),
  });
  view.queryClient.setQueryDefaults(['user', 'session'], {
    queryFn: async () => ({ user: { id: '7', effective_level: 6 } }),
  });
  view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'fixture-admin' } });
  return view;
}
afterEach(() => vi.unstubAllGlobals());
describe('endpoint policy management', () => {
  it('retains list filters, page and draft while switching tabs and returning', async () => {
    const data = fixture();
    const view = await open();
    await view.user.click(await screen.findByRole('button', { name: 'Edit rules' }));
    fireEvent.change(await screen.findByLabelText('Rule 1 value'), {
      target: { value: 'draft-kept' },
    });
    await view.user.click(screen.getByRole('tab', { name: 'Outbound routes' }));
    await screen.findByText('https://gateway.example');
    expect(screen.getByText('Header: X-Api-Key and query parameter: key')).toBeVisible();
    expect(screen.getByText(/does not indicate connectivity or health/)).toBeVisible();
    await view.user.click(screen.getByRole('tab', { name: 'Extra backoff rules' }));
    expect(screen.getByLabelText('Rule 1 value')).toHaveValue('draft-kept');
    await view.user.click(screen.getByRole('button', { name: 'Back to endpoints' }));
    expect(screen.getByRole('searchbox')).toHaveValue('provider');
    expect(screen.getByLabelText('Go to page')).toHaveValue('2');
    await view.user.click(screen.getByRole('button', { name: 'Edit rules' }));
    expect(screen.getByLabelText('Rule 1 value')).toHaveValue('draft-kept');
    expect(data.writes).toHaveLength(0);
  });
  it('changes list size and jumps to a specified page', async () => {
    const data = fixture();
    const view = await open();
    fireEvent.change(await screen.findByLabelText('Items per page'), { target: { value: '20' } });
    await waitFor(() =>
      expect(data.requests.some((path) => path.includes('page=1&page_size=20'))).toBe(true),
    );
    fireEvent.change(await screen.findByLabelText('Go to page'), { target: { value: '4' } });
    fireEvent.keyDown(screen.getByLabelText('Go to page'), { key: 'Enter' });
    await waitFor(() =>
      expect(data.requests.some((path) => path.includes('page=4&page_size=20'))).toBe(true),
    );
    await waitFor(() => expect(view.container).toHaveTextContent('91'));
  });
  it('paginates loaded routes locally, keeps the page across tabs and clamps a shorter refresh', async () => {
    const data = fixture();
    data.routeCount(21);
    const view = await open('/endpoint-policies?view=routes');
    const panel = screen.getByRole('tabpanel', { name: 'Outbound routes' });
    await within(panel).findByRole('table');
    fireEvent.change(within(panel).getByLabelText('Items per page'), { target: { value: '10' } });
    fireEvent.change(within(panel).getByLabelText('Go to page'), { target: { value: '2' } });
    fireEvent.keyDown(within(panel).getByLabelText('Go to page'), { key: 'Enter' });
    expect(within(panel).getByLabelText('Go to page')).toHaveValue('2');
    expect(within(panel).getAllByRole('row')).toHaveLength(11);
    await view.user.click(screen.getByRole('tab', { name: 'Extra backoff rules' }));
    await view.user.click(screen.getByRole('tab', { name: 'Outbound routes' }));
    expect(within(panel).getByLabelText('Go to page')).toHaveValue('2');
    data.routeCount(5);
    await view.user.click(within(panel).getByRole('button', { name: 'Refresh' }));
    await waitFor(() => expect(within(panel).getAllByRole('row')).toHaveLength(6));
    expect(within(panel).queryByLabelText('Go to page')).not.toBeInTheDocument();
    expect(within(panel).getByText('5 items')).toBeVisible();
    data.routeCount(21);
    await view.user.click(within(panel).getByRole('button', { name: 'Refresh' }));
    expect(await within(panel).findByLabelText('Go to page')).toHaveValue('1');
    expect(within(panel).getByLabelText('Items per page')).toHaveValue('10');
  });
  it('reorders, removes and clears rules without resetting key state', async () => {
    const data = fixture();
    const view = await open();
    await view.user.click(await screen.findByRole('button', { name: 'Edit rules' }));
    await view.user.click(await screen.findByRole('button', { name: 'Add rule' }));
    fireEvent.change(screen.getByLabelText('Rule 2 match'), { target: { value: 'message' } });
    fireEvent.change(screen.getByLabelText('Rule 2 value'), { target: { value: 'quota message' } });
    fireEvent.change(screen.getByLabelText('Rule 2 period'), { target: { value: 'week' } });
    await view.user.click(screen.getByRole('button', { name: 'Move rule 2 up' }));
    await view.user.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(data.writes).toHaveLength(1));
    expect(JSON.parse(String(data.writes[0]!.body))).toEqual({
      ...identity,
      expected_revision: '2',
      rules: [{ kind: 'message', value: 'quota message', period: 'week' }, initial.rules[0]],
    });
    await waitFor(() =>
      expect(screen.queryByRole('button', { name: 'Save' })).not.toBeInTheDocument(),
    );
    await view.user.click(screen.getByRole('button', { name: 'Remove rule 2' }));
    await view.user.click(screen.getByRole('button', { name: 'Remove rule 1' }));
    await view.user.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(data.writes).toHaveLength(2));
    expect(JSON.parse(String(data.writes[1]!.body))).toEqual({
      ...identity,
      expected_revision: '3',
      rules: [],
    });
  });
  it('preserves a conflicting draft while updating its expected revision', async () => {
    let attempts = 0;
    const data = fixture(async (body) =>
      attempts++ === 0
        ? json({ error: { code: 'revision_conflict', message: 'Changed elsewhere' } }, 409)
        : json({ ...body, revision: '6' }),
    );
    const view = await open();
    await view.user.click(await screen.findByRole('button', { name: 'Edit rules' }));
    fireEvent.change(await screen.findByLabelText('Rule 1 value'), {
      target: { value: 'my-rule' },
    });
    await view.user.click(screen.getByRole('button', { name: 'Save' }));
    data.update({
      ...initial,
      revision: '5',
      rules: [{ kind: 'code', value: 'other-rule', period: 'day' }],
    });
    await view.user.click(await screen.findByRole('button', { name: 'Load latest' }));
    await waitFor(() => expect(screen.getByLabelText('Rule 1 value')).not.toBeDisabled());
    expect(screen.getByLabelText('Rule 1 value')).toHaveValue('my-rule');
    await view.user.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(data.writes).toHaveLength(2));
    expect(JSON.parse(String(data.writes[1]!.body))).toMatchObject({
      expected_revision: '5',
      rules: [{ value: 'my-rule' }],
    });
  });
  it('cancels a conflict reread when returning to the list and ignores its late response', async () => {
    let reads = 0;
    let signal: AbortSignal | null | undefined;
    let finish: ((response: Response) => void) | undefined;
    fixture(
      async () => json({ error: { code: 'revision_conflict', message: 'Changed' } }, 409),
      async (_url, init) => {
        if (++reads === 1) return json(initial);
        signal = init?.signal;
        return new Promise<Response>((resolve) => {
          finish = resolve;
        });
      },
    );
    const view = await open();
    await view.user.click(await screen.findByRole('button', { name: 'Edit rules' }));
    fireEvent.change(await screen.findByLabelText('Rule 1 value'), {
      target: { value: 'preserved-on-return' },
    });
    await view.user.click(screen.getByRole('button', { name: 'Save' }));
    await view.user.click(await screen.findByRole('button', { name: 'Load latest' }));
    await waitFor(() => expect(reads).toBe(2));
    await view.user.click(screen.getByRole('button', { name: 'Back to endpoints' }));
    expect(signal?.aborted).toBe(true);
    finish!(json({ ...initial, revision: '9', rules: [] }));
    await view.user.click(screen.getByRole('button', { name: 'Edit rules' }));
    await waitFor(() => expect(screen.getByRole('button', { name: 'Load latest' })).toBeEnabled());
    expect(screen.getByLabelText('Rule 1 value')).toHaveValue('preserved-on-return');
    expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled();
  });
  it('retries an unknown save with the same body and idempotency key', async () => {
    let attempts = 0;
    const data = fixture(async (body) => {
      if (attempts++ === 0) throw new TypeError('Connection lost');
      return json({ ...body, revision: '3' });
    });
    const view = await open();
    await view.user.click(await screen.findByRole('button', { name: 'Edit rules' }));
    fireEvent.change(await screen.findByLabelText('Rule 1 value'), {
      target: { value: 'retry-me' },
    });
    await view.user.click(screen.getByRole('button', { name: 'Save' }));
    await view.user.click(await screen.findByRole('button', { name: 'Retry the original action' }));
    await waitFor(() => expect(data.writes).toHaveLength(2));
    expect(data.writes[0]!.body).toEqual(data.writes[1]!.body);
    expect(new Headers(data.writes[0]!.headers).get('Idempotency-Key')).toBe(
      new Headers(data.writes[1]!.headers).get('Idempotency-Key'),
    );
  });
  it('refreshes a confirmed save after a list failure without writing twice', async () => {
    const data = fixture(async (body) => {
      data.failList(true);
      return json({ ...body, revision: '3' });
    });
    const view = await open();
    await view.user.click(await screen.findByRole('button', { name: 'Edit rules' }));
    fireEvent.change(await screen.findByLabelText('Rule 1 value'), {
      target: { value: 'saved-rule' },
    });
    await view.user.click(screen.getByRole('button', { name: 'Save' }));
    const check = await screen.findByRole('button', { name: 'Check again' });
    data.failList(false);
    await view.user.click(check);
    await waitFor(() =>
      expect(screen.queryByRole('button', { name: 'Check again' })).not.toBeInTheDocument(),
    );
    expect(data.writes).toHaveLength(1);
    expect(screen.getByLabelText('Rule 1 value')).toHaveValue('saved-rule');
  });
  it('opens a preconfigured endpoint using the actual connector registry without writing', async () => {
    const data = fixture();
    const view = await open();
    await view.user.click(await screen.findByRole('button', { name: 'Configure an endpoint' }));
    fireEvent.change(await screen.findByLabelText('Connector type'), {
      target: { value: 'google-gemini' },
    });
    fireEvent.change(screen.getByLabelText('Complete base URL'), {
      target: { value: 'https://new.example/v1' },
    });
    await view.user.click(screen.getByRole('button', { name: 'Open rule editor' }));
    await screen.findByLabelText('Rule 1 value');
    expect(
      data.requests.some(
        (path) =>
          path.includes('connector_type=google-gemini') &&
          path.includes('base_url=https%3A%2F%2Fnew.example%2Fv1'),
      ),
    ).toBe(true);
    expect(data.writes).toHaveLength(0);
  });
  it('refuses level 5 and ordinary users before loading privileged content', async () => {
    const data = fixture();
    const view = await open('/endpoint-policies', 'steward');
    expect(screen.getByRole('alert')).toHaveTextContent('Only administrators and level 6');
    expect(data.requests).toHaveLength(0);
    view.queryClient.setQueryData(['user', 'session'], { user: { id: '7', effective_level: 5 } });
    await waitFor(() => expect(screen.getByRole('alert')).toBeVisible());
    expect(data.requests).toHaveLength(0);
    view.queryClient.setQueryData(['user', 'session'], { user: { id: '7', effective_level: 6 } });
    await screen.findByRole('button', { name: 'Edit rules' });
    expect(
      data.requests.some((path) => path.startsWith('/api/steward/endpoint-backoff-policies')),
    ).toBe(true);
    view.queryClient.setQueryData(['user', 'session'], { user: { id: '7', effective_level: 5 } });
    await waitFor(() =>
      expect(screen.queryByRole('button', { name: 'Edit rules' })).not.toBeInTheDocument(),
    );
  });
  it('hides the entire privileged workspace immediately after a denied save', async () => {
    fixture(async () => json({ error: { code: 'forbidden', message: 'Permission revoked' } }, 403));
    const view = await open();
    await view.user.click(await screen.findByRole('button', { name: 'Edit rules' }));
    fireEvent.change(await screen.findByLabelText('Rule 1 value'), {
      target: { value: 'revoked' },
    });
    await view.user.click(screen.getByRole('button', { name: 'Save' }));
    await screen.findByRole('alert');
    await waitFor(() => expect(screen.queryByLabelText('Rule 1 value')).not.toBeInTheDocument());
  });
});
