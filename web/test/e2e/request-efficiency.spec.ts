import { mkdir, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { gamesSnapshotWire } from '../../src/user/games/common/testFixtures';
import { blackjackWire } from '../../src/user/games/blackjack/testFixtures';
import { biddingHomeWire } from '../../src/user/games/bidding/testFixtures';
import { USER_ORIGIN } from './ports';
import {
  collectConsoleViolations,
  mockPublicConfig,
  mockRoleSession,
  userSession,
} from './support';
import { expect, test, type Page } from './test';

const evidenceDirectory = process.env.NONBIRI_REQUEST_EVIDENCE;
async function saveRequests(name: string, counts: Record<string, number>) {
  if (!evidenceDirectory) return;
  await mkdir(evidenceDirectory, { recursive: true });
  await writeFile(
    resolve(evidenceDirectory, `requests-${name}.json`),
    JSON.stringify(counts, null, 2) + '\n',
  );
}

async function screenshots(page: Page, name: string) {
  if (!evidenceDirectory) return;
  for (const theme of ['light', 'dark'] as const) {
    await page.emulateMedia({ colorScheme: theme, reducedMotion: 'reduce' });
    await expect(page.locator('html')).toHaveAttribute('data-theme', theme);
    for (const width of [1440, 390]) {
      await page.setViewportSize({ width, height: width === 390 ? 844 : 1000 });
      await page.evaluate(() => scrollTo(0, 0));
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      );
      await page.screenshot({ path: resolve(evidenceDirectory, `${name}-${theme}-${width}.png`) });
      if (width === 390 && name.startsWith('home-')) {
        await page.locator('.home-wallet').scrollIntoViewIfNeeded();
        await page.screenshot({
          path: resolve(evidenceDirectory, `${name}-wallet-${theme}-${width}.png`),
        });
      }
      if (width === 390 && name === 'blackjack-idle') {
        await page.locator('.bj-queue input').scrollIntoViewIfNeeded();
        await page.locator('.bj-queue input').focus();
        await page.screenshot({
          path: resolve(evidenceDirectory, `${name}-stake-${theme}-${width}.png`),
        });
      }
      if (width === 390 && name === 'bidding-active') {
        await page.locator('.bid-table').scrollIntoViewIfNeeded();
        await page.screenshot({
          path: resolve(evidenceDirectory, `${name}-table-${theme}-${width}.png`),
        });
      }
    }
  }
}

async function common(page: Page) {
  const counts: Record<string, number> = {};
  page.on('request', (request) => {
    const path = new URL(request.url()).pathname;
    if (path.startsWith('/api/')) counts[path] = (counts[path] ?? 0) + 1;
  });
  await mockRoleSession(page, 'user', 'user');
  await mockPublicConfig(page, 'user');
  await page.route('**/api/**', async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (/\/randomness\//.test(path)) return route.fulfill({ json: { proof: null } });
    if (path.endsWith('/net-profit'))
      return route.fulfill({
        json: {
          as_of: 1_800_000_000,
          statistics_start: 1_800_000_000,
          window: '7d',
          rows: [],
          me: null,
          ...(path.includes('/bidding/')
            ? {
                rebuild_status: 'completed',
                missing_events: '0',
                history_coverage_start: 1_800_000_000,
              }
            : {}),
        },
      });
    return route.fallback();
  });
  return counts;
}

for (const existingModels of [false, true]) {
  test(`home ${existingModels ? 'existing models' : 'new account'} avoids redundant reads`, async ({
    page,
  }) => {
    const errors = collectConsoleViolations(page);
    const counts = await common(page);
    const session = userSession('user');
    session.user.balance = '-0.001';
    await page.route('**/api/{session,me}', (route) => route.fulfill({ json: session }));
    const model = {
      id: '31',
      provider: 'fixture',
      model: 'fixture-model',
      full_name: 'fixture/fixture-model',
      model_types: ['chat_completions'],
      route_strategy: 'ordered',
      silent_retry: false,
      transport_rule: 'passthrough',
      flatten_tool_calls: false,
      revision: '1',
      binding_revision: '1',
      binding_count: '1',
      created_at: 1_700_000_000,
      updated_at: 1_700_000_000,
    };
    await page.route('**/api/models?*', (route) =>
      route.fulfill({
        json: {
          data: existingModels ? [model] : [],
          next_cursor: null,
          pagination: {
            page: '1',
            page_size: 10,
            total_items: existingModels ? '1' : '0',
            total_pages: '1',
          },
        },
      }),
    );
    await page.route('**/api/charity/models?*', (route) =>
      route.fulfill({
        json: {
          models: [],
          pagination: { page: '1', page_size: 10, total_items: '0', total_pages: '1' },
          donation_intake: 'closed',
          server_now: 1_800_000_000,
        },
      }),
    );
    await page.route('**/api/caller-key', (route) =>
      route.fulfill({ json: null, headers: { 'X-Caller-Key-Generation': '0' } }),
    );
    await page.route('**/api/checkin', (route) =>
      route.fulfill({
        json: {
          enabled: false,
          mutually_exclusive: false,
          blocked_by_other_checkin: false,
        },
      }),
    );
    await page.route('**/api/checkin/game', (route) =>
      route.fulfill({
        json: {
          enabled: false,
          mutually_exclusive: false,
          blocked_by_other_checkin: false,
        },
      }),
    );
    await page.route('**/api/home/game-summary', (route) =>
      route.fulfill({ json: { continue: [], pending_results: [] } }),
    );
    await page.route('**/api/announcements?*', (route) =>
      route.fulfill({ json: { data: [], next_cursor: null } }),
    );
    await page.goto(USER_ORIGIN);
    await expect(page.locator('.home-wallet .nb-stat__value').first()).toContainText('-0.001');
    await expect.poll(() => counts['/api/models'] ?? 0).toBeGreaterThan(0);
    if (!existingModels)
      await expect.poll(() => counts['/api/charity/models'] ?? 0).toBeGreaterThan(0);
    await page.waitForTimeout(100);
    await saveRequests(`home-${existingModels ? 'existing' : 'new'}`, counts);
    expect(counts['/api/me'] ?? 0).toBe(0);
    expect(counts['/api/charity/models'] ?? 0).toBe(existingModels ? 0 : 1);
    await screenshots(page, `home-${existingModels ? 'existing' : 'new'}`);
    errors.assertNone();
  });
}

