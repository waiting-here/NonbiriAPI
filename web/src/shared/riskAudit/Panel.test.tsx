import { act, screen, waitFor, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { RiskAuditPanel } from './Panel';
import { ApiError } from '@shared/query/http';
import { useLocation, useNavigate } from 'react-router';

const api = vi.hoisted(() => ({
  recentTasks: vi.fn(),
  taskResults: vi.fn(),
  createTask: vi.fn(),
  config: vi.fn(),
  numberedRules: vi.fn(),
  saveConfig: vi.fn(),
  saveRule: vi.fn(),
  deleteRule: vi.fn(),
  user: vi.fn(),
}));
vi.mock('./api', async (load) => ({
  ...(await load<typeof import('./api')>()),
  riskAPI: () => api,
}));
const summary = {
  user_id: '9007199254740993',
  rpm_committed: 10,
  rpm_denied: 0,
  concurrency_denied: 0,
  peak: 2,
  occupancy_millis: 60000,
  complete_minutes: 1,
  incomplete_minutes: 2,
  high_rpm_minutes: 0,
  high_concurrency_minutes: 0,
  rpm_risk: false,
  concurrency_risk: false,
  risk_scope: 'total',
};
const scanID = 'scn_AAAAAAAAAAAAAAAAAAAAAA';
const scan = {
  id: scanID,
  kind: 'users',
  call_kind: 'total',
  signal: '',
  model: '',
  state: 'completed',
  scanned_candidates: '1',
  candidates: '1',
  matched: '1',
  from: 1,
  to: 2,
  filter_revision: 1,
  expires_at: 1800000000,
  rule_count: 0,
  coverage: 'complete',
  changed: false,
  truncated_reason: '',
};
const selectedScanRoute = '/risk-audit?audit_users_scan=' + scanID;
function HistoryProbe() {
  const navigate = useNavigate();
  const location = useLocation();
  return (
    <>
      <button type="button" onClick={() => navigate(-1)}>
        Browser back
      </button>
      <button type="button" onClick={() => navigate(1)}>
        Browser forward
      </button>
      <output data-testid="audit-location">{location.search}</output>
    </>
  );
}
it('shows a single Discord IP window with retained original accounts and bounded evidence links', async () => {
  const ipScan = {
    ...scan,
    kind: 'user_ips',
    coverage: 'partial',
    truncated_reason: 'identity_or_ip_unavailable',
    source_watermark: '4097',
    last_source_at: 1,
    last_source_id: '4096',
  };
  api.recentTasks.mockResolvedValue([ipScan]);
  api.taskResults.mockResolvedValue({
    scan: ipScan,
    items: [
      {
        discord_id: '123456789012345678',
        peak: 3,
        window_from: 1,
        window_to: 2,
        ips: ['203.0.113.1', '203.0.113.2', '203.0.113.3'],
        ips_truncated: false,
        accounts_truncated: true,
        accounts: [
          {
            user_id: '7',
            call_kind: 'self',
            requests: 4,
            dispatched: 3,
            rejected: 1,
            first_seen: 1,
            last_seen: 2,
          },
        ],
      },
    ],
    page: '1',
    page_size: 20,
    total_items: '1',
    total_pages: '1',
    coverage: 'partial',
  });
  const view = await renderWithProviders(
    <>
      <RiskAuditPanel role="steward" scopeKey="6" />
      <HistoryProbe />
    </>,
    {
      station: 'user',
      role: 'level6',
      route: '/steward?audit_tab=user_ips&audit_user_ips_scan=' + scanID,
    },
  );
  expect(
    await screen.findByRole('heading', { name: 'Discord ID: 123456789012345678' }),
  ).toBeVisible();
  const link = screen.getByRole('link', { name: 'View original requests in this window' });
  const path = new URL(link.getAttribute('href')!, 'https://example.test');
  expect(path.pathname).toBe('/steward');
  expect(path.searchParams.get('tab')).toBe('logs');
  expect(path.searchParams.get('user_id')).toBe('7');
  expect(path.searchParams.get('from')).toBe('1');
  expect(path.searchParams.get('to')).toBe('3');
  expect(screen.getByText(/Some requests lack original identity/)).toBeVisible();
  expect(screen.getByText(/IP or account details are truncated here/)).toBeVisible();
  expect(api.user).not.toHaveBeenCalled();
  await view.user.selectOptions(screen.getByLabelText('Time range'), '168');
  await view.user.click(screen.getByRole('button', { name: 'Apply filters' }));
  expect(screen.getByTestId('audit-location')).not.toHaveTextContent('audit_user_ips_scan');
  expect(
    screen.queryByRole('heading', { name: 'Discord ID: 123456789012345678' }),
  ).not.toBeInTheDocument();
});

it('retries a missed scan response using the original filters and request token', async () => {
  api.createTask
    .mockRejectedValueOnce(new ApiError('network_error', 'Connection interrupted.', 0))
    .mockResolvedValueOnce(scan);
  const view = await renderWithProviders(<RiskAuditPanel role="admin" scopeKey="operator" />, {
    station: 'admin',
    role: 'admin',
    route: '/risk-audit',
  });
  view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'operator' } });
  await view.user.click(await screen.findByRole('button', { name: 'Start new scan' }));
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Start new scan' })).toBeDisabled(),
  );
  const original = structuredClone(api.createTask.mock.calls[0][0]);
  await view.user.selectOptions(screen.getByLabelText('Time range'), '168');
  await view.user.click(screen.getByRole('button', { name: 'Apply filters' }));
  await view.user.click(await screen.findByRole('button', { name: 'Retry' }));
  await waitFor(() => expect(api.createTask).toHaveBeenCalledTimes(2));
  expect(api.createTask.mock.calls[1][0]).toEqual(original);
});

