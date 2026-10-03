import { mkdirSync } from 'node:fs';
import { expect, test } from './test';
import {
  collectConsoleViolations,
  mockPublicConfig,
  mockRoleSession,
  userSession,
} from './support';
import { USER_ORIGIN } from './ports';
import { gamesSnapshotWire } from '../../src/user/games/common/testFixtures';
import { catalogWire } from '../../src/user/games/likes/testCatalog';
import { biddingHomeWire } from '../../src/user/games/bidding/testFixtures';
import { blackjackWire } from '../../src/user/games/blackjack/testFixtures';

for (const width of [1440, 768, 390])
  for (const locale of ['en', 'zh'] as const) {
    test(`game page controls ${locale} ${width}`, async ({ page }) => {
      const guard = collectConsoleViolations(page);
      await page.setViewportSize({ width, height: 900 });
      await page.emulateMedia({ reducedMotion: 'reduce' });
      await page.addInitScript(
        ({ locale }) => {
          localStorage.setItem('nb.lang', locale);
          localStorage.setItem('nb.theme', locale === 'en' ? 'light' : 'dark');
        },
        { locale },
      );
      await mockRoleSession(page, 'user', 'user');
      await mockPublicConfig(page, 'user');
      await page.route('**/api/events', (route) =>
        route.fulfill({
          contentType: 'text/event-stream',
          body: ': connected' + String.fromCharCode(10, 10),
        }),
      );
      const snapshot = gamesSnapshotWire();
      snapshot.balance = '10000';
      for (const game of ['bidding', 'likes'] as const) {
        snapshot[game].enabled = true;
        for (const [key, mode] of Object.entries(snapshot[game].modes)) {
          mode.enabled = true;
          mode.content_hash = (game === 'likes' && key === 'standard' ? 'b' : 'a').repeat(64);
        }
      }
      snapshot.blackjack.enabled = true;
      let active = false;
      await page.route('**/api/games**', (route) => {
        const url = new URL(route.request().url()),
          path = url.pathname;
        if (path === '/api/games') return route.fulfill({ json: snapshot });
        if (path.endsWith('/catalog')) return route.fulfill({ json: catalogWire() });
        if (path === '/api/games/likes/loadouts')
          return route.fulfill({ json: { capacity: 10, slots: [] } });
        if (path === '/api/games/fishing/state')
          return route.fulfill({
            json: { settlement_pending: null, unrevealed: null, has_more_unrevealed: false },
          });
        if (path === '/api/games/linklink/session') return route.fulfill({ json: null });
        if (path === '/api/games/blackjack/state')
          return route.fulfill({
            json: blackjackWire(active ? 'decision' : 'seating', active ? 0 : null),
          });
        if (path === '/api/games/rps/state')
          return route.fulfill({
            json: { kind: 'idle', tutorial_seen: true, modes: snapshot.rps.modes },
          });
        if (path.endsWith('/state'))
          return route.fulfill({
            json:
              active && path.includes('/bidding/')
                ? biddingHomeWire()
                : { server_now: 1800000000, current: null, queue: null, latest_result: null },
          });
        if (path.includes('/fishing/leaderboard'))
          return route.fulfill({
            json: {
              board: url.searchParams.get('board'),
              window_start: url.searchParams.get('board') === 'single' ? null : 1700000000,
              entries: [],
              me: null,
            },
          });
        if (path.includes('/linklink/leaderboard'))
          return route.fulfill({
            json: {
              spec: url.searchParams.get('spec'),
              window_days: Number((url.searchParams.get('window') ?? '7d').slice(0, -1)),
              window_start: 1700000000,
              as_of: 1800000000,
              rules_version: 2,
              rows: [],
              me: null,
            },
          });
        if (path.includes('/rps/leaderboard'))
          return route.fulfill({
            json: {
              mode: url.searchParams.get('mode'),
              board: url.searchParams.get('board'),
              window_days: 30,
              window_start: 1700000000,
              min_sessions: 10,
              rows: [],
              me: null,
            },
          });
        if (
          path.includes('/leaderboards/') ||
          path.endsWith('/net-profit') ||
          path.endsWith('/blackjack/leaderboard') ||
          path.endsWith('/bidding/leaderboard')
        )
          return route.fulfill({
            json: {
              ...(path.includes('/bidding/net-profit')
                ? { rebuild_status: 'completed', missing_events: '0' }
                : {}),
              window: url.searchParams.get('window') ?? '7d',
              as_of: 1800000000,
              statistics_start: 1700000000,
              rows: [],
              me: null,
            },
          });
        return route.fallback();
      });
      const evidence = process.env.NONBIRI_GAME_PRESENTATION_EVIDENCE;
      if (evidence) mkdirSync(evidence, { recursive: true });
      for (const game of ['', 'fishing', 'linklink', 'rps', 'bidding', 'blackjack', 'likes']) {
        await page.goto(`${USER_ORIGIN}/games${game ? `/${game}` : ''}`);
        await expect(page.locator('h1')).toBeVisible();
        if (game) await expect(page.locator('.game-actionbar'), game).toHaveCount(1);
        else {
          await expect(page.locator('a.game-center-card')).toHaveCount(6);
          await expect(page.locator('a.game-center-card button')).toHaveCount(0);
          if (width === 390) {
            const cards = page.locator('a.game-center-card');
            expect(
              await page
                .locator('.game-center-grid')
                .evaluate((el) => el.getBoundingClientRect().height),
            ).toBeLessThan(1000);
            for (const card of await cards.all()) {
              const hero = await card.locator('.game-center-card__hero').boundingBox();
              const body = await card.locator('.game-center-card__body').boundingBox();
              expect(hero).not.toBeNull();
              expect(body).not.toBeNull();
              expect(hero!.x + hero!.width).toBeLessThanOrEqual(body!.x + 1);
              expect(Math.abs(hero!.y - body!.y)).toBeLessThanOrEqual(1);
            }
          }
        }
        if (game === 'likes' && width === 390) {
          const steps = page.locator('.likes-loadout-step');
          await expect(steps).toHaveCount(4);
          await steps.nth(1).locator('summary').click();
          await expect(page.locator('.likes-loadout-step[open]')).toHaveCount(1);
          await steps.nth(1).locator('[data-guide^="role:"]').nth(1).click();
          const detail = page.getByRole('dialog');
          await expect(detail).toBeVisible();
          await detail
            .getByRole('button', { name: locale === 'zh' ? '关闭' : 'Close', exact: true })
            .click();
          await expect(detail).not.toBeVisible();
          await steps.nth(1).locator('summary').click();
          await expect(page.locator('.likes-loadout-step[open]')).toHaveCount(0);
          expect(
            await page.locator('.likes-game').evaluate((el) => el.getBoundingClientRect().height),
          ).toBeLessThan(2500);
        }
        if (width === 390 && game) {
          const box = await page.locator('.game-actionbar button').boundingBox();
          expect(box!.y).toBeGreaterThan(0);
          expect(box!.y + box!.height).toBeLessThanOrEqual(900);
        }
        await expect(page.locator('.progression-ranking .loading-dot')).toHaveCount(0);
        await expect(page.locator('.progression-ranking [role=alert]')).toHaveCount(0);
        expect(
          await page.evaluate(() => document.documentElement.scrollWidth - innerWidth),
        ).toBeLessThanOrEqual(1);
        if (!game) {
          await page.locator('a.game-center-card').last().scrollIntoViewIfNeeded();
          await expect(page.locator('a.game-center-card').last().locator('img')).toHaveJSProperty(
            'complete',
            true,
          );
          await page.evaluate(() => scrollTo(0, 0));
        }
        await page.evaluate(() => scrollTo(0, 0));
        if (evidence)
          await page.screenshot({
            path: `${evidence}/${game || 'lobby'}-${locale}-${width}.png`,
            fullPage: true,
          });
      }
      active = true;
      for (const game of ['bidding', 'blackjack']) {
        await page.goto(`${USER_ORIGIN}/games/${game}`);
        await expect(page.locator('.game-actionbar')).toHaveCount(0);
        const controls = page.locator(game === 'bidding' ? '.bid-confirm' : '.bj-controls');
        await expect(controls).toBeVisible();
        await page.evaluate(() => scrollTo(0, 0));
        if (evidence)
          await page.screenshot({
            path: `${evidence}/${game}-active-${locale}-${width}.png`,
            fullPage: true,
          });
      }
      guard.assertNone();
    });
  }

