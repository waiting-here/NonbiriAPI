import { copyFile, mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { expect, test, type Page } from './test';
import { collectConsoleViolations, mockPublicConfig, mockRoleSession } from './support';
import { USER_ORIGIN } from './ports';

const NOW = 1_800_000_000;
const SESSION_ID = 'rps_AAAAAAAAAAAAAAAAAAAAAA';
const LINK_SESSION_ID = 'll_AAAAAAAAAAAAAAAAAAAAAA';
const FISHING_BATCH_ID = 'fb_AAAAAAAAAAAAAAAAAAAAAA';

type RPSPhase = 'gesture' | 'dealer_raise' | 'followers';
type RPSMode = 'quick' | 'standard';

function gameMode(base: string) {
  return {
    enabled: true,
    base,
    pumps_bp: { platform: 100, welfare: 50, thursday: 0 },
    queue_seconds: 60,
    gesture_seconds: 10,
    dealer_seconds: 8,
    follower_seconds: 8,
    queue_capacity: 4096,
  };
}

function gamesSnapshot() {
  return {
    server_now: NOW,
    balance: '12345678901234567890.125',
    tutorial_rps_seen: true,
    games_enabled: true,
    fishing: {
      enabled: true,
      available: true,
      bait_prices: { worm: '1', lure: '2.5', premium: '10' },
    },
    linklink: {
      enabled: true,
      specs: {
        '6x8': { enabled: true, price: '3', seconds: 150 },
        '8x8': { enabled: true, price: '4', seconds: 180 },
        '10x10': { enabled: true, price: '5', seconds: 240 },
      },
    },
    rps: {
      enabled: true,
      modes: {
        quick: gameMode('1'),
        standard: gameMode('2'),
        deathmatch: gameMode('3'),
      },
    },
  };
}

function rpsSeat(
  seatNo: number,
  options: {
    readonly currentBalance?: string;
    readonly startingBalance?: string;
    readonly totalInput?: string;
    readonly totalReturned?: string;
    readonly currentRoundInput?: string;
    readonly currentAllIn?: boolean;
    readonly visibleGesture?: 'rock' | 'scissors' | 'paper';
  } = {},
) {
  const startingBalance = options.startingBalance ?? '20';
  const totalInput = options.totalInput ?? '2';
  const totalReturned = options.totalReturned ?? '0';
  const currentBalance = options.currentBalance ?? '18';
  return {
    seat_no: seatNo,
    viewer: seatNo === 0 ? 'self' : 'opponent',
    deletion_state: 'active',
    display_name: `Fixture Player ${seatNo + 1}`,
    avatar_url: null,
    starting_balance: startingBalance,
    current_balance: currentBalance,
    current_round_input: options.currentRoundInput ?? '1',
    current_all_in: options.currentAllIn ?? false,
    total_input: totalInput,
    total_returned: totalReturned,
    timeout_count: '0',
    fun_snapshot:
      seatNo === 0
        ? {
            state: 'full',
            completed_count: '12',
            profitable_count: '7',
            rock_count: '4',
            scissors_count: '4',
            paper_count: '4',
          }
        : { state: 'none' },
    ...(options.visibleGesture ? { visible_gesture: options.visibleGesture } : {}),
  };
}

function rpsState(options: {
  readonly mode?: RPSMode;
  readonly phase?: RPSPhase;
  readonly revision?: string;
  readonly phaseSeq?: string;
  readonly identityEpoch?: string;
  readonly poolTieCount?: string | null;
  readonly dealerRaise?: string | null;
  readonly allIn?: boolean;
}) {
  const mode = options.mode ?? 'standard';
  const phase = options.phase ?? 'gesture';
  const allIn = options.allIn ?? false;
  const dealerRaise = options.dealerRaise ?? (phase === 'followers' ? '4.125' : null);
  const currentActorOptions =
    phase === 'gesture'
      ? ['gesture']
      : phase === 'dealer_raise'
        ? ['dealer_decision']
        : phase === 'followers'
          ? ['follower_decision']
          : [];
  const seats = allIn
    ? [
        rpsSeat(0, {
          currentBalance: '10',
          startingBalance: '20',
          totalInput: '10',
          currentRoundInput: '2',
        }),
        rpsSeat(1, {
          currentBalance: '10',
          startingBalance: '20',
          totalInput: '10',
          currentRoundInput: '2',
        }),
        rpsSeat(2, {
          currentBalance: '11',
          startingBalance: '20',
          totalInput: '9',
          currentRoundInput: '2',
        }),
      ]
    : [
        rpsSeat(0, {
          currentBalance: '12.345',
          startingBalance: '20',
          totalInput: '7.655',
          currentRoundInput: '2',
        }),
        rpsSeat(1, {
          currentBalance: '10.75',
          startingBalance: '20',
          totalInput: '9.25',
          currentRoundInput: '2',
        }),
        rpsSeat(2, {
          currentBalance: '14.5',
          startingBalance: '20',
          totalInput: '5.5',
          currentRoundInput: '2',
        }),
      ];
  const poolPhase = phase === 'gesture' || phase === 'dealer_raise' || phase === 'followers';
  return {
    session_id: SESSION_ID,
    mode,
    state: 'started',
    phase,
    phase_seq: options.phaseSeq ?? '1',
    revision: options.revision ?? '1',
    identity_epoch: options.identityEpoch ?? '1',
    server_now: NOW,
    deadline: NOW + 10,
    rule_snapshot: {
      rules_version: 1,
      base: mode === 'quick' ? '1' : '2',
      pumps_bp: { platform: 100, welfare: 50, thursday: 0 },
      gesture_seconds: 10,
      dealer_seconds: 8,
      follower_seconds: 8,
      standard_multiplier: 5,
      free_tie_reminder: 3,
      free_tie_limit: 6,
    },
    economy: {
      player_pool: '8',
      permanent_multiplier: '1',
      pool_base_multiplier: null,
      current_plan_multiplier: poolPhase ? '1' : null,
      dealer_raise: phase === 'followers' ? dealerRaise : null,
      cuts: { platform: '0', welfare: '0', thursday: '0' },
      welfare_carry: '0',
    },
    seats,
    current_actor_options: currentActorOptions,
    round_summary: {
      base_round_count: '1',
      paid_tie_count: '0',
      free_tie_count: '0',
      paid_pool_streak: '0',
      free_pool_streak: '0',
      reminder_active: false,
      last_reveal_result: null,
      pool_tie_count: options.poolTieCount ?? '0',
    },
    recent_events: [],
    events_truncated: false,
    first_available_seq: '0',
  };
}

function pendingResult(known: boolean, longAmounts = false) {
  const input = longAmounts ? '12345678901234567890.125' : '20';
  const returned = longAmounts ? '12345678901234567891.125' : '21';
  const buyIn = longAmounts ? '9876543210123456789.125' : '5';
  const cashOut = longAmounts ? '9876543210123456790.125' : '6';
  return {
    kind: 'pending_result',
    result: {
      session_id: SESSION_ID,
      mode: 'quick',
      terminal_reason: 'quick_resolved',
      own_seat_no: 0,
      own_input: input,
      own_returned: returned,
      own_wallet_net: '1',
      own_buy_in: known ? buyIn : null,
      own_cash_out: known ? cashOut : null,
      seats: [
        { seat_no: 0, result: 'win', gesture: known ? 'paper' : null },
        { seat_no: 1, result: 'loss', gesture: known ? 'rock' : null },
        { seat_no: 2, result: 'deidentified', gesture: known ? 'rock' : null },
      ],
      created_at: NOW,
    },
  };
}

function idleHome() {
  return {
    kind: 'idle',
    tutorial_seen: true,
    modes: gamesSnapshot().rps.modes,
  };
}

function fishingState(unrevealed: unknown = null) {
  return {
    settlement_pending: null,
    unrevealed,
    has_more_unrevealed: unrevealed !== null,
  };
}

function fishingLeaderboard(board: 'single' | 'recent_single' | 'total') {
  return {
    board,
    window_start: board === 'single' ? null : NOW - 30 * 24 * 60 * 60,
    entries: [],
    me: null,
  };
}

function linkState() {
  return null;
}

function linkActive(
  revision: string,
  pairsRemoved: number,
  rearranged: boolean,
): Record<string, unknown> {
  const tiles = Array.from({ length: 48 }, (_, index) => ({
    row: Math.floor(index / 8),
    col: index % 8,
    tile_key: `tile_${String(Math.floor(index / 4) + 1).padStart(2, '0')}`,
    removed: index < pairsRemoved * 2,
  }));
  if (rearranged) {
    const first = tiles[6].tile_key;
    tiles[6].tile_key = tiles[8].tile_key;
    tiles[8].tile_key = first;
  }
  return {
    session_id: LINK_SESSION_ID,
    spec: '6x8',
    price: '3',
    state: 'active',
    revision,
    board: { rows: 6, cols: 8, tiles },
    pairs_removed: pairsRemoved,
    total_pairs: 24,
    started_at: NOW,
    deadline: NOW + 150,
    server_now: NOW + 10,
  };
}

function linkCompletedSummary(): Record<string, unknown> {
  return {
    session_id: LINK_SESSION_ID,
    spec: '6x8',
    price: '3',
    terminal_reason: 'completed',
    started_at: NOW,
    deadline: NOW + 150,
    terminal_at: NOW + 140,
    pairs_removed: 24,
    total_pairs: 24,
    score: '2410',
  };
}

function fishingResult(tier: 'big' | 'legend'): Record<string, unknown> {
  const legendary = tier === 'legend';
  return {
    batch_id: FISHING_BATCH_ID,
    bait: 'worm',
    count: 1,
    unit_price: '1',
    entry_total: '1',
    outcomes: [
      {
        ordinal: 0,
        species_key: legendary ? 'koi' : 'common_carp',
        tier,
        size_cm: legendary ? 120 : 42,
        reward: legendary ? '8' : '3',
      },
    ],
    payout_total: legendary ? '8' : '3',
    balance: '12345678901234567890.125',
    settled_at: NOW + 1,
    idempotent_replay: false,
  };
}

function leaderboard(mode: RPSMode, board: 'profit_rate' | 'net_profit') {
  return {
    mode,
    board,
    window_days: 30,
    window_start: NOW - 30 * 24 * 60 * 60,
    min_sessions: 10,
    rows: [],
    me: null,
  };
}

async function signedIn(page: Page) {
  await mockRoleSession(page, 'user', 'user');
  await mockPublicConfig(page, 'user');
  await installEventSource(page);
}

async function installEventSource(page: Page) {
  await page.addInitScript(() => {
    const streams: Array<{ readonly emit: (type: string, data: string, id: string) => void }> = [];
    class FixtureEventSource extends EventTarget {
      static readonly CONNECTING = 0;
      static readonly OPEN = 1;
      static readonly CLOSED = 2;
      readonly CONNECTING = 0;
      readonly OPEN = 1;
      readonly CLOSED = 2;
      readyState = 0;
      onopen: (() => void) | null = null;
      onerror: (() => void) | null = null;
      constructor(url: string | URL, options?: EventSourceInit) {
        super();
        void url;
        void options;
        const stream = {
          emit: (type: string, data: string, id: string) => {
            this.dispatchEvent(new MessageEvent(type, { data, lastEventId: id }));
          },
        };
        streams.push(stream);
        queueMicrotask(() => {
          if (this.readyState === 0) {
            this.readyState = 1;
            this.onopen?.();
          }
        });
      }
      close() {
        this.readyState = 2;
      }
    }
    Object.defineProperty(window, 'EventSource', { configurable: true, value: FixtureEventSource });
    Object.defineProperty(window, '__rpsEmit', {
      configurable: false,
      value: (type: string, data: string, id: string) => streams.at(-1)?.emit(type, data, id),
    });
  });
}

async function emitRPSFrame(
  page: Page,
  type: 'snapshot' | 'delta' | 'gap',
  home: unknown,
  metadata: { readonly revision: string | null; readonly identityEpoch: string | null },
) {
  const frame = {
    version: 1,
    channel: 'rps',
    type,
    revision: metadata.revision,
    identity_epoch: metadata.identityEpoch,
    occurred_at: NOW,
    data: home,
  };
  await page.evaluate(
    ({ type: eventType, frame: rawFrame }) => {
      const emit = (
        window as Window & { __rpsEmit?: (name: string, data: string, id: string) => void }
      ).__rpsEmit;
      emit?.(eventType, JSON.stringify(rawFrame), 'sse_AAAAAAAAAAAAAAAAAAAAAA');
    },
    { type, frame },
  );
}

async function installGameRoutes(
  page: Page,
  options: {
    readonly rpsHome?: unknown;
    readonly onRPSAction?: (body: unknown) => unknown;
    readonly onRPSAck?: () => void;
    readonly linkHome?: unknown;
    readonly onLinkStart?: (body: unknown) => unknown;
    readonly onLinkMatch?: (body: unknown, matchNumber: number) => unknown;
    readonly linkMatchFailureAt?: number;
    readonly fishingHome?: unknown;
    readonly onFishingStart?: (body: unknown) => unknown;
    readonly fishingAckFailures?: number;
  } = {},
) {
  let rpsHome: unknown = options.rpsHome ?? { kind: 'session', session: rpsState({}) };
  let linkHome: unknown = options.linkHome ?? linkState();
  let fishingHome: unknown = options.fishingHome ?? fishingState();
  let linkMatchNumber = 0;
  let fishingAckFailures = options.fishingAckFailures ?? 0;
  let linkGetCount = 0;
  let fishingGetCount = 0;
  await page.route('**/api/games**', async (route) => {
    const method = route.request().method();
    const url = new URL(route.request().url());
    if (url.pathname === '/api/games' && method === 'GET') {
      await route.fulfill({ json: gamesSnapshot() });
      return;
    }
    if (url.pathname === '/api/games/rps/state' && method === 'GET') {
      await route.fulfill({ json: rpsHome });
      return;
    }
    if (url.pathname.includes('/rps/leaderboard') && method === 'GET') {
      const mode = (url.searchParams.get('mode') ?? 'quick') as RPSMode;
      const board = (url.searchParams.get('board') ?? 'profit_rate') as
        'profit_rate' | 'net_profit';
      await route.fulfill({ json: leaderboard(mode, board) });
      return;
    }
    if (url.pathname.endsWith('/lease') && method === 'POST') {
      await route.fulfill({ json: { expires_at: NOW + 30 } });
      return;
    }
    if (url.pathname.endsWith('/actions') && method === 'POST') {
      const body = route.request().postDataJSON();
      rpsHome = options.onRPSAction?.(body) ?? rpsHome;
      await route.fulfill({ json: rpsHome });
      return;
    }
    if (url.pathname.endsWith('/pending-result/ack') && method === 'POST') {
      options.onRPSAck?.();
      rpsHome = idleHome();
      await route.fulfill({ status: 204 });
      return;
    }
    if (url.pathname.endsWith('/tutorial/seen') && method === 'POST') {
      await route.fulfill({ status: 204 });
      return;
    }
    if (url.pathname === '/api/games/linklink/sessions' && method === 'POST') {
      const body = route.request().postDataJSON();
      linkHome = options.onLinkStart?.(body) ?? linkActive('1', 0, false);
      await route.fulfill({ status: 201, json: linkHome });
      return;
    }
    if (
      url.pathname.startsWith('/api/games/linklink/sessions/') &&
      url.pathname.endsWith('/matches') &&
      method === 'POST'
    ) {
      const body = route.request().postDataJSON();
      linkMatchNumber += 1;
      if (options.linkMatchFailureAt === linkMatchNumber) {
        await route.fulfill({
          status: 409,
          json: { error: { code: 'conflict', message: 'The LinkLink board changed.' } },
        });
        return;
      }
      linkHome =
        options.onLinkMatch?.(body, linkMatchNumber) ??
        linkActive(String(linkMatchNumber + 1), 1, false);
      await route.fulfill({ json: linkHome });
      return;
    }
    if (
      url.pathname.startsWith('/api/games/linklink/sessions/') &&
      url.pathname.endsWith('/abandon') &&
      method === 'POST'
    ) {
      linkHome = {
        ...linkCompletedSummary(),
        terminal_reason: 'abandoned',
        score: null,
      };
      await route.fulfill({ json: linkHome });
      return;
    }
    if (url.pathname === '/api/games/fishing/batches' && method === 'POST') {
      const body = route.request().postDataJSON();
      const response = options.onFishingStart?.(body) ?? fishingResult('big');
      fishingHome =
        response && typeof response === 'object' && 'outcomes' in response
          ? fishingState(response)
          : fishingState();
      await route.fulfill({ json: response });
      return;
    }
    if (
      url.pathname.startsWith('/api/games/fishing/batches/') &&
      url.pathname.endsWith('/ack') &&
      method === 'POST'
    ) {
      if (fishingAckFailures > 0) {
        fishingAckFailures -= 1;
        await route.fulfill({
          status: 500,
          json: { error: { code: 'temporary', message: 'The result is still being recorded.' } },
        });
        return;
      }
      await route.fulfill({ status: 204 });
      return;
    }
    if (
      url.pathname.startsWith('/api/games/fishing/batches/') &&
      url.pathname.endsWith('/recover') &&
      method === 'POST'
    ) {
      const response = fishingHome;
      await route.fulfill({ json: response });
      return;
    }
    if (url.pathname === '/api/games/fishing/state' && method === 'GET') {
      fishingGetCount += 1;
      await route.fulfill({ json: fishingHome });
      return;
    }
    if (url.pathname.endsWith('/fishing/leaderboard') && method === 'GET') {
      const board = url.searchParams.get('board');
      if (board !== 'single' && board !== 'recent_single' && board !== 'total') {
        await route.fallback();
        return;
      }
      await route.fulfill({ json: fishingLeaderboard(board) });
      return;
    }
    if (url.pathname === '/api/games/linklink/session' && method === 'GET') {
      linkGetCount += 1;
      await route.fulfill({ json: linkHome });
      return;
    }
    await route.fallback();
  });
  return {
    getRPSHome: () => rpsHome,
    setRPSHome: (next: unknown) => {
      rpsHome = next;
    },
    getLinkHome: () => linkHome,
    getLinkGetCount: () => linkGetCount,
    getLinkMatchNumber: () => linkMatchNumber,
    setLinkHome: (next: unknown) => {
      linkHome = next;
    },
    getFishingHome: () => fishingHome,
    getFishingGetCount: () => fishingGetCount,
    setFishingHome: (next: unknown) => {
      fishingHome = next;
    },
  };
}

async function assertNoHorizontalOverflow(page: Page) {
  await expect
    .poll(() => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth))
    .toBe(true);
}