it('does not navigate after an old scan response arrives for a replaced account', async () => {
  let finish!: (value: typeof scan) => void;
  api.createTask.mockImplementationOnce(
    () =>
      new Promise((resolve) => {
        finish = resolve;
      }),
  );
  const view = await renderWithProviders(
    <>
      <RiskAuditPanel role="admin" scopeKey="operator" />
      <HistoryProbe />
    </>,
    { station: 'admin', role: 'admin', route: '/risk-audit' },
  );
  view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'operator' } });
  await view.user.click(await screen.findByRole('button', { name: 'Start new scan' }));
  await waitFor(() => expect(api.createTask).toHaveBeenCalledTimes(1));
  await act(async () => {
    view.queryClient.setQueryData(['admin', 'session'], {
      admin: { username: 'another-operator' },
    });
    finish(scan);
  });
  expect(api.createTask.mock.calls[0][1].aborted).toBe(true);
  expect(screen.getByTestId('audit-location')).toHaveTextContent('');
  expect(api.taskResults).not.toHaveBeenCalled();
});
beforeEach(() => {
  vi.resetAllMocks();
  api.recentTasks.mockResolvedValue([scan]);
  api.taskResults.mockResolvedValue({
    scan,
    items: [summary],
    page: '1',
    page_size: 20,
    total_items: '1',
    total_pages: '1',
    coverage: 'complete',
  });
  api.createTask.mockResolvedValue(scan);
  api.config.mockResolvedValue({
    threshold_percent: 80,
    consecutive_minutes: 5,
    shared_ip_hours: 24,
    shared_ip_users: 3,
    revision: 1,
    updated_at: 0,
  });
  api.numberedRules.mockResolvedValue({
    items: [],
    page: '1',
    page_size: 20,
    total_items: '0',
    total_pages: '1',
    revision: 'r1',
    changed: false,
  });
  api.user.mockImplementation(async (_id: string, filters: { model?: string }) => ({
    user_id: summary.user_id,
    summary,
    minutes: [],
    coverage: 'complete',
    requests: {
      items: [],
      next: '',
      has_more: false,
      from: 1790000000,
      to: 1790003600,
      coverage: 'complete',
      scanned: 0,
    },
    comparison: {
      model: filters.model ?? '',
      from: 1790000000,
      to: 1790003600,
      user: {
        samples: 0,
        dispatched: 0,
        rejected: 0,
        failed: 0,
        unknown_duration: 0,
        cancellations: [],
      },
      others: {
        samples: 0,
        dispatched: 0,
        rejected: 0,
        failed: 0,
        unknown_duration: 0,
        cancellations: [],
      },
      has_more: false,
      coverage: 'complete',
    },
    source_distribution: [],
  }));
});
describe('Risk audit access and evidence presentation', () => {
  it('submits pasted IP addresses as one alternative condition alongside other conditions', async () => {
    const view = await renderWithProviders(<RiskAuditPanel role="steward" scopeKey="6" />, {
      station: 'user',
      role: 'level6',
    });
    view.queryClient.setQueryData(['user', 'session'], {
      user: { id: '6', username: 'Steward', level: 6, effective_level: 6 },
    });
    await view.user.click(screen.getByRole('tab', { name: 'Client rules' }));
    await view.user.click(await screen.findByRole('button', { name: 'New rule' }));
    await view.user.type(screen.getByLabelText('Rule name'), 'Example relay');
    await view.user.selectOptions(screen.getByLabelText('Field'), 'effective_ip');
    expect(screen.getByLabelText('Operator')).toHaveValue('ip_in');
    const addresses = screen.getByRole('textbox', { name: 'IP addresses' });
    await view.user.click(addresses);
    await view.user.paste('192.0.2.1, 192.0.2.2\n2001:db8::1，::ffff:192.0.2.1\n');
    await view.user.click(screen.getByRole('button', { name: 'Add AND condition' }));
    await view.user.type(screen.getByLabelText('Match value'), 'Example/');
    await view.user.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() =>
      expect(api.saveRule).toHaveBeenCalledWith(
        expect.objectContaining({
          conditions: [
            {
              field: 'effective_ip',
              operator: 'ip_in',
              value: '',
              values: ['192.0.2.1', '192.0.2.2', '2001:db8::1', '::ffff:192.0.2.1'],
              case_sensitive: false,
            },
            { field: 'user_agent', operator: 'prefix', value: 'Example/', case_sensitive: false },
          ],
        }),
        undefined,
        expect.any(AbortSignal),
      ),
    );
  });

  it('retains all saved IP alternatives when reopening and editing a rule', async () => {
    const savedRules = {
      items: [
        {
          id: 'rsk_example',
          name: 'Example relay',
          status: 'suspected',
          enabled: true,
          revision: 2,
          conditions: [
            {
              field: 'effective_ip',
              operator: 'ip_in',
              value: '',
              values: ['192.0.2.1', '2001:db8::1'],
              case_sensitive: false,
            },
          ],
          evidence_note: '',
          evidence_url: '',
          auto_ban: null,
          updated_at: 1800000000,
          updated_by_role: 'admin',
        },
      ],
      page: '1',
      page_size: 20,
      total_items: '1',
      total_pages: '1',
      revision: 'r2',
      changed: false,
    };
    let completeRefresh!: (value: typeof savedRules) => void;
    const refreshedRules = new Promise<typeof savedRules>((resolve) => {
      completeRefresh = resolve;
    });
    api.numberedRules.mockResolvedValueOnce(savedRules).mockReturnValue(refreshedRules);
    const view = await renderWithProviders(<RiskAuditPanel role="admin" scopeKey="operator" />, {
      station: 'admin',
      role: 'admin',
    });
    view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'operator' } });
    await view.user.click(screen.getByRole('tab', { name: 'Client rules' }));
    await waitFor(() =>
      expect(api.numberedRules).toHaveBeenCalledWith('1', 20, 'r2', expect.any(AbortSignal)),
    );
    expect(screen.queryByRole('button', { name: 'Edit' })).not.toBeInTheDocument();
    await act(async () => completeRefresh(savedRules));
    expect(await screen.findByText('Observed IP Matches any IP 192.0.2.1, 2001:db8::1')).toBeVisible();
    await view.user.click(screen.getByRole('button', { name: 'Edit' }));
    const addresses = screen.getByRole('textbox', { name: 'IP addresses' });
    expect(addresses).toHaveValue('192.0.2.1\n2001:db8::1');
    await view.user.type(addresses, '\n192.0.2.3');
    await view.user.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() =>
      expect(api.saveRule.mock.lastCall?.[0]).toMatchObject({
        revision: 2,
        conditions: [
          { operator: 'ip_in', value: '', values: ['192.0.2.1', '2001:db8::1', '192.0.2.3'] },
        ],
      }),
    );
  });

  it.each([
    ['Tavo', 'user_agent', 'prefix', 'Tavo/'],
    ['New API', 'openrouter_title', 'equals', 'New API'],
    ['One API', 'legacy_title', 'equals', 'One API'],
  ])(
    'populates the %s preset without saving or requiring a website',
    async (name, field, operator, value) => {
      const view = await renderWithProviders(<RiskAuditPanel role="admin" scopeKey="operator" />, {
        station: 'admin',
        role: 'admin',
      });
      await view.user.click(screen.getByRole('tab', { name: 'Client rules' }));
      await view.user.click(await screen.findByRole('button', { name: 'New rule' }));
      await view.user.click(screen.getByRole('button', { name }));
      expect(screen.getByLabelText('Rule name')).toHaveValue(name);
      expect(screen.getByLabelText('Field')).toHaveValue(field);
      expect(screen.getByLabelText('Operator')).toHaveValue(operator);
      expect(screen.getByLabelText('Match value')).toHaveValue(value);
      expect(screen.getAllByLabelText('Field')).toHaveLength(1);
      expect(api.saveRule).not.toHaveBeenCalled();
    },
  );
  it('removes privileged evidence immediately after a revoked-session response', async () => {
    const view = await renderWithProviders(<RiskAuditPanel role="admin" scopeKey="operator" />, {
      station: 'admin',
      role: 'admin',
      route: selectedScanRoute,
    });
    await screen.findByText(/User ID: 9007199254740993/);
    api.taskResults.mockRejectedValueOnce(new ApiError('forbidden', 'Session revoked.', 403));
    await view.user.click(screen.getAllByRole('button', { name: 'Refresh' })[1]);
    await waitFor(() => {
      expect(screen.queryByText(/User ID: 9007199254740993/)).not.toBeInTheDocument();
      expect(
        view.queryClient
          .getQueryCache()
          .getAll()
          .filter((q) => q.queryKey[0] === 'risk'),
      ).toHaveLength(0);
    });
  });
  it('keeps large user IDs exact and explains incomplete evidence', async () => {
    const view = await renderWithProviders(<RiskAuditPanel role="admin" scopeKey="operator" />, {
      station: 'admin',
      role: 'admin',
      route: selectedScanRoute,
    });
    expect(await screen.findByText(/User ID: 9007199254740993/)).toBeVisible();
    await view.user.click(screen.getByText('Audit basis', { exact: true }));
    expect(screen.getByText(/not proof of identity or misuse/)).toBeVisible();
    expect(screen.getByText(/Incomplete minutes/)).toBeVisible();
  });
  it('exposes thresholds as read-only for a steward', async () => {
    const view = await renderWithProviders(<RiskAuditPanel role="steward" scopeKey="6" />, {
      station: 'user',
    });
    await view.user.click(screen.getByRole('tab', { name: 'Thresholds' }));
    expect(await screen.findByText('Only administrators can change thresholds.')).toBeVisible();
    for (const input of screen.getAllByRole('spinbutton')) expect(input).toBeDisabled();
    expect(screen.queryByRole('button', { name: 'Save' })).not.toBeInTheDocument();
  });
  it('does not fetch while scope is unavailable', async () => {
    await renderWithProviders(<RiskAuditPanel role="steward" scopeKey="" enabled={false} />, {
      station: 'user',
    });
    await waitFor(() => expect(api.recentTasks).not.toHaveBeenCalled());
  });
  it('allows bounded rule editing with all conditions visible', async () => {
    const view = await renderWithProviders(<RiskAuditPanel role="admin" scopeKey="operator" />, {
      station: 'admin',
      role: 'admin',
    });
    await view.user.click(screen.getByRole('tab', { name: 'Client rules' }));
    await view.user.click(await screen.findByRole('button', { name: 'New rule' }));
    await view.user.type(screen.getByLabelText('Rule name'), 'Example app');
    await view.user.type(screen.getByLabelText('Match value'), 'ExampleApp/');
    for (let n = 1; n < 8; n++)
      await view.user.click(screen.getByRole('button', { name: 'Add AND condition' }));
    expect(screen.getByRole('button', { name: 'Add AND condition' })).toBeDisabled();
    expect(screen.getAllByLabelText('Match value')).toHaveLength(8);
  });
});

