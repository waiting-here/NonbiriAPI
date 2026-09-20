import { resolve } from 'node:path';
import { ADMIN_ORIGIN } from './ports';
import { collectConsoleViolations, mockJson, mockPublicConfig, mockRoleSession } from './support';
import { expect, test } from './test';

test.use({ timezoneId: 'America/Los_Angeles' });

for (const scenario of [
  { locale: 'en', theme: 'light', width: 1_280, height: 900 },
  { locale: 'zh', theme: 'dark', width: 390, height: 844 },
] as const) {
  test(`Thursday creation and editing use Beijing time in ${scenario.locale} ${scenario.theme}`, async ({
    page,
  }) => {
    const consoleGuard = collectConsoleViolations(page);
    await page.setViewportSize({ width: scenario.width, height: scenario.height });
    await page.emulateMedia({ reducedMotion: 'reduce' });
    // It is already Thursday in Beijing while still Wednesday in Los Angeles.
    await page.clock.setFixedTime(new Date('2026-12-30T16:00:00Z'));
    await page.addInitScript(({ locale, theme }) => {
      localStorage.setItem('nb.lang', locale);
      localStorage.setItem('nb.theme', theme);
    }, scenario);
    await mockPublicConfig(page, 'admin');
    await mockRoleSession(page, 'admin', 'admin');
    await mockJson(page, {
      origin: ADMIN_ORIGIN,
      method: 'GET',
      path: '/admin/api/activities/config',
      body: {
        revision: '5',
        master_enabled: false,
        loan_enabled: false, loan_tiers: ['10000', '100000', '1000000'], loan_a: '0.9', loan_b: '1.3',
        welfare: { enabled: false, threshold: '1', cap: '2' },
        thursday: { enabled: false },
      },
    });
    await mockJson(page, {
      origin: ADMIN_ORIGIN,
      method: 'GET',
      path: '/admin/api/pools?page=1&page_size=20',
      body: {
        data: [],
        next_cursor: null,
        pagination: { page: '1', page_size: 20, total_items: '0', total_pages: '1' },
      },
    });
    let period: Record<string, unknown> | null = null;
    const writes: Record<string, unknown>[] = [];
    await page.route(`${ADMIN_ORIGIN}/admin/api/activities/thursday`, (route) =>
      route.fulfill({ json: { period } }),
    );
    await page.route(`${ADMIN_ORIGIN}/admin/api/activities/thursday/next`, async (route) => {
      expect(route.request().method()).toBe('PUT');
      expect(route.request().headers()['idempotency-key']).toBeTruthy();
      const body = route.request().postDataJSON() as Record<string, unknown>;
      writes.push(body);
      period = {
        id: `thu_${'A'.repeat(22)}`,
        period_key: body.period_key,
        opens_at: body.opens_at,
        closes_at: Number(body.opens_at) + 86_400,
        state: 'configured',
        revision: String(writes.length),
        literature: body.literature,
        entry: body.entry,
        per_user_limit: body.per_user_limit,
        pumps_bp: body.pumps_bp,
        current_pool_id: `pol_${'A'.repeat(22)}`,
        next_pool_id: `pol_${'B'.repeat(21)}A`,
        settlement: null,
        created_at: 1_798_646_400,
        terminal_at: null,
      };
      await route.fulfill({ json: period });
    });

    const zh = scenario.locale === 'zh';
    const title = zh ? '创建下一活动周期' : 'Create next period';
    const updateTitle = zh ? '更新已配置的下一周期' : 'Update configured next period';
    const entryLabel = zh ? '参与金额（积分）' : 'Entry (credits)';
    const literatureLabel = zh ? '活动文案' : 'Literature';
    const saveLabel = zh ? '保存下一活动周期' : 'Save next period';
    const form = page
      .locator('.card')
      .filter({ has: page.getByRole('heading', { name: title, exact: true }) });
    await page.goto(`${ADMIN_ORIGIN}/activities`);
    await expect(page.getByRole('heading', { name: title, exact: true })).toBeVisible();
    await expect(form.getByText('2027-01-07 00:00 — 2027-01-08 00:00')).toBeVisible();
    await expect(page.getByLabel(zh ? '周期键' : 'Period key', { exact: true })).toHaveCount(0);
    await expect(page.locator('input[type="datetime-local"]')).toHaveCount(0);
    if (process.env.NONBIRI_VISUAL_DIR) {
      await page.screenshot({
        path: resolve(
          process.env.NONBIRI_VISUAL_DIR,
          `thursday-${scenario.locale}-${scenario.width}.png`,
        ),
        fullPage: true,
      });
    }
    await page.getByLabel(entryLabel, { exact: true }).fill('1');
    await page
      .getByRole('textbox', { name: literatureLabel, exact: true })
      .fill('Thursday announcement');
    await page.getByRole('button', { name: saveLabel, exact: true }).click();
    await expect(page.getByRole('heading', { name: updateTitle, exact: true })).toBeVisible();
    expect(writes).toHaveLength(1);
    expect(writes[0]).toMatchObject({
      expected_revision: '5',
      period_key: '2027-01-07',
      opens_at: Date.parse('2027-01-07T00:00:00+08:00') / 1_000,
      entry: '1',
      literature: 'Thursday announcement',
    });

    await page.reload();
    await expect(page.getByRole('textbox', { name: literatureLabel, exact: true })).toHaveValue(
      'Thursday announcement',
    );
    await page
      .getByRole('textbox', { name: literatureLabel, exact: true })
      .fill('Updated announcement');
    await page.getByRole('button', { name: saveLabel, exact: true }).click();
    await expect.poll(() => writes.length).toBe(2);
    expect(writes[1]).toMatchObject({
      expected_revision: '1',
      period_key: '2027-01-07',
      opens_at: writes[0].opens_at,
      literature: 'Updated announcement',
    });
    await expect(page.getByRole('button', { name: saveLabel, exact: true })).toBeEnabled();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
      true,
    );
    consoleGuard.assertNone();
  });
}