async function scrollAllResultSeats(page: Page) {
  const seats = page.locator('[data-result-seat]');
  for (let index = 0; index < 3; index += 1) {
    await seats.nth(index).scrollIntoViewIfNeeded();
    await expect(seats.nth(index)).toBeVisible();
  }
  await page.locator('.rps-result section').evaluate((element) => {
    element.scrollIntoView({ block: 'center', inline: 'nearest' });
  });
}

async function saveScreenshot(page: Page, name: string) {
  const outputPath = test.info().outputPath(name);
  const image = await page.screenshot({ path: outputPath, fullPage: true });
  const evidenceDir = process.env.NONBIRI_EVIDENCE_DIR;
  if (evidenceDir) {
    await mkdir(evidenceDir, { recursive: true });
    await copyFile(outputPath, join(evidenceDir, name));
  }
  await test.info().attach(name, { body: image, contentType: 'image/png' });
}

async function saveJsonArtifact(name: string, value: unknown) {
  const body = JSON.stringify(value, null, 2);
  const outputPath = test.info().outputPath(name);
  await writeFile(outputPath, body, 'utf8');
  const evidenceDir = process.env.NONBIRI_EVIDENCE_DIR;
  if (evidenceDir) {
    await mkdir(evidenceDir, { recursive: true });
    await copyFile(outputPath, join(evidenceDir, name));
  }
  await test.info().attach(name, { body, contentType: 'application/json' });
}

