import { mkdir } from 'node:fs/promises';
import { join } from 'node:path';
import { expect, test } from './test';
import { ADMIN_ORIGIN, USER_ORIGIN } from './ports';
import { mockPublicConfig, mockRoleSession } from './support';
import { managedUserFixture } from '../fixtures/managedUsers';
import en from '../../src/shared/i18n/common/en.json' with { type: 'json' };
import zh from '../../src/shared/i18n/common/zh.json' with { type: 'json' };

for (const role of ['admin', 'steward'] as const)
  for (const { theme, width, language } of [
    { theme: 'light', width: 1440, language: 'en' },
    { theme: 'dark', width: 390, language: 'zh' },
  ] as const) {
    test(`credit conflict stays visible: ${role} ${theme} ${width}`, async ({ page }) => {
      const copy = (language === 'en' ? en : zh).management.users;
      const station = role === 'admin' ? 'admin' : 'user';
      const origin = role === 'admin' ? ADMIN_ORIGIN : USER_ORIGIN;
      const base = role === 'admin' ? '/admin/api/users' : '/api/steward/users';
      const pageErrors: string[] = [];
      page.on('pageerror', (error) => pageErrors.push(error.message));
      await page.setViewportSize({ width, height: 900 });
      await page.addInitScript(
        ({ theme, language }) => {
          localStorage.setItem('nb.theme', theme);
          localStorage.setItem('nb.lang', language);
        },
        { theme, language },
      );
      await mockPublicConfig(page, station);
      await mockRoleSession(page, station, role === 'admin' ? 'admin' : 'level6');
      await page.route('**/api/events', (route) =>
        route.fulfill({
          contentType: 'text/event-stream',
          body: ': connected\n\n',
        }),
      );
      let target = managedUserFixture();
      const writes: { body: Record<string, unknown>; key: string | undefined }[] = [];
      await page.route(`**${base}**`, async (route) => {
        const request = route.request();
        const path = new URL(request.url()).pathname;
        if (request.method() === 'PATCH' && path === base + '/7') {
          writes.push({ body: request.postDataJSON(), key: request.headers()['idempotency-key'] });
          if (writes.length === 1) {
            target = { ...target, revision: '2' };
            return route.fulfill({
              status: 409,
              json: {
                error: { code: 'conflict', message: 'Current state conflicts.' },
              },
            });
          }
          target = { ...target, balance: '1.5', revision: '3' };
          return route.fulfill({ json: target });
        }
        if (request.method() === 'GET' && path === base)
          return route.fulfill({
            json: {
              data: [target],
              next_cursor: null,
              pagination: { page: '1', page_size: 20, total_items: '1', total_pages: '1' },
            },
          });
        if (request.method() === 'GET' && path === base + '/7')
          return route.fulfill({ json: target });
        return route.fallback();
      });
      await page.goto(origin + (role === 'admin' ? '/users?user=7' : '/steward?tab=users&user=7'));
      await page.getByRole('tab', { name: copy.tabEconomy, exact: true }).click();
      await page.getByLabel(copy.economyPositiveAmount, { exact: true }).fill('1.5');
      await page.getByLabel(copy.economyReason, { exact: true }).fill('Reward correction');
      await page.getByRole('button', { name: copy.economySubmit, exact: true }).click();
      const dialog = page.getByRole('alertdialog', { name: copy.economyConfirmTitle });
      await dialog.getByRole('button', { name: copy.economySubmit, exact: true }).click();
      await expect(dialog).toBeHidden();
      const feedback = page.getByText(copy.economyConflict, { exact: true });
      await expect(feedback).toBeVisible();
      await expect(page.getByLabel(copy.economyPositiveAmount, { exact: true })).toHaveValue('1.5');
      await expect(page.getByLabel(copy.economyReason, { exact: true })).toHaveValue(
        'Reward correction',
      );
      expect(writes).toHaveLength(1);
      expect(target.balance).toBe('0');
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      );
      const screenshots = process.env.NONBIRI_VISUAL_DIR;
      if (screenshots) {
        await mkdir(screenshots, { recursive: true });
        await feedback.scrollIntoViewIfNeeded();
        await page.screenshot({ path: join(screenshots, `balance-${role}-${theme}-${width}.png`) });
      }
      await page.getByRole('button', { name: copy.economySubmit, exact: true }).click();
      await dialog.getByRole('button', { name: copy.economySubmit, exact: true }).click();
      await expect(dialog).toBeHidden();
      await expect(
        page.getByText((language === 'en' ? en : zh).common.outcome.saved, { exact: true }),
      ).toBeVisible();
      expect(writes.map((write) => write.body.expected_revision)).toEqual(['1', '2']);
      expect(writes[0].key).toBeTruthy();
      expect(writes[1].key).toBeTruthy();
      expect(writes[1].key).not.toBe(writes[0].key);
      expect(target.balance).toBe('1.5');
      expect(pageErrors).toEqual([]);
    });
  }