for (const game of ['blackjack', 'bidding'] as const) {
  for (const active of [false, true]) {
    test(`${game} ${active ? 'active' : 'idle'} polling respects visibility`, async ({ page }) => {
      const errors = collectConsoleViolations(page);
      const counts = await common(page);
      await page.clock.install();
      const idleBlackjack = blackjackWire();
      const wire =
        game === 'blackjack'
          ? active
            ? blackjackWire('decision', null)
            : {
                ...idleBlackjack,
                phase: 'seating',
                // Keep deadline refreshes outside this polling-only observation.
                deadline: idleBlackjack.server_now + 120,
                next_round_at: idleBlackjack.server_now + 120,
                your_seat: null,
                you: null,
                table: null,
                queue_count: '0',
              }
          : active
            ? biddingHomeWire()
            : { server_now: 1_800_000_000, queue: null, current: null, latest_result: null };
      await page.route('**/api/games**', (route) => {
        const path = new URL(route.request().url()).pathname;
        if (path === '/api/games') return route.fulfill({ json: gamesSnapshotWire() });
        if (path === `/api/games/${game}/state`) return route.fulfill({ json: wire });
        return route.fallback();
      });
      await page.goto(`${USER_ORIGIN}/games/${game}`);
      const statePath = `/api/games/${game}/state`;
      await expect.poll(() => counts[statePath] ?? 0).toBeGreaterThan(0);
      await expect(
        page.locator(game === 'blackjack' ? '.blackjack-game' : '.bidding-game'),
      ).toBeVisible();
      await screenshots(page, `${game}-${active ? 'active' : 'idle'}`);
      // Keep visual checks outside the interval being counted.
      await page.clock.pauseAt(await page.evaluate(() => Date.now() + 100));
      await page.waitForTimeout(50);
      const initial = counts[statePath];
      for (let step = 0; step < 300; step++) {
        await page.clock.runFor(100);
        await page.waitForTimeout(10);
      }
      const visibleReads = counts[statePath] - initial;
      await page.evaluate(() => {
        Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' });
        document.dispatchEvent(new Event('visibilitychange', { bubbles: true }));
      });
      await page.waitForTimeout(20);
      const beforeHidden = counts[statePath];
      for (let step = 0; step < 10; step++) {
        await page.clock.runFor(1_000);
        await page.waitForTimeout(10);
      }
      const hiddenReads = counts[statePath] - beforeHidden;
      await page.evaluate(() => {
        Object.defineProperty(document, 'visibilityState', {
          configurable: true,
          value: 'visible',
        });
        document.dispatchEvent(new Event('visibilitychange', { bubbles: true }));
      });
      await expect.poll(() => counts[statePath]).toBeGreaterThan(beforeHidden + hiddenReads);
      await saveRequests(`${game}-${active ? 'active' : 'idle'}`, {
        visibleSeconds: 30,
        visibleReads,
        hiddenSeconds: 10,
        hiddenReads,
        initialReads: initial,
      });
      expect(hiddenReads).toBe(0);
      if (active) expect(visibleReads).toBeGreaterThan(game === 'blackjack' ? 35 : 25);
      else expect(visibleReads).toBeLessThanOrEqual(6);
      errors.assertNone();
    });
  }

  test(`${game} refreshes a fresh wallet after a hidden settlement`, async ({ page }) => {
    const errors = collectConsoleViolations(page);
    const counts = await common(page);
    await page.clock.install();
    const snapshot = gamesSnapshotWire();
    snapshot.balance = '50';
    snapshot.game_balance = '10';
    await page.route('**/api/games**', (route) => {
      const path = new URL(route.request().url()).pathname;
      if (path === '/api/games') return route.fulfill({ json: snapshot });
      if (path === `/api/games/${game}/state`)
        return route.fulfill({
          json:
            game === 'blackjack'
              ? blackjackWire('result')
              : { server_now: 1_800_000_000, queue: null, current: null, latest_result: null },
        });
      return route.fallback();
    });
    await page.goto(`${USER_ORIGIN}/games/${game}`);
    await expect(page.locator('.game-wallets dd').first()).toContainText('50');
    await page.evaluate(() => {
      Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' });
      document.dispatchEvent(new Event('visibilitychange', { bubbles: true }));
    });
    snapshot.balance = '42';
    snapshot.game_balance = '14';
    await page.clock.runFor(500);
    const beforeResume = counts['/api/games'];
    await page.evaluate(() => {
      Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' });
      document.dispatchEvent(new Event('visibilitychange', { bubbles: true }));
    });
    await expect(page.locator('.game-wallets dd').first()).toContainText('42');
    await expect(page.locator('.game-wallets dd').last()).toContainText('14');
    expect(counts['/api/games']).toBe(beforeResume + 1);
    errors.assertNone();
  });
}