test.describe('RPS result presentation and amount lifecycle', () => {
  for (const known of [true, false] as const) {
    test(`quick terminal reveals all three gestures and ${known ? 'shows' : 'preserves'} transfer values${known ? '' : ' in a static no-ACK fixture'}`, async ({
      page,
    }) => {
      const errors = collectConsoleViolations(page);
      await signedIn(page);
      await page.setViewportSize({ width: 390, height: 844 });
      if (!known) {
        await page.addInitScript(() => {
          class FixtureIntersectionObserver {
            observe() {}
            disconnect() {}
          }
          Object.defineProperty(window, 'IntersectionObserver', {
            configurable: true,
            value: FixtureIntersectionObserver,
          });
        });
      }
      const pending = pendingResult(known, true);
      let ackCount = 0;
      await installGameRoutes(page, {
        rpsHome: pending,
        onRPSAck: () => {
          ackCount += 1;
        },
      });
      await page.goto(`${USER_ORIGIN}/games/rps`);
      await expect(page.locator('.rps-result')).toBeVisible();
      await expect(page.locator('[data-result-seat]')).toHaveCount(3);
      await expect(page.locator('.rps-result__seats svg')).toHaveCount(known ? 3 : 0);
      if (known) {
        await expect(page.locator('[data-result-seat]').nth(0)).toContainText('Paper');
        await expect(page.locator('[data-result-seat]').nth(1)).toContainText('Rock');
        await expect(page.locator('[data-result-seat]').nth(2)).toContainText('Rock');
      }
      const resultText = page.locator('.rps-result');
      if (known) {
        await expect(resultText).toContainText('Starting buy-in (actual input)');
        await expect(resultText).toContainText('Ending cash-out (actual return)');
        await expect(resultText).toContainText('9,876,543,210,123,456,789.125');
      } else {
        await expect(resultText).toHaveText(/Not recorded for this historical result/);
        await expect(
          resultText.locator('text=Not recorded for this historical result'),
        ).toHaveCount(5);
      }
      await assertNoHorizontalOverflow(page);
      await saveScreenshot(page, `rps-result-${known ? 'known' : 'legacy'}-390.png`);
      if (known) {
        await expect.poll(() => ackCount).toBe(1);
        await expect(page.getByRole('button', { name: 'Back to the lobby' })).toBeVisible();
        await page.setViewportSize({ width: 320, height: 740 });
        await expect(resultText).toContainText('9,876,543,210,123,456,789.125');
        await assertNoHorizontalOverflow(page);
        await page.setViewportSize({ width: 844, height: 390 });
        await expect(resultText).toBeVisible();
        await expect.poll(() => ackCount).toBe(1);
        await assertNoHorizontalOverflow(page);
        await expect(resultText).toContainText('9,876,543,210,123,456,789.125');
        await assertNoHorizontalOverflow(page);
        await saveScreenshot(page, 'rps-result-known-landscape.png');
      } else {
        expect(ackCount).toBe(0);
      }
      errors.assertNone();
    });
  }

  test('historical null transfer values are acknowledged after real result visibility', async ({
    page,
  }) => {
    const errors = collectConsoleViolations(page);
    await signedIn(page);
    await page.setViewportSize({ width: 390, height: 844 });
    let ackCount = 0;
    await installGameRoutes(page, {
      rpsHome: pendingResult(false, true),
      onRPSAck: () => {
        ackCount += 1;
      },
    });
    await page.goto(`${USER_ORIGIN}/games/rps`);
    await expect(page.locator('.rps-result')).toBeVisible();
    await expect(page.locator('.rps-result__seats svg')).toHaveCount(0);
    await expect(page.locator('.rps-result')).toHaveText(/Not recorded for this historical result/);
    await scrollAllResultSeats(page);
    await expect.poll(() => ackCount).toBe(1);
    await expect(page.getByRole('button', { name: 'Back to the lobby' })).toBeVisible();
    await assertNoHorizontalOverflow(page);
    errors.assertNone();
  });

  for (const view of [
    { name: '320px portrait', width: 320, height: 740, large: false },
    { name: '844px landscape', width: 844, height: 390, large: false },
    { name: 'large font at 390px', width: 390, height: 844, large: true },
  ] as const) {
    test(`initial pending result is acknowledged at ${view.name}`, async ({ page }) => {
      const errors = collectConsoleViolations(page);
      await signedIn(page);
      await page.setViewportSize({ width: view.width, height: view.height });
      let ackCount = 0;
      await installGameRoutes(page, {
        rpsHome: pendingResult(true, true),
        onRPSAck: () => {
          ackCount += 1;
        },
      });
      if (view.large) {
        await page.goto(`${USER_ORIGIN}/account`);
        await page.getByLabel('Large', { exact: true }).check();
        await expect(page.locator('html')).toHaveAttribute('data-font-size', 'large');
      }
      await page.goto(`${USER_ORIGIN}/games/rps`);
      await expect(page.locator('.rps-result')).toBeVisible();
      await expect(page.locator('.rps-result')).toContainText('9,876,543,210,123,456,789.125');
      await scrollAllResultSeats(page);
      await expect.poll(() => ackCount).toBe(1);
      await expect(page.getByRole('button', { name: 'Back to the lobby' })).toBeVisible();
      await assertNoHorizontalOverflow(page);
      if (view.large) await saveScreenshot(page, 'rps-result-known-large-390.png');
      errors.assertNone();
    });
  }

  test('quick gesture action posts the selected gesture before displaying the authoritative terminal result', async ({
    page,
  }) => {
    const errors = collectConsoleViolations(page);
    await signedIn(page);
    const sessionHome = { kind: 'session', session: rpsState({ mode: 'quick', phase: 'gesture' }) };
    const terminal = pendingResult(true);
    const actions: unknown[] = [];
    await installGameRoutes(page, {
      rpsHome: sessionHome,
      onRPSAction: (body) => {
        actions.push(body);
        return terminal;
      },
    });
    await page.addInitScript(() => {
      class FixtureIntersectionObserver {
        observe() {}
        disconnect() {}
      }
      Object.defineProperty(window, 'IntersectionObserver', {
        configurable: true,
        value: FixtureIntersectionObserver,
      });
    });
    await page.goto(`${USER_ORIGIN}/games/rps`);
    await expect(page.getByRole('button', { name: 'Paper' })).toBeEnabled();
    await page.getByRole('button', { name: 'Paper' }).click();
    await expect(page.locator('.rps-result')).toBeVisible();
    expect(actions).toHaveLength(1);
    expect(actions[0]).toMatchObject({
      phase_seq: '1',
      expected_revision: '1',
      action: 'gesture',
      payload: { gesture: 'paper' },
    });
    errors.assertNone();
  });
});

