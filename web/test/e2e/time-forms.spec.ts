import { expect, test } from './test';
import { ADMIN_ORIGIN, USER_ORIGIN } from './ports';
import { collectConsoleViolations, mockPublicConfig, mockRoleSession } from './support';

const announcementID = `ann_${'A'.repeat(22)}`;
const originalEpoch = 1793511047; // The earlier occurrence of 01:30:47 in New York.
const cases = [
  {
    zone: 'America/New_York',
    locale: 'en',
    theme: 'light',
    width: 430,
    original: '2026-11-01T01:30',
    input: '2026-11-01T01:15:00',
    instant: 1793513700,
    local: '2026-11-01T01:15:00',
    offset: -18000,
    adjustment: 'fold_later',
  },
  {
    zone: 'America/New_York',
    locale: 'en',
    theme: 'dark',
    width: 390,
    original: '2026-11-01T01:30',
    instant: 1772955000,
    local: '2026-03-08T03:30:00',
    offset: -14400,
    adjustment: 'gap_shifted',
  },
  {
    zone: 'America/New_York',
    locale: 'zh',
    theme: 'light',
    width: 320,
    original: '2026-11-01T01:30',
    instant: 1772955000,
    local: '2026-03-08T03:30:00',
    offset: -14400,
    adjustment: 'gap_shifted',
  },
  {
    zone: 'UTC',
    locale: 'en',
    theme: 'light',
    width: 1280,
    original: '2026-11-01T05:30',
    instant: 1772937000,
    local: '2026-03-08T02:30:00',
    offset: 0,
    adjustment: 'none',
  },
  {
    zone: 'Asia/Kolkata',
    locale: 'zh',
    theme: 'dark',
    width: 360,
    original: '2026-11-01T11:00',
    instant: 1772917200,
    local: '2026-03-08T02:30:00',
    offset: 19800,
    adjustment: 'none',
  },
] as const;

