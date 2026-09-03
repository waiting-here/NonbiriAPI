import { act, renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ReactNode } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { clearManagementSession } from '@shared/charityManagement';
import { ApiError } from '@shared/query/http';
import {
  economyKeys,
  useActivities,
  useActivityAccountEvents,
  useClaimWelfare,
  useContributeThursday,
  useCreateDonation,
  useEditDonation,
  useEndpointKeyChoices,
  useTerminateDonation,
  useWithdrawDonation,
} from './queries';
import type { ActivitiesSnapshot, Donation } from './types';

const mocks = vi.hoisted(() => ({
  claimWelfare: vi.fn(),
  connect: vi.fn(),
  contributeThursday: vi.fn(),
  createDonation: vi.fn(),
  editDonation: vi.fn(),
  getActivities: vi.fn(),
  getCharityCapability: vi.fn(),
  getDonation: vi.fn(),
  getDonations: vi.fn(),
  getEndpointChoices: vi.fn(),
  terminateDonation: vi.fn(),
  withdrawDonation: vi.fn(),
}));

vi.mock('./accountEvents', () => ({
  connectActivityAccountEvents: mocks.connect,
}));

vi.mock('./api', async (loadOriginal) => ({
  ...(await loadOriginal<typeof import('./api')>()),
  claimWelfare: mocks.claimWelfare,
  contributeThursday: mocks.contributeThursday,
  createDonation: mocks.createDonation,
  editDonation: mocks.editDonation,
  getActivities: mocks.getActivities,
  getCharityCapability: mocks.getCharityCapability,
  getDonation: mocks.getDonation,
  getDonations: mocks.getDonations,
  getEndpointChoices: mocks.getEndpointChoices,
  terminateDonation: mocks.terminateDonation,
  withdrawDonation: mocks.withdrawDonation,
}));

interface EventHandlers {
  onReplace: (snapshot: ActivitiesSnapshot, revision: string) => void;
  onResync: (reason: 'gap' | 'malformed' | 'disconnect' | 'reconnect') => void;
}

const OLD_SNAPSHOT: ActivitiesSnapshot = {
  master: { enabled: true, available: true, reason: 'available' },
  welfare: {
    enabled: true,
    state: 'available',
    siteDay: '2026-08-31',
    threshold: '10',
    cap: '2.5',
    poolBalance: '10',
    claimedToday: false,
  },
  thursday: {
    enabled: true,
    state: 'ended',
    serverNow: 1_788_200_000,
    current: null,
    next: null,
    lastResult: {
      periodId: 'thu_abcdefghijklmnopqrstuA',
      myCount: '1',
      myContributed: '50',
      payout: '60',
      unpaidReason: null,
    },
  },
};

const NEW_SNAPSHOT: ActivitiesSnapshot = {
  ...OLD_SNAPSHOT,
  welfare: { ...OLD_SNAPSHOT.welfare, state: 'claimed', claimedToday: true },
  thursday: { ...OLD_SNAPSHOT.thursday, lastResult: null },
};

const CHARITY_CAPABILITY = {
  state: 'available' as const,
  models: [],
  donationIntake: 'open' as const,
};

const DONATION: Donation = {
  id: '41',
  status: 'pending',
  revision: '9',
  description: 'same authority value',
  reviewResult: null,
  keys: [],
  createdAt: 1_788_100_000,
  updatedAt: 1_788_100_010,
};

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((complete) => {
    resolve = complete;
  });
  return { promise, resolve };
}

