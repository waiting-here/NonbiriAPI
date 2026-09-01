import { act, fireEvent, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { FishingGame } from './FishingGame';
import { assertNoSensitiveQueryCache, renderWithProviders } from '../../../../test/unit/support';
import { fishingKeys } from './client';

const config = {
  master_enabled: true,
  credits: '5000000',
  game_profile_public: false,
  games: [
    {
      id: 'fishing',
      version: 1,
      enabled: true,
      params: {
        baits: [
          { id: 'worm', price: '2500000' },
          { id: 'lure', price: '5000000' },
          { id: 'premium', price: '7500000' },
        ],
        rtp_percent: { standard: 90, premium: 88 },
        treasure_multipliers: { bottle: 2, clover: 3, shell: 5 },
      },
    },
  ],
};

const roundId = (character: string) => `grd_${character.toLowerCase().repeat(26)}`;
const TEST_ROUND_ID = roundId('a');

const result = {
  round_id: TEST_ROUND_ID,
  game_id: 'fishing',
  game_version: 1,
  bait: 'worm',
  price: '2500000',
  species_key: 'koi',
  tier: 'legend',
  size_cm: 180,
  is_junk: false,
  is_treasure: false,
  meter: true,
  credits_won: '12000000',
  credits: '14500000',
  settled_at: 1_787_450_010,
};

interface FishingFixtureOptions {
  initialState?: Record<string, unknown>;
  settleResult?: Record<string, unknown>;
  settleResults?: Record<string, unknown>[];
  settleFailures?: number;
  settleCredits?: string;
  settleDelayMs?: number;
  stateGate?: Promise<void>;
  stateRefetchGate?: Promise<void>;
  stateGates?: readonly (Promise<void> | undefined)[];
  stateFailures?: number;
  startGate?: Promise<void>;
  startPreserveState?: boolean;
  startFailures?: number;
  startFailureCreatesPending?: boolean;
  startRoundId?: string;
  ackFailures?: number;
  ackResponseLost?: boolean;
  ackDelayMs?: number;
  ackFollowupState?: Record<string, unknown>;
  initialCredits?: string;
  masterEnabled?: boolean;
  gameEnabled?: boolean;
  profilePublic?: boolean;
  profileFailures?: number;
  profileResponseMode?: 'valid' | 'malformed' | 'wrong' | 'no-content';
  profileDelayMs?: number;
  effectiveLevel?: number;
  leaderboardMe?: Record<string, unknown> | null;
  reconcileFailures?: { config?: number; single?: number; total?: number };
}

function installFishingFixture(options: FishingFixtureOptions = {}) {
  let state = options.initialState ?? {
    pending_round: null,
    unrevealed_result: null,
    has_more_unrevealed: false,
  };
  let credits = options.initialCredits ?? config.credits;
  const masterEnabled = options.masterEnabled ?? true;
  const gameEnabled = options.gameEnabled ?? true;
  const startRoundId = options.startRoundId ?? TEST_ROUND_ID;
  let profilePublic = options.profilePublic ?? false;
  let profileFailuresRemaining = options.profileFailures ?? 0;
  let reconciliationStarted = false;
  let configReconcileFailuresRemaining = options.reconcileFailures?.config ?? 0;
  let singleReconcileFailuresRemaining = options.reconcileFailures?.single ?? 0;
  let totalReconcileFailuresRemaining = options.reconcileFailures?.total ?? 0;
  let startFailuresRemaining = options.startFailures ?? 0;
  let stateFailuresRemaining = options.stateFailures ?? 0;
  let settleFailuresRemaining = options.settleFailures ?? 0;
  let settleResultIndex = 0;
  let ackFailuresRemaining = options.ackFailures ?? 0;
  const calls = {
    start: 0,
    settle: 0,
    ack: 0,
    ackResolved: 0,
    profile: 0,
    games: 0,
    state: 0,
    leaderboards: 0,
    idempotencyKeys: [] as string[],
  };
  const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
    const url = new URL(
      input instanceof Request ? input.url : String(input),
      window.location.origin,
    );
    const method = (
      init?.method ?? (input instanceof Request ? input.method : 'GET')
    ).toUpperCase();
    const json = (body: unknown, status = 200) =>
      new Response(JSON.stringify(body), {
        status,
        headers: { 'content-type': 'application/json', 'cache-control': 'no-store' },
      });
    if (method === 'GET' && url.pathname === '/api/games') {
      calls.games += 1;
      if (reconciliationStarted && configReconcileFailuresRemaining > 0) {
        configReconcileFailuresRemaining -= 1;
        return json(
          { error: { code: 'service_unavailable', message: 'Configuration revalidation failed.' } },
          503,
        );
      }
      return json({
        ...config,
        master_enabled: masterEnabled,
        credits,
        game_profile_public: profilePublic,
        games: [{ ...config.games[0], enabled: gameEnabled }],
      });
    }
    if (method === 'GET' && url.pathname === '/api/session') {
      return json({
        user: {
          id: '1',
          username: 'fixture-user',
          avatar: null,
          avatar_url: null,
          guild_nick: 'Fixture User',
          guild_avatar_url: null,
          lang: 'en',
          is_banned: false,
          banned_until: null,
          charity_suspended_until: null,
          endpoint_limit: null,
          effective_endpoint_limit: '10',
          rpm_limit: null,
          effective_rpm_limit: '4096',
          concurrency_limit: null,
          effective_concurrency_limit: '5',
          balance: credits,
          donation_credit: '0',
          effective_level: options.effectiveLevel ?? 1,
          level_display_name: `Lv${options.effectiveLevel ?? 1}`,
          game_profile_public: profilePublic,
          created_at: 1,
          updated_at: 1,
          usage: {
            total_requests: '0',
            total_uncached_input_tokens: '0',
            total_cache_write_input_tokens: '0',
            total_cache_read_input_tokens: '0',
            total_output_tokens: '0',
            total_prompt_tokens: '0',
            total_completion_tokens: '0',
            total_unknown_usage_requests: '0',
          },
        },
      });
    }
    if (method === 'GET' && url.pathname === '/api/games/fishing/state') {
      calls.state += 1;
      if (stateFailuresRemaining > 0) {
        stateFailuresRemaining -= 1;
        return json(
          { error: { code: 'service_unavailable', message: 'Fishing state read failed.' } },
          503,
        );
      }
      const stateGate =
        options.stateGates?.[calls.state - 1] ??
        (calls.state === 1 ? options.stateGate : undefined) ??
        (calls.state > 1 ? options.stateRefetchGate : undefined);
      if (stateGate) await stateGate;
      return json(state);
    }
    if (method === 'GET' && url.pathname === '/api/games/fishing/leaderboard') {
      calls.leaderboards += 1;
      if (url.searchParams.get('board') === 'total') {
        if (reconciliationStarted && totalReconcileFailuresRemaining > 0) {
          totalReconcileFailuresRemaining -= 1;
          return json(
            {
              error: {
                code: 'service_unavailable',
                message: 'Total leaderboard revalidation failed.',
              },
            },
            503,
          );
        }
        return json({ board: 'total', window_start: 1_787_000_000, entries: [], me: null });
      }
      if (reconciliationStarted && singleReconcileFailuresRemaining > 0) {
        singleReconcileFailuresRemaining -= 1;
        return json(
          {
            error: {
              code: 'service_unavailable',
              message: 'Single leaderboard revalidation failed.',
            },
          },
          503,
        );
      }
      const mineIsOutsideTopTwenty =
        typeof options.leaderboardMe?.rank === 'number' && options.leaderboardMe.rank > 20;
      const singleEntries = mineIsOutsideTopTwenty
        ? Array.from({ length: 20 }, (_, index) => ({
            rank: index + 1,
            species_key: index === 0 ? 'taimen' : 'koi',
            size_cm: index === 0 ? 190 : 180 - Math.min(index, 10),
            is_me: false,
          }))
        : [
            { rank: 1, species_key: 'taimen', size_cm: 190, is_me: false },
            {
              rank: 2,
              species_key: 'koi',
              size_cm: 180,
              display_name: 'Public angler',
              avatar_url: 'https://cdn.discordapp.com/avatars/a/b.png?size=64',
              level4_badge: true,
              is_me: false,
            },
          ];
      return json({
        board: 'single',
        window_start: null,
        entries: singleEntries,
        me: options.leaderboardMe ?? null,
      });
    }
    if (method === 'POST' && url.pathname === '/api/games/fishing/rounds') {
      calls.start += 1;
      const key = new Headers(init?.headers).get('Idempotency-Key');
      if (key) calls.idempotencyKeys.push(key);
      const previousState = state;
      const previousUnrevealed = state.unrevealed_result ?? null;
      if (options.startGate) await options.startGate;
      if (!options.startPreserveState) {
        state = {
          pending_round: {
            round_id: startRoundId,
            bait: 'worm',
            price: '2500000',
            created_at: 1,
            auto_settle_at: 2,
          },
          unrevealed_result: previousUnrevealed,
          has_more_unrevealed: Boolean(previousUnrevealed),
        };
        credits = '2500000';
      }
      if (startFailuresRemaining > 0) {
        startFailuresRemaining -= 1;
        if (options.startFailureCreatesPending === false && !options.startPreserveState)
          state = previousState;
        return json(
          { error: { code: 'service_unavailable', message: 'Start response lost.' } },
          503,
        );
      }
      return json({
        round_id: startRoundId,
        game_id: 'fishing',
        game_version: 1,
        bait: 'worm',
        price: '2500000',
        credits,
        state: 'pending',
        created_at: 1,
        auto_settle_at: 2,
        idempotent_replay: false,
      });
    }
    if (method === 'POST' && url.pathname.endsWith('/settle')) {
      calls.settle += 1;
      if (options.settleDelayMs)
        await new Promise((resolve) => setTimeout(resolve, options.settleDelayMs));
      if (settleFailuresRemaining > 0) {
        settleFailuresRemaining -= 1;
        return json(
          { error: { code: 'service_unavailable', message: 'Temporary settlement outage.' } },
          503,
        );
      }
      const settledResult =
        options.settleResults?.[settleResultIndex++] ?? options.settleResult ?? result;
      const previousUnrevealed = state.unrevealed_result ?? null;
      state = {
        pending_round: null,
        unrevealed_result: previousUnrevealed ?? settledResult,
        has_more_unrevealed: Boolean(previousUnrevealed),
      };
      credits = options.settleCredits ?? '14500000';
      const replay = 'idempotent_replay' in settledResult ? settledResult.idempotent_replay : false;
      return json({ ...settledResult, idempotent_replay: replay ?? false });
    }
    if (method === 'POST' && url.pathname.endsWith('/ack')) {
      calls.ack += 1;
      if (options.ackDelayMs)
        await new Promise((resolve) => setTimeout(resolve, options.ackDelayMs));
      if (ackFailuresRemaining > 0) {
        ackFailuresRemaining -= 1;
        if (options.ackResponseLost) {
          state = options.ackFollowupState ?? {
            pending_round: null,
            unrevealed_result: null,
            has_more_unrevealed: false,
          };
          calls.ackResolved += 1;
        }
        return json(
          { error: { code: 'service_unavailable', message: 'Temporary acknowledgement outage.' } },
          503,
        );
      }
      state = options.ackFollowupState ?? {
        pending_round: null,
        unrevealed_result: null,
        has_more_unrevealed: false,
      };
      calls.ackResolved += 1;
      return new Response(null, { status: 204, headers: { 'cache-control': 'no-store' } });
    }
    if (method === 'PATCH' && url.pathname === '/api/me') {
      calls.profile += 1;
      reconciliationStarted = true;
      if (options.profileDelayMs)
        await new Promise((resolve) => setTimeout(resolve, options.profileDelayMs));
      if (profileFailuresRemaining > 0) {
        profileFailuresRemaining -= 1;
        return json(
          { error: { code: 'service_unavailable', message: 'Privacy update failed.' } },
          503,
        );
      }
      const body =
        typeof init?.body === 'string'
          ? (JSON.parse(init.body) as { game_profile_public?: unknown })
          : {};
      profilePublic = body.game_profile_public === true;
      if (options.profileResponseMode === 'no-content')
        return new Response(null, { status: 204, headers: { 'cache-control': 'no-store' } });
      if (options.profileResponseMode === 'malformed') return json({ user: {} });
      if (options.profileResponseMode === 'wrong')
        return json({ user: { game_profile_public: !profilePublic } });
      return json({ user: { game_profile_public: profilePublic } });
    }
    return json({ error: { code: 'not_found', message: 'fixture route not found' } }, 404);
  });
  vi.stubGlobal('fetch', fetchMock);
  let reducedMotion = false;
  const mediaListeners = new Set<(event: MediaQueryListEvent) => void>();
  const media = {
    get matches() {
      return reducedMotion;
    },
    media: '(prefers-reduced-motion: reduce)',
    onchange: null,
    addListener(listener: (event: MediaQueryListEvent) => void) {
      mediaListeners.add(listener);
    },
    removeListener(listener: (event: MediaQueryListEvent) => void) {
      mediaListeners.delete(listener);
    },
    addEventListener(_type: 'change', listener: (event: MediaQueryListEvent) => void) {
      mediaListeners.add(listener);
    },
    removeEventListener(_type: 'change', listener: (event: MediaQueryListEvent) => void) {
      mediaListeners.delete(listener);
    },
    dispatchEvent: () => false,
  };
  vi.stubGlobal('matchMedia', () => media);
  return {
    calls,
    fetchMock,
    setReducedMotion: (value: boolean) => {
      reducedMotion = value;
      const event = { matches: value } as MediaQueryListEvent;
      mediaListeners.forEach((listener) => listener(event));
    },
    setProfilePublic: (value: boolean) => {
      profilePublic = value;
    },
    setReconciliationStarted: () => {
      reconciliationStarted = true;
    },
    setState: (next: Record<string, unknown>) => {
      state = next;
    },
  };
}

