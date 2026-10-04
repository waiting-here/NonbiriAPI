import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { InactivityPolicyPage } from './InactivityPolicyPage';
import { InactivityStatus } from './InactivityStatus';
import { credits, type Configuration } from './api';
import { DisplayTimeContext } from '@shared/components/timeContextValue';

const requests = vi.hoisted(() => ({ apiFetch: vi.fn() }));
vi.mock('@shared/query/http', async (load) => ({
  ...(await load<typeof import('@shared/query/http')>()),
  apiFetch: requests.apiFetch,
}));
vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key, i18n: { resolvedLanguage: 'en' } }),
}));
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
  return render(
    <QueryClientProvider client={client}>
      <DisplayTimeContext.Provider value={{ mode: 'site', offset_minutes: 480 }}>
        {component}
      </DisplayTimeContext.Provider>
    </QueryClientProvider>,
  );
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
            pagination: { page: '1', page_size: 20, total_items: '1', total_pages: '1' },
            as_of: 1800000000,
            configuration,
          })
        : Promise.resolve(configuration),
    );
    mount(<InactivityPolicyPage />);
    expect(await screen.findByLabelText('Enable inactivity policy')).not.toBeChecked();
    expect(screen.queryByLabelText('Inactive days')).not.toBeInTheDocument();
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
  it('navigates preview pages, changes size, jumps and clears results after editing', async () => {
    requests.apiFetch.mockImplementation(
      (path: string, options?: { json?: { page: string; page_size: number } }) => {
        if (!path.endsWith('/preview')) return Promise.resolve(configuration);
        const { page, page_size: size } = options!.json!;
        return Promise.resolve({
          data: [
            {
              user_id: `account-page-${page}`,
              action: 'none',
              exempt_reason: 'disabled',
              scheduled_at: null,
              general_milli: '0',
              game_milli: '0',
            },
          ],
          next_cursor: null,
          as_of: 1800000000,
          configuration,
          pagination: {
            page,
            page_size: size,
            total_items: '200',
            total_pages: String(Math.ceil(200 / size)),
          },
        });
      },
    );
    mount(<InactivityPolicyPage />);
    fireEvent.click(await screen.findByRole('button', { name: 'Preview accounts' }));
    expect(await screen.findByText('account-page-1')).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'common.next' }));
    expect(await screen.findByText('account-page-2')).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'common.previous' }));
    expect(await screen.findByText('account-page-1')).toBeVisible();
    fireEvent.change(screen.getByLabelText('common.pageControls.jump'), { target: { value: '8' } });
    fireEvent.keyDown(screen.getByLabelText('common.pageControls.jump'), { key: 'Enter' });
    expect(await screen.findByText('account-page-8')).toBeVisible();
    fireEvent.change(screen.getByLabelText('common.pageControls.size'), {
      target: { value: '50' },
    });
    expect(await screen.findByText('account-page-1')).toBeVisible();
    const calls = requests.apiFetch.mock.calls.filter((call) =>
      String(call[0]).endsWith('/preview'),
    );
    expect(calls.map((call) => [call[1].json.page, call[1].json.page_size])).toEqual([
      ['1', 20],
      ['2', 20],
      ['1', 20],
      ['8', 20],
      ['1', 50],
    ]);
    fireEvent.change(screen.getByLabelText('Execution time (optional)'), {
      target: { value: '12:00' },
    });
    expect(screen.queryByText('account-page-1')).not.toBeInTheDocument();
    expect(requests.apiFetch.mock.calls.some((call) => call[1]?.method === 'PUT')).toBe(false);
  });
  it('saves and clears the optional clock while displaying the site timezone', async () => {
    requests.apiFetch.mockImplementation(
      (_path: string, options?: { method?: string; json?: { policy: unknown } }) =>
        Promise.resolve(
          options?.method === 'PUT'
            ? {
                ...configuration,
                ...(options.json!.policy as object),
                revision: '9007199254740994',
              }
            : configuration,
        ),
    );
    mount(<InactivityPolicyPage />);
    const input = await screen.findByLabelText('Execution time (optional)');
    expect(input).toHaveValue('');
    expect(screen.getByText('Site timezone: UTC+08:00')).toBeVisible();
    fireEvent.change(input, { target: { value: '12:00' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save policy' }));
    await screen.findByText('Policy saved.');
    fireEvent.change(screen.getByLabelText('Execution time (optional)'), { target: { value: '' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save policy' }));
    await waitFor(() =>
      expect(requests.apiFetch.mock.calls.filter((call) => call[1]?.method === 'PUT')).toHaveLength(
        2,
      ),
    );
    const saved = requests.apiFetch.mock.calls.filter((call) => call[1]?.method === 'PUT');
    expect(saved[0][1].json.policy.execution_time).toBe('12:00');
    expect(saved[1][1].json.policy.execution_time).toBeUndefined();
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
    mount(<InactivityStatus accountId="17" />);
    expect(await screen.findByText('Observation started')).toBeInTheDocument();
    expect(screen.getByText(/Donors are not exempt/)).toBeInTheDocument();
    expect(requests.apiFetch).toHaveBeenCalledWith(
      '/api/inactivity-policy/status',
      expect.anything(),
    );
    expect(requests.apiFetch.mock.calls.some((call) => call[1]?.method)).toBe(false);
  });
});

it('uses the returned revision immediately after saving without another reload', async () => {
  const updated = { ...configuration, revision: '9007199254740994' };
  requests.apiFetch.mockImplementation((_path: string, options?: { method?: string }) =>
    Promise.resolve(options?.method === 'PUT' ? updated : configuration),
  );
  mount(<InactivityPolicyPage />);
  fireEvent.click(await screen.findByRole('button', { name: 'Save policy' }));
  expect(await screen.findByText('Policy saved.')).toBeVisible();
  fireEvent.click(screen.getByRole('button', { name: 'Save policy' }));
  await waitFor(() =>
    expect(requests.apiFetch.mock.calls.filter((call) => call[1]?.method === 'PUT')).toHaveLength(
      2,
    ),
  );
  expect(
    requests.apiFetch.mock.calls.filter((call) => call[1]?.method === 'PUT')[1][1].json
      .expected_revision,
  ).toBe(updated.revision);
  expect(requests.apiFetch.mock.calls.filter((call) => !call[1]?.method)).toHaveLength(1);
});
it('validates enabled actions before previewing and reveals their required fields', async () => {
  requests.apiFetch.mockResolvedValue(configuration);
  mount(<InactivityPolicyPage />);
  fireEvent.click(await screen.findByLabelText('Enable inactivity policy'));
  fireEvent.click(screen.getByRole('button', { name: 'Preview accounts' }));
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'Enable credit decay or protective bans',
  );
  expect(requests.apiFetch.mock.calls.some((call) => call[1]?.method)).toBe(false);
  fireEvent.click(screen.getByLabelText('Enable credit decay'));
  expect(screen.getByLabelText('Inactive days')).toBeRequired();
});

it('loads execution and policy audit history only when their folds open', async () => {
  requests.apiFetch.mockImplementation((path: string) =>
    Promise.resolve(
      path.endsWith('/runs') || path.endsWith('/audits')
        ? { data: [], next_cursor: null }
        : configuration,
    ),
  );
  mount(<InactivityPolicyPage />);
  await screen.findByRole('switch', { name: 'Enable inactivity policy' });
  expect(requests.apiFetch.mock.calls.map((call) => call[0])).toEqual([
    '/admin/api/inactivity-policy',
  ]);
  const runs = screen.getByText('Execution records').closest('details')!;
  fireEvent.click(runs.querySelector('summary')!);
  await waitFor(() =>
    expect(
      requests.apiFetch.mock.calls.filter((call) => String(call[0]).endsWith('/runs')),
    ).toHaveLength(1),
  );
  expect(requests.apiFetch.mock.calls.some((call) => String(call[0]).endsWith('/audits'))).toBe(
    false,
  );
  fireEvent.click(runs.querySelector('summary')!);
  fireEvent.click(runs.querySelector('summary')!);
  expect(
    requests.apiFetch.mock.calls.filter((call) => String(call[0]).endsWith('/runs')),
  ).toHaveLength(1);
  fireEvent.click(screen.getByText('Configuration and preview audit'));
  await waitFor(() =>
    expect(
      requests.apiFetch.mock.calls.filter((call) => String(call[0]).endsWith('/audits')),
    ).toHaveLength(1),
  );
  expect(requests.apiFetch.mock.calls.some((call) => call[1]?.method)).toBe(false);
});
