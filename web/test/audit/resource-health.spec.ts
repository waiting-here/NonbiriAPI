import { readFileSync, mkdirSync } from 'node:fs';
import { resolve } from 'node:path';
import { test, expect } from '@playwright/test';

test.skip(
  process.env.NONBIRI_MANAGEMENT_BROWSER !== '1',
  'Requires the isolated management fixture.',
);

test('current service failures are visible to owner and managers and recover through the existing action', async ({
  browser,
}) => {
  const fixture = JSON.parse(readFileSync(process.env.NONBIRI_AUDIT_BROWSER_STATE!, 'utf8')) as {
    admin_url: string;
    user_url: string;
    admin_cookie: { Name: string; Value: string };
    users: { level: number; cookie: { Name: string; Value: string } }[];
    management: { resource_health: { donation_id: string; key_id: string; model_id: string } };
  };
  const ids = fixture.management.resource_health;
  for (const role of ['owner', 'admin', 'steward'] as const) {
    const origin = role === 'admin' ? fixture.admin_url : fixture.user_url;
    const cookie =
      role === 'admin'
        ? fixture.admin_cookie
        : fixture.users.find((u) => u.level === (role === 'owner' ? 1 : 6))!.cookie;
    const context = await browser.newContext({
      viewport: { width: 1440, height: 1000 },
      reducedMotion: 'reduce',
    });
    try {
      await context.addCookies([
        {
          name: cookie.Name,
          value: cookie.Value,
          domain: new URL(origin).hostname,
          path: role === 'admin' ? '/admin' : '/api',
          httpOnly: true,
          sameSite: 'Lax',
        },
      ]);
      await context.addInitScript(() => {
        localStorage.setItem('nb.lang', 'en');
        localStorage.setItem('nb.theme', 'light');
      });
      const page = await context.newPage();
      const errors: string[] = [];
      page.on('pageerror', (error) => errors.push(error.message));
      await page.goto(
        origin +
          (role === 'owner'
            ? `/charity/donations/${ids.donation_id}`
            : role === 'admin'
              ? `/charity?donation_id=${ids.donation_id}`
              : `/steward?tab=charity&donation_id=${ids.donation_id}`),
      );
      const health = page.getByRole('region', { name: 'Service health' }).first();
      await expect(
        health.getByText('Paused after consecutive failures', { exact: true }),
      ).toBeVisible();
      await expect(health.getByText('10 consecutive failures', { exact: true })).toBeVisible();
      await expect(health).toContainText('reset failures to resume');
      await expect(
        page.getByRole('button', { name: 'Reset failures and resume services', exact: true }),
      ).toBeVisible();
      for (const width of [1440, 390]) {
        await page.setViewportSize({ width, height: 1000 });
        for (const theme of ['light', 'dark']) {
          await page.evaluate((value) => {
            localStorage.setItem('nb.theme', value);
            document.documentElement.dataset.theme = value;
          }, theme);
          await health.scrollIntoViewIfNeeded();
          expect(
            await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1),
          ).toBe(true);
          if (process.env.NONBIRI_VISUAL_DIR) {
            mkdirSync(process.env.NONBIRI_VISUAL_DIR, { recursive: true });
            await page.screenshot({
              path: resolve(process.env.NONBIRI_VISUAL_DIR, `${role}-${width}-${theme}.png`),
            });
          }
        }
      }
      if (role === 'admin') {
        await page.goto(`${origin}/charity?charity_section=models&charity_model=${ids.model_id}`);
        await expect(page.getByText('health-upstream', { exact: true })).toBeVisible();
        const bindingHealth = page.getByRole('region', { name: 'Service health' }).first();
        await expect(bindingHealth).toContainText('Paused after consecutive failures');
        for (const width of [1440, 390]) {
          await page.setViewportSize({ width, height: 1000 });
          for (const theme of ['light', 'dark']) {
            await page.evaluate((value) => {
              localStorage.setItem('nb.theme', value);
              document.documentElement.dataset.theme = value;
            }, theme);
            await bindingHealth.scrollIntoViewIfNeeded();
            expect(
              await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1),
            ).toBe(true);
            if (process.env.NONBIRI_VISUAL_DIR)
              await page.screenshot({
                path: resolve(process.env.NONBIRI_VISUAL_DIR, `binding-${width}-${theme}.png`),
              });
          }
        }
      }
      expect(errors).toEqual([]);
    } finally {
      await context.close();
    }
  }
  // A real service reset must refresh both the donation projection and binding availability.
  const context = await browser.newContext();
  try {
    const cookie = fixture.admin_cookie;
    await context.addCookies([
      {
        name: cookie.Name,
        value: cookie.Value,
        domain: new URL(fixture.admin_url).hostname,
        path: '/admin',
        httpOnly: true,
        sameSite: 'Lax',
      },
    ]);
    await context.addInitScript(() => localStorage.setItem('nb.lang', 'en'));
    const page = await context.newPage();
    await page.goto(`${fixture.admin_url}/charity?donation_id=${ids.donation_id}`);
    await page
      .getByRole('button', { name: 'Reset failures and resume services', exact: true })
      .click();
    await expect(page.getByRole('region', { name: 'Service health' }).first()).not.toContainText(
      'Paused after consecutive failures',
    );
    await page.goto(
      `${fixture.admin_url}/charity?charity_section=models&charity_model=${ids.model_id}`,
    );
    await expect(page.getByText('Connection available', { exact: true })).toBeVisible();
  } finally {
    await context.close();
  }
});