test('RPS result follows the real language and theme settings on a narrow phone viewport', async ({
  page,
}) => {
  const errors = collectConsoleViolations(page);
  await signedIn(page);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.addInitScript(() => {
    class FixtureIntersectionObserver {
      observe() {}
      disconnect() {}
    }
    Object.defineProperty(window, 'IntersectionObserver', {
      configurable: true,
      value: FixtureIntersectionObserver,
    });
  });
  await installGameRoutes(page, { rpsHome: pendingResult(true, true) });
  await page.goto(`${USER_ORIGIN}/games/rps`);
  await expect(page.locator('.rps-result')).toBeVisible();

  await page.locator('.nb-menu-button').click();
  await page.locator('.nb-user-drawer-actions .theme-select').selectOption('dark');
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
  await page.locator('.nb-menu-button').click();

  await page.goto(`${USER_ORIGIN}/privacy`);
  const chinese = page.locator('.lang-switcher button').filter({ hasText: '中文' });
  await expect(chinese).toBeVisible();
  await chinese.click();
  await expect(page.locator('html')).toHaveAttribute('lang', 'zh-CN');
  await page.goto(`${USER_ORIGIN}/games/rps`);
  await expect(page.locator('.rps-result')).toContainText('本局战报');
  await expect(page.locator('.rps-result')).toContainText('9,876,543,210,123,456,789.125');
  await assertNoHorizontalOverflow(page);
  errors.assertNone();
});

