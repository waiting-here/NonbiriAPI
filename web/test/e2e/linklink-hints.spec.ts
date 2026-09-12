import { mkdirSync } from 'node:fs';
import { join } from 'node:path';
import { expect, test } from './test';
import { mockPublicConfig, mockRoleSession } from './support';
import { USER_ORIGIN } from './ports';
import { gamesSnapshotWire } from '../../src/user/games/common/testFixtures';

for (const fixture of [
  { spec: '6x8', width: 320, language: 'en', theme: 'light', motion: 'no-preference' },
  { spec: '8x8', width: 390, language: 'zh', theme: 'dark', motion: 'no-preference' },
  { spec: '10x10', width: 360, language: 'zh', theme: 'dark', motion: 'reduce' },
] as const) {
  test(`LinkLink ${fixture.spec} hints stay visible and responsive at ${fixture.width}px`, async ({
    page,
  }) => {
    await page.setViewportSize({ width: fixture.width, height: 800 });
    await page.emulateMedia({ reducedMotion: fixture.motion });
    await page.addInitScript(({ language, theme }) => {
      localStorage.setItem('nb.lang', language);
      localStorage.setItem('nb.theme', theme);
    }, fixture);
    await mockRoleSession(page, 'user', 'user');
    await mockPublicConfig(page, 'user');
    const [rows, cols] = fixture.spec.split('x').map(Number);
    const initial = fixture.spec === '6x8' ? 2 : fixture.spec === '8x8' ? 3 : 5;
    const state = {
      session_id: 'll_AAAAAAAAAAAAAAAAAAAAAA',
      spec: fixture.spec,
      state: 'active',
      price: '3',
      rules_version: 2,
      payment: { general: '0', game: '3' },
      opportunities_initial: initial,
      opportunities_remaining: initial,
      revision: '1',
      pairs_removed: 0,
      total_pairs: (rows * cols) / 2,
      started_at: 1_800_000_000,
      deadline: 1_800_000_000 + (fixture.spec === '6x8' ? 150 : fixture.spec === '8x8' ? 180 : 240),
      server_now: 1_800_000_010,
      board: {
        rows,
        cols,
        tiles: Array.from({ length: rows * cols }, (_, i) => ({
          row: Math.floor(i / cols),
          col: i % cols,
          tile_key: `tile_${String(Math.floor(i / 4) + 1).padStart(2, '0')}`,
          removed: false,
        })),
      },
    };
    [state.board.tiles[1].tile_key, state.board.tiles[4].tile_key] = [
      state.board.tiles[4].tile_key,
      state.board.tiles[1].tile_key,
    ];
    let snapshots = 0,
      matches = 0,
      hints = 0;
    let releaseHint: (() => void) | undefined;
    const errors: string[] = [];
    page.on('pageerror', (error) => errors.push(error.message));
    await page.route('**/api/games**', async (route) => {
      const request = route.request();
      const path = new URL(request.url()).pathname;
      if (path === '/api/games') {
        snapshots++;
        await route.fulfill({ json: gamesSnapshotWire() });
        return;
      }
      if (path === '/api/games/linklink/session') {
        await route.fulfill({ json: state });
        return;
      }
      if (path.endsWith('/lease')) {
        await route.fulfill({ json: { expires_at: 1_800_000_025 } });
        return;
      }
      if (path.endsWith('/hint')) {
        hints++;
        expect(request.postDataJSON()).toEqual({ expected_revision: state.revision });
        await new Promise<void>((resolve) => {
          releaseHint = resolve;
        });
        state.opportunities_remaining--;
        state.revision = String(Number(state.revision) + 1);
        await route.fulfill({
          json: {
            ...state,
            reshuffled: false,
            hint: {
              first: { row: 0, col: 0 },
              second: { row: 0, col: 2 },
              path: [
                { row: 0, col: 0 },
                { row: -1, col: 0 },
                { row: -1, col: 2 },
                { row: 0, col: 2 },
              ],
            },
          },
        });
        return;
      }
      if (path.endsWith('/matches')) {
        matches++;
        const body = request.postDataJSON();
        expect(body.expected_revision).toBe(state.revision);
        await new Promise((resolve) => setTimeout(resolve, 150));
        for (const point of [body.first, body.second])
          state.board.tiles[point.row * cols + point.col].removed = true;
        state.pairs_removed++;
        state.revision = String(Number(state.revision) + 1);
        await route.fulfill({
          json: {
            ...state,
            match_path: [
              body.first,
              { row: -1, col: body.first.col },
              { row: -1, col: body.second.col },
              body.second,
            ],
          },
        });
        return;
      }
      await route.fallback();
    });
    await page.goto(`${USER_ORIGIN}/games/linklink`);
    const button = page.locator('.linklink-hint');
    await expect(button).toBeEnabled();
    const initialSnapshotRequests = snapshots;
    await button.click();
    await expect(button).toBeDisabled();
    await expect(button).toContainText(String(initial));
    await expect(page.locator('.linklink-tile.is-hinted')).toHaveCount(0);
    await expect.poll(() => Boolean(releaseHint)).toBe(true);
    releaseHint!();
    await expect(button).toBeEnabled();
    await expect(button).toContainText(String(initial - 1));
    await expect(page.locator('.linklink-tile.is-hinted')).toHaveCount(2);
    const beam = page.locator('.linklink-match-effect.is-hint polyline');
    await expect(beam).toHaveAttribute('points', /,/);
    expect(await beam.evaluate((element) => getComputedStyle(element).strokeDasharray)).not.toBe(
      'none',
    );
    expect(await beam.evaluate((element) => getComputedStyle(element).animationName)).toBe('none');
    const geometry = await page.evaluate(() => {
      const board = document.querySelector('.linklink-board')!;
      const points = [
        ...document.querySelector<SVGPolylineElement>('.is-hint polyline')!.points,
      ].map((point) => ({ x: point.x, y: point.y }));
      return {
        width: innerWidth,
        pageWidth: document.documentElement.scrollWidth,
        boardWidth: board.clientWidth,
        boardHeight: board.clientHeight,
        points,
      };
    });
    expect(geometry.pageWidth).toBeLessThanOrEqual(fixture.width);
    for (const point of geometry.points) {
      expect(point.x).toBeGreaterThanOrEqual(0);
      expect(point.x).toBeLessThanOrEqual(geometry.boardWidth);
      expect(point.y).toBeGreaterThanOrEqual(0);
      expect(point.y).toBeLessThanOrEqual(geometry.boardHeight);
    }
    if (process.env.NONBIRI_VISUAL_DIR) {
      mkdirSync(process.env.NONBIRI_VISUAL_DIR, { recursive: true });
      await page.screenshot({
        path: join(process.env.NONBIRI_VISUAL_DIR, `linklink-hint-${fixture.spec}.png`),
        fullPage: true,
      });
    }
    const first = page.locator('[role=gridcell][aria-rowindex="1"][aria-colindex="1"]');
    const second = page.locator('[role=gridcell][aria-rowindex="1"][aria-colindex="3"]');
    await first.focus();
    await page.keyboard.press('Enter');
    await expect(first).toHaveAttribute('aria-selected', 'true');
    await expect(page.locator('.linklink-tile.is-hinted')).toHaveCount(2);
    const started = performance.now();
    await second.click();
    await expect(first).toHaveClass(/is-removed/);
    const responseElapsed = performance.now() - started;
    await expect(page.locator('.linklink-tile.is-hinted')).toHaveCount(0);
    const next = page.locator('[role=gridcell][aria-rowindex="1"][aria-colindex="4"]');
    await expect(next).toBeEnabled();
    await next.click();
    await expect(next).toHaveAttribute('aria-selected', 'true');
    expect(matches).toBe(1);
    expect(hints).toBe(1);
    expect(snapshots).toBe(initialSnapshotRequests);
    expect(errors).toEqual([]);
    console.log(
      JSON.stringify({
        spec: fixture.spec,
        width: fixture.width,
        injectedMatchLatencyMS: 150,
        clickToAuthoritativeRemovalMS: responseElapsed,
        snapshots,
        matches,
        hints,
      }),
    );
  });
}