for (const width of [1440, 768, 390])
  for (const locale of ['en', 'zh'] as const) {
    test(`activity open and closed presentation ${locale} ${width}`, async ({ page }) => {
      const guard = collectConsoleViolations(page);
      await page.setViewportSize({ width, height: 900 });
      await page.emulateMedia({ reducedMotion: 'reduce' });
      await page.addInitScript(
        ({ locale }) => {
          localStorage.setItem('nb.lang', locale);
          localStorage.setItem('nb.theme', locale === 'en' ? 'light' : 'dark');
        },
        { locale },
      );
      await mockRoleSession(page, 'user', 'user');
      await mockPublicConfig(page, 'user');
      await page.route('**/api/events', (route) =>
        route.fulfill({
          contentType: 'text/event-stream',
          body: ': connected' + String.fromCharCode(10, 10),
        }),
      );
      const activity = {
        loan: {
          enabled: false,
          available: false,
          reason: 'disabled',
          tiers: ['10000', '100000', '1000000'],
        },
        master: { enabled: true, available: true, reason: 'available' },
        welfare: {
          asset_type: 'game',
          pool_asset_type: 'general',
          enabled: true,
          state: 'available',
          site_day: '2026-08-31',
          threshold: '10',
          cap: '2.5',
          pool_balance: '9.999',
          claimed_today: false,
        },
        thursday: {
          enabled: true,
          state: 'open',
          server_now: 1_788_111_000,
          current: {
            period_id: 'thu_abcdefghijklmnopqrstuA',
            revision: '7',
            opens_at: 1_788_110_000,
            closes_at: 1_788_196_400,
            literature: '<b>plain text only</b> V me 50',
            entry: '50',
            per_user_limit: 3,
            pool_balance: '12345678901234567890.123',
            my_count: '1',
            my_contributed: '50',
          },
          next: null,
          last_result: null,
        },
      };
      activity.loan = { ...activity.loan, enabled: true, available: true, reason: 'available' };
      await page.route('**/api/activities', (route) => route.fulfill({ json: activity }));
      await page.route('**/api/limited-activities', (route) => route.fulfill({ json: [] }));
      const evidence = process.env.NONBIRI_GAME_PRESENTATION_EVIDENCE;
      if (evidence) mkdirSync(evidence, { recursive: true });
      for (const open of [true, false]) {
        activity.master = {
          enabled: open,
          available: open,
          reason: open ? 'available' : 'disabled',
        };
        if (!open) {
          activity.loan = {
            ...activity.loan,
            available: false,
            reason: 'disabled',
            enabled: false,
          };
          activity.welfare.state = 'unavailable';
          activity.thursday.state = 'unavailable';
        }
        await page.goto(`${USER_ORIGIN}/activities`);
        const slots = page.locator('.activity-slot');
        await expect(slots).toHaveCount(3);
        await expect(page.locator('.activity-slot.is-available')).toHaveCount(open ? 3 : 0);
        await expect(page.locator('#limited-activities-heading')).toHaveCount(0);
        if (!open) {
          await expect(slots.locator('summary').first()).toBeVisible();
          await expect(page.locator('.activity-slot[open]')).toHaveCount(0);
          await slots.first().locator(':scope > summary').click();
          await expect(slots.first()).toHaveAttribute('open', '');
          await expect(slots.first().locator('.loan-actions .btn-primary')).toBeDisabled();
          await slots.first().locator(':scope > summary').click();
        }
        expect(
          await page.evaluate(() => document.documentElement.scrollWidth - innerWidth),
        ).toBeLessThanOrEqual(1);
        await page.evaluate(() => scrollTo(0, 0));
        if (evidence)
          await page.screenshot({
            path: `${evidence}/activities-${open ? 'open' : 'closed'}-${locale}-${width}.png`,
            fullPage: true,
          });
      }
      guard.assertNone();
    });
  }

