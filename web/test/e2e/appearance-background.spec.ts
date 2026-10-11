import { resolve } from 'node:path';
import { expect, test } from './test';
import { collectConsoleViolations, mockPublicConfig, mockRoleSession } from './support';
import { USER_ORIGIN } from './ports';
import { loanActivity } from '../fixtures/loans';

for (const design of ['celestial', 'wandering'])
  for (const mode of ['light', 'dark'])
    for (const width of [1440, 390])
      test(`background appreciation preserves page state ${design} ${mode} ${width}`, async ({
        page,
      }) => {
        const guard = collectConsoleViolations(page);
        await page.setViewportSize({ width, height: 900 });
        await page.emulateMedia({ reducedMotion: 'reduce' });
        await page.addInitScript((value) => {
          localStorage.setItem('nb.lang', 'zh');
          localStorage.setItem('nb.theme', value);
        }, mode);
        await mockRoleSession(page, 'user', 'user');
        await mockPublicConfig(page, 'user');
        await page.route('**/api/events', (route) =>
          route.fulfill({ contentType: 'text/event-stream', body: ': connected\n\n' }),
        );
        await page.route('**/api/activities', (route) => route.fulfill({ json: loanActivity() }));
        await page.route('**/api/limited-activities', (route) => route.fulfill({ json: [] }));
        await page.route('**/api/gacha/entry', (route) =>
          route.fulfill({ json: { enabled: false, entered: false, pools: [] } }),
        );
        let selected = 'default';
        let unlocked = ['default'];
        let visible = ['default'];
        await page.route('**/api/appearance', (route) => {
          expect(route.request().method()).toBe('GET');
          return route.fulfill({
            json: { selected, unlocked, visible, updated_at: null },
          });
        });
        await page.goto(USER_ORIGIN + '/activities');
        const trigger = page.getByRole('button', { name: '欣赏动态背景', exact: true });
        await expect(page.locator('.loan-card')).toBeVisible();
        await expect(trigger).toHaveCount(0);
        const appearance = page.getByRole('button', { name: '外观', exact: true });
        if (width < 1024) await page.getByRole('button', { name: '打开导航', exact: true }).click();
        await appearance.click();
        await expect(page.locator('.nb-appearance-option')).toHaveCount(1);
        await expect(page.getByText('获得对应 SP 卡牌后解锁', { exact: true })).toHaveCount(0);
        await page.getByRole('button', { name: '关闭', exact: true }).click();
        visible = ['default', design];
        await page.reload();
        if (width < 1024) await page.getByRole('button', { name: '打开导航', exact: true }).click();
        await appearance.click();
        await expect(page.locator('.nb-appearance-option')).toHaveCount(2);
        await expect(page.getByText('获得对应 SP 卡牌后解锁', { exact: true })).toBeVisible();
        await expect(page.locator('.nb-appearance-option input:disabled')).toHaveCount(1);
        await page.getByRole('button', { name: '关闭', exact: true }).click();
        selected = design;
        unlocked = ['default', design];
        await page.reload();
        await expect(trigger).toBeVisible();
        const amount = page.locator('.loan-card select');
        await amount.selectOption('2');
        await trigger.click();
        const view = page.getByRole('dialog', { name: '欣赏动态背景', exact: true });
        await expect(view).toBeVisible();
        await expect(page.locator('.nb-user-shell')).toBeHidden();
        const back = view.getByRole('button', { name: '返回页面', exact: true });
        await expect(back).toBeFocused();
        await page.keyboard.press('Tab');
        await expect(page.locator('.nb-user-shell :focus')).toHaveCount(0);
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
          true,
        );
        if (process.env.NONBIRI_VISUAL_DIR)
          await page.screenshot({
            path: resolve(
              process.env.NONBIRI_VISUAL_DIR,
              `background-${design}-${mode}-${width}.png`,
            ),
          });
        await back.click();
        await expect(view).toHaveCount(0);
        await expect(page.locator('.nb-user-shell')).toBeVisible();
        await expect(amount).toHaveValue('2');
        await expect(trigger).toBeFocused();
        await trigger.click();
        await page.keyboard.press('Escape');
        await expect(view).toHaveCount(0);
        await expect(trigger).toBeFocused();
        if (process.env.NONBIRI_VISUAL_DIR)
          await page.screenshot({
            path: resolve(
              process.env.NONBIRI_VISUAL_DIR,
              `background-restored-${design}-${mode}-${width}.png`,
            ),
          });
        guard.assertNone();
      });
