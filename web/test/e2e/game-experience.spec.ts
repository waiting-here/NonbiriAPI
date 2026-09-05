import { expect, test, type Page } from './test';
import { collectConsoleViolations, mockPublicConfig, mockRoleSession } from './support';
import { USER_ORIGIN } from './ports';
import { gamesSnapshotWire } from '../../src/user/games/common/testFixtures';
import { rpsStateWire, rpsTestSessionID } from '../../src/user/games/rps/testFixtures';

async function signedIn(page: Page) {
  await mockRoleSession(page, 'user', 'user');
  await mockPublicConfig(page, 'user');
}

for (const spec of ['6x8', '8x8', '10x10'] as const) {
  test(`LinkLink ${spec} uses distinct pictures and draws the confirmed path without moving the board`, async ({
    page,
  }) => {
    const errors = collectConsoleViolations(page);
    await signedIn(page);
    await page.setViewportSize({ width: 1440, height: 1000 });
    const [rows, cols] = spec.split('x').map(Number);
    const count = rows * cols;
    const state = {
      session_id: `ll_${'A'.repeat(22)}`,
      spec,
      state: 'active',
      price: '3',
      revision: '1',
      board: {
        rows,
        cols,
        tiles: Array.from({ length: count }, (_, index) => ({
          row: Math.floor(index / cols),
          col: index % cols,
          tile_key: `tile_${String(Math.floor(index / 4) + 1).padStart(2, '0')}`,
          removed: false,
        })),
      },
      pairs_removed: 0,
      total_pairs: count / 2,
      started_at: 1_800_000_000,
      deadline: 1_800_000_000 + ({ '6x8': 150, '8x8': 180, '10x10': 240 } as const)[spec],
      server_now: 1_800_000_010,
    };
    const matches: unknown[] = [];
    await page.route('**/api/games**', async (route) => {
      const path = new URL(route.request().url()).pathname;
      if (path === '/api/games') {
        await route.fulfill({ json: gamesSnapshotWire() });
        return;
      }
      if (path.endsWith('/lease')) {
        await route.fulfill({ json: { expires_at: 1_800_000_035 } });
        return;
      }
      if (path.endsWith('/matches')) {
        const input = route.request().postDataJSON();
        matches.push(input);
        state.revision = '2';
        state.pairs_removed = 1;
        state.board.tiles[0].removed = true;
        state.board.tiles[3].removed = true;
        await route.fulfill({
          json: {
            ...state,
            match_path: [
              { row: 0, col: 0 },
              { row: -1, col: 0 },
              { row: -1, col: 3 },
              { row: 0, col: 3 },
            ],
          },
        });
        return;
      }
      if (path === '/api/games/linklink/session') {
        await route.fulfill({ json: state });
        return;
      }
      await route.fallback();
    });
    await page.goto(`${USER_ORIGIN}/games/linklink`);
    const tiles = page.locator('.linklink-tile');
    await expect(tiles).toHaveCount(count);
    expect(await tiles.allTextContents()).toEqual(Array(count).fill(''));
    const pictures = await tiles
      .locator('svg')
      .evaluateAll((elements) => elements.map((svg) => svg.getAttribute('aria-label')));
    expect(new Set(pictures).size).toBe(count / 4);
    await expect(tiles.first()).toBeEnabled();
    const before = await page.locator('.linklink-board').boundingBox();
    await tiles.first().click();
    const selected = await page.locator('.linklink-board').boundingBox();
    expect(selected?.y).toBe(before?.y);
    await tiles.nth(3).click();
    await expect(page.locator('.linklink-match-beam')).toHaveAttribute('points', /\S+/);
    expect(matches).toHaveLength(1);
    expect(matches[0]).toMatchObject({
      include_path: true,
      first: { row: 0, col: 0 },
      second: { row: 0, col: 3 },
    });
    const geometry = await page.locator('.linklink-match-effect').evaluate((svg) => {
      const node = svg as SVGSVGElement;
      const points = node
        .querySelector('polyline')!
        .getAttribute('points')!
        .split(' ')
        .map((point) => point.split(',').map(Number));
      return { points, width: node.viewBox.baseVal.width, height: node.viewBox.baseVal.height };
    });
    expect(geometry.points).toHaveLength(4);
    expect(geometry.points[1][1]).toBeLessThan(geometry.points[0][1]);
    expect(
      geometry.points.every(
        ([x, y]) => x >= 0 && y >= 0 && x <= geometry.width && y <= geometry.height,
      ),
    ).toBe(true);
    await page.screenshot({ path: `../tmp/linklink-${spec}-effect.png`, fullPage: true });
    await expect(tiles.first()).toHaveClass(/is-removed/);
    await expect(page.locator('.linklink-match-effect')).toHaveCount(0);
    const after = await page.locator('.linklink-board').boundingBox();
    expect(after?.y).toBe(before?.y);
    await page.setViewportSize({ width: 390, height: 844 });
    await page.emulateMedia({ reducedMotion: 'reduce' });
    await expect
      .poll(() => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth))
      .toBe(true);
    if (spec !== '10x10')
      await expect
        .poll(() =>
          page
            .locator('.linklink-board-scroll')
            .evaluate((board) => board.scrollWidth <= board.clientWidth + 1),
        )
        .toBe(true);
    await page.evaluate(() => window.scrollTo(0, 0));
    await page.screenshot({ path: `../tmp/linklink-${spec}-mobile.png`, fullPage: true });
    errors.assertNone();
  });
}

