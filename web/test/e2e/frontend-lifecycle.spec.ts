import { appendFileSync, mkdirSync } from 'node:fs';
import { join } from 'node:path';
import { expect, test } from './test';
import { mockPublicConfig, mockRoleSession, userSession } from './support';
import { USER_ORIGIN } from './ports';
import { gamesSnapshotWire } from '../../src/user/games/common/testFixtures';
import { gachaSnapshot } from '../fixtures/gacha';
import { loanActivity } from '../fixtures/loans';

for (const theme of ['light', 'dark'])
  for (const width of [1440, 390]) {
    test(`LinkLink pending feedback with 100 tiles ${theme} ${width}`, async ({
      page,
      context,
    }) => {
      await page.setViewportSize({ width, height: 900 });
      await page.emulateMedia({ reducedMotion: 'reduce' });
      await page.addInitScript(
        ({ theme }) => {
          localStorage.setItem('nb.lang', 'en');
          localStorage.setItem('nb.theme', theme);
        },
        { theme },
      );
      await mockRoleSession(page, 'user', 'user');
      await mockPublicConfig(page, 'user');
      const state = {
        session_id: 'll_AAAAAAAAAAAAAAAAAAAAAA',
        spec: '10x10',
        state: 'active',
        price: '3',
        rules_version: 2,
        payment: { general: '0', game: '3' },
        opportunities_initial: 5,
        opportunities_remaining: 5,
        revision: '1',
        pairs_removed: 0,
        total_pairs: 50,
        started_at: 1_800_000_000,
        deadline: 1_800_000_240,
        server_now: 1_800_000_010,
        board: {
          rows: 10,
          cols: 10,
          tiles: Array.from({ length: 100 }, (_, index) => ({
            row: Math.floor(index / 10),
            col: index % 10,
            tile_key: `tile_${String(Math.floor(index / 4) + 1).padStart(2, '0')}`,
            removed: false,
          })),
        },
      };
      let matches = 0;
      await page.route('**/api/games**', async (route) => {
        const path = new URL(route.request().url()).pathname;
        if (path === '/api/games') return route.fulfill({ json: gamesSnapshotWire() });
        if (path === '/api/games/linklink/session') return route.fulfill({ json: state });
        if (path.endsWith('/lease')) return route.fulfill({ json: { expires_at: 1_800_000_025 } });
        if (path.endsWith('/matches')) {
          matches++;
          await new Promise((resolve) => setTimeout(resolve, 300));
          state.board.tiles[0].removed = true;
          state.board.tiles[1].removed = true;
          state.pairs_removed = 1;
          state.revision = '2';
          return route.fulfill({
            json: {
              ...state,
              match_path: [
                { row: 0, col: 0 },
                { row: 0, col: 1 },
              ],
            },
          });
        }
        await route.fallback();
      });
      await page.goto(USER_ORIGIN + '/games/linklink');
      await expect(page.locator('.linklink-board')).toBeVisible();
      await expect(page.getByText('Playing', { exact: true })).toBeVisible();
      await context.tracing.start({ screenshots: false, snapshots: true });
      const metrics = await page.evaluate(async () => {
        const tiles = document.querySelectorAll<HTMLButtonElement>('[role=gridcell]');
        const longTasks: number[] = [];
        const observer = new PerformanceObserver((list) =>
          longTasks.push(...list.getEntries().map((entry) => entry.duration)),
        );
        observer.observe({ type: 'longtask', buffered: false });
        const start = performance.now();
        tiles[0].click();
        await new Promise<void>((resolve) => requestAnimationFrame(() => resolve()));
        const selectedMS = performance.now() - start;
        tiles[1].click();
        await new Promise<void>((resolve) => requestAnimationFrame(() => resolve()));
        const pending = document.querySelectorAll('.linklink-tile.is-pending').length;
        const removed = document.querySelectorAll('.linklink-tile.is-removed').length;
        tiles[2].click();
        tiles[3].click();
        observer.disconnect();
        return { selectedMS, pending, removed, longTasks };
      });
      expect(metrics.selectedMS).toBeLessThan(100);
      expect(metrics.pending).toBe(2);
      expect(metrics.removed).toBe(0);
      await expect(page.locator('.linklink-tile.is-removed')).toHaveCount(2);
      expect(matches).toBe(1);
      await expect(page.locator('.linklink-tile.is-pending')).toHaveCount(0);
      expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(
        width,
      );
      const selected = page.locator('[role=gridcell]').nth(2);
      await selected.focus();
      await page.keyboard.press('Enter');
      await expect(selected).toHaveAttribute('aria-selected', 'true');
      if (process.env.NONBIRI_VISUAL_DIR) {
        mkdirSync(process.env.NONBIRI_VISUAL_DIR, { recursive: true });
        await page.screenshot({
          path: join(process.env.NONBIRI_VISUAL_DIR, `linklink-${theme}-${width}.png`),
          fullPage: true,
        });
        appendFileSync(
          join(process.env.NONBIRI_VISUAL_DIR, 'linklink-response.jsonl'),
          JSON.stringify({ theme, width, ...metrics }) + '\n',
        );
        await context.tracing.stop({
          path: join(process.env.NONBIRI_VISUAL_DIR, `linklink-${theme}-${width}.zip`),
        });
      } else await context.tracing.stop();
    });
  }