it('uses server-relative quick ranges even when the browser clock is ahead', async () => {
  const view = await renderWithProviders(<RiskAuditPanel role="admin" scopeKey="operator" />, {
    station: 'admin',
    role: 'admin',
    route: selectedScanRoute,
  });
  await screen.findByText(/User ID: 9007199254740993/);
  view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'operator' } });
  await view.user.selectOptions(screen.getByLabelText('Time range'), '168');
  await view.user.click(screen.getByRole('button', { name: 'Apply filters' }));
  await view.user.click(screen.getByRole('button', { name: 'Start new scan' }));
  await waitFor(() =>
    expect(api.createTask.mock.lastCall?.[0]).toMatchObject({ lookback_hours: 168 }),
  );
  expect(api.createTask.mock.lastCall?.[0].to).toBeUndefined();
});
it('builds a two-condition website and title rule without silently saving it', async () => {
  const view = await renderWithProviders(<RiskAuditPanel role="admin" scopeKey="operator" />, {
    station: 'admin',
    role: 'admin',
  });
  await view.user.click(screen.getByRole('tab', { name: 'Client rules' }));
  await view.user.click(await screen.findByRole('button', { name: 'New rule' }));
  await view.user.click(
    screen.getByRole('button', { name: 'Source website and application title' }),
  );
  expect(screen.getAllByLabelText('Field').map((x) => (x as HTMLSelectElement).value)).toEqual([
    'http_referer',
    'openrouter_title',
  ]);
  expect(screen.getAllByLabelText('Match value')).toHaveLength(2);
  expect(api.saveRule).not.toHaveBeenCalled();
});

