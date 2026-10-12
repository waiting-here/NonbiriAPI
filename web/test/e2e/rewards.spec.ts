import { resolve } from 'node:path';
import { expect, test, type Page } from './test';
import { collectConsoleViolations, mockPublicConfig, mockRoleSession } from './support';
import { ADMIN_ORIGIN, USER_ORIGIN } from './ports';
import type { Campaign, Input, Receipt } from '../../src/shared/rewards/api';

const assets = [
  {
    id: 'general',
    name: '通用积分',
    name_en: 'General credits',
    group: 'credits',
    precision: 3,
    maximum: '1000000000',
  },
  {
    id: 'gacha:limited',
    name: '限定抽奖券',
    name_en: 'Limited ticket',
    group: 'gacha',
    precision: 0,
    maximum: '9007199254740991',
  },
];
async function capture(page: Page, name: string) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  if (process.env.NONBIRI_VISUAL_DIR) {
    await page.screenshot({
      path: resolve(process.env.NONBIRI_VISUAL_DIR, name + '.png'),
      fullPage: true,
    });
    await page.screenshot({
      path: resolve(process.env.NONBIRI_VISUAL_DIR, name + '-viewport.png'),
    });
  }
}
for (const theme of ['light', 'dark'] as const)
  for (const width of [1440, 390]) {
    test(`reward creation and redemption ${theme} ${width}`, async ({ page }) => {
      const guard = collectConsoleViolations(page);
      await page.setViewportSize({ width, height: 900 });
      await page.addInitScript(
        ({ theme }) => {
          localStorage.setItem('nb.lang', 'zh');
          localStorage.setItem('nb.theme', theme);
        },
        { theme },
      );
      await mockRoleSession(page, 'admin', 'admin');
      await mockPublicConfig(page, 'admin');
      const campaigns: Campaign[] = [];
      await page.route('**/admin/api/rewards**', async (route) => {
        const path = new URL(route.request().url()).pathname;
        if (path.endsWith('/assets')) return route.fulfill({ json: assets });
        if (path.endsWith('/preview'))
          return route.fulfill({ json: { matched: 2, banned: 0, at: 1800000000 } });
        if (path.endsWith('/create')) {
          const body = route.request().postDataJSON() as Input;
          expect(route.request().headers()['idempotency-key']).toBeTruthy();
          const c: Campaign = {
            ...body,
            id: 'rw_' + String(campaigns.length).padStart(22, '0'),
            code: body.kind === 'code' ? body.code || 'AUTO-GIFT' : undefined,
            created_at: 1800000000,
            revision: 1,
            claimed: 0,
            matched: 2,
            skipped: 0,
            pending: 0,
            failed: 0,
          };
          campaigns.unshift(c);
          return route.fulfill({ json: c });
        }
        if (path.endsWith('/update')) {
          const body = route.request().postDataJSON();
          expect(body).not.toHaveProperty('rewards');
          expect(body).not.toHaveProperty('conditions');
          const c = campaigns.find((c) => c.id === body.id)!;
          Object.assign(c, body, { revision: c.revision + 1 });
          return route.fulfill({ json: c });
        }
        return route.fulfill({ json: { items: campaigns, next_cursor: '' } });
      });
      await page.goto(ADMIN_ORIGIN + '/rewards');
      await page.getByLabel('奖励名称', { exact: true }).fill('秋日赠礼');
      await page.getByRole('button', { name: '添加奖励', exact: true }).click();
      await page.getByLabel('数量', { exact: true }).nth(0).fill('100.125');
      await page.locator('summary').filter({ hasText: '领取条件' }).click();
      await page.getByLabel('组合方式', { exact: true }).first().selectOption('and');
      await page.getByRole('button', { name: '添加条件', exact: true }).click();
      await page.getByLabel('组合方式', { exact: true }).nth(2).selectOption('not');
      await page.getByLabel('判断内容', { exact: true }).nth(1).selectOption('banned');
      await capture(page, `admin-reward-conditions-${theme}-${width}`);
      await page.getByRole('button', { name: '预览奖励', exact: true }).click();
      const dialog = page.getByRole('alertdialog', { name: '确认奖励' });
      await expect(dialog).toContainText('当前符合条件 2 人');
      await expect(dialog).toContainText('100.125');
      if (process.env.NONBIRI_VISUAL_DIR)
        await page.screenshot({
          path: resolve(
            process.env.NONBIRI_VISUAL_DIR,
            `admin-reward-confirm-${theme}-${width}.png`,
          ),
        });
      await dialog.getByRole('button', { name: '确认发放', exact: true }).click();
      await expect(dialog).toBeHidden();
      expect(campaigns[0].conditions.children?.[1].op).toBe('not');
      await page.getByRole('tab', { name: '兑换码', exact: true }).click();
      await page.getByLabel('奖励名称', { exact: true }).fill('兑换礼包');
      await page.getByLabel('兑换码', { exact: true }).fill('AUTUMN-GIFT');
      await page.getByLabel('可兑总人数', { exact: true }).fill('garbage');
      await expect(page.getByRole('button', { name: '预览奖励', exact: true })).toBeDisabled();
      await page.getByLabel('可兑总人数', { exact: true }).fill('10');
      await page.getByRole('button', { name: '预览奖励', exact: true }).click();
      await page
        .getByRole('alertdialog')
        .getByRole('button', { name: '创建兑换码', exact: true })
        .click();
      await expect(page.getByRole('alertdialog')).toBeHidden();
      await page.getByRole('button', { name: '调整设置', exact: true }).click();
      await page.getByRole('alertdialog').getByLabel('可兑总人数', { exact: true }).fill('12');
      await page
        .getByRole('alertdialog')
        .getByRole('button', { name: '保存', exact: true })
        .click();
      await expect(page.getByRole('alertdialog')).toBeHidden();
      expect(campaigns[0].quota).toBe(12);
      await capture(page, `admin-rewards-${theme}-${width}`);
      await mockRoleSession(page, 'user', 'user');
      await mockPublicConfig(page, 'user');
      let receipt: Receipt | undefined;
      await page.route('**/api/rewards/**', async (route) => {
        const path = new URL(route.request().url()).pathname;
        if (path.endsWith('/assets')) return route.fulfill({ json: assets });
        if (path.endsWith('/history'))
          return route.fulfill({ json: { items: receipt ? [receipt] : [], next_cursor: '' } });
        if (route.request().postDataJSON().code !== 'AUTUMN-GIFT')
          return route.fulfill({ json: { rejection: 'unavailable' } });
        if (receipt) return route.fulfill({ json: { ...receipt, already_claimed: true } });
        receipt = {
          id: 1,
          campaign_id: campaigns[0].id,
          title: '兑换礼包',
          kind: 'code',
          status: 'success',
          created_at: 1800000000,
          completed_at: 1800000000,
          results: [
            { asset: 'general', quantity: '100.125', granted: '100.125' },
            { asset: 'gacha:limited', quantity: '1', granted: '1' },
          ],
        };
        return route.fulfill({ json: { ...receipt, already_claimed: false } });
      });
      await page.goto(USER_ORIGIN + '/rewards');
      await capture(page, `user-rewards-empty-${theme}-${width}`);
      await page.getByLabel('兑换码', { exact: true }).fill('INVALID');
      await page.getByRole('button', { name: '领取奖励', exact: true }).click();
      await expect(page.locator('.nb-note')).toContainText('兑换码不可用');
      await capture(page, `user-rewards-error-${theme}-${width}`);
      await page.getByLabel('兑换码', { exact: true }).fill('AUTUMN-GIFT');
      await page.getByRole('button', { name: '领取奖励', exact: true }).click();
      await expect(page.getByText('奖励已到账', { exact: true })).toBeVisible();
      await expect(page.locator('.reward-list')).toContainText('100.125');
      await capture(page, `user-rewards-${theme}-${width}`);
      await page.reload();
      await page.getByLabel('兑换码', { exact: true }).fill('AUTUMN-GIFT');
      await page.getByRole('button', { name: '领取奖励', exact: true }).click();
      await expect(page.getByText('你已领取过此兑换码', { exact: true })).toBeVisible();
      await expect(page.getByText('奖励已到账', { exact: true })).toHaveCount(0);
      await expect(page.locator('.reward-list > article')).toHaveCount(1);
      await expect(page.locator('.reward-form').locator('..')).toContainText('奖励不会重复发放');
      await capture(page, `user-rewards-already-claimed-${theme}-${width}`);
      guard.assertNone();
    });
  }