test.describe('RPS dealer and follower controls', () => {
  test('shortcuts are local drafts, clamp fractional balances, and submit the latest integer only', async ({
    page,
  }) => {
    const errors = collectConsoleViolations(page);
    await signedIn(page);
    const actions: unknown[] = [];
    await installGameRoutes(page, {
      rpsHome: { kind: 'session', session: rpsState({ phase: 'dealer_raise' }) },
      onRPSAction: (body) => {
        actions.push(body);
        return {
          kind: 'session',
          session: rpsState({
            phase: 'followers',
            revision: '2',
            phaseSeq: '2',
            dealerRaise: '10',
          }),
        };
      },
    });
    await page.goto(`${USER_ORIGIN}/games/rps`);
    await expect(page.locator('.rps-dealer input')).toBeEnabled();
    const input = page.locator('.rps-dealer input');
    const add = page.getByRole('button', { name: '+2 credits' });
    const subtract = page.getByRole('button', { name: '−2 credits' });
    const maximum = page.getByRole('button', { name: 'Max 10 credits' });
    await expect(add).toBeVisible();
    await input.fill('');
    await add.click();
    await expect(input).toHaveValue('2');
    expect(actions).toHaveLength(0);
    await input.fill('999.999');
    await subtract.click();
    await expect(input).toHaveValue('10');
    await maximum.click();
    await expect(input).toHaveValue('10');
    await expect(page.locator('.rps-raise-flash')).toHaveClass(/is-maximum/);
    expect(actions).toHaveLength(0);
    await page.getByRole('button', { name: 'Raise', exact: true }).click();
    await expect.poll(() => actions.length).toBe(1);
    expect(actions[0]).toMatchObject({
      phase_seq: '1',
      expected_revision: '1',
      action: 'dealer_decision',
      payload: { decision: 'raise', amount: '10' },
    });
    await expect(page.getByRole('heading', { name: 'Follower decision' }).first()).toBeVisible();
    errors.assertNone();
  });

  test('labels a complete whole-credit remainder as all in and shows the authoritative follower raise', async ({
    page,
  }) => {
    const errors = collectConsoleViolations(page);
    await signedIn(page);
    const routes = await installGameRoutes(page, {
      rpsHome: { kind: 'session', session: rpsState({ phase: 'dealer_raise', allIn: true }) },
    });
    await page.goto(`${USER_ORIGIN}/games/rps`);
    await expect(page.getByRole('button', { name: 'All in 10 credits' })).toBeVisible();
    await page.getByRole('button', { name: 'All in 10 credits' }).click();
    await expect(page.locator('.rps-dealer input')).toHaveValue('10');
    routes.setRPSHome({
      kind: 'session',
      session: rpsState({ phase: 'followers', revision: '2', phaseSeq: '2', allIn: true }),
    });
    await page.goto(`${USER_ORIGIN}/games/rps?follower=1`);
    await expect(page.getByText('Required to call: 4.125 credits')).toBeVisible();
    await expect(page.getByRole('button', { name: 'Call' })).toBeEnabled();
    errors.assertNone();
  });
});

test('RPS stream transitions emphasize a new phase, disable during a gap, and recover on a full snapshot', async ({
  page,
}) => {
  const errors = collectConsoleViolations(page);
  await signedIn(page);
  const initial = rpsState({ phase: 'gesture', revision: '1', phaseSeq: '1' });
  await installGameRoutes(page, { rpsHome: { kind: 'session', session: initial } });
  await page.goto(`${USER_ORIGIN}/games/rps`);
  await expect(page.getByRole('button', { name: 'Rock' })).toBeEnabled();
  await emitRPSFrame(
    page,
    'snapshot',
    { kind: 'session', session: initial },
    { revision: '1', identityEpoch: '1' },
  );
  const dealer = rpsState({ phase: 'dealer_raise', revision: '2', phaseSeq: '2' });
  await emitRPSFrame(
    page,
    'delta',
    { kind: 'session', session: dealer },
    { revision: '2', identityEpoch: '1' },
  );
  await expect(page.locator('.rps-phase-flash')).toHaveCount(1);
  await expect(page.getByRole('button', { name: 'Do not raise' })).toBeEnabled();
  const flash = page.locator('.rps-phase-flash');
  await emitRPSFrame(
    page,
    'delta',
    { kind: 'session', session: dealer },
    { revision: '2', identityEpoch: '1' },
  );
  await expect(page.locator('.rps-phase-flash')).toHaveCount(1);
  await expect(page.locator('.rps-phase-flash')).toHaveAttribute('aria-hidden', 'true');
  await emitRPSFrame(
    page,
    'gap',
    { reason: 'ring_evicted', last_event_id: null },
    { revision: null, identityEpoch: null },
  );
  await expect(page.getByRole('button', { name: 'Do not raise' })).toBeDisabled();
  await expect(page.getByText('Waiting for the current decision to sync…')).toBeVisible();
  await emitRPSFrame(
    page,
    'snapshot',
    { kind: 'session', session: dealer },
    { revision: '2', identityEpoch: '1' },
  );
  await expect(page.getByRole('button', { name: 'Do not raise' })).toBeEnabled();
  await expect(flash).toHaveCount(0);
  errors.assertNone();
});

test('RPS atmosphere uses authoritative pool tie bands, particle caps, reduced motion, and background pause', async ({
  page,
}) => {
  const errors = collectConsoleViolations(page);
  await signedIn(page);
  const initial = rpsState({ poolTieCount: '0', revision: '1' });
  await installGameRoutes(page, { rpsHome: { kind: 'session', session: initial } });
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto(`${USER_ORIGIN}/games/rps`);
  await expect(page.locator('.rps-atmosphere')).toHaveAttribute('data-level', '0');
  const expectedDesktop: readonly [string, number][] = [
    ['1', 8],
    ['3', 20],
    ['5', 32],
  ];
  for (const [count, particles] of expectedDesktop) {
    const revision = String(Number(count) + 1);
    const next = rpsState({ poolTieCount: count, revision });
    await emitRPSFrame(
      page,
      'delta',
      { kind: 'session', session: next },
      { revision, identityEpoch: '1' },
    );
    await expect(page.locator('.rps-atmosphere')).toHaveAttribute(
      'data-level',
      Number(count) >= 5 ? '3' : Number(count) >= 3 ? '2' : '1',
    );
    await expect(page.locator('.rps-atmosphere i')).toHaveCount(particles);
  }
  const metrics = await page.evaluate(async () => {
    const layoutStart = performance.now();
    const items = Array.from(document.querySelectorAll<HTMLElement>('.rps-atmosphere i'));
    const rects = items.map((item) => item.getBoundingClientRect());
    const layoutReadMs = performance.now() - layoutStart;
    const frameIntervalsMs = await new Promise<number[]>((resolve) => {
      const samples: number[] = [];
      let previous: number | undefined;
      const sample = (now: number) => {
        if (previous !== undefined) samples.push(now - previous);
        previous = now;
        if (samples.length === 6) resolve(samples);
        else requestAnimationFrame(sample);
      };
      requestAnimationFrame(sample);
    });
    return {
      viewport: { width: innerWidth, height: innerHeight },
      particleCount: items.length,
      measuredRects: rects.length,
      layoutReadMs,
      frameIntervalsMs,
      documentScrollWidth: document.documentElement.scrollWidth,
    };
  });
  expect(metrics.particleCount).toBe(32);
  expect(metrics.measuredRects).toBe(32);
  expect(metrics.layoutReadMs).toBeGreaterThanOrEqual(0);
  expect(metrics.frameIntervalsMs).toHaveLength(6);
  expect(metrics.frameIntervalsMs.every((interval) => interval >= 0)).toBe(true);
  await saveJsonArtifact('rps-atmosphere-metrics.json', metrics);
  await page.setViewportSize({ width: 390, height: 844 });
  const mobile = rpsState({ poolTieCount: '5', revision: '7' });
  await emitRPSFrame(
    page,
    'delta',
    { kind: 'session', session: mobile },
    { revision: '7', identityEpoch: '1' },
  );
  await expect(page.locator('.rps-atmosphere i')).toHaveCount(12);
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await expect
    .poll(() =>
      page
        .locator('.rps-atmosphere i')
        .evaluateAll((items) => items.every((item) => getComputedStyle(item).display === 'none')),
    )
    .toBe(true);
  await page.emulateMedia({ reducedMotion: 'no-preference' });
  await expect(page.locator('.rps-atmosphere i')).toHaveCount(12);
  await page.evaluate(() => {
    let visible = true;
    Object.defineProperty(document, 'visibilityState', {
      configurable: true,
      get: () => (visible ? 'visible' : 'hidden'),
    });
    visible = false;
    document.dispatchEvent(new Event('visibilitychange'));
  });
  await expect(page.locator('.rps-match')).toHaveClass(/is-background/);
  await expect
    .poll(() =>
      page
        .locator('.rps-atmosphere i')
        .evaluateAll((items) => items.map((item) => getComputedStyle(item).animationPlayState)),
    )
    .toEqual(new Array(12).fill('paused'));
  errors.assertNone();
});

