import { screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '@shared/query/http';
import { renderWithProviders } from '../../../../test/unit/support';
import { ActivitiesPage } from '../../pages/ActivitiesPage';
import { ThursdayCard, WelfareCard } from './ActivitiesPanels';
import * as economyQueries from './queries';
import type {
  ActivitiesSnapshot,
  ThursdayContributionResult,
  ThursdayView,
  WelfareClaimResult,
  WelfareView,
} from './types';

const pageMocks = vi.hoisted(() => ({
  useUserSession: vi.fn(),
}));

vi.mock('../../data', async (loadOriginal) => ({
  ...(await loadOriginal<typeof import('../../data')>()),
  useUserSession: pageMocks.useUserSession,
}));

vi.mock('./queries', async (loadOriginal) => ({
  ...(await loadOriginal<typeof import('./queries')>()),
  useActivities: vi.fn(),
  useActivityAccountEvents: vi.fn(),
  useClaimWelfare: vi.fn(),
  useContributeThursday: vi.fn(),
}));

function mutationResult() {
  return {
    data: undefined as WelfareClaimResult | ThursdayContributionResult | undefined,
    error: null as unknown,
    isPending: false,
    reconcileGeneration: 0,
    reconcileError: null as unknown,
    isReconciling: false,
    retryReconcile: vi.fn(() => Promise.resolve()),
    reset: vi.fn(),
    mutateAsync: vi.fn(),
  };
}

let claimMutation = mutationResult();
let contributionMutation = mutationResult();

const welfare: WelfareView = {
  enabled: true,
  state: 'empty',
  siteDay: '2026-08-31',
  threshold: '10',
  cap: '2.5',
  poolBalance: '0.009',
  claimedToday: false,
};

const thursday: ThursdayView = {
  enabled: true,
  state: 'open',
  serverNow: 1_788_111_000,
  current: {
    periodId: 'thu_abcdefghijklmnopqrstuA',
    revision: '7',
    opensAt: 1_788_110_000,
    closesAt: 1_788_196_400,
    literature: '<img src=x onerror=alert(1)> **not markdown**',
    entry: '50.001',
    perUserLimit: 3,
    poolBalance: '12345678901234567890.123',
    myCount: '1',
    myContributed: '50.001',
  },
  next: null,
  lastResult: null,
};

describe('activity cards', () => {
  beforeEach(() => {
    claimMutation = mutationResult();
    contributionMutation = mutationResult();
    vi.mocked(economyQueries.useClaimWelfare).mockReturnValue(claimMutation as never);
    vi.mocked(economyQueries.useContributeThursday).mockReturnValue(contributionMutation as never);
    pageMocks.useUserSession.mockReturnValue({
      data: { user: { id: '7', username: 'owner', effective_level: 1 } },
      error: null,
      isPending: false,
      refetch: vi.fn(),
    });
  });

  it('keeps actual page locks mounted across ordinary SSE day and period replacement', async () => {
    const unknown = new ApiError('network_error', 'response lost', 0);
    claimMutation = {
      ...mutationResult(),
      error: unknown,
      mutateAsync: vi.fn(() => Promise.reject(unknown)),
    };
    contributionMutation = {
      ...mutationResult(),
      error: unknown,
      mutateAsync: vi.fn(() => Promise.reject(unknown)),
    };
    vi.mocked(economyQueries.useClaimWelfare).mockReturnValue(claimMutation as never);
    vi.mocked(economyQueries.useContributeThursday).mockReturnValue(contributionMutation as never);
    let snapshot: ActivitiesSnapshot = {
      master: { enabled: true, available: true, reason: 'available' },
      welfare: { ...welfare, state: 'available', poolBalance: '10' },
      thursday,
    };
    vi.mocked(economyQueries.useActivities).mockImplementation(
      () =>
        ({
          data: snapshot,
          error: null,
          isPending: false,
          refetch: vi.fn(),
        }) as never,
    );
    vi.mocked(economyQueries.useActivityAccountEvents).mockReturnValue({
      connection: 'connected',
      recoveryError: null,
      reconciledAt: 0,
    });
    const rendered = await renderWithProviders(<ActivitiesPage />, {
      station: 'user',
      role: 'user',
    });
    const actionName = 'user.activities.thursday.contributeOnce';
    const welfareActionName = 'user.activities.welfare.claim';
    await rendered.user.click(screen.getByRole('button', { name: welfareActionName }));
    await rendered.user.click(screen.getByRole('button', { name: actionName }));
    expect(screen.getByRole('button', { name: welfareActionName })).toBeDisabled();
    expect(screen.getByRole('button', { name: actionName })).toBeDisabled();

    snapshot = {
      ...snapshot,
      welfare: { ...snapshot.welfare, siteDay: '2026-09-01', poolBalance: '9' },
      thursday: {
        ...thursday,
        current: thursday.current
          ? {
              ...thursday.current,
              periodId: 'thu_abcdefghijklmnopqrstuB',
              revision: '8',
            }
          : null,
      },
    };
    rendered.rerender(<ActivitiesPage />);
    expect(screen.getByRole('button', { name: welfareActionName })).toBeDisabled();
    expect(screen.getByRole('button', { name: actionName })).toBeDisabled();
    expect(claimMutation.mutateAsync).toHaveBeenCalledTimes(1);
    expect(contributionMutation.mutateAsync).toHaveBeenCalledTimes(1);

    claimMutation.reconcileGeneration = 1;
    contributionMutation.reconcileGeneration = 1;
    rendered.rerender(<ActivitiesPage />);
    expect(screen.getByRole('button', { name: welfareActionName })).toBeEnabled();
    expect(screen.getByRole('button', { name: actionName })).toBeEnabled();
    expect(screen.getAllByText('user.activities.mutationReconciled')).toHaveLength(2);
  });

  it('disables a zero welfare award without consuming an intent', async () => {
    await renderWithProviders(<WelfareCard welfare={welfare} masterAvailable />, {
      station: 'user',
      role: 'user',
    });
    expect(screen.getByRole('button', { name: 'user.activities.welfare.claim' })).toBeDisabled();
    expect(screen.getByText('0', { exact: true })).toBeInTheDocument();
    expect(claimMutation.mutateAsync).not.toHaveBeenCalled();
  });

  it('does not describe a raced zero-award response as a consumed claim', async () => {
    claimMutation = {
      ...mutationResult(),
      data: {
        awarded: '0',
        balance: '1',
        poolBalance: '0.009',
        siteDay: '2026-08-31',
      },
    };
    vi.mocked(economyQueries.useClaimWelfare).mockReturnValue(claimMutation as never);
    await renderWithProviders(<WelfareCard welfare={welfare} masterAvailable />, {
      station: 'user',
      role: 'user',
    });
    expect(screen.getAllByText('user.activities.welfare.zeroAward')).toHaveLength(2);
    expect(screen.queryByText('user.activities.welfare.claimedAmount')).toBeNull();
  });

  it('renders literature as text, exposes one fixed action, and hides internal ids', async () => {
    const rendered = await renderWithProviders(
      <ThursdayCard thursday={thursday} masterAvailable />,
      { station: 'user', role: 'user' },
    );
    expect(screen.getByText('<img src=x onerror=alert(1)> **not markdown**')).toBeInTheDocument();
    expect(rendered.container.querySelector('img')).toBeNull();
    expect(rendered.container.querySelector('input')).toBeNull();
    expect(screen.getAllByRole('button')).toHaveLength(1);
    expect(rendered.container.textContent).toContain('50.001');
    expect(rendered.container.textContent).toContain('12,345,678,901,234,567,890.123');
    expect(rendered.container.textContent).not.toContain(thursday.current?.periodId ?? 'period-id');
    expect(rendered.container.textContent).not.toMatch(
      /_milli|participant|source_events|raw_secret/i,
    );
  });

  it('keeps Thursday locked across changed values and unlocks after a same-value successful GET', async () => {
    const unknown = new ApiError('network_error', 'response lost', 0);
    contributionMutation = {
      ...mutationResult(),
      error: unknown,
      reconcileError: new ApiError('network_error', 'refresh failed', 0),
      mutateAsync: vi.fn(() => Promise.reject(unknown)),
    };
    vi.mocked(economyQueries.useContributeThursday).mockReturnValue(contributionMutation as never);
    const rendered = await renderWithProviders(
      <ThursdayCard thursday={thursday} masterAvailable />,
      { station: 'user', role: 'user' },
    );
    const button = screen.getByRole('button');
    await rendered.user.click(button);
    expect(button).toBeDisabled();
    await rendered.user.click(button);
    expect(contributionMutation.mutateAsync).toHaveBeenCalledTimes(1);
    rendered.rerender(
      <ThursdayCard
        thursday={{
          ...thursday,
          current: thursday.current
            ? {
                ...thursday.current,
                revision: '8',
                poolBalance: '12345678901234567840.122',
              }
            : null,
        }}
        masterAvailable
      />,
    );
    expect(
      screen.getByRole('button', { name: 'user.activities.thursday.contributeOnce' }),
    ).toBeDisabled();
    await rendered.user.click(screen.getByRole('button', { name: /retry/i }));
    expect(contributionMutation.retryReconcile).toHaveBeenCalledTimes(1);
    expect(contributionMutation.mutateAsync).toHaveBeenCalledTimes(1);
    contributionMutation.reconcileGeneration = 1;
    contributionMutation.reconcileError = null;
    rendered.rerender(<ThursdayCard thursday={thursday} masterAvailable />);
    expect(
      screen.getByRole('button', { name: 'user.activities.thursday.contributeOnce' }),
    ).toBeEnabled();
    expect(screen.getByText('user.activities.mutationReconciled')).toBeInTheDocument();
    await rendered.user.click(
      screen.getByRole('button', { name: 'user.activities.thursday.contributeOnce' }),
    );
    expect(contributionMutation.mutateAsync).toHaveBeenCalledTimes(2);
  });

  it('keeps welfare locked across changed values and unlocks after a same-value successful GET', async () => {
    const unknown = new ApiError('network_error', 'response lost', 0);
    claimMutation = {
      ...mutationResult(),
      error: unknown,
      reconcileError: new ApiError('network_error', 'refresh failed', 0),
      mutateAsync: vi.fn(() => Promise.reject(unknown)),
    };
    vi.mocked(economyQueries.useClaimWelfare).mockReturnValue(claimMutation as never);
    const available = {
      ...welfare,
      state: 'available' as const,
      poolBalance: '10',
    };
    const rendered = await renderWithProviders(
      <WelfareCard welfare={available} masterAvailable />,
      { station: 'user', role: 'user' },
    );
    await rendered.user.click(screen.getByRole('button'));
    expect(screen.getByRole('button', { name: 'user.activities.welfare.claim' })).toBeDisabled();
    rendered.rerender(<WelfareCard welfare={{ ...available, poolBalance: '9' }} masterAvailable />);
    expect(screen.getByRole('button', { name: 'user.activities.welfare.claim' })).toBeDisabled();
    rendered.rerender(
      <WelfareCard
        welfare={{ ...available, siteDay: '2026-09-01', poolBalance: '9' }}
        masterAvailable
      />,
    );
    expect(screen.getByRole('button', { name: 'user.activities.welfare.claim' })).toBeDisabled();
    await rendered.user.click(screen.getByRole('button', { name: /retry/i }));
    expect(claimMutation.retryReconcile).toHaveBeenCalledTimes(1);
    expect(claimMutation.mutateAsync).toHaveBeenCalledTimes(1);
    claimMutation.reconcileGeneration = 1;
    claimMutation.reconcileError = null;
    rendered.rerender(<WelfareCard welfare={available} masterAvailable />);
    expect(screen.getByRole('button', { name: 'user.activities.welfare.claim' })).toBeEnabled();
    expect(screen.getByText('user.activities.mutationReconciled')).toBeInTheDocument();
    await rendered.user.click(
      screen.getByRole('button', { name: 'user.activities.welfare.claim' }),
    );
    expect(claimMutation.mutateAsync).toHaveBeenCalledTimes(2);
  });

  it('shows a settled result only until the next period becomes open', async () => {
    const lastResult = {
      periodId: 'thu_abcdefghijklmnopqrstuQ',
      myCount: '2',
      myContributed: '100.002',
      payout: '125.003',
      unpaidReason: null,
    } as const;
    const rendered = await renderWithProviders(
      <ThursdayCard
        thursday={{ ...thursday, state: 'ended', current: null, lastResult }}
        masterAvailable
      />,
      { station: 'user', role: 'user' },
    );
    expect(screen.getByText('user.activities.thursday.lastResultTitle')).toBeInTheDocument();
    rendered.rerender(
      <ThursdayCard
        thursday={{
          ...thursday,
          state: 'settling',
          serverNow: thursday.current?.closesAt ?? thursday.serverNow,
          lastResult,
        }}
        masterAvailable
      />,
    );
    expect(screen.queryByText('user.activities.thursday.lastResultTitle')).toBeNull();
    rendered.rerender(
      <ThursdayCard thursday={{ ...thursday, state: 'open', lastResult }} masterAvailable />,
    );
    expect(screen.queryByText('user.activities.thursday.lastResultTitle')).toBeNull();
  });
});
