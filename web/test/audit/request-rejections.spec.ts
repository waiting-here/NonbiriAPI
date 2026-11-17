import { mkdirSync, readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { expect, test } from '@playwright/test';

type Cookie = { Name: string; Value: string };
interface Fixture {
  admin_url: string;
  user_url: string;
  admin_cookie: Cookie;
  users: { id: string; level: number; cookie: Cookie }[];
  request_rejections: {
    precise_id: string;
    historical_id: string;
    unbound_id: string;
    private_markers: string[];
  };
}

test.skip(
  process.env.NONBIRI_REQUEST_REJECTIONS_BROWSER !== '1',
  'Requires the opt-in real request rejection fixture.',
);

test('real rejection details show safe top-level positions and explain historical missing fields', async ({
  browser,
}) => {
  test.setTimeout(180_000);
  const statePath = process.env.NONBIRI_AUDIT_BROWSER_STATE!;
  const fixture = JSON.parse(readFileSync(statePath, 'utf8')) as Fixture;
  const samples = fixture.request_rejections;
  const visual =
    process.env.NONBIRI_VISUAL_DIR ?? resolve(dirname(statePath), 'request-rejections-preview');
  mkdirSync(visual, { recursive: true });
  for (const role of ['admin', 'steward', 'user'] as const) {
    const origin = role === 'admin' ? fixture.admin_url : fixture.user_url;
    const cookie =
      role === 'admin'
        ? fixture.admin_cookie
        : role === 'steward'
          ? fixture.users.find((user) => user.level === 5)!.cookie
          : fixture.users[0].cookie;
    const api =
      role === 'admin'
        ? '/admin/api/logs/'
        : role === 'steward'
          ? '/api/steward/logs/'
          : '/api/logs/';
    for (const width of [1440, 390]) {
      for (const theme of ['light', 'dark'] as const) {
        const context = await browser.newContext({
          viewport: { width, height: width === 390 ? 844 : 1000 },
          reducedMotion: 'reduce',
          extraHTTPHeaders: { 'Accept-Language': 'zh-CN' },
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
          await context.addInitScript((theme) => {
            localStorage.setItem('nb.lang', 'zh');
            localStorage.setItem('nb.theme', theme);
          }, theme);
          const page = await context.newPage();
          const route = role === 'steward' ? '/steward?tab=logs&request_id=' : '/logs?request_id=';
          const withRoute = await context.request.get(
            origin + api + samples.unbound_id + '?attempt_page=1&attempt_page_size=20',
            { headers: { Origin: origin } },
          );
          expect(withRoute.status()).toBe(200);
          const unbound = await withRoute.json();
          expect(unbound.request.caller_error_code).toBe('unbound_model');
          if (role === 'user') {
            expect(unbound).not.toHaveProperty('route_decision');
            expect(unbound).not.toHaveProperty('attempts');
          } else {
            expect(unbound.route_decision.observations).toContainEqual({ reason: 'no_bindings' });
            expect(unbound.attempts.data).toHaveLength(0);
          }
          const listRoute =
            role === 'steward' ? '/steward?tab=logs&status=400' : '/logs?status=400';
          await page.goto(origin + listRoute);
          await page.getByRole('tab', { name: '按请求 ID 查找' }).click();
          const lookup = page.getByLabel('请求 ID', { exact: true });
          const submit = page.getByRole('button', { name: '打开详情', exact: true });
          if (width === 1440 && theme === 'light') {
            await lookup.fill('req_' + 'A'.repeat(21) + 'Q');
            await submit.click();
            const missing = page.locator('.nb-expandable-panel:not([hidden])');
            await expect(missing.getByRole('status')).toContainText(
              role === 'user' ? '已过期或不属于当前账号' : '没有找到这条记录',
            );
            await missing.getByRole('button', { name: '关闭' }).click();
            expect(new URL(page.url()).searchParams.get('status')).toBe('400');
            expect(new URL(page.url()).searchParams.has('request_id')).toBe(false);
          }
          await lookup.fill(samples.unbound_id);
          await submit.click();
          await expect(page).toHaveURL(
            (url) =>
              url.searchParams.get('request_id') === samples.unbound_id &&
              url.searchParams.get('status') === '400',
          );
          const routing = page.getByRole('region', { name: '本次路由选择' });
          if (role !== 'user') {
            await expect(routing.getByText('当时没有已配置的绑定。')).toBeVisible();
            await routing.scrollIntoViewIfNeeded();
            await page.screenshot({
              path: resolve(visual, `routing-${role}-${width}-${theme}.png`),
            });
          } else {
            await expect(page.locator('.nb-expandable-panel:not([hidden])')).toBeVisible();
            await expect(routing).toHaveCount(0);
          }
          for (const sample of ['precise', 'historical'] as const) {
            const id = sample === 'precise' ? samples.precise_id : samples.historical_id;
            const response = await context.request.get(
              origin + api + id + '?attempt_page=1&attempt_page_size=20',
              { headers: { Origin: origin } },
            );
            expect(response.status()).toBe(200);
            const detail = await response.json();
            expect(detail.request.phase).toBe('pre_handler');
            expect(detail.request.rejection_detail.field).toBe(
              sample === 'precise' ? 'request.fields[3]' : 'request',
            );
            if (role !== 'user') expect(detail.request.attempt_count).toBe('0');
            for (const marker of samples.private_markers)
              expect(JSON.stringify(detail)).not.toContain(marker);
            expect(detail).not.toHaveProperty('route_decision');
            await page.goto(origin + route + id);
            const panel = page.locator('.nb-expandable-panel:not([hidden])');
            await expect(panel).toBeVisible();
            const label =
              sample === 'precise'
                ? '第 3 个顶层字段: 不支持此顶层字段'
                : 'request: 不支持此顶层字段';
            const reason = panel.getByText(label, { exact: true });
            await expect(reason).toBeVisible();
            await reason.scrollIntoViewIfNeeded();
            if (sample === 'precise') {
              await expect(
                panel.getByText(
                  '为保护隐私，未保存自定义字段名。请检查应用参数覆盖后的请求中对应的顶层字段，序号从 1 开始。',
                  { exact: true },
                ),
              ).toBeVisible();
            } else {
              await expect(
                panel.getByText('此条日志未记录不支持的字段名。重新发起请求后可记录字段位置。', {
                  exact: true,
                }),
              ).toBeVisible();
              await expect(panel).not.toContainText('第 3 个顶层字段');
            }
            for (const marker of samples.private_markers)
              expect(await page.locator('body').innerText()).not.toContain(marker);
            expect(
              await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1),
            ).toBe(true);
            await page.screenshot({
              path: resolve(visual, `request-rejection-${role}-${sample}-${width}-${theme}.png`),
            });
          }
        } finally {
          await context.close();
        }
      }
    }
  }
});