type AudioVoice = {
  readonly frequencyValues: readonly number[];
  readonly gainSetCount: number;
  readonly gainRampCount: number;
  readonly startCalls: number;
  readonly stopCalls: number;
};

type AudioContextProbe = {
  readonly state: string;
  readonly resumeCalls: number;
  readonly closeCalls: number;
  readonly oscillatorStarts: number;
  readonly voices: readonly AudioVoice[];
};

function voiceCount(voices: readonly AudioVoice[], frequencies: readonly number[]) {
  return voices.filter((voice) =>
    frequencies.some((frequency) =>
      voice.frequencyValues.some((value) => Math.abs(value - frequency) < 0.01),
    ),
  ).length;
}

function assertEnvelopedCue(voices: readonly AudioVoice[], frequencies: readonly number[]) {
  const selected = voices.filter((voice) =>
    frequencies.some((frequency) =>
      voice.frequencyValues.some((value) => Math.abs(value - frequency) < 0.01),
    ),
  );
  expect(selected, `expected a cue with frequencies ${frequencies.join(', ')}`).not.toHaveLength(0);
  for (const voice of selected) {
    expect(voice.frequencyValues.length).toBeGreaterThan(0);
    expect(voice.gainSetCount).toBeGreaterThanOrEqual(2);
    expect(voice.gainRampCount).toBe(2);
    expect(voice.startCalls).toBe(1);
    expect(voice.stopCalls).toBe(1);
  }
}

