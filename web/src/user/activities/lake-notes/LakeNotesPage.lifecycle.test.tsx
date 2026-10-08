import { StrictMode } from 'react';
import { ApiError } from '@shared/query/http';
import {
  beginManagementSessionRequest,
  noteManagementSessionSuccess,
} from '@shared/charityManagement';
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router';
import { afterEach, expect, test, vi } from 'vitest';
import { initialProfile, RULES_ID, start, step } from './rules';
import { LakeNotesPage } from './LakeNotesPage';
import { lakeKeys, type CastResult, type CheckpointInput, type ProfileView } from './api';

const account = '9';
let server: CastResult;
function advanceCheckpoint(input: CheckpointInput) {
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
}
const checkpointCall = vi.fn(async (_id: string, input: CheckpointInput) =>
  advanceCheckpoint(input),
);
const profileCall = vi.fn(
  async () => ({ ...server.profile, revision: '1', cast: null }) as ProfileView,
);
vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key, i18n: { resolvedLanguage: 'zh' } }),
}));
vi.mock('../../data', () => ({ useUserSession: () => ({ data: { user: { id: account } } }) }));
vi.mock('../../components/UserPageGate', () => ({
  UserPageGate: ({ children }: { children: React.ReactNode }) => children,
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
  getProfile: () => profileCall(),
  startCast: async () => server,
  checkpoint: (id: string, input: CheckpointInput) => checkpointCall(id, input),
  controlCast: async () => ({ ...server, cast: { ...server.cast, paused: true, readonly: true } }),
}));
vi.mock('./NativeLake', () => ({
  NativeLake: ({
    bridge,
  }: {
    bridge: {
      tick: () => void;
      start: () => void;
      snapshot: () => { status: string; result: CastResult | null };
    };
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
      <output data-testid="controller-revision">{bridge.snapshot().result?.cast.revision}</output>
    </>
  ),
}));
function refreshSession(client: QueryClient, id = account) {
  const value = { user: { id, username: 'fishing-fixture-' + id, effective_level: 6 } };
  const generation = beginManagementSessionRequest(client, 'steward');
  expect(noteManagementSessionSuccess(client, 'steward', value, generation)).toBe(true);
  client.setQueryData(['user', 'session'], value);
}
function sessionClient() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  refreshSession(client);
  return client;
}
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
  profileCall.mockClear();
});

test('returning to a cached cast adopts the latest saved revision before resuming', async () => {
  server = fixture();
  server.cast.paused = server.cast.state.paused = true;
  server.cast.readonly = true;
  const client = sessionClient();
  client.setDefaultOptions({ queries: { retry: false, staleTime: 30_000 } });
  client.setQueryData(lakeKeys(account), structuredClone(server.profile));
  server.cast.revision = '2';
  profileCall.mockResolvedValueOnce(structuredClone(server.profile));
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <LakeNotesPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  await waitFor(() => expect(screen.getByTestId('controller-revision')).toHaveTextContent('2'));
  expect(screen.getByTestId('controller-status')).toHaveTextContent('paused');
});
test('the actual page controller saves and publishes after StrictMode cleanup and setup', async () => {
  server = fixture();
  const client = sessionClient();
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
  const client = sessionClient();
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

for (const moment of ['before starting', 'while playing'] as const) {
  test(`same-account session refresh ${moment} keeps the next checkpoint usable`, async () => {
    server = fixture();
    const client = sessionClient();
    render(
      <QueryClientProvider client={client}>
        <MemoryRouter>
          <LakeNotesPage />
        </MemoryRouter>
      </QueryClientProvider>,
    );
    await screen.findByText('cast fixture');
    if (moment === 'before starting') refreshSession(client);
    fireEvent.click(screen.getByText('cast fixture'));
    await waitFor(() =>
      expect(screen.getByTestId('controller-status')).toHaveTextContent('running'),
    );
    if (moment === 'while playing') refreshSession(client);
    await act(async () => {
      fireEvent.click(screen.getByText('advance fixture'));
    });
    expect(checkpointCall).toHaveBeenCalledOnce();
    expect(client.getQueryData<ProfileView>(lakeKeys(account))?.cast?.ack_tick).toBe(120);
    expect(screen.getByTestId('controller-status')).toHaveTextContent('running');
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });
}

test('an old controller cannot send a checkpoint for a different account', async () => {
  server = fixture();
  const client = sessionClient();
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <LakeNotesPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  fireEvent.click(await screen.findByText('cast fixture'));
  await waitFor(() => expect(screen.getByTestId('controller-status')).toHaveTextContent('running'));
  refreshSession(client, '10');
  await act(async () => {
    fireEvent.click(screen.getByText('advance fixture'));
  });
  expect(checkpointCall).not.toHaveBeenCalled();
  expect(client.getQueryData<ProfileView>(lakeKeys(account))?.revision).not.toBe('3');
});

test('a checkpoint response is discarded after an account switch', async () => {
  server = fixture();
  const client = sessionClient();
  const cacheWrite = vi.spyOn(client, 'setQueryData');
  let resolveCheckpoint!: () => void;
  checkpointCall.mockImplementationOnce(
    (_id, input) =>
      new Promise((resolve) => {
        resolveCheckpoint = () => resolve(advanceCheckpoint(input));
      }),
  );
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <LakeNotesPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  fireEvent.click(await screen.findByText('cast fixture'));
  await waitFor(() => expect(screen.getByTestId('controller-status')).toHaveTextContent('running'));
  fireEvent.click(screen.getByText('advance fixture'));
  expect(checkpointCall).toHaveBeenCalledOnce();
  refreshSession(client, '10');
  await act(async () => {
    resolveCheckpoint();
  });
  expect(cacheWrite).not.toHaveBeenCalledWith(
    lakeKeys(account),
    expect.objectContaining({ revision: '3' }),
  );
  expect(client.getQueryData<ProfileView>(lakeKeys(account))?.revision).not.toBe('3');
});
