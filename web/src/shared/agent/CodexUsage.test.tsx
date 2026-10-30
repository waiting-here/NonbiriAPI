import { screen, waitFor } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { DisplayTimeContext } from '@shared/components/timeContextValue';
import common from '@shared/i18n/common/en.json';
import { CodexUsageCard } from './CodexUsage';
import { getCodexUsage, type CodexUsage } from './codexApi';

vi.mock('./codexApi', async (original) => ({
  ...(await original<typeof import('./codexApi')>()),
  getCodexUsage: vi.fn(),
}));
const fetch = vi.mocked(getCodexUsage);
const text = common.common.agent.quota;
const data: CodexUsage = {
  official: {
    source: 'openai_codex',
    revision: '2',
    availability: 'available',
    fetched_at: 1800000000000,
    attempted_at: 1800000000000,
    stale: false,
    error: null,
    data: {
      plan_type: 'business',
      buckets: [
        {
          id: 'codex',
          name: 'Codex',
          windows: [
            { name: 'primary', used_percent: 25, window_seconds: 7200, resets_at: 1800007200 },
            { name: 'secondary', used_percent: null, window_seconds: null, resets_at: null },
          ],
        },
      ],
      partial: false,
      reset_credits_available: null,
    },
  },
  local: {
    source: 'retained_instance_requests',
    availability: 'available',
    fetched_at: 1800000000000,
    error: null,
    data: {
      scope: 'all_codex_connections',
      window_start: 1799913600000,
      window_end: 1800000000000,
      requests: '3',
      input_tokens: '100',
      output_tokens: '20',
      cached_input_tokens: '30',
      cache_write_input_tokens: '0',
      reasoning_tokens: '5',
      unknown_count: '1',
    },
  },
};
beforeEach(() => {
  vi.clearAllMocks();
  fetch.mockResolvedValue(data);
});
async function mount() {
  return renderWithProviders(
    <DisplayTimeContext.Provider value={{ mode: 'site', offset_minutes: 0 }}>
      <CodexUsageCard
        account="test"
        active
        status={{
          connected: true,
          revision: '2',
          account_label: 'Account',
          account_id: 'workspace',
          expires_at: 1800000000,
        }}
      />
    </DisplayTimeContext.Provider>,
    { station: 'admin', role: 'admin' },
  );
}
it('shows actual windows, unknown quota and retained cross-account usage', async () => {
  await mount();
  await screen.findByText(/Used 25%/);
  expect(screen.getAllByRole('progressbar')).toHaveLength(1);
  expect(screen.getByText(text.localScope)).toBeVisible();
  expect(screen.getByText(text.timezone)).toBeVisible();
  expect(screen.getByRole('button', { name: text.records })).toBeEnabled();
});
it('preserves last valid quota during a failed refresh and retries locally', async () => {
  const view = await mount();
  await screen.findByText(/Used 25%/);
  fetch.mockResolvedValueOnce({
    ...data,
    official: {
      ...data.official,
      availability: 'unavailable',
      stale: true,
      error: {
        id: 'error-1',
        operation: 'codex_quota',
        source_name: 'Codex',
        site_status: 503,
        saved: false,
        details: {
          category: 'upstream',
          code: 'upstream_rate_limited',
          phase: 'upstream_response',
          upstream_status: 429,
          headers_received: true,
          causes: [],
          secondary: [],
          correlation: [],
          timings: [],
          incomplete: false,
        },
      },
    },
  });
  await view.user.click(screen.getByRole('button', { name: text.refresh }));
  await screen.findByText(text.stale);
  expect(screen.getByText(/Used 25%/)).toBeVisible();
  expect(
    screen.getByText(common.common.agent.diagnostic.reasons.upstream_rate_limited),
  ).toBeVisible();
  await view.user.click(screen.getByRole('button', { name: common.common.retry }));
  await waitFor(() => expect(fetch).toHaveBeenCalledTimes(3));
  expect(screen.queryByText(text.stale)).not.toBeInTheDocument();
});
it('withholds a late response belonging to a different connection', async () => {
  fetch.mockResolvedValueOnce({ ...data, official: { ...data.official, revision: '3' } });
  await mount();
  await screen.findByText(common.common.agent.diagnostic.reasons.connection_changed);
  expect(screen.queryByText(/Used 25%/)).not.toBeInTheDocument();
  expect(screen.queryByRole('progressbar')).not.toBeInTheDocument();
});