it('converts a bounded administrator ban duration exactly and keeps it off ordinary rules', async () => {
  const view = await renderWithProviders(<RiskAuditPanel role="admin" scopeKey="operator" />, {
    station: 'admin',
    role: 'admin',
  });
  view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'operator' } });
  await view.user.click(screen.getByRole('tab', { name: 'Client rules' }));
  await view.user.click(await screen.findByRole('button', { name: 'New rule' }));
  await view.user.click(screen.getByRole('button', { name: 'Tavo' }));
  await view.user.selectOptions(screen.getByRole('combobox', { name: 'Ban duration' }), 'days');
  await view.user.clear(screen.getByRole('spinbutton', { name: /Ban duration/ }));
  await view.user.type(screen.getByRole('spinbutton', { name: /Ban duration/ }), '3651');
  expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled();
  await view.user.selectOptions(screen.getByRole('combobox', { name: 'Ban duration' }), 'hours');
  await view.user.clear(screen.getByRole('spinbutton', { name: /Ban duration/ }));
  await view.user.type(screen.getByRole('spinbutton', { name: /Ban duration/ }), '2');
  await view.user.click(screen.getByRole('checkbox', { name: 'Automatic ban enabled' }));
  await view.user.click(screen.getByRole('button', { name: 'Save' }));
  expect(api.saveRule).not.toHaveBeenCalled();
  const confirmation = await screen.findByRole('alertdialog');
  expect(confirmation).toHaveTextContent('Tavo');
  expect(confirmation).toHaveTextContent('2 Hours');
  await view.user.click(within(confirmation).getByRole('button', { name: 'Save' }));
  await waitFor(() =>
    expect(api.saveRule).toHaveBeenCalledWith(
      expect.objectContaining({ auto_ban: { enabled: true, duration_seconds: 7200 } }),
      undefined,
      expect.any(AbortSignal),
    ),
  );
});

