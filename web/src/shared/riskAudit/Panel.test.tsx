import { screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { RiskAuditPanel } from './Panel';
import { ApiError } from '@shared/query/http';

const api = vi.hoisted(() => ({
  users: vi.fn(),
  config: vi.fn(),
  rules: vi.fn(),
  saveConfig: vi.fn(),
  saveRule: vi.fn(),
  deleteRule: vi.fn(),
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
beforeEach(() => {
  vi.clearAllMocks();
  api.users.mockResolvedValue({
    items: [summary],
    next: '',
    has_more: false,
    coverage: 'observed_minutes',
    scanned: 1,
    from: 1,
    to: 2,
  });
  api.config.mockResolvedValue({
    threshold_percent: 80,
    consecutive_minutes: 5,
    shared_ip_hours: 24,
    shared_ip_users: 3,
    revision: 1,
    updated_at: 0,
  });
  api.rules.mockResolvedValue({ items: [], total: 0, next: '', has_more: false });
});
describe('Risk audit access and evidence presentation', () => {
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
      await view.user.click(screen.getByRole('button', { name: 'Client rules' }));
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
    });
    await screen.findByText('9007199254740993');
    api.users.mockRejectedValueOnce(new ApiError('forbidden', 'Session revoked.', 403));
    await view.user.selectOptions(screen.getByLabelText('Risk filter'), 'rpm');
    await waitFor(() => {
      expect(screen.queryByText('9007199254740993')).not.toBeInTheDocument();
      expect(
        view.queryClient
          .getQueryCache()
          .getAll()
          .filter((q) => q.queryKey[0] === 'risk'),
      ).toHaveLength(0);
    });
  });
  it('keeps large user IDs exact and explains incomplete evidence', async () => {
    await renderWithProviders(<RiskAuditPanel role="admin" scopeKey="operator" />, {
      station: 'admin',
      role: 'admin',
    });
    expect(await screen.findByText('9007199254740993')).toBeVisible();
    expect(screen.getByText(/not proof of identity or misuse/)).toBeVisible();
    expect(screen.getByText('Incomplete minutes')).toBeVisible();
  });
  it('exposes thresholds as read-only for a steward', async () => {
    const view = await renderWithProviders(<RiskAuditPanel role="steward" scopeKey="6" />, {
      station: 'user',
    });
    await view.user.click(screen.getByRole('button', { name: 'Thresholds' }));
    expect(await screen.findByText('Only administrators can change thresholds.')).toBeVisible();
    for (const input of screen.getAllByRole('spinbutton')) expect(input).toBeDisabled();
    expect(screen.queryByRole('button', { name: 'Save' })).not.toBeInTheDocument();
  });
  it('does not fetch while scope is unavailable', async () => {
    await renderWithProviders(<RiskAuditPanel role="steward" scopeKey="" enabled={false} />, {
      station: 'user',
    });
    await waitFor(() => expect(api.users).not.toHaveBeenCalled());
  });
  it('allows bounded rule editing with all conditions visible', async () => {
    const view = await renderWithProviders(<RiskAuditPanel role="admin" scopeKey="operator" />, {
      station: 'admin',
      role: 'admin',
    });
    await view.user.click(screen.getByRole('button', { name: 'Client rules' }));
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
  });
  await screen.findByText('9007199254740993');
  expect(api.users.mock.calls[0][0]).not.toHaveProperty('to');
  expect(api.users.mock.calls[0][0]).not.toHaveProperty('from');
  await view.user.selectOptions(screen.getByLabelText('Time range'), '168');
  await view.user.click(screen.getByRole('button', { name: 'Apply filters' }));
  await waitFor(() => expect(api.users.mock.lastCall?.[0]).toMatchObject({ lookback_hours: 168 }));
  expect(api.users.mock.lastCall?.[0]).not.toHaveProperty('to');
});
it('builds a two-condition website and title rule without silently saving it', async () => {
  const view = await renderWithProviders(<RiskAuditPanel role="admin" scopeKey="operator" />, {
    station: 'admin',
    role: 'admin',
  });
  await view.user.click(screen.getByRole('button', { name: 'Client rules' }));
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
