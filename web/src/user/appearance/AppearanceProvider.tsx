import { useCallback, useLayoutEffect, useMemo, type ReactNode } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiFetch } from '@shared/query/http';
import {
  captureStationSession,
  stationSessionMatches,
  StationSessionChangedError,
} from '@shared/charityManagement';
import {
  AppearanceContext,
  appearanceKey,
  type AppearancePort,
  type AppearanceState,
} from './context';
import './appearance.css';

const defaults: AppearanceState = { selected: 'default', unlocked: ['default'], updated_at: null };

export function AppearanceProvider({
  port,
  children,
}: {
  port: AppearancePort | null;
  children: ReactNode;
}) {
  const client = useQueryClient();
  const scope = port?.scope ?? 'anonymous';
  const key = appearanceKey(scope);
  const query = useQuery({
    queryKey: key,
    queryFn: () => port!.load(),
    enabled: port !== null,
    staleTime: 60_000,
    retry: false,
  });
  const mutation = useMutation({
    mutationFn: (id: string) => port!.select(id),
    onSuccess: (state) => client.setQueryData(key, state),
  });
  const state = port && !query.error ? (query.data ?? defaults) : defaults;
  useLayoutEffect(() => {
    document.documentElement.dataset.appearance = state.selected;
    return () => {
      delete document.documentElement.dataset.appearance;
    };
  }, [state.selected]);
  const refresh = useCallback(() => {
    void client.invalidateQueries({ queryKey: appearanceKey(scope) });
  }, [client, scope]);
  return (
    <AppearanceContext
      value={
        port
          ? {
              state,
              pending: query.isPending,
              error: query.error,
              saving: mutation.isPending,
              select: async (id) => {
                await mutation.mutateAsync(id);
              },
              refresh,
            }
          : null
      }
    >
      {children}
    </AppearanceContext>
  );
}

export function AccountAppearance({
  account,
  children,
}: {
  account?: string;
  children: ReactNode;
}) {
  const client = useQueryClient();
  const port = useMemo<AppearancePort | null>(() => {
    if (!account) return null;
    const bound = async (run: () => Promise<AppearanceState>) => {
      const owner = client.getQueryData<{ user: { id: string } }>(['user', 'session']);
      if (owner?.user.id !== account) throw new StationSessionChangedError();
      const session = captureStationSession(client, 'steward');
      const state = await run();
      if (!stationSessionMatches(client, 'steward', session))
        throw new StationSessionChangedError();
      return state;
    };
    return {
      scope: account,
      load: () => bound(() => apiFetch<AppearanceState>('/api/appearance')),
      select: (selected) =>
        bound(() =>
          apiFetch<AppearanceState>('/api/appearance', { method: 'POST', json: { selected } }),
        ),
    };
  }, [account, client]);
  return (
    <AppearanceProvider key={account ?? 'anonymous'} port={port}>
      {children}
    </AppearanceProvider>
  );
}