test('RPS separates past reveals from hidden choices and keeps its hidden ending visible after acknowledgement', async ({
  page,
}) => {
  const errors = collectConsoleViolations(page);
  await signedIn(page);
  await page.addInitScript(() => {
    Object.defineProperty(window, 'EventSource', {
      value: class extends EventTarget {
        onopen: (() => void) | null = null;
        closed = false;
        constructor() {
          super();
          queueMicrotask(() => {
            if (!this.closed) this.onopen?.();
          });
        }
        close() {
          this.closed = true;
        }
      },
    });
  });
  const session = rpsStateWire('free_pool_gesture');
  session.round_summary.free_pool_streak = '5';
  session.round_summary.free_tie_count = '5';
  session.round_summary.reminder_active = true;
  Object.assign(session, {
    first_available_seq: '1',
    recent_events: [
      {
        seq: '1',
        identity_epoch: '1',
        kind: 'reveal',
        phase_seq: '1',
        safe_payload: {
          gestures: [0, 1, 2].map((seat_no) => ({ seat_no, gesture: 'rock' })),
          result_code: 'three_equal',
        },
      },
    ],
  });
  let home: unknown = { kind: 'session', session };
  let acknowledgements = 0;
  await page.route('**/api/games**', async (route) => {
    const url = new URL(route.request().url());
    if (url.pathname === '/api/games') {
      await route.fulfill({ json: gamesSnapshotWire() });
      return;
    }
    if (url.pathname === '/api/games/rps/state') {
      await route.fulfill({ json: home });
      return;
    }
    if (url.pathname.endsWith('/lease')) {
      await route.fulfill({ json: { expires_at: 1_800_000_025 } });
      return;
    }
    if (url.pathname.endsWith('/leaderboard')) {
      await route.fulfill({
        json: {
          mode: url.searchParams.get('mode'),
          board: url.searchParams.get('board'),
          window_days: 30,
          window_start: 1_700_000_000,
          min_sessions: 10,
          rows: [],
          me: null,
        },
      });
      return;
    }
    if (url.pathname.endsWith('/actions')) {
      home = {
        kind: 'pending_result',
        result: {
          session_id: rpsTestSessionID,
          mode: 'standard',
          terminal_reason: 'free_tie_limit',
          own_seat_no: 0,
          own_input: '10',
          own_returned: '9',
          own_wallet_net: '-1',
          seats: [0, 1, 2].map((seat_no) => ({ seat_no, result: 'loss' })),
          created_at: 1_800_000_011,
        },
      };
      await route.fulfill({ json: home });
      return;
    }
    if (url.pathname.endsWith('/pending-result/ack')) {
      acknowledgements++;
      home = { kind: 'idle', tutorial_seen: true, modes: gamesSnapshotWire().rps.modes };
      await route.fulfill({ status: 204 });
      return;
    }
    await route.fallback();
  });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(`${USER_ORIGIN}/games/rps`);
  await page.getByRole('button', { name: 'Skip for now' }).click();
  await expect(page.locator('.rps-hidden-gesture')).toHaveCount(3);
  await expect(page.locator('.rps-tie-marks .is-lit')).toHaveCount(5);
  await expect(page.locator('.rps-round-reveal')).toBeVisible();
  await expect
    .poll(() => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth))
    .toBe(true);
  const actions = await page.locator('.rps-actions').boundingBox();
  const seats = await page.locator('.rps-seats').boundingBox();
  expect(actions!.y).toBeLessThan(seats!.y);
  expect(actions!.y + actions!.height).toBeLessThan(844);
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.screenshot({ path: '../tmp/rps-mobile-active.png', fullPage: true });
  await page.locator('.rps-actions button').first().click();
  const ending = page.locator('.rps-result--ascension');
  await expect(ending).toBeVisible();
  await ending.scrollIntoViewIfNeeded();
  await expect.poll(() => acknowledgements).toBe(1);
  await expect(ending).toBeVisible();
  await expect(ending.getByRole('heading', { name: 'A final bow to the heavens' })).toBeVisible();
  await expect(page.getByText('The service returned an invalid response.')).toHaveCount(0);
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.screenshot({ path: '../tmp/rps-mobile-ending.png', fullPage: true });
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await expect(page.locator('.rps-result-orbit')).toHaveCSS('animation-name', 'none');
  await page.setViewportSize({ width: 1600, height: 1000 });
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.screenshot({ path: '../tmp/rps-desktop-ending.png', fullPage: true });
  errors.assertNone();
});
