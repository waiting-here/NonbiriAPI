import { readFileSync } from 'node:fs';
import { mkdir } from 'node:fs/promises';
import { resolve } from 'node:path';
import { expect, test } from './test';
import { mockPublicConfig, mockRoleSession } from './support';
import { ADMIN_ORIGIN, USER_ORIGIN } from './ports';

const dtos = JSON.parse(
  readFileSync(resolve(process.cwd(), '../internal/logapi/testdata/role_dtos.golden'), 'utf8'),
);
const model = {
  recurring_discount: { enabled: false, time_zone: 'UTC', default_percent: 100, periods: [] },
  effective_discount: { source: 'none', percent: 100, next_change_at: null },
  server_now: 1800000000,

  model_types: ['chat_completions'],
  route_strategy: 'expiry_weighted',
  id: '31',
  provider: 'Provider',
  model: 'model',
  full_name: '[公益]Provider/model',
  enabled: true,
  allowed_levels: [1, 2, 3, 4, 5],
  public_description: '',
  pricing: { mode: 'per_request', user_price: '1', donor_reward: '0' },
  discount: { enabled: false, percent: 0, start_at: null, end_at: null },
  transport_rule: 'passthrough',
  cloudflare_proxy_enabled: false,
  cloudflare_proxy_mode: 'direct',
  flatten_tool_calls: false,
  revision: '1',
  binding_revision: '0',
  binding_count: '0',
  rolling_success: { sample_count: '0', success_count: '0', percent: null },
  created_at: 1,
  updated_at: 1,
};

