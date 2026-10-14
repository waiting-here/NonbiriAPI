import { expect, test } from './test';
import { USER_ORIGIN } from './ports';
import { collectConsoleViolations, mockPublicConfig, mockRoleSession } from './support';
import type { Rule, RuleInput } from '../../src/shared/riskAudit/api';

for (const width of [1440, 390]) {
  for (const theme of ['light', 'dark'] as const) {
    test('steward manages automatic bans at ' + width + 'px in ' + theme, async ({ page }) => {
      const errors = collectConsoleViolations(page);
      await page.setViewportSize({ width, height: 1000 });
      await mockRoleSession(page, 'user', 'level6');
      await mockPublicConfig(page, 'user');
      await page.addInitScript((mode) => localStorage.setItem('nb.theme', mode), theme);
      let saved: Rule | undefined;
      await page.route('**/api/steward/abuse-audit/**', async (route) => {
        const request = route.request();
        if (!new URL(request.url()).pathname.includes('/client-rules')) {
          await route.fulfill({ json: { items: [] } });
          return;
        }
        if (request.method() === 'DELETE') {
          saved = undefined;
          await route.fulfill({ json: { deleted: true } });
          return;
        }
        if (['POST', 'PATCH'].includes(request.method())) {
          const input = request.postDataJSON() as RuleInput;
          saved = {
            ...input,
            auto_ban: input.auto_ban ?? null,
            id: 'rsk_AAAAAAAAAAAAAAAAAAAAAA',
            revision: (saved?.revision ?? 0) + 1,
            created_at: 1800000000,
            updated_at: 1800000000,
            created_by_role: 'level6',
            updated_by_role: 'level6',
            created_by_user_id: '6',
            updated_by_user_id: '6',
          };
          await route.fulfill({ json: saved });
          return;
        }
        await route.fulfill({
          json: {
            items: saved ? [saved] : [],
            page: '1',
            page_size: 20,
            total_items: saved ? '1' : '0',
            total_pages: '1',
            revision: 'rules-' + (saved?.revision ?? 0),
            changed: false,
          },
        });
      });
      await page.goto(USER_ORIGIN + '/steward?tab=risk&audit_tab=rules');
      await page.getByRole('button', { name: 'New rule', exact: true }).click();
      await page.getByRole('button', { name: 'Tavo', exact: true }).click();
      await page.getByRole('combobox', { name: 'Ban duration', exact: true }).selectOption('hours');
      await page.getByRole('spinbutton', { name: 'Ban duration (Hours)' }).fill('24');
      await page.getByRole('checkbox', { name: 'Automatic ban enabled' }).check();
      await expect
        .poll(() => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth))
        .toBe(true);
      if (process.env.NONBIRI_VISUAL_DIR) {
        await page.screenshot({
          path: process.env.NONBIRI_VISUAL_DIR + '/rule-' + width + '-' + theme + '.png',
          fullPage: true,
        });
      }
      await page.getByRole('button', { name: 'Save', exact: true }).click();
      const confirmation = page.getByRole('alertdialog');
      await expect(confirmation).toContainText('24 Hours');
      expect(saved).toBeUndefined();
      if (process.env.NONBIRI_VISUAL_DIR) {
        await page.screenshot({
          path: process.env.NONBIRI_VISUAL_DIR + '/confirm-' + width + '-' + theme + '.png',
        });
      }
      await confirmation.getByRole('button', { name: 'Save', exact: true }).click();
      await expect(page.getByRole('heading', { name: 'Tavo', exact: true })).toBeVisible();
      expect(saved?.auto_ban).toEqual({ enabled: true, duration_seconds: 86400 });
      await page.getByRole('button', { name: 'Edit', exact: true }).click();
      await page.getByRole('checkbox', { name: 'Automatic ban enabled' }).uncheck();
      await page.getByRole('button', { name: 'Save', exact: true }).click();
      await expect(page.getByRole('button', { name: 'Edit', exact: true })).toBeVisible();
      expect(saved?.auto_ban).toEqual({ enabled: false, duration_seconds: 86400 });
      await page.getByRole('button', { name: 'Edit', exact: true }).click();
      await page.getByRole('combobox', { name: 'Ban duration', exact: true }).selectOption('none');
      await page.getByRole('button', { name: 'Save', exact: true }).click();
      await expect(page.getByRole('button', { name: 'Delete', exact: true })).toBeVisible();
      expect(saved?.auto_ban).toBeNull();
      await page.getByRole('button', { name: 'Delete', exact: true }).click();
      await expect(page.getByRole('heading', { name: 'Tavo', exact: true })).toHaveCount(0);
      errors.assertNone();
    });
  }
}
