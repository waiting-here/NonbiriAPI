import { readFileSync, mkdirSync } from 'node:fs';
import { expect, test } from './test';
import { collectConsoleViolations, mockPublicConfig, mockRoleSession } from './support';
import { USER_ORIGIN } from './ports';
import { gamesSnapshotWire } from '../../src/user/games/common/testFixtures';
import { advance, newGame, type Phrase } from '../../src/user/games/steady-catch/engine';
import type { Session, Controls } from '../../src/user/games/steady-catch/session';
const phrases = JSON.parse(
  readFileSync('../internal/game/steadycatch/engine/phrases.json', 'utf8'),
) as Phrase[];
for (const theme of ['light', 'dark']) {
  test('catch layout, keyboard and pause in ' + theme, async ({ page }) => {
    const errors = collectConsoleViolations(page);
    await page.addInitScript((theme) => {
      localStorage.setItem('nb.lang', 'en');
      localStorage.setItem('nb.theme', theme);
    }, theme);
    await mockRoleSession(page, 'user', 'user');
    await mockPublicConfig(page, 'user');
    const snapshot = gamesSnapshotWire();
    snapshot.steadycatch.enabled = true;
    let state: Session = {
      id: 'sc_' + 'A'.repeat(22),
      status: 'paused',
      revision: 1,
      state: newGame(123),
      payment: { general: '0', game: '0' },
      first_clear_reward: '0',
      first_clear: false,
      reward: '0',
      created_at: 1800000000,
      expires_at: 1800001800,
      terminal_at: null,
      server_ms: 1800000000000,
    };
    const writes: Controls[] = [];
    await page.route('**/api/games**', async (route) => {
      const path = new URL(route.request().url()).pathname;
      if (path === '/api/games') return route.fulfill({ json: snapshot });
      if (path.endsWith('/catalog')) return route.fulfill({ json: phrases });
      if (path.endsWith('/leaderboard'))
        return route.fulfill({ json: { rows: [], me: null, window: '7d', as_of: 1800000000 } });
      if (path.endsWith('/session')) return route.fulfill({ json: state });
      if (path.endsWith('/controls')) {
        const controls = route.request().postDataJSON() as Controls;
        writes.push(controls);
        expect(controls.revision).toBe(state.revision);
        state = {
          ...state,
          revision: state.revision + 1,
          state: advance(state.state, controls.inputs, controls.until_tick, phrases),
          status:
            controls.action === 'pause'
              ? 'paused'
              : controls.action === 'abandon'
                ? 'abandoned'
                : 'playing',
        };
        if (controls.action === 'abandon') state.terminal_at = 1800000050;
        return route.fulfill({ json: state });
      }
      return route.fulfill({ json: { game_profile_public: false } });
    });
    await page.goto(USER_ORIGIN + '/games/steady-catch');
    await expect(page.getByRole('button', { name: 'Keep catching', exact: true })).toBeVisible();
    for (const [width, height] of [
      [1440, 900],
      [1280, 720],
      [720, 420],
      [390, 844],
      [320, 740],
    ]) {
      await page.setViewportSize({ width, height });
      await page
        .locator('.game-column')
        .evaluate((node) => node.scrollIntoView({ block: 'start' }));
      const bounds = await page.locator('.stage canvas').boundingBox();
      expect(bounds!.width).toBeGreaterThan(0);
      expect(bounds!.height).toBeGreaterThan(0);
      await expect(page.getByRole('button', { name: 'Resume game', exact: true })).toBeVisible();
      const horizontal = await page.evaluate(
        () => document.documentElement.scrollWidth > innerWidth,
      );
      const overflow = await page.locator('.catch-game *').evaluateAll((nodes) =>
        nodes
          .map((node) => ({
            tag: node.tagName,
            cls: node.className,
            right: node.getBoundingClientRect().right,
            width: node.getBoundingClientRect().width,
          }))
          .filter((node) => node.right > innerWidth + 1),
      );
      expect(horizontal, JSON.stringify({ width, overflow })).toBe(false);
      if (process.env.NONBIRI_SCREENSHOT_DIR && [1280, 390].includes(width)) {
        mkdirSync(process.env.NONBIRI_SCREENSHOT_DIR, { recursive: true });
        await page.screenshot({
          path: process.env.NONBIRI_SCREENSHOT_DIR + '/catch-' + theme + '-' + width + '.png',
        });
      }
    }
    await page.getByRole('button', { name: 'Keep catching', exact: true }).click();
    await expect(page.locator('.stage canvas')).toBeFocused();
    await page.keyboard.down('ArrowRight');
    await expect.poll(() => writes.some((w) => w.action === 'advance')).toBe(true);
    await page.keyboard.up('ArrowRight');
    await page.getByRole('button', { name: 'Pause game', exact: true }).click();
    await expect(page.getByRole('button', { name: 'Keep catching', exact: true })).toBeVisible();
    await expect.poll(() => state.status).toBe('paused');
    expect(state.state.x).toBeGreaterThan(300000);
    expect(writes.every((w) => !('score' in w))).toBe(true);
    expect(writes.some((w) => w.inputs.some((input) => input.direction === 1))).toBe(true);
    errors.assertNone();
  });
}