test('game anonymity keeps the existing idempotent profile save', async ({ page }) => {
  await mockRoleSession(page, 'user', 'user');
  await mockPublicConfig(page, 'user');
  const session = userSession('user'),
    writes: unknown[] = [];
  await page.route('**/api/session', (route) => route.fulfill({ json: session }));
  await page.route('**/api/me', (route) => {
    if (route.request().method() === 'PATCH') {
      const body = route.request().postDataJSON();
      writes.push(body);
      expect(route.request().headers()['idempotency-key']).toHaveLength(22);
      session.user.game_profile_public = body.game_profile_public;
    }
    return route.fulfill({ json: session });
  });
  await page.route('**/api/games', (route) => route.fulfill({ json: gamesSnapshotWire() }));
  await page.route('**/api/games/leaderboards/**', (route) =>
    route.fulfill({
      json: { as_of: 1800000000, statistics_start: 1700000000, window: '7d', rows: [], me: null },
    }),
  );
  await page.goto(`${USER_ORIGIN}/games#game-privacy`);
  const toggle = page.locator('#game-privacy').getByRole('switch');
  await expect(toggle).toBeChecked();
  await toggle.focus();
  await toggle.press('Space');
  await expect(toggle).not.toBeChecked();
  expect(writes).toEqual([{ game_profile_public: true }]);
  expect(session.user.charity_profile_public).toBe(false);
});