for (const role of ['admin', 'level6', 'level5'] as const) {
  test(`${role} log shortcuts keep their targets and return window through missing and inaccessible resources`, async ({
    page,
  }) => {
    const station = role === 'admin' ? 'admin' : 'user';
    const output = process.env.NONBIRI_VISUAL_DIR ?? '../.pi/work/log-shortcuts/screenshots';
    await page.setViewportSize({ width: 1440, height: 900 });
    await mockRoleSession(page, station, role);
    await mockPublicConfig(page, station);
    const detail = structuredClone(dtos.admin);
    Object.assign(detail.request, {
      route_kind: 'charity_chat_completions',
      charity_model: 'Historical charity model name',
      charity_model_id: '31',
    });
    detail.attempts.data.forEach((attempt: Record<string, unknown>, index: number) =>
      Object.assign(attempt, {
        donation_id: String(index + 7),
        donation_key_id: String(index + 11),
      }),
    );
    const root = role === 'admin' ? '/admin/api/logs' : '/api/steward/logs';
    await page.route(`**${root}**`, async (route) => {
      const url = new URL(route.request().url());
      if (url.pathname === root)
        return route.fulfill({
          json: {
            data: [detail.request],
            next_cursor: null,
            pagination: { page: '2', page_size: 20, total_items: '21', total_pages: '2' },
          },
        });
      if (url.pathname === `${root}/${detail.request.id}`)
        return route.fulfill({
          json: {
            ...detail,
            attempt_pagination: {
              page: '1',
              page_size: 20,
              total_items: String(detail.attempts.data.length),
              total_pages: '1',
            },
          },
        });
      return route.fallback();
    });
    let missingModel = true;
    await page.route('**/charity-models/31', async (route) =>
      missingModel
        ? route.fulfill({
            status: 404,
            json: { error: { code: 'not_found', message: 'Model unavailable.' } },
          })
        : route.fulfill({ json: model }),
    );
    await page.route('**/donations/**', async (route) =>
      route.fulfill({
        status: 403,
        json: { error: { code: 'forbidden', message: 'Key unavailable.' } },
      }),
    );
    const origin = role === 'admin' ? ADMIN_ORIGIN : USER_ORIGIN;
    const start =
      origin +
      (role === 'admin' ? '/logs?' : '/steward?tab=logs&') +
      `charity_model=historical&page=2&page_size=20&from=&to=&request_id=${detail.request.id}&attempt_page=1&attempt_page_size=20`;
    await page.goto(start);
    const panel = page.locator('.inspection-dock, .nb-sheet:has(.log-detail-content)');
    await expect(
      panel.getByRole('link', { name: 'Manage charity model', exact: true }),
    ).toBeVisible();
    const original = page.url();
    await panel.getByRole('link', { name: 'Manage charity model', exact: true }).click();
    await expect(page.getByRole('button', { name: 'Retry', exact: true })).toBeVisible();
    if (role === 'admin') {
      await mkdir(output, { recursive: true });
      await page.screenshot({ path: resolve(output, 'log-model-unavailable-light-1440.png') });
    }
    await page.getByRole('link', { name: 'Return to request logs', exact: true }).click();
    await expect(page).toHaveURL(original);
    const attemptsHeading = panel.locator('summary').filter({ hasText: 'Service call attempts' });
    if ((await attemptsHeading.locator('..').getAttribute('open')) === null)
      await attemptsHeading.click();
    const links = panel.getByRole('link', { name: 'Manage charity key', exact: true });
    await expect(links).toHaveCount(detail.attempts.data.length);
    if (role === 'admin') {
      await mkdir(output, { recursive: true });
      for (const theme of ['light', 'dark']) {
        await page.evaluate((value) => {
          document.documentElement.dataset.theme = value;
        }, theme);
        for (const width of [1440, 390]) {
          await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
          await expect(
            page.locator(width === 1440 ? '.inspection-dock' : '.nb-sheet .log-detail-content'),
          ).toBeVisible();
          if ((await attemptsHeading.locator('..').getAttribute('open')) === null)
            await attemptsHeading.click();
          await expect(links.first()).toBeVisible();
          await links.first().scrollIntoViewIfNeeded();
          expect(
            await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth),
          ).toBe(true);
          const overflowing = await panel.evaluate((element) => {
            if (element.scrollWidth <= element.clientWidth + 1) return [];
            const edge = element.getBoundingClientRect().left + element.clientWidth;
            const nodes = [...element.querySelectorAll<HTMLElement>('*')]
              .filter((child) => child.getBoundingClientRect().right > edge + 1)
              .map((child) => [
                child.tagName,
                child.className,
                Math.round(child.getBoundingClientRect().width),
              ])
              .slice(0, 16);
            return [element.scrollWidth - element.clientWidth, ...nodes];
          });
          expect(overflowing, `detail layout at ${width}px`).toEqual([]);
          await page.screenshot({
            path: resolve(output, `log-shortcuts-${theme}-${width}.png`),
            animations: 'disabled',
          });
        }
      }
    }
    missingModel = false;
    for (let index = 0; index < detail.attempts.data.length; index++) {
      const toggle = panel.locator('summary').filter({ hasText: 'Service call attempts' });
      if ((await toggle.locator('..').getAttribute('open')) === null) await toggle.click();
      const link = panel.getByRole('link', { name: 'Manage charity key', exact: true }).nth(index);
      const target = new URL((await link.getAttribute('href'))!, origin);
      expect(target.searchParams.get('charity_model')).toBe('31');
      expect(target.searchParams.get('managed_donation')).toBe(String(index + 7));
      expect(target.searchParams.get('managed_key')).toBe(String(index + 11));
      const carried = new URLSearchParams(target.searchParams.get('log_context') ?? '');
      expect(carried.get('request_id')).toBe(detail.request.id);
      expect(carried.has('charity_model')).toBe(false);
      await link.click();
      await expect(
        page.getByRole('heading', { name: `Manage donated key #${index + 11}`, exact: true }),
      ).toBeVisible();
      await expect(page.getByRole('button', { name: 'Retry', exact: true })).toBeVisible();
      if (role === 'admin' && index === 0)
        await page.screenshot({ path: resolve(output, 'log-key-inaccessible-dark-390.png') });
      await page.getByRole('button', { name: 'Return to request logs', exact: true }).click();
      await expect(page).toHaveURL(original);
    }
  });
}
