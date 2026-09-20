import { expect, test } from './test';
import { collectConsoleViolations, mockPublicConfig, mockRoleSession } from './support';
import { USER_ORIGIN } from './ports';
import { blackjackWire } from '../../src/user/games/blackjack/testFixtures';
import { gamesSnapshotWire } from '../../src/user/games/common/testFixtures';
import { biddingHomeWire } from '../../src/user/games/bidding/testFixtures';

for (const [lang, width, theme] of [
  ['zh', 320, 'light'],
  ['en', 390, 'dark'],
  ['en', 1440, 'light'],
  ['zh', 1440, 'dark'],
] as const) {
  test(`quick stakes and nine seats ${lang} ${width} ${theme}`, async ({ page }) => {
    const errors = collectConsoleViolations(page);
    await mockRoleSession(page, 'user', 'user');
    await mockPublicConfig(page, 'user');
    await page.setViewportSize({ width, height: 900 });
    await page.addInitScript(
      ({ lang, theme }) => {
        localStorage.setItem('nb.lang', lang);
        localStorage.setItem('nb.theme', theme);
      },
      { lang, theme },
    );
    const home = blackjackWire('seating', null);
    home.config.quick_stakes = Array.from({ length: 8 }, (_, i) => String((i + 1) * 1000));
    const writes: unknown[] = [];
    await page.route('**/api/games**', async (route) => {
      const path = new URL(route.request().url()).pathname;
      if (path === '/api/games') return route.fulfill({ json: gamesSnapshotWire() });
      if (path.endsWith('/blackjack/state')) return route.fulfill({ json: home });
      if (path.endsWith('/blackjack/queue')) {
        writes.push(route.request().postDataJSON());
        home.config_hash = 'd'.repeat(64);
        home.config.quick_stakes = ['1000'];
        return route.fulfill({
          status: 409,
          json: { error: { code: 'conflict', message: 'Configuration changed' } },
        });
      }
      return route.fallback();
    });
    await page.goto(`${USER_ORIGIN}/games/blackjack`);
    await expect(
      page.getByRole('heading', { name: lang === 'zh' ? '二十一点' : 'Blackjack', exact: true }),
    ).toBeVisible();
    await expect(page.locator('html')).toHaveAttribute('data-theme', theme);
    const grid = page.locator('.bj-seats--seating > .bj-seat');
    await expect(grid).toHaveCount(9);
    const positions = await grid.evaluateAll((nodes) =>
      nodes.map((node) => {
        const box = node.getBoundingClientRect();
        return { x: Math.round(box.x), y: Math.round(box.y) };
      }),
    );
    expect(new Set(positions.map((p) => p.x)).size).toBe(3);
    expect(new Set(positions.map((p) => p.y)).size).toBe(3);
    const quick = page.locator('.bj-quick-stakes');
    await expect(quick.getByRole('button')).toHaveCount(8);
    for (const index of [7, 0, 4, 2]) await quick.getByRole('button').nth(index).click();
    const selected = quick.getByRole('button').nth(1);
    await selected.focus();
    await page.keyboard.press('Space');
    await expect(selected).toHaveAttribute('aria-pressed', 'true');
    const input = page.locator('.bj-queue input');
    await expect(input).toHaveValue('2000');
    expect(writes).toHaveLength(0);
    for (const box of await quick.getByRole('button').evaluateAll((nodes) =>
      nodes.map((node) => {
        const r = node.getBoundingClientRect();
        // Chromium can report 43.999969px for a 44px layout box.
        return {
          width: Math.round(r.width * 1000) / 1000,
          height: Math.round(r.height * 1000) / 1000,
        };
      }),
    )) {
      expect(box.width).toBeGreaterThanOrEqual(44);
      expect(box.height).toBeGreaterThanOrEqual(44);
    }
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
      true,
    );
    await page.screenshot({
      path: `../tmp/quick-stakes-${lang}-${width}-${theme}.png`,
      fullPage: true,
    });
    errors.assertNone();
    await page.locator('.bj-queue button[type=submit]').click();
    await expect.poll(() => writes.length).toBe(1);
    expect(writes[0]).toEqual({ stake: '2000', config_hash: 'c'.repeat(64) });
    await expect(quick.getByRole('button')).toHaveCount(1);
    await expect(input).toHaveValue('2000');
    home.config.quick_stakes = [];
    await expect(quick).toHaveCount(0);
    expect(writes).toHaveLength(1);
  });
}

for (const you of [0, 1]) {
  test(`bidding suits follow seat ${you} through bidding, joker and public history`, async ({
    page,
  }) => {
    const errors = collectConsoleViolations(page);
    await mockRoleSession(page, 'user', 'user');
    await mockPublicConfig(page, 'user');
    await page.addInitScript(() => localStorage.setItem('nb.lang', 'en'));
    const home = biddingHomeWire();
    home.current.you = you;
    home.current.round = 2;
    home.current.view.played = [[2], [3]];
    home.current.view.rewards.push(
      { round: 2, side: 0, rank: 4, multiplier: 1, status: 'pool', owner: null },
      { round: 2, side: 1, rank: 9, multiplier: 1, status: 'pool', owner: null },
    );
    home.current.view.hand_remaining = home.current.view.hand_remaining.map((cards, side) =>
      cards.filter((n) => n !== (side === 0 ? 2 : 3)),
    );
    const suit = you === 0 ? 'Hearts' : 'Spades',
      symbol = you === 0 ? '♥' : '♠';
    const other = you === 0 ? 'Spades' : 'Hearts';
    await page.route('**/api/games**', async (route) => {
      const path = new URL(route.request().url()).pathname;
      if (path === '/api/games') return route.fulfill({ json: gamesSnapshotWire() });
      if (path.endsWith('/bidding/state')) return route.fulfill({ json: home });
      return route.fallback();
    });
    await page.goto(`${USER_ORIGIN}/games/bidding`);
    const king = page.getByRole('button', { name: `Bid ${suit} K (13)`, exact: true });
    await expect(king).toContainText(symbol);
    await expect(
      page.getByRole('button', { name: `Opponent’s thirteen cards ${other} K (13)`, exact: true }),
    ).toBeDisabled();
    await expect(page.locator('.bid-played')).toContainText('Hearts 2');
    await expect(page.locator('.bid-played')).toContainText('Spades 3');
    await expect(page.locator('.bid-reward--0').first()).toContainText('♦');
    await expect(page.locator('.bid-reward--1').first()).toContainText('♣');
    home.current.phase = 'joker';
    home.current.phase_seq = '2';
    home.current.revision = '2';
    await expect(
      page.getByRole('button', { name: `Your thirteen cards ${suit} K (13)`, exact: true }),
    ).toContainText(symbol);
    await page.getByRole('button', { name: 'Rules', exact: true }).click();
    await expect(page.getByRole('dialog')).toContainText('red side bids with hearts');
    await expect(page.getByRole('dialog')).toContainText('black side bids with spades');
    errors.assertNone();
  });
}