for (const scenario of cases) {
  const inputLocal = 'input' in scenario ? scenario.input : '2026-03-08T02:30:00';
  test.describe(`${scenario.zone} ${scenario.locale} ${scenario.theme}`, () => {
    test.use({ timezoneId: scenario.zone, viewport: { width: scenario.width, height: 1000 } });
    test.beforeEach(async ({ page }) => {
      await page.addInitScript(({ locale, theme }) => {
        localStorage.setItem('nb.lang', locale);
        localStorage.setItem('nb.theme', theme);
      }, scenario);
      await page.emulateMedia({ reducedMotion: 'reduce' });
    });

    test('administrator preserves an unchanged instant, sees clock adjustment, saves and reloads', async ({
      page,
    }) => {
      const guard = collectConsoleViolations(page);
      await mockPublicConfig(page, 'admin');
      await mockRoleSession(page, 'admin', 'admin');
      const browserZone = await page.evaluate(
        () => Intl.DateTimeFormat().resolvedOptions().timeZone,
      );
      expect(
        scenario.zone === 'Asia/Kolkata' ? ['Asia/Kolkata', 'Asia/Calcutta'] : [scenario.zone],
      ).toContain(browserZone);
      let authority = {
        id: announcementID,
        state: 'draft',
        revision: '1',
        published: null,
        draft: {
          zh: { title: '时间测试', body: '测试正文' },
          en: { title: 'Time example', body: 'Example body' },
        },
        severity: 'info',
        pinned: false,
        dismissible: true,
        expires_at: originalEpoch,
        withdrawn_at: null,
        created_at: 1770000000,
        updated_at: 1770000000,
      };
      const submitted: number[] = [];
      await page.route('**/admin/api/announcements**', async (route) => {
        const request = route.request();
        const path = new URL(request.url()).pathname;
        if (request.method() === 'GET') {
          await route.fulfill({
            json: path.endsWith(announcementID)
              ? authority
              : { data: [authority], next_cursor: null },
          });
        } else if (request.method() === 'PATCH' && path.endsWith(announcementID)) {
          const body = request.postDataJSON();
          expect(body.expected_revision).toBe(authority.revision);
          expect(Number.isSafeInteger(body.expires_at)).toBe(true);
          submitted.push(body.expires_at);
          authority = {
            ...authority,
            draft: {
              zh: { title: body.title_zh, body: body.body_zh },
              en: { title: body.title_en, body: body.body_en },
            },
            expires_at: body.expires_at,
            revision: String(Number(authority.revision) + 1),
          };
          await route.fulfill({ json: { id: announcementID, revision: authority.revision } });
        } else await route.fallback();
      });
      await page.route('**/admin/api/time/resolve?**', async (route) => {
        const params = new URL(route.request().url()).searchParams;
        expect(params.get('time_zone')).toBe(browserZone);
        if (params.get('local') !== inputLocal) {
          await route.abort();
          return;
        }
        await route.fulfill({
          json: {
            instant: scenario.instant,
            local: scenario.local,
            time_zone: browserZone,
            offset_seconds: scenario.offset,
            adjustment: scenario.adjustment,
          },
        });
      });
      await page.goto(`${ADMIN_ORIGIN}/announcements`);
      await page
        .getByRole('button', { name: scenario.locale === 'en' ? 'Edit' : '编辑', exact: true })
        .click();
      const expiry = page.getByLabel(scenario.locale === 'en' ? 'Expiry' : '到期时间', {
        exact: true,
      });
      const save = page.getByRole('button', {
        name: scenario.locale === 'en' ? 'Save private draft' : '保存私有草稿',
        exact: true,
      });
      await expect(expiry).toHaveValue(scenario.original);
      await page
        .getByLabel(scenario.locale === 'en' ? 'Chinese title' : '中文标题', { exact: true })
        .fill('时间测试更新');
      await save.click();
      await expect.poll(() => submitted).toEqual([originalEpoch]);
      await page.reload();
      await expect(expiry).toHaveValue(scenario.original);
      await expiry.fill(inputLocal.slice(0, 16));
      await expect(save).toBeDisabled();
      if (scenario.adjustment !== 'none')
        await expect(
          page.getByText(scenario.local.replace('T', ' '), { exact: false }),
        ).toBeVisible();
      await expect(save).toBeEnabled();
      await expiry.scrollIntoViewIfNeeded();
      await page.screenshot({
        path: `../tmp/time-admin-${scenario.locale}-${scenario.width}.png`,
        fullPage: true,
      });
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      );
      await save.click();
      await expect.poll(() => submitted).toEqual([originalEpoch, scenario.instant]);
      await page.reload();
      await expect(expiry).toHaveValue(scenario.local.slice(0, 16));
      guard.assertNone();
    });

    test('ordinary user applies and clears a locally resolved history filter', async ({ page }) => {
      const guard = collectConsoleViolations(page);
      await mockPublicConfig(page, 'user');
      await mockRoleSession(page, 'user', 'user');
      const browserZone = await page.evaluate(
        () => Intl.DateTimeFormat().resolvedOptions().timeZone,
      );
      expect(
        scenario.zone === 'Asia/Kolkata' ? ['Asia/Kolkata', 'Asia/Calcutta'] : [scenario.zone],
      ).toContain(browserZone);
      const queries: URLSearchParams[] = [];
      await page.route('**/api/credits/history**', async (route) => {
        const params = new URL(route.request().url()).searchParams;
        queries.push(params);
        await route.fulfill({
          json: {
            data: [],
            page: '1',
            page_size: 20,
            total: '0',
            total_pages: '1',
            anchor: null,
            current_balance: '0',
            server_now: 1800000000,
          },
        });
      });
      await page.route('**/api/time/resolve?**', async (route) => {
        const params = new URL(route.request().url()).searchParams;
        expect(params.get('local')).toBe(inputLocal);
        expect(params.get('time_zone')).toBe(browserZone);
        await route.fulfill({
          json: {
            instant: scenario.instant,
            local: scenario.local,
            time_zone: browserZone,
            offset_seconds: scenario.offset,
            adjustment: scenario.adjustment,
          },
        });
      });
      await page.goto(`${USER_ORIGIN}/credits`);
      const from = page.locator('input[type="datetime-local"]').first();
      await from.fill(inputLocal.slice(0, 16));
      const apply = page.getByRole('button', {
        name: scenario.locale === 'en' ? 'Apply filters' : '筛选',
        exact: true,
      });
      await expect(apply).toBeDisabled();
      if (scenario.adjustment !== 'none')
        await expect(
          page.getByText(scenario.local.replace('T', ' '), { exact: false }),
        ).toBeVisible();
      await expect(apply).toBeEnabled();
      await from.scrollIntoViewIfNeeded();
      await page.screenshot({
        path: `../tmp/time-user-${scenario.locale}-${scenario.width}.png`,
        fullPage: true,
      });
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      );
      await apply.click();
      await expect.poll(() => queries.at(-1)?.get('from')).toBe(String(scenario.instant));
      await page
        .getByRole('button', {
          name: scenario.locale === 'en' ? 'Clear filters' : '清除筛选',
          exact: true,
        })
        .click();
      await expect(from).toHaveValue('');
      await expect.poll(() => queries.at(-1)?.has('from')).toBe(false);
      guard.assertNone();
    });
  });
}