test('gacha retries an unknown entry with the same key', async ({ page }) => {
  await mockRoleSession(page, 'user', 'user');
  await mockPublicConfig(page, 'user');
  await page.addInitScript(() => localStorage.setItem('nb.lang', 'en'));
  const keys: string[] = [];
  await page.route('**/api/gacha/actions', (route) => {
    keys.push(route.request().headers()['idempotency-key']);
    return keys.length === 1
      ? route.abort('failed')
      : route.fulfill({ json: { snapshot: gachaSnapshot() } });
  });
  await page.goto(USER_ORIGIN + '/activities/gacha');
  await page.getByRole('button', { name: 'Retry', exact: true }).click();
  await expect(page.locator('.gacha-app')).toBeVisible();
  expect(keys).toHaveLength(2);
  expect(keys[0]).toBeTruthy();
  expect(keys[1]).toBe(keys[0]);
});

test('gacha entry survives a same-account session refresh', async ({ page }) => {
  await page.clock.install();
  await mockPublicConfig(page, 'user');
  await page.addInitScript(() => localStorage.setItem('nb.lang', 'en'));
  let sessions = 0;
  await page.route('**/api/session', (route) => {
    const value = userSession('user');
    sessions++;
    value.user.username = sessions === 1 ? 'before-refresh' : 'after-refresh';
    return route.fulfill({ json: value });
  });
  let entered = 0;
  let release!: () => void;
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route('**/api/gacha/actions', async (route) => {
    entered++;
    await held;
    await route.fulfill({ json: { snapshot: gachaSnapshot() } });
  });
  await page.goto(USER_ORIGIN + '/activities/gacha');
  await expect.poll(() => entered).toBe(1);
  await page.clock.runFor(16_000);
  await page.evaluate(() => window.dispatchEvent(new Event('offline')));
  await page.clock.runFor(10);
  await page.evaluate(() => window.dispatchEvent(new Event('online')));
  await page.clock.runFor(10);
  await expect.poll(() => sessions).toBe(2);
  release();
  await expect(page.getByRole('button', { name: /Check in.*standard/i })).toBeVisible();
  expect(entered).toBe(1);
  await expect(page.getByText('Could not open the activity. Please retry.')).toHaveCount(0);
});

test('a missing deployed page chunk has loading feedback and one explicit reload', async ({
  page,
}) => {
  await mockRoleSession(page, 'user', 'user');
  await mockPublicConfig(page, 'user');
  await page.addInitScript(() => localStorage.setItem('nb.lang', 'en'));
  await page.route('**/api/gacha/actions', (route) =>
    route.fulfill({ json: { snapshot: gachaSnapshot() } }),
  );
  await page.route('**/api/models**', (route) =>
    route.fulfill({
      json: {
        data: [],
        pagination: { page: '1', page_size: 20, total_items: '0', total_pages: '1' },
        next_cursor: null,
      },
    }),
  );
  await page.goto(USER_ORIGIN + '/activities/gacha');
  await expect(page.getByRole('button', { name: /Check in.*standard/i })).toBeVisible();
  let release!: () => void;
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  let failures = 0;
  await page.route('**/assets/ModelsPage-*.js', async (route) => {
    if (failures++ === 0) {
      await held;
      await route.fulfill({ status: 404, body: 'Not found' });
    } else await route.continue();
  });
  let documents = 0;
  page.on('request', (request) => {
    if (request.isNavigationRequest()) documents++;
  });
  await page.locator('a[href="/models"]').first().click();
  await expect(page.getByRole('progressbar')).toBeVisible();
  release();
  await expect(page.getByRole('heading', { name: 'This page needs to be reloaded' })).toBeVisible();
  expect(documents).toBe(0);
  await page.getByRole('button', { name: 'Reload page', exact: true }).click();
  await expect(page.locator('[data-error-kind="resource"]')).toHaveCount(0);
  await expect.poll(() => documents).toBe(1);
});

test('activity stream interruptions stay quiet until authority is unavailable for 15 seconds', async ({
  page,
}) => {
  await page.clock.install();
  await mockRoleSession(page, 'user', 'user');
  await mockPublicConfig(page, 'user');
  await page.addInitScript(() => localStorage.setItem('nb.lang', 'en'));
  const snapshot = loanActivity();
  let reads = 0;
  let restored = false;
  await page.route('**/api/activities', (route) => {
    reads++;
    return reads === 1 || restored
      ? route.fulfill({ json: snapshot })
      : route.fulfill({
          status: 503,
          json: { error: { code: 'unavailable', message: 'Try again' } },
        });
  });
  await page.route('**/api/limited-activities', (route) => route.fulfill({ json: [] }));
  await page.route('**/api/gacha/entry', (route) =>
    route.fulfill({ json: { enabled: false, entered: false, pools: [] } }),
  );
  let streams = 0;
  await page.route('**/api/events', (route) => {
    streams++;
    const frame = {
      version: 1,
      channel: 'activities',
      type: 'snapshot',
      revision: '1',
      identity_epoch: null,
      occurred_at: 1_800_000_000,
      data: snapshot,
    };
    return streams === 1
      ? route.fulfill({
          contentType: 'text/event-stream',
          body:
            'id: sse_AAAAAAAAAAAAAAAAAAAAAA\nevent: snapshot\ndata: ' +
            JSON.stringify(frame) +
            '\n\n',
        })
      : route.fulfill({ status: 503, body: 'Unavailable' });
  });
  await page.goto(USER_ORIGIN + '/activities');
  await expect.poll(() => streams).toBe(1);
  await page.clock.runFor(14_000);
  await expect(page.locator('.economy-stream-state')).toHaveCount(0);
  expect(reads).toBe(1);
  await page.clock.runFor(1_100);
  await expect(page.locator('.economy-stream-state')).toHaveCount(1);
  await expect(page.getByText('Activity updates are temporarily unavailable.')).toBeVisible();
  restored = true;
  await page.locator('.economy-stream-state').getByRole('button', { name: 'Retry' }).click();
  await expect(page.locator('.economy-stream-state')).toHaveCount(0);
});
