import { act, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { SmartBackoff } from '@shared/operations/smartBackoff';
import { renderWithProviders } from '../../../test/unit/support';
import { KeyRateLimitTime } from './KeyRateLimitTime';

const now = 1_800_000_000;
const backoff: SmartBackoff = {
  state: 'waiting',
  reason: 'day_quota',
  source: 'custom_rule',
  retry_at: now + 7200,
};
afterEach(() => vi.useRealTimers());

describe('key recovery status', () => {
  it('shows independent upstream and quota waits with their own times and historical source', async () => {
    vi.spyOn(Date, 'now').mockReturnValue(now * 1000);
    await renderWithProviders(<KeyRateLimitTime until={now + 3600} smartBackoff={backoff} />, {
      station: 'user',
    });
    const status = screen.getByRole('region', { name: 'Recovery status' });
    expect(within(status).getByText('Upstream wait')).toBeInTheDocument();
    expect(within(status).getByText('Smart backoff')).toBeInTheDocument();
    expect(within(status).getByText('Waiting for recovery')).toBeInTheDocument();
    expect(
      within(status).getByText('Daily allowance exhausted · Historical rule'),
    ).toBeInTheDocument();
    expect(within(status).getByText(/Both waits apply independently/)).toBeInTheDocument();
    expect(Array.from(status.querySelectorAll('time')).map((time) => time.dateTime)).toEqual([
      new Date((now + 3600) * 1000).toISOString(),
      new Date(backoff.retry_at * 1000).toISOString(),
    ]);
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it.each([
    ['ready', 'Ready for a trial request'],
    ['probing', 'Trial request in progress'],
  ] as const)(
    'retains the server-owned %s state after the header wait ended',
    async (state, label) => {
      vi.spyOn(Date, 'now').mockReturnValue(now * 1000);
      await renderWithProviders(
        <KeyRateLimitTime
          until={now - 60}
          smartBackoff={{ ...backoff, state, source: 'builtin' }}
        />,
        { station: 'admin' },
      );
      expect(screen.getByText(label)).toBeInTheDocument();
      expect(screen.queryByText('Upstream wait')).not.toBeInTheDocument();
      expect(
        screen.getByText('Daily allowance exhausted · Built-in recognition'),
      ).toBeInTheDocument();
    },
  );

  it('removes an expired header-only area without leaving an empty frame', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(now * 1000);
    const view = await renderWithProviders(<KeyRateLimitTime until={now + 60} />, {
      station: 'user',
    });
    expect(screen.getByRole('region', { name: 'Recovery status' })).toBeInTheDocument();
    await act(async () => vi.advanceTimersByTime(60_000));
    expect(screen.queryByRole('region', { name: 'Recovery status' })).not.toBeInTheDocument();
    view.rerender(<KeyRateLimitTime until={now - 60} />);
    expect(screen.queryByRole('region', { name: 'Recovery status' })).not.toBeInTheDocument();
  });
});