test('game sound uses one real AudioContext per mounted game, remembers each game boolean, cleans up, and recovers without replay', async ({
  page,
}) => {
  const consoleErrors: string[] = [];
  page.on('console', (message) => {
    if (message.type() === 'error') consoleErrors.push(message.text());
  });
  page.on('pageerror', (error) => consoleErrors.push(`pageerror: ${error.message}`));
  await signedIn(page);
  await page.addInitScript(() => {
    const OriginalAudioContext = window.AudioContext;
    const probe = {
      contexts: [] as Array<{
        state: string;
        resumeCalls: number;
        closeCalls: number;
        oscillatorStarts: number;
        voices: Array<{
          frequencyValues: number[];
          gainSetCount: number;
          gainRampCount: number;
          startCalls: number;
          stopCalls: number;
        }>;
      }>,
    };
    if (typeof OriginalAudioContext === 'function') {
      function WrappedAudioContext(
        this: unknown,
        ...args: ConstructorParameters<typeof OriginalAudioContext>
      ) {
        const context = new OriginalAudioContext(...args);
        const record = {
          state: context.state,
          resumeCalls: 0,
          closeCalls: 0,
          oscillatorStarts: 0,
          voices: [] as Array<{
            frequencyValues: number[];
            gainSetCount: number;
            gainRampCount: number;
            startCalls: number;
            stopCalls: number;
          }>,
        };
        probe.contexts.push(record);
        const pendingVoices: Array<(typeof record.voices)[number]> = [];
        const originalResume = context.resume.bind(context);
        context.resume = (() => {
          record.resumeCalls += 1;
          return originalResume().then(() => {
            record.state = context.state;
          });
        }) as typeof context.resume;
        const originalClose = context.close.bind(context);
        context.close = (() => {
          record.closeCalls += 1;
          return originalClose().then(() => {
            record.state = context.state;
          });
        }) as typeof context.close;
        const wrapParam = (
          parameter: AudioParam,
          voice: (typeof record.voices)[number],
          kind: 'frequency' | 'gain',
        ) => {
          const originalSet = parameter.setValueAtTime.bind(parameter);
          parameter.setValueAtTime = ((value: number, when: number) => {
            if (kind === 'frequency') voice.frequencyValues.push(value);
            else voice.gainSetCount += 1;
            return originalSet(value, when);
          }) as typeof parameter.setValueAtTime;
          const originalLinearRamp = parameter.linearRampToValueAtTime.bind(parameter);
          parameter.linearRampToValueAtTime = ((value: number, when: number) => {
            if (kind === 'gain') voice.gainRampCount += 1;
            return originalLinearRamp(value, when);
          }) as typeof parameter.linearRampToValueAtTime;
          const originalExponentialRamp = parameter.exponentialRampToValueAtTime.bind(parameter);
          parameter.exponentialRampToValueAtTime = ((value: number, when: number) => {
            if (kind === 'gain') voice.gainRampCount += 1;
            return originalExponentialRamp(value, when);
          }) as typeof parameter.exponentialRampToValueAtTime;
        };
        const originalCreateOscillator = context.createOscillator.bind(context);
        context.createOscillator = (() => {
          const oscillator = originalCreateOscillator();
          const voice = {
            frequencyValues: [] as number[],
            gainSetCount: 0,
            gainRampCount: 0,
            startCalls: 0,
            stopCalls: 0,
          };
          record.voices.push(voice);
          pendingVoices.push(voice);
          wrapParam(oscillator.frequency, voice, 'frequency');
          const originalStart = oscillator.start.bind(oscillator);
          oscillator.start = ((when?: number) => {
            record.oscillatorStarts += 1;
            voice.startCalls += 1;
            return originalStart(when);
          }) as typeof oscillator.start;
          const originalStop = oscillator.stop.bind(oscillator);
          oscillator.stop = ((when?: number) => {
            voice.stopCalls += 1;
            return originalStop(when);
          }) as typeof oscillator.stop;
          return oscillator;
        }) as typeof context.createOscillator;
        const originalCreateGain = context.createGain.bind(context);
        context.createGain = (() => {
          const gain = originalCreateGain();
          const voice = pendingVoices.shift();
          if (voice) wrapParam(gain.gain, voice, 'gain');
          return gain;
        }) as typeof context.createGain;
        return context;
      }
      Object.setPrototypeOf(WrappedAudioContext, OriginalAudioContext);
      WrappedAudioContext.prototype = OriginalAudioContext.prototype;
      Object.defineProperty(window, 'AudioContext', {
        configurable: true,
        writable: true,
        value: WrappedAudioContext,
      });
    }
    Object.defineProperty(window, '__audioProbe', { configurable: false, value: probe });
  });
  const session = rpsState({ mode: 'quick', phase: 'gesture' });
  const routes = await installGameRoutes(page, {
    rpsHome: { kind: 'session', session },
    onRPSAction: () => ({ kind: 'session', session }),
    linkMatchFailureAt: 2,
    onLinkMatch: (_body, matchNumber) => {
      if (matchNumber === 1) return linkActive('2', 1, false);
      if (matchNumber === 3) return linkActive('3', 2, true);
      return linkCompletedSummary();
    },
    onFishingStart: () => fishingResult('big'),
    fishingAckFailures: 1,
  });
  const readAudioContexts = () =>
    page.evaluate(
      () =>
        (window as Window & { __audioProbe?: { contexts: AudioContextProbe[] } }).__audioProbe
          ?.contexts ?? [],
    );
  await page.goto(`${USER_ORIGIN}/games/rps`);
  await expect(page.getByRole('button', { name: 'Sound off' })).toBeVisible();
  expect(
    await page.evaluate(
      () =>
        (window as Window & { __audioProbe?: { contexts: unknown[] } }).__audioProbe?.contexts
          .length,
    ),
  ).toBe(0);
  await page.getByRole('button', { name: 'Sound off' }).click();
  await expect
    .poll(() =>
      page.evaluate(
        () =>
          (window as Window & { __audioProbe?: { contexts: Array<{ resumeCalls: number }> } })
            .__audioProbe?.contexts.length,
      ),
    )
    .toBe(1);
  await expect
    .poll(() =>
      page.evaluate(
        () =>
          (window as Window & { __audioProbe?: { contexts: Array<{ resumeCalls: number }> } })
            .__audioProbe?.contexts[0]?.resumeCalls ?? 0,
      ),
    )
    .toBeGreaterThan(0);
  const rpsContext = (await readAudioContexts())[0];
  expect(rpsContext).toBeDefined();
  const rpsStartsBeforePaper = rpsContext.oscillatorStarts;
  await page.getByRole('button', { name: 'Paper' }).click();
  await expect
    .poll(() =>
      page.evaluate(
        () =>
          (window as Window & { __audioProbe?: { contexts: Array<{ oscillatorStarts: number }> } })
            .__audioProbe?.contexts[0]?.oscillatorStarts ?? 0,
      ),
    )
    .toBeGreaterThan(0);
  const rpsAfterPaper = (await readAudioContexts())[0];
  const paperVoices = rpsAfterPaper.voices.slice(
    rpsAfterPaper.voices.length - (rpsAfterPaper.oscillatorStarts - rpsStartsBeforePaper),
  );
  assertEnvelopedCue(paperVoices, [440]);
  const startsBeforeHidden = await page.evaluate(
    () =>
      (window as Window & { __audioProbe?: { contexts: Array<{ oscillatorStarts: number }> } })
        .__audioProbe?.contexts[0]?.oscillatorStarts ?? 0,
  );
  await page.evaluate(() => {
    let visible = true;
    Object.defineProperty(document, 'visibilityState', {
      configurable: true,
      get: () => (visible ? 'visible' : 'hidden'),
    });
    visible = false;
    document.dispatchEvent(new Event('visibilitychange'));
  });
  await page.getByRole('button', { name: 'Rock' }).click();
  await page.waitForTimeout(100);
  const startsWhileHidden = await page.evaluate(
    () =>
      (window as Window & { __audioProbe?: { contexts: Array<{ oscillatorStarts: number }> } })
        .__audioProbe?.contexts[0]?.oscillatorStarts ?? 0,
  );
  expect(startsWhileHidden).toBe(startsBeforeHidden);
  await page.evaluate(() => {
    Object.defineProperty(document, 'visibilityState', {
      configurable: true,
      get: () => 'visible',
    });
    document.dispatchEvent(new Event('visibilitychange'));
  });
  await page.getByRole('button', { name: 'Scissors' }).click();
  await expect
    .poll(() =>
      page.evaluate(
        () =>
          (window as Window & { __audioProbe?: { contexts: Array<{ oscillatorStarts: number }> } })
            .__audioProbe?.contexts[0]?.oscillatorStarts ?? 0,
      ),
    )
    .toBeGreaterThan(startsWhileHidden);
  await page.getByRole('link', { name: 'Back to game center' }).click();
  await expect(page).toHaveURL(/\/games$/);
  await page.locator('a[href="/games/linklink"]').click();
  await expect(page.getByRole('button', { name: 'Sound off' })).toBeVisible();
  await expect
    .poll(() =>
      page.evaluate(
        () =>
          (window as Window & { __audioProbe?: { contexts: Array<{ closeCalls: number }> } })
            .__audioProbe?.contexts[0]?.closeCalls ?? 0,
      ),
    )
    .toBe(1);
  await page.getByRole('button', { name: 'Sound off' }).click();
  await expect
    .poll(() =>
      page.evaluate(
        () =>
          (window as Window & { __audioProbe?: { contexts: unknown[] } }).__audioProbe?.contexts
            .length,
      ),
    )
    .toBe(2);
  await expect(page.getByRole('button', { name: 'Start 6x8', exact: true }).first()).toBeEnabled();
  await page.getByRole('button', { name: 'Start 6x8', exact: true }).first().click();
  await expect(page.getByRole('alertdialog')).toBeVisible();
  await page
    .getByRole('alertdialog')
    .getByRole('button', { name: 'Start 6x8', exact: true })
    .click();
  await expect(page.locator('.linklink-board')).toBeVisible();
  const linkTiles = page.locator('.linklink-tile:not(.is-removed)');
  await expect(linkTiles.first()).toBeEnabled();
  await linkTiles.nth(0).click();
  await expect(linkTiles.nth(0)).toHaveAttribute('aria-selected', 'true');
  await linkTiles.nth(1).click();
  expect(routes.getLinkMatchNumber()).toBe(1);
  await expect(page.locator('.linklink-progress progress')).toHaveAttribute('value', '1');
  await expect(page.locator('.linklink-tile.is-removed')).toHaveCount(2);
  const linkAfterMatch = await readAudioContexts();
  const matchContext = linkAfterMatch[1];
  expect(voiceCount(matchContext.voices, [660, 880])).toBe(2);
  assertEnvelopedCue(matchContext.voices, [660, 880]);
  const linkMatchCueCount = voiceCount(matchContext.voices, [660, 880]);
  const activeAfterMatch = page.locator('.linklink-tile:not(.is-removed)');
  await activeAfterMatch.nth(0).click();
  await activeAfterMatch.nth(1).click();
  await expect(page.locator('.linklink-feedback')).toContainText('This pair could not be removed.');
  const linkAfterRejected = await readAudioContexts();
  expect(voiceCount(linkAfterRejected[1].voices, [660, 880])).toBe(linkMatchCueCount);
  const activeAfterRejected = page.locator('.linklink-tile:not(.is-removed)');
  await activeAfterRejected.nth(0).click();
  await activeAfterRejected.nth(1).click();
  await expect(page.locator('.linklink-feedback')).toContainText('No moves remained');
  const linkAfterShuffle = await readAudioContexts();
  expect(voiceCount(linkAfterShuffle[1].voices, [330, 495])).toBe(2);
  assertEnvelopedCue(linkAfterShuffle[1].voices, [330, 495]);
  const activeAfterRecovery = page.locator('.linklink-tile:not(.is-removed)');
  await activeAfterRecovery.nth(0).click();
  await activeAfterRecovery.nth(1).click();
  await expect(page.locator('.linklink-summary')).toBeVisible();
  await expect(page.locator('.linklink-summary')).toContainText('Board completed');
  const linkAfterWin = await readAudioContexts();
  expect(voiceCount(linkAfterWin[1].voices, [523.25, 659.25, 783.99])).toBe(3);
  assertEnvelopedCue(linkAfterWin[1].voices, [523.25, 659.25, 783.99]);
  const winCueCount = voiceCount(linkAfterWin[1].voices, [523.25, 659.25, 783.99]);
  const summaryGetsBeforeRecovery = routes.getLinkGetCount();
  await page.getByRole('link', { name: 'Back to game center' }).click();
  await expect(page).toHaveURL(/\/games$/);
  await page.locator('a[href="/games/linklink"]').click();
  await expect(page.locator('.linklink-summary')).toBeVisible();
  await expect.poll(() => routes.getLinkGetCount()).toBeGreaterThan(summaryGetsBeforeRecovery);
  const linkAfterSummaryRecovery = await readAudioContexts();
  expect(linkAfterSummaryRecovery).toHaveLength(2);
  expect(linkAfterSummaryRecovery[1].closeCalls).toBe(1);
  expect(voiceCount(linkAfterSummaryRecovery[1].voices, [523.25, 659.25, 783.99])).toBe(
    winCueCount,
  );
  await page.getByRole('link', { name: 'Back to game center' }).click();
  await expect(page).toHaveURL(/\/games$/);
  await page.locator('a[href="/games/fishing"]').click();
  await expect(page.getByRole('button', { name: 'Sound off' })).toBeVisible();
  await page.getByRole('button', { name: 'Sound off' }).click();
  await expect
    .poll(() =>
      page.evaluate(
        () =>
          (window as Window & { __audioProbe?: { contexts: unknown[] } }).__audioProbe?.contexts
            .length,
      ),
    )
    .toBe(4);
  await expect(page.locator('.fishing-start')).toBeEnabled();
  const fishingContextIndex = (await readAudioContexts()).length - 1;
  const fishingStartsBeforeCast = (await readAudioContexts())[fishingContextIndex].oscillatorStarts;
  const fishingGetsBeforeStart = routes.getFishingGetCount();
  await page.locator('.fishing-start').click();
  await expect
    .poll(() =>
      readAudioContexts().then((contexts) => contexts[fishingContextIndex].oscillatorStarts),
    )
    .toBeGreaterThan(fishingStartsBeforeCast);
  const fishingAfterCast = await readAudioContexts();
  const castVoices = fishingAfterCast[fishingContextIndex].voices.slice(
    fishingAfterCast[fishingContextIndex].voices.length -
      (fishingAfterCast[fishingContextIndex].oscillatorStarts - fishingStartsBeforeCast),
  );
  assertEnvelopedCue(castVoices, [440]);
  await expect(page.locator('[data-ordinal="0"]')).toBeVisible({ timeout: 5_000 });
  const fishingRare = [392, 587.33, 783.99] as const;
  await expect
    .poll(async () =>
      voiceCount((await readAudioContexts())[fishingContextIndex].voices, fishingRare),
    )
    .toBe(3);
  const fishingAfterResult = await readAudioContexts();
  assertEnvelopedCue(fishingAfterResult[fishingContextIndex].voices, fishingRare);
  const fishingRareCueCount = voiceCount(
    fishingAfterResult[fishingContextIndex].voices,
    fishingRare,
  );
  await expect(page.getByRole('button', { name: 'Retry marking as viewed' })).toBeVisible({
    timeout: 5_000,
  });
  expect(routes.getFishingGetCount()).toBeGreaterThan(fishingGetsBeforeStart);
  const fishingAfterRecovery = await readAudioContexts();
  expect(voiceCount(fishingAfterRecovery[fishingContextIndex].voices, fishingRare)).toBe(
    fishingRareCueCount,
  );
  const fishingGetsBeforeACK = routes.getFishingGetCount();
  await page.getByRole('button', { name: 'Retry marking as viewed' }).click();
  await expect(page.getByRole('button', { name: 'Retry marking as viewed' })).toHaveCount(0);
  await expect.poll(() => routes.getFishingGetCount()).toBeGreaterThan(fishingGetsBeforeACK);
  const fishingAfterACKRecovery = await readAudioContexts();
  expect(voiceCount(fishingAfterACKRecovery[fishingContextIndex].voices, fishingRare)).toBe(
    fishingRareCueCount,
  );
  const preferences = await page.evaluate(() => ({
    rps: localStorage.getItem('nonbiri.games.sound.v1.rps'),
    linklink: localStorage.getItem('nonbiri.games.sound.v1.linklink'),
    fishing: localStorage.getItem('nonbiri.games.sound.v1.fishing'),
  }));
  expect(preferences).toEqual({ rps: 'true', linklink: 'true', fishing: 'true' });
  await page.getByRole('link', { name: 'Back to game center' }).click();
  await expect(page).toHaveURL(/\/games$/);
  await expect
    .poll(() => readAudioContexts().then((contexts) => contexts[fishingContextIndex].closeCalls))
    .toBe(1);
  await page.locator('a[href="/games/rps"]').click();
  await expect(page.getByRole('button', { name: 'Sound on' })).toBeVisible();
  await saveJsonArtifact('game-audio-cue-metrics.json', await readAudioContexts());
  expect([...consoleErrors].sort()).toEqual(
    [
      'Failed to load resource: the server responded with a status of 409 (Conflict)',
      'Failed to load resource: the server responded with a status of 500 (Internal Server Error)',
    ].sort(),
  );
});