it('blocks steward editing of a bound rule even when its ban is disabled', async () => {
  api.numberedRules.mockResolvedValue({
    items: [
      {
        id: 'rsk_AAAAAAAAAAAAAAAAAAAAAA',
        name: 'Protected',
        status: 'suspected',
        enabled: true,
        revision: 3,
        conditions: [
          { field: 'user_agent', operator: 'prefix', value: 'App/', case_sensitive: false },
        ],
        evidence_note: '',
        evidence_url: '',
        created_at: 1790000640,
        updated_at: 1790000640,
        created_by_role: 'admin',
        updated_by_role: 'admin',
        created_by_user_id: null,
        updated_by_user_id: null,
        auto_ban: { enabled: false, duration_seconds: null },
      },
    ],
    page: '1',
    page_size: 20,
    total_items: '1',
    total_pages: '1',
    revision: 'r2',
    changed: false,
  });
  const view = await renderWithProviders(<RiskAuditPanel role="steward" scopeKey="6" />, {
    station: 'user',
  });
  await view.user.click(screen.getByRole('tab', { name: 'Client rules' }));
  await waitFor(() => {
    expect(
      screen.getByText(/Only an administrator can edit or remove this bound rule/),
    ).toBeVisible();
    expect(screen.getByRole('button', { name: 'Edit' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Delete' })).toBeDisabled();
  });
});

it('restores a saved aggregate scan’s frozen signal, call kind and time across history navigation', async () => {
  const old = {
    ...scan,
    call_kind: 'charity',
    signal: 'rpm',
    model: '',
    from: 1790000000,
    to: 1790003600,
    filter_revision: 7,
  };
  api.recentTasks.mockResolvedValue([old]);
  api.taskResults.mockResolvedValue({
    scan: old,
    items: [summary],
    page: '1',
    page_size: 20,
    total_items: '1',
    total_pages: '1',
    coverage: 'complete',
  });
  const view = await renderWithProviders(
    <>
      <RiskAuditPanel role="admin" scopeKey="operator" />
      <HistoryProbe />
    </>,
    {
      station: 'admin',
      role: 'admin',
      locale: 'en',
      route: '/risk-audit?audit_kind=self&audit_signal=concurrency&audit_model=current-model',
    },
  );
  await view.user.selectOptions(
    await screen.findByRole('combobox', { name: 'Recent scans' }),
    scanID,
  );
  expect(await screen.findByText(/Frozen conditions for this scan/)).toHaveTextContent('Charity');
  expect(screen.getByText(/Frozen conditions for this scan/)).toHaveTextContent('High RPM');
  expect(screen.getByText(/Frozen conditions for this scan/)).toHaveTextContent('09/21/2026');
  expect(screen.getByRole('combobox', { name: 'Call type' })).toHaveValue('charity');
  expect(screen.getByRole('combobox', { name: 'Risk filter' })).toHaveValue('rpm');
  const selected = new URLSearchParams(screen.getByTestId('audit-location').textContent ?? '');
  expect(selected.get('audit_from')).toBe('1790000000');
  expect(selected.get('audit_to')).toBe('1790003600');
  expect(selected.get('audit_model')).toBeNull();
  expect(selected.get('audit_users_scan')).toBe(scanID);
  await view.user.click(screen.getByRole('button', { name: 'Browser back' }));
  expect(screen.getByRole('combobox', { name: 'Call type' })).toHaveValue('self');
  expect(screen.getByRole('combobox', { name: 'Risk filter' })).toHaveValue('concurrency');
  expect(
    new URLSearchParams(screen.getByTestId('audit-location').textContent ?? '').get('audit_model'),
  ).toBe('current-model');
  await view.user.click(screen.getByRole('button', { name: 'Browser forward' }));
  expect(screen.getByRole('combobox', { name: 'Call type' })).toHaveValue('charity');
  expect(screen.getByRole('combobox', { name: 'Risk filter' })).toHaveValue('rpm');
  expect(
    new URLSearchParams(screen.getByTestId('audit-location').textContent ?? '').get('audit_model'),
  ).toBeNull();
  expect(await screen.findByText(/Frozen conditions for this scan/)).toHaveTextContent('High RPM');
  view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'operator' } });
  await view.user.click(screen.getByRole('button', { name: 'Start new scan' }));
  await waitFor(() =>
    expect(api.createTask.mock.lastCall?.[0]).toMatchObject({
      from: old.from,
      to: old.to,
      call_kind: 'charity',
      signal: 'rpm',
    }),
  );
  expect(api.createTask.mock.lastCall?.[0].model).toBeUndefined();
});

