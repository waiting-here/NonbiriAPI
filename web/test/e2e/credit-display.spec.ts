import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { expect, test, type Page } from './test';
import { USER_ORIGIN } from './ports';
import { creditDisplayFixtures } from './credit-display-fixtures';
import { pixelContrastSamples } from './appearance-contrast';
import { gamesSnapshotWire } from '../../src/user/games/common/testFixtures';
import { blackjackWire } from '../../src/user/games/blackjack/testFixtures';
import { LAST_TICK, newGame, type Phrase } from '../../src/user/games/steady-catch/engine';
import type { Session } from '../../src/user/games/steady-catch/session';
import { gachaSnapshot } from '../fixtures/gacha';

const phrases = JSON.parse(
  readFileSync('../internal/game/steadycatch/engine/phrases.json', 'utf8'),
) as Phrase[];

async function recordFractions(page: Page, label: string) {
  await page.evaluate(() => document.fonts.ready);
  const samples = (await pixelContrastSamples(page)).filter((sample) =>
    sample.selector.includes('.credit-number__fraction'),
  );
  expect(samples.length, label + ' actual fraction samples').toBeGreaterThan(0);
  expect(
    samples.filter((sample) => sample.ratio < 4.5),
    label,
  ).toEqual([]);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  const directory = process.env.NONBIRI_CREDIT_VISUAL_DIR;
  if (directory) {
    mkdirSync(directory, { recursive: true });
    await page.screenshot({ path: resolve(directory, label + '.png') });
    writeFileSync(resolve(directory, label + '.json'), JSON.stringify(samples, null, 2));
  }
}

for (const theme of ['light', 'dark']) {
  for (const width of [1440, 390]) {
    test(`credit fixed game surfaces ${theme} ${width}`, async ({ page }) => {
      await page.setViewportSize({ width, height: 1000 });
      await page.emulateMedia({ reducedMotion: 'reduce' });
      await page.addInitScript((mode) => {
        localStorage.setItem('nb.lang', 'en');
        localStorage.setItem('nb.theme', mode);
        localStorage.setItem(
          'nb.gacha.preferences',
          JSON.stringify({
            music: false,
            sound: false,
            reduced: true,
          }),
        );
      }, theme);
      await creditDisplayFixtures(page, 'default');
      const snapshot = gamesSnapshotWire();
      snapshot.balance = '0.001';
      snapshot.game_balance = '0.001';
      snapshot.steadycatch.enabled = true;
      snapshot.steadycatch.price = '0.001';
      snapshot.steadycatch.first_clear_reward = '0.001';
      for (const mode of Object.values(snapshot.bidding.modes)) mode.ticket = '0.001';
      const blackjack = blackjackWire();
      if (blackjack.you) {
        blackjack.you.stake = '0.001';
        blackjack.you.payment = { general: '0.001', game: '0' };
      }
      for (const seat of blackjack.table.fact.seats) seat.stake = '0.001';
      const result: Session = {
        id: 'sc_' + 'A'.repeat(22),
        status: 'completed',
        revision: 1,
        state: { ...newGame(123), tick: LAST_TICK, cause: 'time', score: 700 },
        payment: { general: '0.001', game: '0' },
        first_clear_reward: '0.001',
        first_clear: true,
        reward: '0.001',
        created_at: 1800000000,
        expires_at: 1800001800,
        terminal_at: 1800000090,
        server_ms: 1800000090000,
      };
      await page.route('**/api/games**', (route) => {
        const path = new URL(route.request().url()).pathname;
        if (path === '/api/games') return route.fulfill({ json: snapshot });
        if (path.endsWith('/blackjack/state')) return route.fulfill({ json: blackjack });
        if (path.endsWith('/bidding/state'))
          return route.fulfill({
            json: { server_now: 1800000000, queue: null, current: null, latest_result: null },
          });
        if (path.endsWith('/steady-catch/catalog')) return route.fulfill({ json: phrases });
        if (path.endsWith('/steady-catch/session')) return route.fulfill({ json: result });
        if (path.endsWith('/steady-catch/leaderboard'))
          return route.fulfill({ json: { rows: [], me: null, window: '7d', as_of: 1800000000 } });
        return route.fallback();
      });
      const gacha = gachaSnapshot();
      gacha.credits = { general: '0.001', game: '0.001' };
      gacha.shop.permanent_price = '0.001';
      await page.route('**/api/gacha/state', (route) => route.fulfill({ json: gacha }));
      await page.route('**/api/gacha/entry', (route) =>
        route.fulfill({ json: { enabled: true, entered: true, pools: [] } }),
      );
      for (const [path, selector] of [
        ['/games/blackjack', '.bj-board'],
        ['/games/bidding', '.bidding-game .bid-modes'],
        ['/games/steady-catch', '.catch-result .first-clear-badge'],
        ['/activities/gacha', '.gacha-app'],
      ]) {
        await page.goto(USER_ORIGIN + path);
        await expect(page.locator(selector).first()).toBeVisible();
        if (path === '/activities/gacha') {
          await page.getByTitle('Buy standard tickets', { exact: true }).click();
          await expect(page.getByRole('dialog')).toBeVisible();
        } else {
          await page.locator(selector).first().scrollIntoViewIfNeeded();
        }
        await expect(page.locator(selector).first()).toContainText('0.001');
        await recordFractions(page, `credit-${path.split('/').pop()}-${theme}-${width}`);
      }
    });
  }

  test(`credit extreme exact balance wraps at 390 in ${theme}`, async ({ page }) => {
    const balance = '170141183460469231731687303715884105727.001';
    await page.setViewportSize({ width: 390, height: 1000 });
    await page.addInitScript((mode) => {
      localStorage.setItem('nb.lang', 'en');
      localStorage.setItem('nb.theme', mode);
    }, theme);
    await creditDisplayFixtures(page, 'default', balance);
    for (const [path, selector] of [
      ['/', '.home-wallet'],
      ['/games/fishing', '.game-wallets'],
    ]) {
      await page.goto(USER_ORIGIN + path);
      const region = page.locator(selector);
      await expect(region).toBeVisible();
      const amount = region.locator('.credit-number').filter({ hasText: '170,141' }).first();
      await expect(amount).toHaveText('170,141,183,460,469,231,731,687,303,715,884,105,727.001');
      await amount.scrollIntoViewIfNeeded();
      const bounds = await amount.evaluate((element) => {
        const range = document.createRange();
        range.selectNodeContents(element);
        return [...range.getClientRects()].map((rect) => ({ left: rect.left, right: rect.right }));
      });
      expect(bounds.every((rect) => rect.left >= 0 && rect.right <= 391)).toBe(true);
      await recordFractions(page, `credit-extreme-${path === '/' ? 'home' : 'game'}-${theme}-390`);
    }
  });
}
