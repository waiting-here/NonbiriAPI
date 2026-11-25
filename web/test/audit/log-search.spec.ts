import { mkdirSync, readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { expect, test, type Browser, type Page } from '@playwright/test';
import commonEn from '../../src/shared/i18n/common/en.json' with { type: 'json' };
import commonZh from '../../src/shared/i18n/common/zh.json' with { type: 'json' };
import userZh from '../../src/user/i18n/zh.json' with { type: 'json' };

type Cookie = { Name: string; Value: string };
type Fixture = {
  admin_url: string;
  user_url: string;
  admin_cookie: Cookie;
  users: { level: number; cookie: Cookie }[];
};

test.skip(process.env.NONBIRI_MANAGEMENT_BROWSER !== '1', 'Requires the audit fixture.');

async function logSession(browser: Browser, role: 'admin' | 'steward' | 'user') {
  const fixture: Fixture = JSON.parse(
    readFileSync(process.env.NONBIRI_AUDIT_BROWSER_STATE!, 'utf8'),
  );
  const admin = role === 'admin';
  const origin = admin ? fixture.admin_url : fixture.user_url;
  const cookie = admin
    ? fixture.admin_cookie
    : fixture.users.find(({ level }) => level === (role === 'steward' ? 6 : 1))!.cookie;
  const context = await browser.newContext({
    viewport: admin ? { width: 1440, height: 900 } : { width: 390, height: 844 },
    reducedMotion: admin ? 'no-preference' : 'reduce',
  });
  await context.addCookies([
    {
      name: cookie.Name,
      value: cookie.Value,
      domain: new URL(origin).hostname,
      path: admin ? '/admin' : '/api',
      httpOnly: true,
      sameSite: 'Lax',
    },
  ]);
  await context.addInitScript(
    ({ language, theme }) => {
      localStorage.setItem('nb.lang', language);
      localStorage.setItem('nb.theme', theme);
    },
    admin ? { language: 'en', theme: 'light' } : { language: 'zh', theme: 'dark' },
  );
  return { context, origin };
}

function progress(page: Page, state: 'loading' | 'long' | 'complete' | 'canceled' | 'error') {
  return page.locator('[data-log-search-state="' + state + '"]');
}

async function screenshot(page: Page, name: string) {
  const directory = process.env.NONBIRI_VISUAL_DIR;
  if (!directory) return;
  mkdirSync(directory, { recursive: true });
  await page.locator('[data-log-search-state]').scrollIntoViewIfNeeded();
  await page.screenshot({ path: resolve(directory, 'log-search-' + name + '.png') });
}

test('request logs hand off to a real search and show the exact results', async ({ browser }) => {
  const { context, origin } = await logSession(browser, 'admin');
  const copy = commonEn.common.operations.logs.search;
  try {
    const page = await context.newPage();
    const listPath = '/admin/api/logs';
    let initialRequests = 0;
    await page.route(
      (url) => url.pathname === listPath,
      async (route) => {
        const headers = route.request().headers();
        expect(headers.prefer).toBe('respond-async');
        if (++initialRequests === 1) headers['x-fixture-slow-log-search'] = '1';
        await route.continue({ headers });
      },
    );
    const accepted = page.waitForResponse(
      (response) => new URL(response.url()).pathname === listPath && response.status() === 202,
    );
    const result = page.waitForResponse(
      (response) =>
        new URL(response.url()).pathname.startsWith('/admin/api/log-searches/') &&
        response.status() === 200,
    );
    await page.goto(origin + '/logs?page=1&page_size=20', { waitUntil: 'domcontentloaded' });
    await expect(progress(page, 'loading')).toContainText(copy.loading);
    await expect(progress(page, 'loading').locator('.log-search-feedback__icon')).toHaveCSS(
      'animation-name',
      'log-search-pulse',
    );
    await screenshot(page, 'loading-desktop-light');
    await expect(progress(page, 'long')).toContainText(copy.long);
    await expect(page.getByRole('button', { name: copy.cancel })).toBeVisible();
    await expect(progress(page, 'long').locator('.log-search-feedback__icon')).toHaveCSS(
      'animation-name',
      'log-search-scan',
    );
    await screenshot(page, 'long-desktop-light');

    const receipt = await (await accepted).json();
    const response = await result;
    const items = await response.json();
    expect(new URL(response.url()).pathname).toBe('/admin/api/log-searches/' + receipt.search_id);
    expect(receipt.status).toBe('running');
    expect(items.pagination.total_items).toMatch(/^[1-9][0-9]*$/);
    expect(items.data.length).toBeGreaterThan(0);
    await expect(progress(page, 'complete')).toContainText(copy.complete);
    await expect(progress(page, 'complete').locator('.log-search-feedback__icon')).toHaveCSS(
      'animation-name',
      'none',
    );
    await expect(page.getByRole('navigation', { name: commonEn.common.pagination })).toContainText(
      items.pagination.total_items,
    );
    await expect(page.getByRole('button', { name: copy.cancel })).toHaveCount(0);
    expect(initialRequests).toBe(1);
    await screenshot(page, 'complete-desktop-light');

    const released = page.waitForResponse(
      (candidate) =>
        new URL(candidate.url()).pathname === new URL(response.url()).pathname &&
        candidate.request().method() === 'DELETE',
    );
    const refreshed = page.waitForResponse(
      (candidate) => new URL(candidate.url()).pathname === listPath && candidate.status() === 200,
    );
    await page.getByRole('button', { name: commonEn.common.refresh, exact: true }).click();
    expect((await released).status()).toBe(204);
    await refreshed;
    await expect(progress(page, 'complete')).toContainText(copy.complete);
    expect(initialRequests).toBe(2);
  } finally {
    await context.close();
  }
});

test('a steward search crossing five seconds completes with an exact empty page', async ({
  browser,
}) => {
  const { context, origin } = await logSession(browser, 'steward');
  try {
    const page = await context.newPage();
    const listPath = '/api/steward/logs';
    await page.route(
      (url) => url.pathname === listPath,
      (route) =>
        route.continue({
          headers: { ...route.request().headers(), 'x-fixture-slow-log-search': '1' },
        }),
    );
    const accepted = page.waitForResponse(
      (response) => new URL(response.url()).pathname === listPath && response.status() === 202,
    );
    const completed = page.waitForResponse(
      (response) =>
        new URL(response.url()).pathname.startsWith('/api/steward/log-searches/') &&
        response.status() === 200,
    );
    await page.goto(origin + '/steward?tab=logs&status=599', { waitUntil: 'domcontentloaded' });
    await expect(progress(page, 'long')).toContainText(commonZh.common.operations.logs.search.long);
    const receipt = await (await accepted).json();
    const response = await completed;
    expect(new URL(response.url()).pathname).toBe('/api/steward/log-searches/' + receipt.search_id);
    const result = await response.json();
    expect(result.pagination.total_items).toBe('0');
    expect(result.data).toEqual([]);
    await expect(progress(page, 'complete')).toBeVisible();
    await expect(
      page.getByText(commonZh.common.operations.logs.stewardEmpty, { exact: true }),
    ).toBeVisible();
  } finally {
    await context.close();
  }
});

test('a late receipt is released after keyboard cancel and a failed search can be retried', async ({
  browser,
}) => {
  const { context, origin } = await logSession(browser, 'user');
  const copy = commonZh.common.operations.logs.search;
  let sendReceipt!: () => void;
  const delayedReceipt = new Promise<void>((resolve) => {
    sendReceipt = resolve;
  });
  try {
    const page = await context.newPage();
    const listPath = '/api/logs';
    const requests: string[] = [];
    await page.route(
      (url) => url.pathname.startsWith('/api/log-searches/'),
      async (route) => {
        const method = route.request().method();
        const pathname = new URL(route.request().url()).pathname;
        requests.push(method + ' ' + pathname);
        if (method === 'DELETE') {
          await route.fulfill({ status: 204 });
        } else {
          await route.fulfill({
            status: 503,
            json: { error: { code: 'service_unavailable', message: '检索暂时不可用。' } },
          });
        }
      },
    );
    let launches = 0;
    await page.route(
      (url) => url.pathname === listPath,
      async (route) => {
        expect(route.request().headers().prefer).toBe('respond-async');
        launches += 1;
        if (launches === 1) {
          await delayedReceipt;
          await route.fulfill({
            status: 202,
            json: { status: 'running', search_id: 'late-search' },
          });
        } else if (launches === 2) {
          await route.fulfill({
            status: 202,
            json: { status: 'running', search_id: 'failed-search' },
          });
        } else {
          await route.continue();
        }
      },
    );

    await page.goto(origin + '/logs?status=599', { waitUntil: 'domcontentloaded' });
    await expect(progress(page, 'loading')).toContainText(copy.loading);
    await expect(progress(page, 'loading').locator('.log-search-feedback__icon')).toHaveCSS(
      'animation-name',
      'none',
    );
    await expect(progress(page, 'long')).toContainText(copy.long);
    await expect(progress(page, 'long').locator('.log-search-feedback__icon')).toHaveCSS(
      'animation-name',
      'none',
    );
    await screenshot(page, 'long-mobile-dark');
    if (process.env.NONBIRI_VISUAL_DIR) {
      await page.setViewportSize({ width: 1440, height: 900 });
      await screenshot(page, 'long-desktop-dark');
      await page.setViewportSize({ width: 390, height: 844 });
      await page.evaluate(() => {
        document.documentElement.dataset.theme = 'light';
      });
      await screenshot(page, 'long-mobile-light');
      await page.evaluate(() => {
        document.documentElement.dataset.theme = 'dark';
      });
    }
    const cancel = page.getByRole('button', { name: copy.cancel });
    await cancel.focus();
    await page.keyboard.press('Enter');
    await expect(progress(page, 'canceled')).toContainText(copy.canceled);
    const again = page.getByRole('button', { name: copy.again });
    await expect(again).toBeFocused();
    sendReceipt();
    await expect.poll(() => requests).toEqual(['DELETE /api/log-searches/late-search']);
    await screenshot(page, 'canceled-mobile-dark');
    await expect(page).toHaveURL(/status=599/);

    await page.keyboard.press('Enter');
    await expect(progress(page, 'error').getByRole('alert')).toContainText('检索暂时不可用。');
    await screenshot(page, 'error-mobile-dark');
    await page.getByRole('button', { name: commonZh.common.retry }).click();
    await expect(progress(page, 'complete')).toContainText(copy.complete);
    await expect(page.getByText(userZh.user.logs.empty, { exact: true })).toBeVisible();
    expect(launches).toBe(3);
    expect(requests).toEqual([
      'DELETE /api/log-searches/late-search',
      'GET /api/log-searches/failed-search',
      'DELETE /api/log-searches/failed-search',
    ]);
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(
      390,
    );
    await screenshot(page, 'empty-mobile-dark');
  } finally {
    sendReceipt();
    await context.close();
  }
});
