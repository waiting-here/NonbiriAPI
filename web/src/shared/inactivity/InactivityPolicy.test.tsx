import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { InactivityPolicyPage } from './InactivityPolicyPage';
import { InactivityStatus } from './InactivityStatus';
import { credits, type Configuration } from './api';

const requests = vi.hoisted(() => ({ apiFetch: vi.fn() }));
vi.mock('@shared/query/http', () => ({ apiFetch: requests.apiFetch }));
vi.mock('react-i18next', () => ({ useTranslation: () => ({ i18n: { resolvedLanguage: 'en' } }) }));
afterEach(() => requests.apiFetch.mockReset());
const configuration: Configuration = {
  enabled: false,
  revision: '9007199254740993',
  updated_at: 1800000000,
  decay_grace_until: 0,
  protection_grace_until: 0,
  decay: {
    enabled: false,
    inactive_days: null,
    interval_days: null,
    assets: { general: null, game: null },
  },
  protection: { enabled: false, inactive_days: null },
};
function mount(component: React.ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(<QueryClientProvider client={client}>{component}</QueryClientProvider>);
}
describe('inactivity policy', () => {
  it('formats full precision balances without Number coercion', () => {
    expect(credits('170141183460469231731687303715884105727')).toBe(
      '170141183460469231731687303715884105.727',
    );
    expect(credits('1')).toBe('0.001');
    expect(credits('-1001')).toBe('-1.001');
  });
  it('keeps defaults disabled and previews without saving', async () => {
    requests.apiFetch.mockImplementation((path: string) =>
      path.endsWith('/preview')
        ? Promise.resolve({
            data: [
              {
                user_id: '9007199254740994',
                exempt_reason: 'disabled',
                action: 'none',
                scheduled_at: null,
                general_milli: '0',
                game_milli: '0',
              },
            ],
            next_cursor: null,
            as_of: 1800000000,
            configuration,
          })
        : Promise.resolve(configuration),
    );
    mount(<InactivityPolicyPage />);
    expect(await screen.findByLabelText('Enable inactivity policy')).not.toBeChecked();
    expect(screen.getByLabelText('Inactive days')).toHaveValue(null);
    fireEvent.click(screen.getByRole('button', { name: 'Preview accounts' }));
    expect(await screen.findByText('9007199254740994')).toBeInTheDocument();
    const preview = requests.apiFetch.mock.calls.find((call) =>
      String(call[0]).endsWith('/preview'),
    );
    expect(preview?.[1].json.expected_revision).toBe('9007199254740993');
    expect(requests.apiFetch.mock.calls.some((call) => call[1]?.method === 'PUT')).toBe(false);
    fireEvent.click(screen.getByLabelText('Enable inactivity policy'));
    expect(screen.queryByText('9007199254740994')).not.toBeInTheDocument();
  });
  it('reuses the exact uncertain save request and key', async () => {
    let writes = 0;
    requests.apiFetch.mockImplementation((_path: string, options?: { method?: string }) => {
      if (options?.method === 'PUT' && writes++ === 0)
        return Promise.reject(new Error('lost response'));
      return Promise.resolve(configuration);
    });
    mount(<InactivityPolicyPage />);
    fireEvent.click(await screen.findByRole('button', { name: 'Save policy' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Retry save' }));
    await waitFor(() =>
      expect(requests.apiFetch.mock.calls.filter((call) => call[1]?.method === 'PUT')).toHaveLength(
        2,
      ),
    );
    const calls = requests.apiFetch.mock.calls.filter((call) => call[1]?.method === 'PUT');
    expect(calls[1][1].headers['Idempotency-Key']).toBe(calls[0][1].headers['Idempotency-Key']);
    expect(calls[1][1].json).toEqual(calls[0][1].json);
  });
  it('shows the owner activity dates and donor non-exemption', async () => {
    requests.apiFetch.mockResolvedValue({
      configuration,
      activity: {
        observation_started_at: 1800000000,
        last_active_at: null,
        last_decay_at: null,
        activity_seq: '0',
        activity_epoch: '0',
      },
      exempt_reason: 'disabled',
      decay_at: null,
      protection_at: null,
    });
    mount(<InactivityStatus />);
    expect(await screen.findByText('Observation started')).toBeInTheDocument();
    expect(screen.getByText(/Donors are not exempt/)).toBeInTheDocument();
    expect(requests.apiFetch).toHaveBeenCalledWith(
      '/api/inactivity-policy/status',
      expect.anything(),
    );
    expect(requests.apiFetch.mock.calls.some((call) => call[1]?.method)).toBe(false);
  });
});
