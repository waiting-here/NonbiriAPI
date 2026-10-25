import { mkdirSync, readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { expect, test } from '@playwright/test';

test.skip(process.env.NONBIRI_MANAGEMENT_BROWSER !== '1', 'Requires the management fixture.');

test('owner donation views and details accept real multiline approval notes', async ({
  browser,
}) => {
  const state = JSON.parse(readFileSync(process.env.NONBIRI_AUDIT_BROWSER_STATE!, 'utf8')) as {
    user_url: string;
    users: { level: number; cookie: { Name: string; Value: string } }[];
    management: { multiline_review_donation_id: string };
  };
  const context = await browser.newContext({
    viewport: { width: 1440, height: 1000 },
    reducedMotion: 'reduce',
  });
  try {
    const cookie = state.users.find((user) => user.level === 1)!.cookie;
    await context.addCookies([
      {
        name: cookie.Name,
        value: cookie.Value,
        domain: new URL(state.user_url).hostname,
        path: '/api',
        httpOnly: true,
        sameSite: 'Lax',
      },
    ]);
    await context.addInitScript(() => localStorage.setItem('nb.lang', 'en'));
    const page = await context.newPage();
    const errors: string[] = [];
    page.on('pageerror', (error) => errors.push(error.message));
    for (const view of ['current', 'all', 'history', 'hidden']) {
      const response = page.waitForResponse((result) => {
        const url = new URL(result.url());
        return url.pathname === '/api/donations' && url.searchParams.get('view') === view;
      });
      await page.goto(`${state.user_url}/charity?tab=donations&donation_view=${view}`);
      const received = await response;
      expect(received.status()).toBe(200);
      const data = (await received.json()) as {
        data: { id: string; description: string; review_result: { reason: string } | null }[];
      };
      if (view === 'current' || view === 'all') {
        expect(
          data.data.find((row) => row.id === state.management.multiline_review_donation_id)
            ?.review_result?.reason,
        ).toBe('First approval line\nSecond approval line');
        await expect(page.getByText('Multiline approval fixture', { exact: true })).toBeVisible();
      } else if (data.data.length) {
        await expect(page.getByText(data.data[0].description, { exact: true })).toBeVisible();
      } else {
        await expect(page.locator('.economy-owner-pages .empty-state')).toBeVisible();
      }
      await expect(page.locator('.economy-owner-pages .error-state')).toHaveCount(0);
    }
    await page.goto(
      `${state.user_url}/charity/donations/${state.management.multiline_review_donation_id}`,
    );
    await expect(page.getByText('First approval line', { exact: false })).toBeVisible();
    await expect(page.getByText('Second approval line', { exact: false })).toBeVisible();
    for (const width of [1440, 390]) {
      await page.setViewportSize({ width, height: 1000 });
      for (const theme of ['light', 'dark']) {
        await page.evaluate((value) => {
          document.documentElement.dataset.theme = value;
        }, theme);
        await page.getByText('First approval line', { exact: false }).scrollIntoViewIfNeeded();
        expect(
          await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1),
        ).toBe(true);
        if (process.env.NONBIRI_VISUAL_DIR) {
          mkdirSync(process.env.NONBIRI_VISUAL_DIR, { recursive: true });
          await page.screenshot({
            path: resolve(process.env.NONBIRI_VISUAL_DIR, `donation-review-${width}-${theme}.png`),
          });
        }
      }
    }
    expect(errors).toEqual([]);
  } finally {
    await context.close();
  }
});
