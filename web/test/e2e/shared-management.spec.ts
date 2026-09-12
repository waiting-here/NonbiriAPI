import { mkdirSync } from 'node:fs';
import { join } from 'node:path';
import { expect, test } from './test';
import { ADMIN_ORIGIN, USER_ORIGIN } from './ports';
import { mockPublicConfig, mockRoleSession, userSession } from './support';
import en from '../../src/shared/i18n/common/en.json' with { type: 'json' };
import zh from '../../src/shared/i18n/common/zh.json' with { type: 'json' };

for (const scenario of [
  { role: 'admin', language: 'zh', theme: 'light', width: 1280 },
  { role: 'steward', language: 'en', theme: 'dark', width: 390 },
] as const) {
  test(`shared user and announcement management: ${scenario.role}`, async ({ page }) => {
    const station = scenario.role === 'admin' ? 'admin' : 'user';
    const origin = station === 'admin' ? ADMIN_ORIGIN : USER_ORIGIN;
    const api = station === 'admin' ? '/admin/api' : '/api/steward';
    const copy = (scenario.language === 'zh' ? zh : en).management;
    await page.setViewportSize({ width: scenario.width, height: 900 });
    await page.addInitScript(({ language, theme }) => {
      localStorage.setItem('nb.lang', language);
      localStorage.setItem('nb.theme', theme);
    }, scenario);
    await mockPublicConfig(page, station);
    await mockRoleSession(page, station, station === 'admin' ? 'admin' : 'level5');
    const fields = Object.fromEntries(
      Object.entries(userSession('user').user).filter(
        ([key]) => !['avatar', 'effective_level', 'level_display_name'].includes(key),
      ),
    );
    const target = {
      ...fields,
      id: '7',
      username: 'Managed member',
      discord_id: null,
      is_admin: false,
      banned_reason: '',
      level: { manual: null, automatic: 1, effective: 1, display_name: 'Lv1' },
      revision: '1',
      game_balance: '-2.5',
    };
    const id = 'ann_' + 'A'.repeat(22);
    const notice = {
      id,
      state: 'draft',
      revision: '1',
      draft: {
        zh: null as null | { title: string; body: string },
        en: null as null | { title: string; body: string },
      },
      published: null,
      severity: 'info',
      pinned: false,
      dismissible: true,
      expires_at: null,
      withdrawn_at: null,
      created_at: 1_800_000_000,
      updated_at: 1_800_000_000,
    };
    const writes: { path: string; body: Record<string, unknown> }[] = [];
    const pageBody = (data: unknown[]) => ({
      data,
      next_cursor: null,
      pagination: { page: '1', page_size: 20, total_items: String(data.length), total_pages: '1' },
    });
    await page.route('**/*', async (route) => {
      const request = route.request();
      const url = new URL(request.url());
      const path = url.pathname;
      if (
        url.origin !== origin ||
        ![
          api + '/users',
          api + '/users/7',
          api + '/announcements',
          api + '/announcements/' + id,
        ].includes(path)
      ) {
        await route.fallback();
        return;
      }
      if (request.method() !== 'GET') writes.push({ path, body: request.postDataJSON() });
      if (path === api + '/users') {
        await route.fulfill({ json: pageBody([target]) });
        return;
      }
      if (path === api + '/users/7') {
        if (request.method() === 'PATCH') {
          const body = request.postDataJSON();
          Object.assign(target, {
            endpoint_limit: body.endpoint_limit,
            rpm_limit: body.rpm_limit,
            concurrency_limit: body.concurrency_limit,
            revision: '2',
          });
        }
        await route.fulfill({ json: target });
        return;
      }
      if (path === api + '/announcements' && request.method() === 'POST') {
        const body = request.postDataJSON();
        notice.draft.en = { title: body.title_en, body: body.body_en };
        await route.fulfill({ status: 201, json: { id, revision: '1' } });
        return;
      }
      await route.fulfill({ json: path === api + '/announcements' ? pageBody([]) : notice });
    });
    const errors: string[] = [];
    page.on('pageerror', (error) => errors.push(error.message));
    await page.goto(origin + (station === 'admin' ? '/users?user=7' : '/steward?tab=users&user=7'));
    await page.getByLabel(copy.users.endpointLimit, { exact: true }).fill('99');
    await page.getByLabel(copy.users.rpmLimit, { exact: true }).fill('');
    await page.getByLabel(copy.users.concurrencyLimit, { exact: true }).fill('999');
    await page.getByRole('button', { name: copy.users.saveLimits, exact: true }).click();
    await expect.poll(() => writes.length).toBe(1);
    expect(writes[0].body).toMatchObject({
      endpoint_limit: '99',
      rpm_limit: null,
      concurrency_limit: '999',
      expected_revision: '1',
    });
    if (station === 'user') {
      await expect(page.getByRole('button', { name: copy.users.delete, exact: true })).toHaveCount(
        0,
      );
      await expect(
        page.getByLabel(copy.users.levelControl).locator('option[value="5"]'),
      ).toHaveCount(0);
    }
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
      true,
    );
    const screenshotDir = process.env.NONBIRI_MANAGEMENT_SCREENSHOTS;
    if (screenshotDir) {
      mkdirSync(screenshotDir, { recursive: true });
      await page.screenshot({
        path: join(screenshotDir, `users-${scenario.role}.png`),
        fullPage: true,
      });
    }
    if (station === 'user')
      await page.getByRole('tab', { name: copy.announcements.title, exact: true }).click();
    else await page.goto(origin + '/announcements');
    await page.getByRole('button', { name: copy.announcements.createDraft, exact: true }).click();
    await page
      .getByLabel(copy.announcements.englishTitle, { exact: true })
      .fill('Shared editor notice');
    await page
      .getByLabel(/^English Markdown|^英文 Markdown/)
      .fill('Managed from the shared editor.');
    await page
      .getByRole('button', { name: copy.announcements.createPrivateDraft, exact: true })
      .click();
    await expect(
      page.getByRole('heading', { name: copy.announcements.detail.editorTitle, exact: true }),
    ).toBeVisible();
    await expect(page.getByLabel(copy.announcements.englishTitle, { exact: true })).toHaveValue(
      'Shared editor notice',
    );
    expect(writes[1].path).toBe(api + '/announcements');
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
      true,
    );
    if (screenshotDir)
      await page.screenshot({
        path: join(screenshotDir, `announcements-${scenario.role}.png`),
        fullPage: true,
      });
    expect(errors).toEqual([]);
  });
}