test('a real AudioContext resume refusal leaves the authoritative game action usable', async ({
  page,
}) => {
  const errors = collectConsoleViolations(page);
  await signedIn(page);
  await page.addInitScript(() => {
    const OriginalAudioContext = window.AudioContext;
    const probe = { resumeAttempts: 0 };
    if (typeof OriginalAudioContext === 'function') {
      function RefusingAudioContext(
        this: unknown,
        ...args: ConstructorParameters<typeof OriginalAudioContext>
      ) {
        const context = new OriginalAudioContext(...args);
        context.resume = (() => {
          probe.resumeAttempts += 1;
          return Promise.reject(new Error('audio device refused access'));
        }) as typeof context.resume;
        return context;
      }
      Object.setPrototypeOf(RefusingAudioContext, OriginalAudioContext);
      RefusingAudioContext.prototype = OriginalAudioContext.prototype;
      Object.defineProperty(window, 'AudioContext', {
        configurable: true,
        writable: true,
        value: RefusingAudioContext,
      });
    }
    Object.defineProperty(window, '__audioRefusalProbe', { configurable: false, value: probe });
  });
  const actions: unknown[] = [];
  await installGameRoutes(page, {
    rpsHome: { kind: 'session', session: rpsState({ mode: 'quick', phase: 'gesture' }) },
    onRPSAction: (body) => {
      actions.push(body);
      return pendingResult(true);
    },
  });
  await page.goto(`${USER_ORIGIN}/games/rps`);
  await page.getByRole('button', { name: 'Sound off' }).click();
  await expect
    .poll(() =>
      page.evaluate(
        () =>
          (window as Window & { __audioRefusalProbe?: { resumeAttempts: number } })
            .__audioRefusalProbe?.resumeAttempts ?? 0,
      ),
    )
    .toBe(1);
  await page.getByRole('button', { name: 'Paper' }).click();
  await expect(page.locator('.rps-result')).toBeVisible();
  expect(actions).toHaveLength(1);
  errors.assertNone();
});
