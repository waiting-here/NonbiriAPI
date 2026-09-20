import { mkdirSync } from 'node:fs';
import { join } from 'node:path';
import { expect, test } from './test';
import { ADMIN_ORIGIN, USER_ORIGIN } from './ports';
import {
  collectConsoleViolations,
  mockPublicConfig,
  mockRoleSession,
  userSession,
} from './support';
import {
  evidencePage,
  penalty,
  penaltyAction,
  penaltyID,
  penaltyPage,
} from '../fixtures/penalties';

for (const scenario of [
  { role: 'admin', locale: 'zh', width: 1440, theme: 'light' },
  { role: 'steward', locale: 'en', width: 390, theme: 'dark' },
] as const) {
  test(`penalty history and paginated preserved evidence: ${scenario.role}`, async ({ page }) => {
    const guard = collectConsoleViolations(page);
    const station = scenario.role === 'admin' ? 'admin' : 'user',
      origin = station === 'admin' ? ADMIN_ORIGIN : USER_ORIGIN;
    const api = station === 'admin' ? '/admin/api' : '/api/steward',
      zh = scenario.locale === 'zh';
    await page.setViewportSize({ width: scenario.width, height: 900 });
    await page.addInitScript(({ locale, theme }) => {
      localStorage.setItem('nb.lang', locale);
      localStorage.setItem('nb.theme', theme);
    }, scenario);
    await mockPublicConfig(page, station);
    await mockRoleSession(page, station, station === 'admin' ? 'admin' : 'level5');
    const fields = Object.fromEntries(
      Object.entries(userSession('user').user).filter(
        ([key]) =>
          ![
            'avatar',
            'effective_level',
            'level_display_name',
            'charity_profile_public',
            'automatic_restrictions',
          ].includes(key),
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
      game_balance: '0',
    };
    const queries: string[] = [];
    await page.route(`**${api}/users**`, async (route) => {
      const url = new URL(route.request().url());
      expect(route.request().method()).toBe('GET');
      if (url.pathname === `${api}/users`)
        return route.fulfill({ json: { ...penaltyPage([target]), next_cursor: null } });
      if (url.pathname === `${api}/users/7`) return route.fulfill({ json: target });
      queries.push(url.pathname + url.search);
      const size = Number(url.searchParams.get('page_size')),
        requested = url.searchParams.get('page')!;
      if (url.pathname.endsWith('/evidence'))
        return route.fulfill({ json: evidencePage(requested, size) });
      if (url.pathname.endsWith(penaltyID))
        return route.fulfill({
          json: { case: penalty, actions: penaltyPage([penaltyAction], size) },
        });
      if (url.pathname.endsWith('/penalties'))
        return route.fulfill({
          json: { ...penaltyPage([penalty], size), legacy_details_unavailable: true },
        });
      return route.fallback();
    });
    await page.goto(origin + (station === 'admin' ? '/users?user=7' : '/steward?tab=users&user=7'));
    const opener = page.getByRole('button', {
      name: zh ? '自动处罚记录' : 'Automatic penalties',
      exact: true,
    });
    await opener.click();
    const dialog = page.getByRole('dialog', {
      name: zh ? '自动处罚记录' : 'Automatic penalties',
      exact: true,
    });
    await expect(dialog.getByRole('status')).toContainText(
      zh ? '没有保存统计依据' : 'no saved statistical evidence',
    );
    await dialog.getByLabel(zh ? '处罚类型' : 'Penalty type').selectOption('ban');
    await expect.poll(() => queries.some((q) => q.includes('type=ban'))).toBe(true);
    await dialog.getByRole('button', { name: zh ? '查看处理记录' : 'View actions' }).click();
    await expect(
      dialog.getByText(zh ? '关联请求日志已过期' : 'Related request log has expired'),
    ).toBeVisible();
    await expect(
      dialog.getByRole('link', { name: zh ? '查看关联请求' : 'View related request' }),
    ).toHaveCount(0);
    await dialog
      .getByRole('button', { name: zh ? '查看当时依据 (21)' : 'View evidence (21)' })
      .click();
    await expect(dialog.locator('.loan-record')).toHaveCount(20);
    await dialog.getByRole('button', { name: zh ? '下一页' : 'Next', exact: true }).click();
    await expect(dialog.locator('.loan-record')).toHaveCount(1);
    await expect
      .poll(() => queries.some((q) => q.includes('/evidence?page=2&page_size=20')))
      .toBe(true);
    await dialog
      .getByText(zh ? '查看当时完整规则' : 'View all rules at the time', { exact: true })
      .click();
    await expect(
      dialog.getByText(zh ? '频率违规封禁阈值（次）' : 'Rate ban threshold (requests)'),
    ).toBeVisible();
    expect(await dialog.evaluate((el) => el.scrollWidth <= el.clientWidth + 1)).toBe(true);
    const folder = process.env.NONBIRI_PENALTY_SCREENSHOTS;
    if (folder) {
      mkdirSync(folder, { recursive: true });
      await page.screenshot({ path: join(folder, `penalty-${scenario.role}.png`) });
    }
    await page.keyboard.press('Escape');
    await expect(dialog).toHaveCount(0);
    await expect(opener).toBeFocused();
    guard.assertNone();
  });
}

for (const role of ['user', 'admin', 'steward'] as const) {
  test(`pre-handler filters and export preserve ${role} projection`, async ({ page }) => {
    const guard = collectConsoleViolations(page),
      station = role === 'admin' ? 'admin' : 'user';
    const origin = role === 'admin' ? ADMIN_ORIGIN : USER_ORIGIN,
      path =
        role === 'admin'
          ? '/admin/api/logs'
          : role === 'steward'
            ? '/api/steward/logs'
            : '/api/logs';
    await mockPublicConfig(page, station);
    await mockRoleSession(
      page,
      station,
      role === 'admin' ? 'admin' : role === 'steward' ? 'level5' : 'user',
    );
    const base = {
      id: `req_${'A'.repeat(22)}`,
      route_kind: 'charity_chat_completions',
      phase: 'pre_handler',
      rejection_stage: 'flow',
      rejection_reason: 'user_rpm',
      request_method: 'POST',
      request_path: '/v1/chat/completions',
      caller_result_class: 'failed',
      caller_status: 429,
      caller_error_code: 'rate_limited',
      started_at: 1800000000,
      completed_at: 1800000000,
      usage: {
        uncached_input_tokens: '0',
        cache_write_input_tokens: '0',
        cache_read_input_tokens: '0',
        output_tokens: '0',
        total_tokens: '0',
        usage_unknown: false,
        charge: '0',
      },
    };
    const row =
      role === 'user'
        ? { ...base, model: '[公益]p/m' }
        : { ...base, user_id: '7', caller_identity: null, attempt_count: '0' };
    const filters: string[] = [];
    await page.route(`**${path}**`, (route) => {
      const url = new URL(route.request().url());
      filters.push(url.search);
      if (url.pathname === path)
        return route.fulfill({
          json: {
            ...penaltyPage([row], Number(url.searchParams.get('page_size'))),
            next_cursor: null,
          },
        });
      const detail =
        role === 'user'
          ? { request: row, caller_safe_result: { class: 'failed' } }
          : {
              request: row,
              attempts: { data: [], next_cursor: null },
              attempt_pagination: penaltyPage([], 20).pagination,
            };
      return route.fulfill({ json: detail });
    });
    await page.goto(origin + (role === 'steward' ? '/steward' : '/logs'));
    await page
      .getByRole('combobox', { name: 'Request stage', exact: true })
      .selectOption('pre_handler');
    await page.getByRole('button', { name: 'Apply filter', exact: true }).click();
    await expect.poll(() => filters.some((q) => q.includes('phase=pre_handler'))).toBe(true);
    await expect(page.getByRole('link', { name: 'Export JSON' })).toHaveAttribute(
      'href',
      path + '/export.json?phase=pre_handler',
    );
    await page.getByRole('button', { name: 'Details', exact: true }).click();
    const dialog = page.getByRole('dialog');
    await expect(dialog.getByText('Rejected before a call', { exact: true })).toBeVisible();
    await expect(dialog.getByText('POST /v1/chat/completions', { exact: true })).toBeVisible();
    guard.assertNone();
  });
}

test('verified banned login shows only safe fields and removes the fragment', async ({ page }) => {
  const guard = collectConsoleViolations(page);
  await mockPublicConfig(page, 'user');
  const start = Math.floor(Date.now() / 1000);
  const restrictions = [
    {
      kind: 'ban',
      reason_code: 'charity_rpm',
      reason: 'Account access restricted.',
      started_at: start,
      ends_at: start + 600,
    },
  ];
  const fragment = Buffer.from(JSON.stringify(restrictions)).toString('base64url');
  const authenticated: string[] = [];
  page.on('request', (r) => {
    if (/\/api\/(auth|session|me)/.test(new URL(r.url()).pathname)) authenticated.push(r.url());
  });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(`${USER_ORIGIN}/access-denied#restrictions=${fragment}`);
  await expect(page.getByRole('heading', { name: 'Current automatic restrictions' })).toBeVisible();
  await expect(
    page.getByText('Repeated charity requests exceeded the rate limit.', { exact: true }),
  ).toBeVisible();
  await expect(page).toHaveURL(`${USER_ORIGIN}/access-denied`);
  expect(authenticated).toEqual([]);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  guard.assertNone();
});