describe('FishingGame', () => {
  it('renders a recovered server result, keeps anonymous identity omitted, and ACKs once', async () => {
    const { calls } = installFishingFixture({
      initialState: { pending_round: null, unrevealed_result: result, has_more_unrevealed: false },
    });
    const rendered = await renderWithProviders(<FishingGame />, { station: 'user', role: 'user' });

    expect(await screen.findByRole('heading', { name: 'Pond fishing' })).toBeInTheDocument();
    await waitFor(
      () => expect(screen.getByRole('heading', { name: 'Catch confirmed' })).toBeInTheDocument(),
      { timeout: 5_000 },
    );
    const resultCard = document.querySelector('.fishing-result');
    expect(screen.getByRole('region', { name: 'Catch confirmed' })).toBe(resultCard);
    expect(resultCard).toHaveAttribute('aria-live', 'polite');
    expect(resultCard).toHaveAttribute('data-round-id', TEST_ROUND_ID);
    expect(resultCard).toHaveAttribute('data-species-key', 'koi');
    expect(resultCard).toHaveAttribute('data-credits-won', '12000000');
    expect(screen.getByText('Recovered from the server')).toBeInTheDocument();
    expect(screen.getAllByText('Anonymous angler').length).toBeGreaterThan(0);
    expect(screen.getByText('Public angler')).toBeInTheDocument();
    expect(screen.getByAltText('Avatar for Public angler')).toBeInTheDocument();
    expect(screen.getByText('L4')).toBeInTheDocument();
    expect(screen.getByRole('checkbox', { name: 'Show my profile' })).not.toBeChecked();
    expect(screen.getByText('No settled catches yet.')).toBeInTheDocument();
    expect(screen.getAllByRole('button', { name: /credits/ }).length).toBeGreaterThanOrEqual(3);
    const baitList = screen.getByRole('list', { name: 'Fishing bait' });
    expect(
      Array.from(baitList.children).every((child) => child.getAttribute('role') === 'listitem'),
    ).toBe(true);
    expect(screen.getAllByRole('listitem')).toHaveLength(3);

    await rendered.user.click(screen.getByRole('checkbox', { name: 'Show my profile' }));
    await waitFor(() =>
      expect(screen.getByRole('checkbox', { name: 'Show my profile' })).toBeChecked(),
    );
    await rendered.user.click(screen.getByRole('button', { name: 'Mark as viewed' }));
    await waitFor(() => expect(calls.ack).toBe(1));
    await waitFor(() =>
      expect(screen.queryByRole('heading', { name: 'Catch confirmed' })).not.toBeInTheDocument(),
    );
    expect(calls.ack).toBe(1);
  });

  it.each([4, 5])(
    'applies the L4 cosmetic theme for authoritative level %s and above',
    async (effectiveLevel) => {
      installFishingFixture({ effectiveLevel });
      await renderWithProviders(<FishingGame />, { station: 'user', role: 'level4' });

      await screen.findByRole('heading', { name: 'Pond fishing' });
      expect(document.querySelector('.fishing-stage')).toHaveClass('fishing-stage--l4');
      expect(document.querySelector('.fishing-pond')).toBeInTheDocument();
      expect(document.querySelector('.fishing-pond__rod')).toBeInTheDocument();
      expect(document.querySelector('.fishing-bait-grid')).toBeInTheDocument();
    },
  );

  it('runs the local presentation through settle, then updates from server result and ACKs', async () => {
    const { calls } = installFishingFixture();
    const rendered = await renderWithProviders(<FishingGame />, { station: 'user', role: 'user' });
    const cast = await screen.findByRole('button', { name: 'Cast line' });
    await waitFor(() => expect(cast).toBeEnabled());
    expect(
      screen.getByRole('button', { name: /Premium lure.*Not enough available credits/ }),
    ).toBeDisabled();
    await rendered.user.click(cast);
    expect(calls.start).toBe(1);
    expect(calls.idempotencyKeys[0]).toMatch(
      /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i,
    );
    await waitFor(() => expect(calls.settle).toBe(1), { timeout: 5_000 });
    expect(await screen.findByRole('heading', { name: 'Catch confirmed' })).toBeInTheDocument();
    expect(calls.settle).toBe(1);
    expect(screen.getByText('Won: 12,000')).toBeInTheDocument();
    expect(screen.getAllByTitle('14,500,000 milli-credits').length).toBeGreaterThan(0);

    await rendered.user.click(screen.getByRole('button', { name: 'Mark as viewed' }));
    await waitFor(() => expect(calls.ack).toBe(1));
    expect(calls.ack).toBe(1);
    // Scan the actual fixture round id; a marker absent from the response
    // would make this persistence assertion vacuous.
    expect(assertNoSensitiveQueryCache(rendered.queryClient, [TEST_ROUND_ID])).toBeDefined();
  }, 10_000);

  it('restarts the active presentation when the reduced-motion preference changes', async () => {
    const { calls, setReducedMotion } = installFishingFixture();
    const rendered = await renderWithProviders(<FishingGame />, { station: 'user', role: 'user' });
    await rendered.user.click(await screen.findByRole('button', { name: 'Cast line' }));
    await waitFor(() =>
      expect(document.querySelector('.fishing-stage')?.getAttribute('data-phase')).toBe('waiting'),
    );
    setReducedMotion(true);
    await waitFor(() => expect(calls.settle).toBe(1), { timeout: 1_000 });
    expect(await screen.findByRole('heading', { name: 'Catch confirmed' })).toBeInTheDocument();
  }, 5_000);

  it('keeps a paid pending round recoverable when settle fails, then retries explicitly', async () => {
    const { calls } = installFishingFixture({ settleFailures: 1 });
    const rendered = await renderWithProviders(<FishingGame />, { station: 'user', role: 'user' });
    await rendered.user.click(await screen.findByRole('button', { name: 'Cast line' }));

    const retry = await screen.findByRole(
      'button',
      { name: 'Retry settlement' },
      { timeout: 7_000 },
    );
    expect(calls.settle).toBe(1);
    expect(screen.getByText('Round pending')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Cast line' })).toBeDisabled();
    expect(
      screen.getByRole('button', { name: /Worm.*Finish the pending round first/ }),
    ).toBeDisabled();
    await rendered.user.click(retry);
    await waitFor(() => expect(calls.settle).toBe(2), { timeout: 5_000 });
    expect(await screen.findByRole('heading', { name: 'Catch confirmed' })).toBeInTheDocument();
  }, 15_000);

  it('releases a failed-settlement retry marker once the server reports a terminal result', async () => {
    const fixture = installFishingFixture({ settleFailures: 1 });
    const rendered = await renderWithProviders(<FishingGame />, { station: 'user', role: 'user' });
    await rendered.user.click(await screen.findByRole('button', { name: 'Cast line' }));
    await screen.findByRole('button', { name: 'Retry settlement' }, { timeout: 7_000 });

    fixture.setState({
      pending_round: null,
      unrevealed_result: result,
      has_more_unrevealed: false,
    });
    await rendered.queryClient.refetchQueries({ queryKey: fishingKeys.state, exact: true });
    await waitFor(() =>
      expect(screen.getByRole('heading', { name: 'Catch confirmed' })).toBeInTheDocument(),
    );
    await waitFor(() => expect(screen.getByRole('button', { name: 'Cast line' })).toBeEnabled());
    expect(screen.queryByRole('button', { name: 'Retry settlement' })).not.toBeInTheDocument();

    await rendered.user.click(screen.getByRole('button', { name: 'Cast line' }));
    await waitFor(() => expect(fixture.calls.start).toBe(2));
  }, 15_000);

  it('releases a failed-settlement retry marker for a released round with no result', async () => {
    const fixture = installFishingFixture({ settleFailures: 1 });
    const rendered = await renderWithProviders(<FishingGame />, { station: 'user', role: 'user' });
    await rendered.user.click(await screen.findByRole('button', { name: 'Cast line' }));
    await screen.findByRole('button', { name: 'Retry settlement' }, { timeout: 7_000 });

    fixture.setState({ pending_round: null, unrevealed_result: null, has_more_unrevealed: false });
    await rendered.queryClient.refetchQueries({ queryKey: fishingKeys.state, exact: true });
    await waitFor(() => expect(screen.getByRole('button', { name: 'Cast line' })).toBeEnabled());
    expect(screen.queryByRole('button', { name: 'Retry settlement' })).not.toBeInTheDocument();
  }, 15_000);

  it('supersedes an old settlement error when authority reports a newer pending round', async () => {
    const oldRoundId = roundId('a');
    const newerRoundId = roundId('b');
    const newerPending = {
      round_id: newerRoundId,
      bait: 'worm',
      price: '2500000',
      created_at: 1,
      auto_settle_at: 2,
    };
    const oldResult = { ...result, round_id: oldRoundId };
    const newerResult = {
      ...result,
      round_id: newerRoundId,
      species_key: 'taimen',
      credits_won: '1000000',
    };
    const fixture = installFishingFixture({
      initialState: {
        pending_round: {
          round_id: oldRoundId,
          bait: 'worm',
          price: '2500000',
          created_at: 1,
          auto_settle_at: 2,
        },
        unrevealed_result: null,
        has_more_unrevealed: false,
      },
      settleFailures: 1,
      settleResult: newerResult,
    });
    const rendered = await renderWithProviders(<FishingGame />, { station: 'user', role: 'user' });
    await screen.findByRole('button', { name: 'Retry settlement' }, { timeout: 7_000 });
    await waitFor(() => expect(fixture.calls.state).toBeGreaterThanOrEqual(2));

    // Another device completed/replaced the failed round while this page was
    // showing its retry UI. The same authoritative read also exposes an
    // older unrevealed result, which must stay available while the new round
    // is driven through the server settlement path.
    fixture.setState({
      pending_round: newerPending,
      unrevealed_result: oldResult,
      has_more_unrevealed: false,
    });
    await act(async () => {
      await rendered.queryClient.refetchQueries({ queryKey: fishingKeys.state, exact: true });
    });
    await waitFor(() =>
      expect(document.querySelector('.fishing-stage')?.getAttribute('data-phase')).toBe('casting'),
    );
    await waitFor(() =>
      expect(document.querySelector('.fishing-result')?.getAttribute('data-round-id')).toBe(
        oldRoundId,
      ),
    );
    expect(screen.queryByRole('button', { name: 'Retry settlement' })).not.toBeInTheDocument();

    await waitFor(() => expect(fixture.calls.settle).toBe(2), { timeout: 7_000 });
    expect(document.querySelector('.fishing-result')?.getAttribute('data-round-id')).toBe(
      oldRoundId,
    );
  }, 15_000);

  it('recovers a pending round when the start response is lost', async () => {
    const { calls } = installFishingFixture({ startFailures: 1 });
    const rendered = await renderWithProviders(<FishingGame />, { station: 'user', role: 'user' });
    await rendered.user.click(await screen.findByRole('button', { name: 'Cast line' }));

    await waitFor(() => expect(calls.start).toBe(1));
    await waitFor(() => expect(calls.settle).toBe(1), { timeout: 7_000 });
    expect(await screen.findByRole('heading', { name: 'Catch confirmed' })).toBeInTheDocument();
  }, 15_000);

  it('drops a delayed start success after a newer recovered result takes authority', async () => {
    let releaseStart!: () => void;
    const startGate = new Promise<void>((resolve) => {
      releaseStart = resolve;
    });
    const newerRoundId = roundId('b');
    const newerResult = {
      ...result,
      round_id: newerRoundId,
      species_key: 'taimen',
      credits_won: '1000000',
    };
    const fixture = installFishingFixture({
      startGate,
      startPreserveState: true,
      startRoundId: roundId('a'),
    });
    const rendered = await renderWithProviders(<FishingGame />, { station: 'user', role: 'user' });
    const cast = await screen.findByRole('button', { name: 'Cast line' });
    await waitFor(() => expect(cast).toBeEnabled());
    await rendered.user.click(cast);
    await waitFor(() => expect(fixture.calls.start).toBe(1));

    fixture.setState({
      pending_round: null,
      unrevealed_result: newerResult,
      has_more_unrevealed: false,
    });
    await act(async () => {
      await rendered.queryClient.refetchQueries({ queryKey: fishingKeys.state, exact: true });
    });
    await waitFor(() =>
      expect(document.querySelector('.fishing-result')?.getAttribute('data-round-id')).toBe(
        newerRoundId,
      ),
    );

    releaseStart();
    await waitFor(() => expect(fixture.calls.games).toBeGreaterThanOrEqual(2));
    expect(document.querySelector('.fishing-result')?.getAttribute('data-round-id')).toBe(
      newerRoundId,
    );
    expect(
      screen.queryByText('No paid round was created. Choose bait to try again.'),
    ).not.toBeInTheDocument();
  }, 10_000);

  it('drops a delayed start error after a newer pending round takes authority', async () => {
    let releaseStart!: () => void;
    const startGate = new Promise<void>((resolve) => {
      releaseStart = resolve;
    });
    const newerRoundId = roundId('b');
    const newerPending = {
      round_id: newerRoundId,
      bait: 'worm',
      price: '2500000',
      created_at: 1,
      auto_settle_at: 2,
    };
    const fixture = installFishingFixture({
      startGate,
      startPreserveState: true,
      startFailures: 1,
      startRoundId: roundId('a'),
      settleResult: { ...result, round_id: newerRoundId },
    });
    const rendered = await renderWithProviders(<FishingGame />, { station: 'user', role: 'user' });
    const cast = await screen.findByRole('button', { name: 'Cast line' });
    await waitFor(() => expect(cast).toBeEnabled());
    await rendered.user.click(cast);
    await waitFor(() => expect(fixture.calls.start).toBe(1));

    fixture.setState({
      pending_round: newerPending,
      unrevealed_result: null,
      has_more_unrevealed: false,
    });
    await act(async () => {
      await rendered.queryClient.refetchQueries({ queryKey: fishingKeys.state, exact: true });
    });
    await waitFor(() =>
      expect(document.querySelector('.fishing-stage')?.getAttribute('data-phase')).toBe('casting'),
    );

    releaseStart();
    await waitFor(() => expect(fixture.calls.games).toBeGreaterThanOrEqual(2));
    expect(screen.queryByText('Start response lost.')).not.toBeInTheDocument();
    await waitFor(() => expect(fixture.calls.settle).toBe(1), { timeout: 7_000 });
    await waitFor(() =>
      expect(document.querySelector('.fishing-result')?.getAttribute('data-round-id')).toBe(
        newerRoundId,
      ),
    );
  }, 15_000);

  it('keeps start and bait controls closed while Fishing state is loading', async () => {
    let releaseState!: () => void;
    const stateGate = new Promise<void>((resolve) => {
      releaseState = resolve;
    });
    const fixture = installFishingFixture({ stateGate });
    await renderWithProviders(<FishingGame />, { station: 'user', role: 'user' });
    const cast = await screen.findByRole('button', { name: 'Cast line' });
    expect(cast).toBeDisabled();
    expect(
      screen.getByRole('button', { name: /Worm.*Finish the pending round first/ }),
    ).toBeDisabled();

    releaseState();
    await waitFor(() => expect(cast).toBeEnabled());
    expect(fixture.calls.state).toBeGreaterThanOrEqual(1);
  });

  it('closes start controls during a state refetch and reopens only after success', async () => {
    let releaseState!: () => void;
    const stateRefetchGate = new Promise<void>((resolve) => {
      releaseState = resolve;
    });
    const fixture = installFishingFixture({ stateRefetchGate });
    const rendered = await renderWithProviders(<FishingGame />, { station: 'user', role: 'user' });
    const cast = await screen.findByRole('button', { name: 'Cast line' });
    await waitFor(() => expect(cast).toBeEnabled());

    void rendered.queryClient.refetchQueries({ queryKey: fishingKeys.state, exact: true });
    await waitFor(() => expect(fixture.calls.state).toBeGreaterThanOrEqual(2));
    expect(cast).toBeDisabled();
    expect(
      screen.getByRole('button', { name: /Worm.*Finish the pending round first/ }),
    ).toBeDisabled();

    releaseState();
    await waitFor(() => expect(cast).toBeEnabled());
  });

  it('does not POST settlement while the state authority is still fetching', async () => {
    let releaseState!: () => void;
    const stateRefetchGate = new Promise<void>((resolve) => {
      releaseState = resolve;
    });
    const fixture = installFishingFixture({ stateRefetchGate });
    const rendered = await renderWithProviders(<FishingGame />, { station: 'user', role: 'user' });
    await rendered.user.click(await screen.findByRole('button', { name: 'Cast line' }));
    await waitFor(() => expect(fixture.calls.start).toBe(1));
    await waitFor(() => expect(fixture.calls.state).toBeGreaterThanOrEqual(2));
    await new Promise((resolve) => setTimeout(resolve, 2_600));
    expect(fixture.calls.settle).toBe(0);

    releaseState();
    await waitFor(() => expect(fixture.calls.settle).toBe(1), { timeout: 5_000 });
  }, 12_000);

  it('keeps controls closed after a state read error until retry succeeds', async () => {
    const fixture = installFishingFixture({ stateFailures: 1 });
    const rendered = await renderWithProviders(<FishingGame />, { station: 'user', role: 'user' });
    const cast = await screen.findByRole('button', { name: 'Cast line' });
    await waitFor(() => expect(cast).toBeDisabled());
    expect(screen.getByRole('alert')).toHaveTextContent('Fishing state read failed.');
    await rendered.user.click(screen.getByRole('button', { name: 'Retry' }));
    await waitFor(() => expect(cast).toBeEnabled());
    expect(fixture.calls.state).toBeGreaterThanOrEqual(2);
  });

  it('keeps a newer ACK write uncertain when an older authority read finishes first', async () => {
    let releaseOldRead!: () => void;
    let releaseAckRead!: () => void;
    const oldReadGate = new Promise<void>((resolve) => {
      releaseOldRead = resolve;
    });
    const ackReadGate = new Promise<void>((resolve) => {
      releaseAckRead = resolve;
    });
    const fixture = installFishingFixture({
      initialState: { pending_round: null, unrevealed_result: result, has_more_unrevealed: false },
      ackDelayMs: 100,
      ackFailures: 1,
      stateGates: [undefined, oldReadGate, ackReadGate],
    });
    const rendered = await renderWithProviders(<FishingGame />, { station: 'user', role: 'user' });
    expect(await screen.findByRole('heading', { name: 'Catch confirmed' })).toBeInTheDocument();
    const ackButton = screen.getByRole('button', { name: 'Mark as viewed' });

    // Start an older authority read and dispatch ACK from the same committed
    // result render before React can commit the read's fetching state. This
    // models the settle-result/ACK overlap at the event boundary.
    await act(async () => {
      void rendered.queryClient.refetchQueries({ queryKey: fishingKeys.state, exact: true });
      fireEvent.click(ackButton);
      await Promise.resolve();
    });
    await waitFor(() => expect(fixture.calls.state).toBeGreaterThanOrEqual(2));
    await waitFor(() => expect(fixture.calls.ack).toBe(1));

    // The older read began before the ACK token existed. Its successful
    // result must not clear that newer token or reopen the result action.
    releaseOldRead();
    await waitFor(() => expect(fixture.calls.state).toBeGreaterThanOrEqual(3));
    await waitFor(() =>
      expect(screen.getByRole('alert')).toHaveTextContent('Temporary acknowledgement outage.'),
    );
    expect(screen.getByRole('button', { name: 'Mark as viewed' })).toBeDisabled();
    fireEvent.click(screen.getByRole('button', { name: 'Mark as viewed' }));
    expect(fixture.calls.ack).toBe(1);

    // Only the authority read that started after the ACK can clear its token.
    releaseAckRead();
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Mark as viewed' })).toBeEnabled(),
    );
    expect(fixture.calls.ack).toBe(1);
  }, 10_000);

  it('keeps the result visible after an ACK failure and retries the original ACK action', async () => {
    const { calls } = installFishingFixture({
      ackFailures: 1,
      initialState: { pending_round: null, unrevealed_result: result, has_more_unrevealed: false },
    });
    const rendered = await renderWithProviders(<FishingGame />, { station: 'user', role: 'user' });
    expect(await screen.findByRole('heading', { name: 'Catch confirmed' })).toBeInTheDocument();
    await waitFor(() => expect(screen.getByRole('button', { name: 'Cast line' })).toBeEnabled());

    await rendered.user.click(screen.getByRole('button', { name: 'Mark as viewed' }));
    await waitFor(() => expect(calls.ack).toBe(1));
    expect(screen.getByRole('heading', { name: 'Catch confirmed' })).toBeInTheDocument();
    expect(screen.getByRole('alert')).toHaveTextContent('Temporary acknowledgement outage.');
    expect(screen.queryByRole('button', { name: 'Retry settlement' })).not.toBeInTheDocument();

    await rendered.user.click(screen.getByRole('button', { name: 'Mark as viewed' }));
    await waitFor(() => expect(calls.ack).toBe(2));
    await waitFor(() =>
      expect(screen.queryByRole('heading', { name: 'Catch confirmed' })).not.toBeInTheDocument(),
    );
  }, 10_000);

  it('cleans a result and restores focus when an ACK response is lost after commit', async () => {
    const fixture = installFishingFixture({
      ackFailures: 1,
      ackResponseLost: true,
      initialState: { pending_round: null, unrevealed_result: result, has_more_unrevealed: false },
    });
    const rendered = await renderWithProviders(<FishingGame />, { station: 'user', role: 'user' });
    expect(await screen.findByRole('heading', { name: 'Catch confirmed' })).toBeInTheDocument();
    const ackButton = screen.getByRole('button', { name: 'Mark as viewed' });
    ackButton.focus();
    await rendered.user.click(ackButton);
    await waitFor(() => expect(fixture.calls.ack).toBe(1));
    await waitFor(() =>
      expect(screen.queryByRole('heading', { name: 'Catch confirmed' })).not.toBeInTheDocument(),
    );
    await waitFor(() => expect(document.activeElement).toBe(screen.getByTestId('fishing-cast')));
    expect(fixture.calls.ack).toBe(1);
  }, 10_000);

  it('keeps the oldest result visible while a new round settles and queues each ACK', async () => {
    const firstResult = {
      ...result,
      round_id: roundId('b'),
      species_key: 'taimen',
      credits_won: '1000000',
      credits: '6000000',
    };
    const nextResult = {
      ...result,
      round_id: roundId('c'),
      species_key: 'yellowcheek',
      credits_won: '2000000',
      credits: '7000000',
    };
    const { calls } = installFishingFixture({
      initialState: {
        pending_round: {
          round_id: roundId('b'),
          bait: 'worm',
          price: '2500000',
          created_at: 1,
          auto_settle_at: 2,
        },
        unrevealed_result: result,
        has_more_unrevealed: false,
      },
      settleResults: [firstResult, nextResult],
      startRoundId: roundId('c'),
      ackFollowupState: {
        pending_round: null,
        unrevealed_result: firstResult,
        has_more_unrevealed: false,
      },
    });
    const rendered = await renderWithProviders(<FishingGame />, { station: 'user', role: 'user' });

    await waitFor(() => expect(calls.settle).toBe(1), { timeout: 7_000 });
    expect(await screen.findByText('Recovered from the server')).toBeInTheDocument();
    const cast = screen.getByRole('button', { name: 'Cast line' });
    await waitFor(() => expect(cast).toBeEnabled());
    await rendered.user.click(cast);
    expect(calls.start).toBe(1);
    expect(await screen.findByRole('heading', { name: 'Catch confirmed' })).toBeInTheDocument();
    await waitFor(() => expect(calls.settle).toBe(2), { timeout: 7_000 });
    expect(screen.getByText('Recovered from the server')).toBeInTheDocument();
    await rendered.user.click(screen.getByRole('button', { name: 'Mark as viewed' }));
    await waitFor(() => expect(calls.ack).toBe(1));

    expect(await screen.findByText('Server confirmed')).toBeInTheDocument();
    await rendered.user.click(screen.getByRole('button', { name: 'Mark as viewed' }));
    await waitFor(() => expect(calls.ack).toBe(2));
    await waitFor(() => expect(screen.getByRole('button', { name: 'Cast line' })).toBeEnabled());
  }, 15_000);

  it('does not let an ACK started from an older render reset a newer round', async () => {
    const nextRoundId = roundId('c');
    const nextResult = {
      ...result,
      round_id: nextRoundId,
      species_key: 'taimen',
      credits_won: '1000000',
      credits: '3500000',
    };
    const { calls } = installFishingFixture({
      initialState: { pending_round: null, unrevealed_result: result, has_more_unrevealed: false },
      startRoundId: nextRoundId,
      settleResult: nextResult,
      ackDelayMs: 100,
      ackFollowupState: {
        pending_round: {
          round_id: nextRoundId,
          bait: 'worm',
          price: '2500000',
          created_at: 1,
          auto_settle_at: 2,
        },
        unrevealed_result: null,
        has_more_unrevealed: false,
      },
    });
    await renderWithProviders(<FishingGame />, { station: 'user', role: 'user' });
    expect(await screen.findByRole('heading', { name: 'Catch confirmed' })).toBeInTheDocument();

    const cast = screen.getByRole('button', { name: 'Cast line' });
    await waitFor(() => expect(cast).toBeEnabled());
    const ackButton = screen.getByRole('button', { name: 'Mark as viewed' });
    // Dispatch both before React commits the first event. This models a
    // fast/programmatic overlap and proves the async ACK cannot reset the
    // presentation started by the cast.
    await act(async () => {
      fireEvent.click(cast);
      fireEvent.click(ackButton);
    });
    await waitFor(() => expect(calls.start).toBe(1));
    await waitFor(() => expect(calls.ackResolved).toBe(1));
    expect(document.querySelector('.fishing-stage')?.getAttribute('data-phase')).not.toBe('idle');
    await waitFor(() => expect(calls.settle).toBe(1), { timeout: 7_000 });
    expect(await screen.findByRole('heading', { name: 'Catch confirmed' })).toBeInTheDocument();
    expect(document.querySelector('.fishing-result')?.getAttribute('data-round-id')).toBe(
      nextRoundId,
    );
  }, 10_000);

  it('renders a Top20-external mine row and applies privacy changes before refetch', async () => {
    const fixture = installFishingFixture({
      profilePublic: true,
      profileDelayMs: 100,
      leaderboardMe: {
        rank: 21,
        species_key: 'koi',
        size_cm: 180,
        display_name: 'Mine only',
        avatar_url: 'https://cdn.discordapp.com/avatars/a/b.png?size=64',
        level4_badge: true,
        is_me: true,
      },
    });
    const view = await renderWithProviders(<FishingGame />, { station: 'user', role: 'user' });

    expect(await screen.findByText('Mine only')).toBeInTheDocument();
    expect(screen.getByText('Your rank: #21')).toBeInTheDocument();
    const toggle = await screen.findByRole('checkbox', { name: 'Show my profile' });
    await view.user.click(toggle);
    expect(screen.queryByText('Mine only')).not.toBeInTheDocument();
    expect(screen.getAllByText('Anonymous angler').length).toBeGreaterThan(0);
    expect(toggle).not.toBeChecked();
    expect(
      screen.getByText('Privacy status is being rechecked with the server…'),
    ).toBeInTheDocument();

    await waitFor(() => expect(toggle).toBeEnabled(), { timeout: 2_000 });
    await view.user.click(toggle);
    expect(screen.queryByText('Mine only')).not.toBeInTheDocument();
    await waitFor(() => expect(fixture.calls.profile).toBe(2), { timeout: 2_000 });
    await waitFor(() => expect(screen.getByText('Mine only')).toBeInTheDocument(), {
      timeout: 2_000,
    });
    expect(fixture.calls.profile).toBe(2);
  });

  it('reconciles a failed privacy downgrade against the still-public server state', async () => {
    const { calls } = installFishingFixture({
      profilePublic: true,
      profileFailures: 1,
      leaderboardMe: {
        rank: 21,
        species_key: 'koi',
        size_cm: 180,
        display_name: 'Mine only',
        avatar_url: 'https://cdn.discordapp.com/avatars/a/b.png?size=64',
        level4_badge: true,
        is_me: true,
      },
    });
    const view = await renderWithProviders(<FishingGame />, { station: 'user', role: 'user' });
    expect(await screen.findByText('Mine only')).toBeInTheDocument();
    const toggle = screen.getByRole('checkbox', { name: 'Show my profile' });
    expect(toggle).toBeChecked();
    await view.user.click(toggle);
    await waitFor(() => expect(toggle).toBeChecked());
    expect(await screen.findByText('Mine only')).toBeInTheDocument();
    expect(await screen.findByRole('alert')).toHaveTextContent('Privacy update failed.');
    expect(calls.profile).toBe(1);
    expect(calls.games).toBeGreaterThanOrEqual(2);
    expect(calls.leaderboards).toBeGreaterThanOrEqual(4);
  });

  it.each(['malformed', 'no-content', 'wrong'] as const)(
    'reconciles a %s privacy response with the authoritative public state',
    async (profileResponseMode) => {
      installFishingFixture({
        profileResponseMode,
        leaderboardMe: {
          rank: 21,
          species_key: 'koi',
          size_cm: 180,
          display_name: 'Mine only',
          avatar_url: 'https://cdn.discordapp.com/avatars/a/b.png?size=64',
          level4_badge: true,
          is_me: true,
        },
      });
      const view = await renderWithProviders(<FishingGame />, { station: 'user', role: 'user' });
      const toggle = await screen.findByRole('checkbox', { name: 'Show my profile' });
      expect(toggle).not.toBeChecked();
      await view.user.click(toggle);
      await waitFor(() => expect(toggle).toBeChecked());
      await expect(screen.findByRole('alert')).resolves.toHaveTextContent(
        'invalid fishing profile response',
      );
      expect(await screen.findByText('Mine only')).toBeInTheDocument();
    },
  );

  it('re-reads an accepted privacy state after remount instead of trusting local checkbox state', async () => {
    const fixture = installFishingFixture({
      profileResponseMode: 'no-content',
      leaderboardMe: {
        rank: 21,
        species_key: 'koi',
        size_cm: 180,
        display_name: 'Mine only',
        avatar_url: 'https://cdn.discordapp.com/avatars/a/b.png?size=64',
        level4_badge: true,
        is_me: true,
      },
    });
    const view = await renderWithProviders(<FishingGame />, { station: 'user', role: 'user' });
    const toggle = await screen.findByRole('checkbox', { name: 'Show my profile' });
    await view.user.click(toggle);
    await waitFor(() => expect(toggle).toBeChecked());
    expect(await screen.findByText('Mine only')).toBeInTheDocument();
    view.unmount();
    await renderWithProviders(<FishingGame />, { station: 'user', role: 'user' });
    await waitFor(() =>
      expect(screen.getByRole('checkbox', { name: 'Show my profile' })).toBeChecked(),
    );
    expect(await screen.findByText('Mine only')).toBeInTheDocument();
    expect(fixture.calls.games).toBeGreaterThanOrEqual(3);
  });

  it.each([
    ['config', { config: 1 }],
    ['single leaderboard', { single: 1 }],
    ['total leaderboard', { total: 1 }],
  ] as const)(
    'keeps privacy fail-closed and retries all authority reads after a %s revalidation failure',
    async (_label, reconcileFailures) => {
      const fixture = installFishingFixture({
        reconcileFailures,
        leaderboardMe: {
          rank: 21,
          species_key: 'koi',
          size_cm: 180,
          display_name: 'Mine only',
          avatar_url: 'https://cdn.discordapp.com/avatars/a/b.png?size=64',
          level4_badge: true,
          is_me: true,
        },
      });
      const view = await renderWithProviders(<FishingGame />, { station: 'user', role: 'user' });
      const toggle = await screen.findByRole('checkbox', { name: 'Show my profile' });
      await view.user.click(toggle);
      await waitFor(() =>
        expect(screen.getByRole('button', { name: 'Retry privacy check' })).toBeEnabled(),
      );
      expect(toggle).not.toBeChecked();
      expect(screen.queryByText('Mine only')).not.toBeInTheDocument();

      await view.user.click(screen.getByRole('button', { name: 'Retry privacy check' }));
      await waitFor(() => expect(toggle).toBeChecked());
      expect(await screen.findByText('Mine only')).toBeInTheDocument();
      expect(fixture.calls.games).toBeGreaterThanOrEqual(3);
      expect(fixture.calls.leaderboards).toBeGreaterThanOrEqual(6);
    },
  );

  it('masks cached public identity when a background authority read fails and offers retry', async () => {
    const fixture = installFishingFixture({
      profilePublic: true,
      reconcileFailures: { config: 1 },
      leaderboardMe: {
        rank: 21,
        species_key: 'koi',
        size_cm: 180,
        display_name: 'Mine only',
        avatar_url: 'https://cdn.discordapp.com/avatars/a/b.png?size=64',
        level4_badge: true,
        is_me: true,
      },
    });
    const view = await renderWithProviders(<FishingGame />, { station: 'user', role: 'user' });
    expect(await screen.findByText('Mine only')).toBeInTheDocument();

    fixture.setReconciliationStarted();
    await view.queryClient.refetchQueries({ queryKey: fishingKeys.config, exact: true });

    await waitFor(() => expect(screen.queryByText('Mine only')).not.toBeInTheDocument());
    expect(screen.getByRole('checkbox', { name: 'Show my profile' })).not.toBeChecked();
    expect(
      screen.getByText(
        'Privacy status could not be confirmed. Your identity is hidden until you retry.',
      ),
    ).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Retry privacy check' })).toBeEnabled();

    await view.user.click(screen.getByRole('button', { name: 'Retry privacy check' }));
    await waitFor(() =>
      expect(screen.getByRole('checkbox', { name: 'Show my profile' })).toBeChecked(),
    );
    expect(await screen.findByText('Mine only')).toBeInTheDocument();
  });

  it('marks the balance unknown after a failed games authority refetch and recovers without a false disabled-game state', async () => {
    const fixture = installFishingFixture({ reconcileFailures: { config: 1 } });
    const view = await renderWithProviders(<FishingGame />, { station: 'user', role: 'user' });
    await screen.findByRole('heading', { name: 'Pond fishing' });

    fixture.setReconciliationStarted();
    await view.queryClient.refetchQueries({ queryKey: fishingKeys.config, exact: true });
    await waitFor(() =>
      expect(
        screen.getByText('The current balance could not be confirmed. No new round can start.'),
      ).toBeInTheDocument(),
    );
    expect(screen.getByRole('button', { name: 'Cast line' })).toBeDisabled();
    expect(screen.queryByRole('heading', { name: 'Fishing is not open' })).not.toBeInTheDocument();
    expect(
      screen.getByRole('button', {
        name: /Worm.*Balance unavailable until the server confirms it/,
      }),
    ).toBeDisabled();

    await view.user.click(screen.getByRole('button', { name: 'Retry balance check' }));
    await waitFor(() => expect(screen.getByRole('button', { name: 'Cast line' })).toBeEnabled());
    expect(screen.getByText('Available credits')).toBeInTheDocument();
  });

  it('immediately hides the caller when a later authoritative read changes public to private', async () => {
    const fixture = installFishingFixture({
      profilePublic: true,
      leaderboardMe: {
        rank: 21,
        species_key: 'koi',
        size_cm: 180,
        display_name: 'Mine only',
        avatar_url: 'https://cdn.discordapp.com/avatars/a/b.png?size=64',
        level4_badge: true,
        is_me: true,
      },
    });
    const view = await renderWithProviders(<FishingGame />, { station: 'user', role: 'user' });
    expect(await screen.findByText('Mine only')).toBeInTheDocument();
    fixture.setProfilePublic(false);
    await view.queryClient.refetchQueries({ queryKey: fishingKeys.config, exact: true });
    await waitFor(() =>
      expect(screen.getByRole('checkbox', { name: 'Show my profile' })).not.toBeChecked(),
    );
    expect(screen.queryByText('Mine only')).not.toBeInTheDocument();
    expect(screen.getAllByText('Anonymous angler').length).toBeGreaterThan(0);
  });

  it('rejects a settle response for another round without rendering or ACKing it', async () => {
    const { calls } = installFishingFixture({
      settleResult: { ...result, round_id: roundId('d') },
      settleCredits: '2500000',
    });
    const rendered = await renderWithProviders(<FishingGame />, { station: 'user', role: 'user' });
    await rendered.user.click(await screen.findByRole('button', { name: 'Cast line' }));
    await waitFor(() =>
      expect(
        rendered.queryClient.getQueryData<{ credits: string }>(fishingKeys.config)?.credits,
      ).toBe('2500000'),
    );
    await screen.findByRole('button', { name: 'Retry settlement' }, { timeout: 7_000 });
    expect(screen.queryByRole('heading', { name: 'Catch confirmed' })).not.toBeInTheDocument();
    expect(calls.ack).toBe(0);
    expect(
      rendered.queryClient.getQueryData<{ credits: string }>(fishingKeys.config)?.credits,
    ).toBe('2500000');
  }, 12_000);

  it('renders the balance from the refreshed games authority, not the settle payload snapshot', async () => {
    installFishingFixture({ settleCredits: '2500000' });
    const rendered = await renderWithProviders(<FishingGame />, { station: 'user', role: 'user' });
    await rendered.user.click(await screen.findByRole('button', { name: 'Cast line' }));
    await waitFor(
      () => expect(screen.getByTestId('fishing-result-balance')).toHaveTextContent('2,500'),
      { timeout: 7_000 },
    );
    expect(screen.getByTestId('fishing-result-balance')).toHaveTextContent('2,500');
    expect(screen.getByTestId('fishing-result-balance')).not.toHaveTextContent('14,500');
  }, 12_000);

  it.each([
    ['bait', { bait: 'lure', price: '2500000' }],
    ['price', { bait: 'worm', price: '5000000' }],
  ] as const)(
    'keeps a %s-mismatched settlement out of the current pending flow',
    async (_label, contextMismatch) => {
      const { calls } = installFishingFixture({ settleResult: { ...result, ...contextMismatch } });
      const rendered = await renderWithProviders(<FishingGame />, {
        station: 'user',
        role: 'user',
      });
      await rendered.user.click(await screen.findByRole('button', { name: 'Cast line' }));
      const retry = await screen.findByRole(
        'button',
        { name: 'Retry settlement' },
        { timeout: 7_000 },
      );
      expect(calls.settle).toBe(1);
      expect(screen.queryByRole('heading', { name: 'Catch confirmed' })).not.toBeInTheDocument();
      expect(screen.queryByText('Won: 12,000')).not.toBeInTheDocument();
      await rendered.user.click(retry);
      expect(calls.settle).toBe(2);
    },
    15_000,
  );

  it('does not offer settlement retry when start fails without a server round', async () => {
    const { calls } = installFishingFixture({
      startFailures: 1,
      startFailureCreatesPending: false,
    });
    const rendered = await renderWithProviders(<FishingGame />, { station: 'user', role: 'user' });
    await rendered.user.click(await screen.findByRole('button', { name: 'Cast line' }));
    await waitFor(() => expect(calls.start).toBe(1));
    expect(await screen.findByText('Start response lost.')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Retry settlement' })).not.toBeInTheDocument();
  });

  it('renders a disabled game without allowing a start', async () => {
    const { calls } = installFishingFixture({ masterEnabled: false });
    const rendered = await renderWithProviders(<FishingGame />, { station: 'user', role: 'user' });
    expect(await screen.findByRole('heading', { name: 'Fishing is not open' })).toBeInTheDocument();
    const cast = screen.getByRole('button', { name: 'Cast line' });
    expect(cast).toBeDisabled();
    expect(calls.start).toBe(0);
    rendered.unmount();
  });
});
