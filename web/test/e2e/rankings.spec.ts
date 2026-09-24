import { expect, test } from './test';
import {
  collectConsoleViolations,
  mockPublicConfig,
  mockRoleSession,
  userSession,
} from './support';
import { USER_ORIGIN } from './ports';
import { gamesSnapshotWire } from '../../src/user/games/common/testFixtures';
import { biddingHomeWire } from '../../src/user/games/bidding/testFixtures';
import { blackjackWire } from '../../src/user/games/blackjack/testFixtures';

for (const scenario of [
  { locale: 'zh', theme: 'dark', width: 390 },
  { locale: 'en', theme: 'light', width: 1440 },
] as const) {
  test(`new rankings, privacy and paging ${scenario.locale} ${scenario.width}`, async ({
    page,
  }) => {
    const guard = collectConsoleViolations(page),
      zh = scenario.locale === 'zh';
    await page.setViewportSize({ width: scenario.width, height: 900 });
    await page.emulateMedia({ reducedMotion: 'reduce' });
    await page.addInitScript(({ locale, theme }) => {
      localStorage.setItem('nb.lang', locale);
      localStorage.setItem('nb.theme', theme);
    }, scenario);
    await mockRoleSession(page, 'user', 'user');
    await mockPublicConfig(page, 'user');
    const session = userSession('user'),
      writes: unknown[] = [],
      windows: string[] = [];
    await page.route('**/api/events', (route) =>
      route.fulfill({ contentType: 'text/event-stream', body: ': connected\n\n' }),
    );
    await page.route('**/api/session', (route) => route.fulfill({ json: session }));
    await page.route('**/api/me', (route) => {
      if (route.request().method() === 'PATCH') {
        const body = route.request().postDataJSON();
        writes.push(body);
        expect(route.request().headers()['idempotency-key']).toHaveLength(22);
        session.user.charity_profile_public = body.charity_profile_public;
      }
      return route.fulfill({ json: session });
    });
    await page.route('**/api/games**', async (route) => {
      const url = new URL(route.request().url()),
        path = url.pathname;
      if (path === '/api/games') return route.fulfill({ json: gamesSnapshotWire() });
      if (
        path.endsWith('/leaderboard') ||
        path.endsWith('/net-profit') ||
        path === '/api/games/leaderboards/charity'
      ) {
        const window = url.searchParams.get('window') ?? '7d';
        windows.push(window);
        return route.fulfill({
          json: {
            as_of: 2000000000,
            statistics_start: 1900000000,
            window,
            rows: Array.from({ length: 20 }, (_, i) => ({
              rank: String(i + 1),
              amount: '9007199254740993.123',
              is_me: false,
              identity: { kind: 'anonymous' },
            })),
            me: { rank: '21', amount: '17.125', is_me: true, identity: { kind: 'anonymous' } },
          },
        });
      }
      if (path === '/api/games/bidding/state')
        return route.fulfill({ json: { ...biddingHomeWire(), current: null } });
      if (path === '/api/games/blackjack/state')
        return route.fulfill({ json: blackjackWire('seating', null) });
      return route.fallback();
    });
    await page.route('**/api/charity/leaderboard**', (route) => {
      const p = new URL(route.request().url()).searchParams.get('page') ?? '1',
        second = p === '2';
      return route.fulfill({
        json: {
          as_of: 2000000000,
          statistics_start: 1900000000,
          window: 'history',
          rows: Array.from({ length: second ? 1 : 20 }, (_, i) => ({
            rank: String(second ? 21 : i + 1),
            amount: '12345678901234567.123',
            is_me: second,
            identity: { kind: 'anonymous' },
          })),
          me: null,
          pagination: { page: p, page_size: 20, total_items: '21', total_pages: '2' },
        },
      });
    });
    await page.route('**/api/charity/models**', (route) => {
      const catalog = new URL(route.request().url()).searchParams.get('view') === 'catalog';
      return route.fulfill({
        json: {
          models: [],
          donation_intake: 'open',
          server_now: 2000000000,
          ...(catalog
            ? { pagination: { page: '1', page_size: 20, total_items: '0', total_pages: '1' } }
            : { state: 'no_models' }),
        },
      });
    });
    await page.route('**/api/donations**', (route) =>
      route.fulfill({ json: { data: [], next_cursor: null } }),
    );
    for (const path of ['/games', '/games/bidding', '/games/blackjack']) {
      await page.goto(`${USER_ORIGIN}${path}`);
      const cards = page.locator('.progression-ranking');
      await expect(cards).toHaveCount(path === '/games' ? 2 : 1);
      if (path === '/games/blackjack') {
        await expect(cards.first().getByRole('heading')).toHaveText(
          zh ? '赌神榜' : 'Card master leaderboard',
        );
        await expect(cards.first().locator('select')).toHaveCount(0);
        const tabs = page.getByRole('tablist', {
          name: zh ? '选择排行榜' : 'Choose a leaderboard',
        });
        await expect(tabs.getByRole('tab').first()).toHaveAttribute('aria-selected', 'true');
        await tabs.getByRole('tab').first().press('End');
        await expect(tabs.getByRole('tab').last()).toBeFocused();
        await expect(cards.first().getByRole('heading')).toHaveText(
          zh ? '利润榜' : 'Profit leaderboard',
        );
      }
      const card = cards.first();
      await expect(card.locator('tbody tr')).toHaveCount(21);
      await expect(card.locator('[data-own-rank]')).toContainText('17.125');
      await expect(card.getByText('9,007,199,254,740,993.123', { exact: true })).toHaveCount(20);
      expect(await card.locator('img').count()).toBe(0);
      if (path !== '/games') {
        await card.locator('select').selectOption('30d');
        await expect.poll(() => windows.at(-1)).toBe('30d');
        await card.locator('select').selectOption('history');
        await expect(card.locator('time')).toBeVisible();
      }
      if (path !== '/games/bidding') {
        if (path === '/games/blackjack')
          await page.getByRole('tab', { name: zh ? '赌神榜' : 'Card master leaderboard' }).click();
        const profit = cards.last();
        await expect(profit.getByRole('heading')).toHaveText(
          path === '/games'
            ? zh
              ? '游戏暴富榜'
              : 'Game fortune leaderboard'
            : zh
              ? '赌神榜'
              : 'Card master leaderboard',
        );
        await expect(profit.locator('tbody tr')).toHaveCount(21);
        await expect(profit.locator('select')).toHaveCount(0);
        await expect(profit).toContainText('7×24');
      }
      await card.scrollIntoViewIfNeeded();
      if (path === '/games/blackjack')
        await page
          .locator('.rank-switcher')
          .screenshot({ path: '../tmp/blackjack-rankings-' + scenario.width + '.png' });
      expect(
        await card
          .locator('.rank-table-scroll')
          .evaluate((node) => node.scrollWidth <= node.clientWidth + 1),
      ).toBe(true);
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      );
    }
    await page.goto(`${USER_ORIGIN}/charity`);
    const card = page.locator('.progression-ranking');
    await expect(card.locator('tbody tr')).toHaveCount(20);
    const privacy = card.getByRole('checkbox');
    await expect(privacy).toBeChecked();
    await privacy.focus();
    await page.keyboard.press('Space');
    await expect(privacy).not.toBeChecked();
    expect(writes).toEqual([{ charity_profile_public: true }]);
    expect(session.user.game_profile_public).toBe(false);
    await card.getByRole('button', { name: zh ? '下一页' : 'Next', exact: true }).click();
    await expect(card.locator('tbody tr')).toHaveCount(1);
    await expect(card.locator('[data-own-rank]')).toContainText('21');
    await expect(
      card.getByRole('button', { name: zh ? '下一页' : 'Next', exact: true }),
    ).toBeDisabled();
    await card.screenshot({ path: `../tmp/rankings-${scenario.width}.png` });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
      true,
    );
    guard.assertNone();
  });
}
