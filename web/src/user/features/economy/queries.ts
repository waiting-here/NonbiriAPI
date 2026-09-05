import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useMutation, useQuery, useQueryClient, type QueryClient } from '@tanstack/react-query';
import {
  captureStationSession,
  clearStationSession,
  isStationSessionChanged,
  StationSessionChangedError,
  stationSessionMatches,
} from '@shared/charityManagement';
import { isApiError, isUnauthorized } from '@shared/query/http';
import {
  claimWelfare,
  contributeThursday,
  createDonation,
  editDonation,
  getActivities,
  getCharityCapability,
  getDonation,
  getDonations,
  getEndpointChoices,
  terminateDonation,
  withdrawDonation,
  type CreateDonationInput,
} from './api';
import { connectActivityAccountEvents } from './accountEvents';
import type { ActivitiesSnapshot, Donation } from './types';

export const economyKeys = {
  all: ['user', 'economy'] as const,
  charityCapability: ['user', 'economy', 'charity-capability'] as const,
  donations: ['user', 'economy', 'donations'] as const,
  donation: (id: string) => ['user', 'economy', 'donations', id] as const,
  endpointChoices: (membershipSignature: string) =>
    ['user', 'economy', 'endpoint-choices', membershipSignature] as const,
  endpointChoicesRoot: ['user', 'economy', 'endpoint-choices'] as const,
  activities: ['user', 'economy', 'activities'] as const,
};

async function economySessionRequest<T>(
  queryClient: QueryClient,
  request: () => Promise<T>,
): Promise<T> {
  const station = captureStationSession(queryClient, 'steward');
  try {
    const value = await request();
    if (!stationSessionMatches(queryClient, 'steward', station)) {
      throw new StationSessionChangedError();
    }
    return value;
  } catch (error) {
    if (!stationSessionMatches(queryClient, 'steward', station)) {
      throw new StationSessionChangedError();
    }
    if (isUnauthorized(error) || (isApiError(error) && error.code === 'forbidden')) {
      clearStationSession(queryClient, 'steward');
    }
    throw error;
  }
}

export function useCharityCapability(enabled = true) {
  const queryClient = useQueryClient();
  return useQuery({
    queryKey: economyKeys.charityCapability,
    queryFn: ({ signal }) => economySessionRequest(queryClient, () => getCharityCapability(signal)),
    enabled,
    staleTime: 15_000,
  });
}

export function useDonations(enabled = true) {
  const queryClient = useQueryClient();
  return useQuery({
    queryKey: economyKeys.donations,
    queryFn: ({ signal }) => economySessionRequest(queryClient, () => getDonations(signal)),
    enabled,
    staleTime: 5_000,
  });
}

export function useDonation(id: string | undefined, enabled = true) {
  const queryClient = useQueryClient();
  return useQuery({
    queryKey: id ? economyKeys.donation(id) : ([...economyKeys.donations, 'none'] as const),
    queryFn: ({ signal }) => {
      if (!id) throw new Error('A donation id is required.');
      return economySessionRequest(queryClient, () => getDonation(id, signal));
    },
    enabled: enabled && Boolean(id),
  });
}

function membershipSignature(donations: readonly Donation[]): string {
  return donations
    .filter((donation) => donation.status === 'pending' || donation.status === 'approved')
    .flatMap((donation) => donation.keys)
    .flatMap((key) => (key.endpointKeyId ? [key.endpointKeyId] : []))
    .sort((left, right) =>
      BigInt(left) < BigInt(right) ? -1 : BigInt(left) > BigInt(right) ? 1 : 0,
    )
    .join('|');
}

export function useEndpointKeyChoices(donations: readonly Donation[], enabled: boolean) {
  const queryClient = useQueryClient();
  const signature = useMemo(() => membershipSignature(donations), [donations]);
  return useQuery({
    queryKey: economyKeys.endpointChoices(signature),
    queryFn: ({ signal }) =>
      economySessionRequest(queryClient, () => getEndpointChoices(donations, signal)),
    enabled,
    staleTime: 5_000,
    placeholderData: (previous) => previous,
  });
}

async function reconcileDonations(
  queryClient: QueryClient,
  donationId?: string,
  includeCapability = false,
): Promise<void> {
  const includeEndpointChoices = queryClient
    .getQueryCache()
    .findAll({ queryKey: economyKeys.endpointChoicesRoot, exact: false })
    .some((query) => query.isActive());
  await queryClient.cancelQueries({ queryKey: economyKeys.donations, exact: false });
  if (includeCapability) {
    await queryClient.cancelQueries({ queryKey: economyKeys.charityCapability, exact: true });
  }
  if (includeEndpointChoices) {
    await queryClient.cancelQueries({ queryKey: economyKeys.endpointChoicesRoot, exact: false });
  }
  const [donationValues, donationValue, capabilityValue, endpointChoices] =
    await economySessionRequest(queryClient, async () => {
      const [donationValues, donationValue, capabilityValue] = await Promise.all([
        getDonations(),
        donationId ? getDonation(donationId) : Promise.resolve(null),
        includeCapability ? getCharityCapability() : Promise.resolve(null),
      ]);
      const endpointChoices = includeEndpointChoices
        ? await getEndpointChoices(donationValues)
        : null;
      return [donationValues, donationValue, capabilityValue, endpointChoices] as const;
    });
  queryClient.setQueryData(economyKeys.donations, donationValues);
  if (donationId && donationValue) {
    queryClient.setQueryData(economyKeys.donation(donationId), donationValue);
  }
  if (capabilityValue) {
    queryClient.setQueryData(economyKeys.charityCapability, capabilityValue);
  }
  if (endpointChoices) {
    queryClient.setQueryData(
      economyKeys.endpointChoices(membershipSignature(donationValues)),
      endpointChoices,
    );
  }
}