for (const scenario of [
  { width: 1440, locale: 'en' },
  { width: 390, locale: 'zh' },
] as const) {
  test(`compact game header tools ${scenario.locale} ${scenario.width}`, async ({ page }) => {
    const guard = collectConsoleViolations(page);
    await page.setViewportSize({ width: scenario.width, height: 900 });
    await page.addInitScript(({ locale }) => localStorage.setItem('nb.lang', locale), scenario);
    await mockRoleSession(page, 'user', 'user');
    await mockPublicConfig(page, 'user');
    const snapshot = gamesSnapshotWire();
    snapshot.likes.enabled = true;
    for (const [key, mode] of Object.entries(snapshot.likes.modes)) {
      mode.enabled = true;
      mode.content_hash = (key === 'quick' ? 'a' : 'b').repeat(64);
    }
    await page.route('**/api/games**', (route) => {
      const path = new URL(route.request().url()).pathname;
      if (path === '/api/games') return route.fulfill({ json: snapshot });
      if (path.endsWith('/catalog')) return route.fulfill({ json: catalogWire() });
      if (path.endsWith('/loadouts')) return route.fulfill({ json: { capacity: 10, slots: [] } });
      if (path.endsWith('/state'))
        return route.fulfill({
          json: { server_now: 1800000000, current: null, queue: null, latest_result: null },
        });
      return route.fallback();
    });
    await page.goto(`${USER_ORIGIN}/games/likes`);
    const header = page.locator('.likes-heading');
    await expect(header.locator('.game-header-tool')).toHaveCount(7);
    const sound = header.locator('button[aria-pressed]').first();
    await expect(sound).toHaveAttribute('aria-label', /.+/);
    const pressed = await sound.getAttribute('aria-pressed');
    await sound.focus();
    await sound.press('Space');
    await expect(sound).toHaveAttribute('aria-pressed', pressed === 'true' ? 'false' : 'true');
    const rules = header.getByRole('button', {
      name: scenario.locale === 'zh' ? '规则' : 'Rules',
      exact: true,
    });
    await rules.click();
    await expect(page.getByRole('dialog')).toBeVisible();
    await page
      .getByRole('dialog')
      .getByRole('button', { name: scenario.locale === 'zh' ? '关闭' : 'Close', exact: true })
      .click();
    await expect(rules).toBeFocused();
    await expect(
      header.getByRole('link', {
        name: scenario.locale === 'zh' ? '综合榜' : 'Overall rankings',
        exact: true,
      }),
    ).toHaveAttribute('href', '/games#game-rankings');
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth - innerWidth),
    ).toBeLessThanOrEqual(1);
    await page.evaluate(() => scrollTo(0, 0));
    const evidence = process.env.NONBIRI_GAME_PRESENTATION_EVIDENCE;
    if (evidence)
      await page.screenshot({
        path: `${evidence}/header-${scenario.locale}-${scenario.width}.png`,
        fullPage: true,
      });
    guard.assertNone();
  });
}