it('restores the user model draft with the query on Back and Forward', async () => {
  const view = await renderWithProviders(
    <>
      <RiskAuditPanel role="admin" scopeKey="operator" />
      <HistoryProbe />
    </>,
    {
      station: 'admin',
      role: 'admin',
      locale: 'en',
      route: `/risk-audit?audit_user=${summary.user_id}&audit_user_from=1790000000&audit_user_to=1790003600&audit_user_model=A`,
    },
  );
  expect(await screen.findByRole('heading', { name: /Same-model comparison: A/ })).toBeVisible();
  const model = screen.getByRole('textbox', { name: 'Model' });
  expect(model).toHaveValue('A');
  await view.user.clear(model);
  await view.user.type(model, 'B');
  await view.user.click(
    within(model.closest('form')!).getByRole('button', { name: 'Apply filters' }),
  );
  expect(await screen.findByRole('heading', { name: /Same-model comparison: B/ })).toBeVisible();
  expect(screen.getByRole('textbox', { name: 'Model' })).toHaveValue('B');
  await view.user.click(screen.getByRole('button', { name: 'Browser back' }));
  expect(await screen.findByRole('heading', { name: /Same-model comparison: A/ })).toBeVisible();
  expect(screen.getByRole('textbox', { name: 'Model' })).toHaveValue('A');
  await view.user.click(screen.getByRole('button', { name: 'Browser forward' }));
  expect(await screen.findByRole('heading', { name: /Same-model comparison: B/ })).toBeVisible();
  expect(screen.getByRole('textbox', { name: 'Model' })).toHaveValue('B');
  expect(api.user.mock.calls.map(([, filters]) => filters.model)).toContain('A');
  expect(api.user.mock.calls.map(([, filters]) => filters.model)).toContain('B');
});