function isStationBoundaryError(error: unknown): boolean {
  return (
    isStationSessionChanged(error) ||
    isUnauthorized(error) ||
    (isApiError(error) && error.code === 'forbidden')
  );
}

interface AuthoritativeReconciliation {
  reconcileGeneration: number;
  reconcileError: unknown;
  isReconciling: boolean;
  runReconcile: (request: () => Promise<unknown>) => Promise<void>;
  retryReconcile: () => Promise<void>;
}

function useAuthoritativeReconciliation(): AuthoritativeReconciliation {
  const [reconcileGeneration, setReconcileGeneration] = useState(0);
  const [reconcileError, setReconcileError] = useState<unknown>(null);
  const [isReconciling, setIsReconciling] = useState(false);
  const requestRef = useRef<(() => Promise<unknown>) | null>(null);
  const inFlightRef = useRef<Promise<void> | null>(null);

  const runReconcile = useCallback((request: () => Promise<unknown>) => {
    requestRef.current = request;
    if (inFlightRef.current) return inFlightRef.current;
    setIsReconciling(true);
    setReconcileError(null);
    const operation = Promise.resolve()
      .then(request)
      .then(() => {
        setReconcileGeneration((current) => current + 1);
        setReconcileError(null);
      })
      .catch((error: unknown) => {
        setReconcileError(error);
      })
      .finally(() => {
        if (inFlightRef.current === operation) inFlightRef.current = null;
        setIsReconciling(false);
      });
    inFlightRef.current = operation;
    return operation;
  }, []);

  const retryReconcile = useCallback(() => {
    const request = requestRef.current;
    return request ? runReconcile(request) : Promise.resolve();
  }, [runReconcile]);

  return {
    reconcileGeneration,
    reconcileError,
    isReconciling,
    runReconcile,
    retryReconcile,
  };
}

export function useCreateDonation() {
  const queryClient = useQueryClient();
  const reconciliation = useAuthoritativeReconciliation();
  const mutation = useMutation({
    mutationFn: (input: CreateDonationInput) =>
      economySessionRequest(queryClient, () => createDonation(input)),
    retry: false,
    onSettled: (_data, error) =>
      isStationBoundaryError(error)
        ? undefined
        : reconciliation.runReconcile(() => reconcileDonations(queryClient, undefined, true)),
  });
  return { ...mutation, ...reconciliation };
}

export function useEditDonation() {
  const queryClient = useQueryClient();
  const reconciliation = useAuthoritativeReconciliation();
  const mutation = useMutation({
    mutationFn: ({
      id,
      description,
      expectedRevision,
    }: {
      id: string;
      description: string;
      expectedRevision: string;
    }) => economySessionRequest(queryClient, () => editDonation(id, description, expectedRevision)),
    retry: false,
    onSettled: (_data, error, variables) =>
      isStationBoundaryError(error)
        ? undefined
        : reconciliation.runReconcile(() => reconcileDonations(queryClient, variables.id)),
  });
  return { ...mutation, ...reconciliation };
}

export function useWithdrawDonation() {
  const queryClient = useQueryClient();
  const reconciliation = useAuthoritativeReconciliation();
  const mutation = useMutation({
    mutationFn: ({ id, expectedRevision }: { id: string; expectedRevision: string }) =>
      economySessionRequest(queryClient, () => withdrawDonation(id, expectedRevision)),
    retry: false,
    onSettled: (_data, error, variables) =>
      isStationBoundaryError(error)
        ? undefined
        : reconciliation.runReconcile(() => reconcileDonations(queryClient, variables.id)),
  });
  return { ...mutation, ...reconciliation };
}

export function useTerminateDonation() {
  const queryClient = useQueryClient();
  const reconciliation = useAuthoritativeReconciliation();
  const mutation = useMutation({
    mutationFn: ({ id, expectedRevision }: { id: string; expectedRevision: string }) =>
      economySessionRequest(queryClient, () => terminateDonation(id, expectedRevision)),
    retry: false,
    onSettled: (_data, error, variables) =>
      isStationBoundaryError(error)
        ? undefined
        : reconciliation.runReconcile(() => reconcileDonations(queryClient, variables.id)),
  });
  return { ...mutation, ...reconciliation };
}