describe('activity query authority recovery', () => {
  let handlers: EventHandlers;
  let queryClient: QueryClient;

  beforeEach(() => {
    queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    queryClient.setQueryData(['user', 'session'], {
      user: { id: '7', username: 'tester', effective_level: 1 },
    });
    mocks.claimWelfare.mockReset();
    mocks.contributeThursday.mockReset();
    mocks.createDonation.mockReset();
    mocks.editDonation.mockReset();
    mocks.getActivities.mockReset();
    mocks.getCharityCapability.mockReset();
    mocks.getDonation.mockReset();
    mocks.getDonations.mockReset();
    mocks.getEndpointChoices.mockReset();
    mocks.terminateDonation.mockReset();
    mocks.withdrawDonation.mockReset();
    mocks.connect.mockReset();
    mocks.connect.mockImplementation((options: EventHandlers) => {
      handlers = options;
      return vi.fn();
    });
  });

  function wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
  }

  it('uses complete SSE replacement and prevents an older gap GET from overwriting it', async () => {
    queryClient.setQueryData(economyKeys.activities, OLD_SNAPSHOT);
    const request = deferred<ActivitiesSnapshot>();
    mocks.getActivities.mockReturnValue(request.promise);
    const rendered = renderHook(() => useActivityAccountEvents(true, '7'), { wrapper });
    await waitFor(() => expect(mocks.connect).toHaveBeenCalledTimes(1));

    act(() => handlers.onResync('gap'));
    await waitFor(() => expect(mocks.getActivities).toHaveBeenCalledTimes(1));
    act(() => handlers.onReplace(NEW_SNAPSHOT, '9'));
    expect(queryClient.getQueryData(economyKeys.activities)).toEqual(NEW_SNAPSHOT);
    expect(
      queryClient.getQueryData<ActivitiesSnapshot>(economyKeys.activities)?.thursday.lastResult,
    ).toBeNull();

    await act(async () => request.resolve(OLD_SNAPSHOT));
    await waitFor(() =>
      expect(queryClient.getQueryData(economyKeys.activities)).toEqual(NEW_SNAPSHOT),
    );
    rendered.unmount();
  });

  it('prevents an ordinary activities GET from overwriting a newer SSE snapshot', async () => {
    const request = deferred<ActivitiesSnapshot>();
    mocks.getActivities.mockReturnValue(request.promise);
    const rendered = renderHook(() => useActivities(), { wrapper });
    await waitFor(() => expect(mocks.getActivities).toHaveBeenCalledTimes(1));
    act(() => queryClient.setQueryData(economyKeys.activities, NEW_SNAPSHOT));
    await act(async () => {
      request.resolve(OLD_SNAPSHOT);
      await request.promise;
    });
    await waitFor(() =>
      expect(queryClient.getQueryData(economyKeys.activities)).toEqual(NEW_SNAPSHOT),
    );
    rendered.unmount();
  });

  it('drops an old account stream replacement after the station session closes', async () => {
    queryClient.setQueryData(economyKeys.activities, OLD_SNAPSHOT);
    const rendered = renderHook(() => useActivityAccountEvents(true, '7'), { wrapper });
    await waitFor(() => expect(mocks.connect).toHaveBeenCalledTimes(1));
    act(() => clearManagementSession(queryClient, 'steward'));
    act(() => handlers.onReplace(NEW_SNAPSHOT, '9'));
    expect(queryClient.getQueryData(economyKeys.activities)).toBeUndefined();
    rendered.unmount();
  });

  it('keeps the account session open for a domain feature_disabled 403', async () => {
    const rejected = new ApiError('feature_disabled', 'closed', 403);
    mocks.claimWelfare.mockRejectedValue(rejected);
    mocks.getActivities.mockResolvedValue(OLD_SNAPSHOT);
    const rendered = renderHook(() => useClaimWelfare(), { wrapper });
    let caught: unknown;
    await act(async () => {
      try {
        await rendered.result.current.mutateAsync();
      } catch (error) {
        caught = error;
      }
    });
    expect(caught).toBe(rejected);
    expect(queryClient.getQueryData<{ user: { id: string } }>(['user', 'session'])?.user.id).toBe(
      '7',
    );
    expect(mocks.getActivities).toHaveBeenCalledTimes(1);
    rendered.unmount();
  });

  it.each([
    ['409 conflict', new ApiError('conflict', 'revision changed', 409)],
    ['response unknown', new ApiError('network_error', 'response lost', 0)],
  ])(
    'reconciles an activity %s with one GET and never resubmits the mutation',
    async (_name, rejected) => {
      mocks.claimWelfare.mockRejectedValue(rejected);
      mocks.getActivities.mockResolvedValue(NEW_SNAPSHOT);
      const rendered = renderHook(() => useClaimWelfare(), { wrapper });
      await act(async () => {
        await expect(rendered.result.current.mutateAsync()).rejects.toBe(rejected);
      });
      await waitFor(() => expect(mocks.getActivities).toHaveBeenCalledTimes(1));
      await waitFor(() => expect(rendered.result.current.reconcileGeneration).toBe(1));
      expect(rendered.result.current.reconcileError).toBeNull();
      expect(mocks.claimWelfare).toHaveBeenCalledTimes(1);
      expect(queryClient.getQueryData(economyKeys.activities)).toEqual(NEW_SNAPSHOT);
      rendered.rerender();
      expect(mocks.claimWelfare).toHaveBeenCalledTimes(1);
      rendered.unmount();
    },
  );

  it.each([
    ['409 conflict', new ApiError('conflict', 'revision changed', 409)],
    ['response unknown', new ApiError('network_error', 'response lost', 0)],
  ])(
    'reconciles a donation %s with one GET and never resubmits the mutation',
    async (_name, rejected) => {
      mocks.createDonation.mockRejectedValue(rejected);
      mocks.getDonations.mockResolvedValue([]);
      mocks.getCharityCapability.mockResolvedValue(CHARITY_CAPABILITY);
      const rendered = renderHook(() => useCreateDonation(), { wrapper });
      await act(async () => {
        await expect(
          rendered.result.current.mutateAsync({
            description: 'one intent',
            keys: [{ endpointKeyId: '61', expiresAt: null }],
            ownershipAuthorized: true,
          }),
        ).rejects.toBe(rejected);
      });
      await waitFor(() => expect(mocks.getDonations).toHaveBeenCalledTimes(1));
      await waitFor(() => expect(rendered.result.current.reconcileGeneration).toBe(1));
      expect(rendered.result.current.reconcileError).toBeNull();
      expect(mocks.createDonation).toHaveBeenCalledTimes(1);
      expect(mocks.getCharityCapability).toHaveBeenCalledTimes(1);
      expect(queryClient.getQueryData(economyKeys.donations)).toEqual([]);
      expect(queryClient.getQueryData(economyKeys.charityCapability)).toEqual(CHARITY_CAPABILITY);
      rendered.rerender();
      expect(mocks.createDonation).toHaveBeenCalledTimes(1);
      rendered.unmount();
    },
  );

  it('keeps an activity reconcile generation blocked after GET failure and retries only the GET', async () => {
    const rejected = new ApiError('network_error', 'response lost', 0);
    const refreshFailure = new ApiError('network_error', 'refresh failed', 0);
    mocks.claimWelfare.mockRejectedValue(rejected);
    mocks.getActivities.mockRejectedValueOnce(refreshFailure).mockResolvedValueOnce(OLD_SNAPSHOT);
    const rendered = renderHook(() => useClaimWelfare(), { wrapper });
    await act(async () => {
      await expect(rendered.result.current.mutateAsync()).rejects.toBe(rejected);
    });
    await waitFor(() => expect(rendered.result.current.reconcileError).toBe(refreshFailure));
    expect(rendered.result.current.reconcileGeneration).toBe(0);
    expect(mocks.claimWelfare).toHaveBeenCalledTimes(1);

    await act(async () => rendered.result.current.retryReconcile());
    await waitFor(() => expect(rendered.result.current.reconcileGeneration).toBe(1));
    expect(rendered.result.current.reconcileError).toBeNull();
    expect(mocks.getActivities).toHaveBeenCalledTimes(2);
    expect(mocks.claimWelfare).toHaveBeenCalledTimes(1);
    rendered.unmount();
  });

  it('reconciles a Thursday response-unknown with one GET and never resubmits it', async () => {
    const rejected = new ApiError('network_error', 'response lost', 0);
    mocks.contributeThursday.mockRejectedValue(rejected);
    mocks.getActivities.mockResolvedValue(OLD_SNAPSHOT);
    const rendered = renderHook(() => useContributeThursday(), { wrapper });
    await act(async () => {
      await expect(
        rendered.result.current.mutateAsync({
          periodId: 'thu_abcdefghijklmnopqrstuA',
          expectedRevision: '7',
        }),
      ).rejects.toBe(rejected);
    });
    await waitFor(() => expect(rendered.result.current.reconcileGeneration).toBe(1));
    expect(rendered.result.current.reconcileError).toBeNull();
    expect(mocks.getActivities).toHaveBeenCalledTimes(1);
    expect(mocks.contributeThursday).toHaveBeenCalledTimes(1);
    rendered.rerender();
    expect(mocks.contributeThursday).toHaveBeenCalledTimes(1);
    rendered.unmount();
  });

  it('requires both donation and capability GET success before create reconcile advances', async () => {
    const rejected = new ApiError('conflict', 'final gate rejected', 409);
    const refreshFailure = new ApiError('network_error', 'capability refresh failed', 0);
    mocks.createDonation.mockRejectedValue(rejected);
    mocks.getDonations.mockResolvedValue([]);
    mocks.getCharityCapability
      .mockRejectedValueOnce(refreshFailure)
      .mockResolvedValueOnce(CHARITY_CAPABILITY);
    const rendered = renderHook(() => useCreateDonation(), { wrapper });
    await act(async () => {
      await expect(
        rendered.result.current.mutateAsync({
          description: 'one intent',
          keys: [{ endpointKeyId: '61', expiresAt: null }],
          ownershipAuthorized: true,
        }),
      ).rejects.toBe(rejected);
    });
    await waitFor(() => expect(rendered.result.current.reconcileError).toBe(refreshFailure));
    expect(rendered.result.current.reconcileGeneration).toBe(0);
    expect(mocks.createDonation).toHaveBeenCalledTimes(1);

    await act(async () => rendered.result.current.retryReconcile());
    await waitFor(() => expect(rendered.result.current.reconcileGeneration).toBe(1));
    expect(rendered.result.current.reconcileError).toBeNull();
    expect(mocks.getDonations).toHaveBeenCalledTimes(2);
    expect(mocks.getCharityCapability).toHaveBeenCalledTimes(2);
    expect(mocks.createDonation).toHaveBeenCalledTimes(1);
    expect(queryClient.getQueryData(economyKeys.charityCapability)).toEqual(CHARITY_CAPABILITY);
    rendered.unmount();
  });

  it('keeps the composer mounted and blocked until its endpoint-choice GET succeeds', async () => {
    const rejected = new ApiError('network_error', 'response lost', 0);
    const refreshFailure = new ApiError('network_error', 'choice refresh failed', 0);
    mocks.createDonation.mockRejectedValue(rejected);
    mocks.getDonations.mockResolvedValue([]);
    mocks.getCharityCapability.mockResolvedValue(CHARITY_CAPABILITY);
    mocks.getEndpointChoices
      .mockResolvedValueOnce([])
      .mockRejectedValueOnce(refreshFailure)
      .mockResolvedValueOnce([]);
    const rendered = renderHook(
      () => ({
        choices: useEndpointKeyChoices([], true),
        create: useCreateDonation(),
      }),
      { wrapper },
    );
    await waitFor(() => expect(rendered.result.current.choices.isSuccess).toBe(true));

    await act(async () => {
      await expect(
        rendered.result.current.create.mutateAsync({
          description: 'one intent',
          keys: [{ endpointKeyId: '61', expiresAt: null }],
          ownershipAuthorized: true,
        }),
      ).rejects.toBe(rejected);
    });
    await waitFor(() => expect(rendered.result.current.create.reconcileError).toBe(refreshFailure));
    expect(rendered.result.current.create.reconcileGeneration).toBe(0);
    expect(rendered.result.current.choices.error).toBeNull();
    expect(mocks.createDonation).toHaveBeenCalledTimes(1);

    await act(async () => rendered.result.current.create.retryReconcile());
    await waitFor(() => expect(rendered.result.current.create.reconcileGeneration).toBe(1));
    expect(rendered.result.current.create.reconcileError).toBeNull();
    expect(mocks.getEndpointChoices).toHaveBeenCalledTimes(3);
    expect(mocks.createDonation).toHaveBeenCalledTimes(1);
    rendered.unmount();
  });

  it('advances edit, withdraw, and terminate only from their successful list/detail GETs', async () => {
    const rejected = new ApiError('network_error', 'response lost', 0);
    mocks.getDonations.mockResolvedValue([DONATION]);
    mocks.getDonation.mockResolvedValue(DONATION);

    mocks.editDonation.mockRejectedValue(rejected);
    const edit = renderHook(() => useEditDonation(), { wrapper });
    await act(async () => {
      await expect(
        edit.result.current.mutateAsync({
          id: DONATION.id,
          description: DONATION.description,
          expectedRevision: DONATION.revision,
        }),
      ).rejects.toBe(rejected);
    });
    await waitFor(() => expect(edit.result.current.reconcileGeneration).toBe(1));
    expect(edit.result.current.reconcileError).toBeNull();
    expect(mocks.editDonation).toHaveBeenCalledTimes(1);
    edit.unmount();

    mocks.withdrawDonation.mockRejectedValue(rejected);
    const withdraw = renderHook(() => useWithdrawDonation(), { wrapper });
    await act(async () => {
      await expect(
        withdraw.result.current.mutateAsync({
          id: DONATION.id,
          expectedRevision: DONATION.revision,
        }),
      ).rejects.toBe(rejected);
    });
    await waitFor(() => expect(withdraw.result.current.reconcileGeneration).toBe(1));
    expect(withdraw.result.current.reconcileError).toBeNull();
    expect(mocks.withdrawDonation).toHaveBeenCalledTimes(1);
    withdraw.unmount();

    mocks.terminateDonation.mockRejectedValue(rejected);
    const terminate = renderHook(() => useTerminateDonation(), { wrapper });
    await act(async () => {
      await expect(
        terminate.result.current.mutateAsync({
          id: DONATION.id,
          expectedRevision: DONATION.revision,
        }),
      ).rejects.toBe(rejected);
    });
    await waitFor(() => expect(terminate.result.current.reconcileGeneration).toBe(1));
    expect(terminate.result.current.reconcileError).toBeNull();
    expect(mocks.terminateDonation).toHaveBeenCalledTimes(1);
    expect(mocks.getDonations).toHaveBeenCalledTimes(3);
    expect(mocks.getDonation).toHaveBeenCalledTimes(3);
    terminate.unmount();
  });

  it('closes the station on a true forbidden authentication boundary', async () => {
    const rejected = new ApiError('forbidden', 'closed', 403);
    mocks.claimWelfare.mockRejectedValue(rejected);
    queryClient.setQueryData(economyKeys.activities, OLD_SNAPSHOT);
    const rendered = renderHook(() => useClaimWelfare(), { wrapper });
    let caught: unknown;
    await act(async () => {
      try {
        await rendered.result.current.mutateAsync();
      } catch (error) {
        caught = error;
      }
    });
    expect(caught).toBe(rejected);
    expect(queryClient.getQueryData(['user', 'session'])).toBeNull();
    expect(queryClient.getQueryData(economyKeys.activities)).toBeUndefined();
    expect(mocks.getActivities).not.toHaveBeenCalled();
    rendered.unmount();
  });
});
