import { StrictMode } from 'react';
import { ApiError } from '@shared/query/http';
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router';
import { afterEach, expect, test, vi } from 'vitest';
import { initialProfile, RULES_ID, start, step } from './rules';
import { LakeNotesPage } from './LakeNotesPage';
import { lakeKeys, type CastResult, type CheckpointInput, type ProfileView } from './api';

const account = 'lake-account-fixture';
let server: CastResult;
const checkpointCall = vi.fn(async (_id: string, input: CheckpointInput) => {
  const result = structuredClone(server);
  let held = input.initial_held;
  for (let tick = input.from_tick; tick <= input.to_tick; tick++) {
    held = input.edges.find((edge) => edge.tick === tick)?.held ?? held;
    step(result.profile.profile, result.cast.state, held);
  }
  result.cast.ack_tick = result.cast.state.tick;
  result.cast.revision = '2';
  result.profile.revision = '3';
  result.cast.profile_revision = '3';
  result.profile.cast = result.cast;
  server = result;
  return result;
});
vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key, i18n: { resolvedLanguage: 'zh' } }),
}));
vi.mock('../../data', () => ({ useUserSession: () => ({ data: { user: { id: account } } }) }));
vi.mock('../../components/UserPageGate', () => ({
  UserPageGate: ({ children }: { children: React.ReactNode }) => children,
}));
vi.mock('@shared/charityManagement', () => ({
  captureStationSession: () => ({}),
  stationSessionMatches: () => true,
  StationSessionChangedError: Error,
}));
vi.mock('../../features/economy/queries', () => ({
  economySessionRequest: (_client: unknown, request: () => unknown) => request(),
}));
vi.mock('./copy', () => ({ useLakeCopy: () => ({ language: 'zh', t: (key: string) => key }) }));
vi.mock('@shared/operations/useRetainedOperation', () => ({
  useRetainedOperation: (
    run: (
      input: unknown,
      key: string,
      context: { signal: AbortSignal; commit: (fn: () => unknown) => unknown },
    ) => Promise<unknown>,
  ) => ({
    isPending: false,
    error: null,
    outcome: null,
    mutate: (input: unknown) => {
      void run(input, 'fixture-key', {
        signal: new AbortController().signal,
        commit: (fn) => fn(),
      });
    },
  }),
}));
vi.mock('./api', async (original) => ({
  ...(await original<typeof import('./api')>()),
  getProfile: async () => ({ ...server.profile, revision: '1', cast: null }),
  startCast: async () => server,
  checkpoint: (id: string, input: CheckpointInput) => checkpointCall(id, input),
  controlCast: async () => ({ ...server, cast: { ...server.cast, paused: true, readonly: true } }),
}));
vi.mock('./NativeLake', () => ({
  NativeLake: ({
    bridge,
  }: {
    bridge: { tick: () => void; start: () => void; snapshot: () => { status: string } };
  }) => (
    <>
      <button onClick={bridge.start}>cast fixture</button>
      <button
        onClick={() => {
          for (let i = 0; i < 120; i++) bridge.tick();
        }}
      >
        advance fixture
      </button>
      <output data-testid="controller-status">{bridge.snapshot().status}</output>
    </>
  ),
}));
function fixture(): CastResult {
  const prediction = start(initialProfile(), () => 0, 1);
  const cast: CastResult['cast'] = {
    id: 'lnc_AAAAAAAAAAAAAAAAAAAAAA',
    rules_id: RULES_ID,
    generation: '1',
    revision: '1',
    ack_tick: 0,
    phase: 'waiting',
    paused: false,
    readonly: false,
    state: prediction.cast,
    profile_revision: '2',
  };
  const profile: ProfileView = {
    readonly: false,
    revision: '2',
    rules_id: RULES_ID,
    profile: prediction.profile,
    wallet: { general_milli: '0', game_milli: '0' },
    settings: {
      enabled: true,
      revision: '1',
      exchanges: {
        coins_to_general: { enabled: false, source_amount: '', target_amount: '' },
        general_to_coins: { enabled: false, source_amount: '', target_amount: '' },
        coins_to_game: { enabled: false, source_amount: '', target_amount: '' },
        game_to_coins: { enabled: false, source_amount: '', target_amount: '' },
      },
    },
    cast,
  };
  return { cast, profile };
}
afterEach(() => {
  cleanup();
  checkpointCall.mockClear();
});
test('the actual page controller saves and publishes after StrictMode cleanup and setup', async () => {
  server = fixture();
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <StrictMode>
      <QueryClientProvider client={client}>
        <MemoryRouter>
          <LakeNotesPage />
        </MemoryRouter>
      </QueryClientProvider>
    </StrictMode>,
  );
  fireEvent.click(await screen.findByText('cast fixture'));
  await waitFor(() => expect(screen.getByTestId('controller-status')).toHaveTextContent('running'));
  await act(async () => {
    fireEvent.click(screen.getByText('advance fixture'));
  });
  expect(checkpointCall).toHaveBeenCalledOnce();
  await waitFor(() =>
    expect(client.getQueryData<ProfileView>(lakeKeys(account))?.revision).toBe('3'),
  );
  expect(screen.getByTestId('controller-status')).toHaveTextContent('running');
});

test('the actual StrictMode page receives a controller error without a query update', async () => {
  server = fixture();
  checkpointCall.mockRejectedValueOnce(new ApiError('network_error', 'Response lost', 0));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <StrictMode>
      <QueryClientProvider client={client}>
        <MemoryRouter>
          <LakeNotesPage />
        </MemoryRouter>
      </QueryClientProvider>
    </StrictMode>,
  );
  fireEvent.click(await screen.findByText('cast fixture'));
  await waitFor(() => expect(screen.getByTestId('controller-status')).toHaveTextContent('running'));
  await act(async () => {
    fireEvent.click(screen.getByText('advance fixture'));
  });
  await waitFor(() => expect(screen.getByTestId('controller-status')).toHaveTextContent('unknown'));
  expect(client.getQueryData<ProfileView>(lakeKeys(account))?.revision).toBe('2');
});