export function useActivities(enabled = true) {
  const queryClient = useQueryClient();
  return useQuery({
    queryKey: economyKeys.activities,
    queryFn: async ({ signal }) => {
      const beforeData = queryClient.getQueryData<ActivitiesSnapshot>(economyKeys.activities);
      const beforeUpdatedAt =
        queryClient.getQueryState<ActivitiesSnapshot>(economyKeys.activities)?.dataUpdatedAt ?? 0;
      const snapshot = await economySessionRequest(queryClient, () => getActivities(signal));
      const currentData = queryClient.getQueryData<ActivitiesSnapshot>(economyKeys.activities);
      const currentUpdatedAt =
        queryClient.getQueryState<ActivitiesSnapshot>(economyKeys.activities)?.dataUpdatedAt ?? 0;
      return currentData !== undefined &&
        (currentData !== beforeData || currentUpdatedAt !== beforeUpdatedAt)
        ? currentData
        : snapshot;
    },
    enabled,
    staleTime: 0,
  });
}

async function reconcileActivities(
  queryClient: QueryClient,
  signal?: AbortSignal,
): Promise<ActivitiesSnapshot> {
  await queryClient.cancelQueries({ queryKey: economyKeys.activities, exact: true });
  const beforeData = queryClient.getQueryData<ActivitiesSnapshot>(economyKeys.activities);
  const beforeUpdatedAt =
    queryClient.getQueryState<ActivitiesSnapshot>(economyKeys.activities)?.dataUpdatedAt ?? 0;
  const snapshot = await economySessionRequest(queryClient, () => getActivities(signal));
  if (signal?.aborted) return snapshot;
  const currentData = queryClient.getQueryData<ActivitiesSnapshot>(economyKeys.activities);
  const currentUpdatedAt =
    queryClient.getQueryState<ActivitiesSnapshot>(economyKeys.activities)?.dataUpdatedAt ?? 0;
  if (currentData === beforeData && currentUpdatedAt === beforeUpdatedAt) {
    queryClient.setQueryData(economyKeys.activities, snapshot);
  }
  return snapshot;
}

export function useClaimWelfare() {
  const queryClient = useQueryClient();
  const reconciliation = useAuthoritativeReconciliation();
  const mutation = useMutation({
    mutationFn: () => economySessionRequest(queryClient, claimWelfare),
    retry: false,
    onSettled: (_data, error) =>
      isStationBoundaryError(error)
        ? undefined
        : reconciliation.runReconcile(() => reconcileActivities(queryClient)),
  });
  return { ...mutation, ...reconciliation };
}

export function useContributeThursday() {
  const queryClient = useQueryClient();
  const reconciliation = useAuthoritativeReconciliation();
  const mutation = useMutation({
    mutationFn: ({ periodId, expectedRevision }: { periodId: string; expectedRevision: string }) =>
      economySessionRequest(queryClient, () => contributeThursday(periodId, expectedRevision)),
    retry: false,
    onSettled: (_data, error) =>
      isStationBoundaryError(error)
        ? undefined
        : reconciliation.runReconcile(() => reconcileActivities(queryClient)),
  });
  return { ...mutation, ...reconciliation };
}

export type ActivityConnectionState = 'connecting' | 'connected' | 'disconnected';

export function useActivityAccountEvents(enabled = true, identity = '', sessionUpdatedAt = 0) {
  const queryClient = useQueryClient();
  const [connection, setConnection] = useState<ActivityConnectionState>('connecting');
  const [recoveryError, setRecoveryError] = useState<unknown>(null);
  const [reconciledAt, setReconciledAt] = useState(0);

  useEffect(() => {
    if (!enabled) return;
    let station: ReturnType<typeof captureStationSession>;
    try {
      station = captureStationSession(queryClient, 'steward');
    } catch {
      return;
    }
    let alive = true;
    const stationCurrent = () => alive && stationSessionMatches(queryClient, 'steward', station);
    let resyncRequest: Promise<void> | null = null;
    const controller = new AbortController();
    const resync = () => {
      if (!stationCurrent()) return Promise.resolve();
      if (resyncRequest) return resyncRequest;
      const request = reconcileActivities(queryClient, controller.signal)
        .then(() => {
          if (!alive || !stationCurrent()) return;
          setRecoveryError(null);
          setReconciledAt(Date.now());
        })
        .catch((error: unknown) => {
          if (alive && stationCurrent()) setRecoveryError(error);
        })
        .finally(() => {
          if (resyncRequest === request) resyncRequest = null;
        });
      resyncRequest = request;
      return request;
    };
    const disconnect = connectActivityAccountEvents({
      onReplace: (snapshot) => {
        if (!stationCurrent()) return;
        void queryClient.cancelQueries({ queryKey: economyKeys.activities, exact: true });
        queryClient.setQueryData(economyKeys.activities, snapshot);
        setRecoveryError(null);
      },
      onResync: () => {
        void resync();
      },
      onConnectionChange: (state) => {
        if (stationCurrent()) setConnection(state);
      },
    });
    const online = () => {
      void resync();
    };
    window.addEventListener('online', online);
    return () => {
      alive = false;
      controller.abort();
      window.removeEventListener('online', online);
      disconnect();
    };
  }, [enabled, identity, queryClient, sessionUpdatedAt]);

  return { connection, recoveryError, reconciledAt };
}
