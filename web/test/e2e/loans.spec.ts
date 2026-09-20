import { expect, test } from './test';
import { mockPublicConfig, mockRoleSession, collectConsoleViolations } from './support';
import { USER_ORIGIN } from './ports';
import { loanActivity, loanQuote, loanReceipt } from '../fixtures/loans';
import { gamesSnapshotWire } from '../../src/user/games/common/testFixtures';
import { catalogWire } from '../../src/user/games/likes/testCatalog';

for (const scenario of [
  { locale: 'en', theme: 'light', width: 1440 },
  { locale: 'zh', theme: 'dark', width: 390 },
] as const) {
  test(`loan disclosure and history on ${scenario.locale} ${scenario.width}`, async ({ page }) => {
    const guard = collectConsoleViolations(page);
    await page.setViewportSize({ width: scenario.width, height: 900 });
    await page.emulateMedia({ reducedMotion: 'reduce' });
    await page.addInitScript(({ locale, theme }) => {
      localStorage.setItem('nb.lang', locale);
      localStorage.setItem('nb.theme', theme);
    }, scenario);
    await mockRoleSession(page, 'user', 'user');
    await mockPublicConfig(page, 'user');
    const activity = loanActivity(),
      writes: string[] = [],
      pages: string[] = [];
    await page.route('**/api/events', (route) =>
      route.fulfill({ contentType: 'text/event-stream', body: ': connected\n\n' }),
    );
    await page.route('**/api/activities**', async (route) => {
      const request = route.request(),
        url = new URL(request.url());
      if (url.pathname === '/api/activities') return route.fulfill({ json: activity });
      if (url.pathname === '/api/activities/loan/quote') {
        expect(request.postDataJSON()).toEqual({ tier: '1' });
        return route.fulfill({ json: loanQuote() });
      }
      if (url.pathname === '/api/activities/loan') {
        expect(request.headers()['idempotency-key']).toHaveLength(22);
        writes.push(request.postData()!);
        activity.loan.available = false;
        activity.loan.reason = 'negative_balance';
        return route.fulfill({ status: 201, json: loanReceipt() });
      }
      if (url.pathname === '/api/activities/loans') {
        pages.push(url.search);
        const size = Number(url.searchParams.get('page_size'));
        return route.fulfill({
          json: {
            data: [loanReceipt()],
            next_cursor: null,
            pagination: { page: '1', page_size: size, total_items: '1', total_pages: '1' },
          },
        });
      }
      return route.fallback();
    });
    await page.goto(`${USER_ORIGIN}/activities`);
    const zh = scenario.locale === 'zh';
    await page.getByRole('button', { name: zh ? '我要借款' : 'Get a loan', exact: true }).click();
    const dialog = page.getByRole('alertdialog');
    await expect(dialog).toBeVisible();
    await expect(dialog.locator('.loan-nominal')).toContainText('10000');
    await expect(dialog.getByText('9000', { exact: true })).toHaveCount(0);
    const star = dialog.getByRole('button', {
      name: zh ? '查看费用与余额详情' : 'View fees and balance details',
    });
    const box = await star.boundingBox();
    expect(box!.width).toBeGreaterThanOrEqual(44);
    expect(box!.height).toBeGreaterThanOrEqual(44);
    await star.focus();
    await page.keyboard.press('Enter');
    await expect(star).toHaveAttribute('aria-expanded', 'true');
    await expect(dialog.getByText('9000', { exact: true })).toBeVisible();
    await expect(dialog.getByText('13000', { exact: true })).toBeVisible();
    await expect(dialog.getByText('0 → -13000', { exact: true })).toBeVisible();
    await dialog.screenshot({ path: `../tmp/loan-details-${scenario.width}.png` });
    await star.click();
    await expect(dialog.getByText('9000', { exact: true })).toHaveCount(0);
    await dialog
      .getByRole('button', { name: zh ? '确认借款' : 'Confirm loan', exact: true })
      .click();
    await expect(
      page.getByText(zh ? '借款已到账' : 'Loan received', { exact: true }),
    ).toBeVisible();
    expect(writes).toHaveLength(1);
    await expect(
      page.getByRole('button', { name: zh ? '我要借款' : 'Get a loan', exact: true }),
    ).toBeDisabled();
    await page.getByRole('button', { name: zh ? '借款明细' : 'Loan history', exact: true }).click();
    const history = page.getByRole('dialog');
    await expect(history.getByText('9007199254740993', { exact: true })).toBeVisible();
    await history.locator('select').selectOption('100');
    await expect.poll(() => pages.length).toBe(2);
    expect(pages[1]).toBe('?page=1&page_size=100');
    expect(await history.evaluate((node) => node.scrollWidth <= node.clientWidth + 1)).toBe(true);
    await history.getByRole('button', { name: zh ? '关闭' : 'Close', exact: true }).click();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
      true,
    );
    guard.assertNone();
  });
}

for (const game of ['bidding', 'likes'] as const) {
  test(`${game} can enter with loan debt and available game credits`, async ({ page }) => {
    const guard = collectConsoleViolations(page);
    await mockRoleSession(page, 'user', 'user');
    await mockPublicConfig(page, 'user');
    const snapshot = gamesSnapshotWire();
    snapshot.balance = '-13000';
    snapshot.game_balance = '9000';
    snapshot[game].enabled = true;
    Object.values(snapshot[game].modes).forEach((mode) => {
      mode.enabled = true;
    });
    await page.route('**/api/games**', async (route) => {
      const path = new URL(route.request().url()).pathname;
      if (path === '/api/games') return route.fulfill({ json: snapshot });
      if (path.endsWith('/catalog')) return route.fulfill({ json: catalogWire() });
      if (path.endsWith('/state'))
        return route.fulfill({
          json: { server_now: 1800000000, current: null, queue: null, latest_result: null },
        });
      return route.fallback();
    });
    await page.goto(`${USER_ORIGIN}/games/${game}`);
    await expect(
      page.getByRole('button', { name: 'Pay entry and find a match', exact: true }),
    ).toBeEnabled();
    guard.assertNone();
  });
}
