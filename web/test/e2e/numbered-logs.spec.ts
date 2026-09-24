import { resolve } from 'node:path';
import { expect, test, type Page } from './test';
import { ADMIN_ORIGIN, USER_ORIGIN } from './ports';
import {
  collectConsoleViolations,
  mockJson,
  mockPublicConfig,
  mockRoleSession,
  userSession,
} from './support';

type Role = 'admin' | 'user' | 'steward';
const usage = {
  uncached_input_tokens: '0',
  cache_write_input_tokens: '0',
  cache_read_input_tokens: '0',
  output_tokens: '1',
  total_tokens: '1',
  usage_unknown: false,
  charge: '0',
};
const requestID = (index: number) => `req_${String(index).padStart(21, '0')}Q`;
const copy = {
  en: {
    details: 'Details',
    close: 'Close',
    size: 'Items per page',
    jump: 'Go to page',
    go: 'Go',
    previous: 'Previous',
  },
  zh: {
    details: '详情',
    close: '关闭',
    size: '每页条数',
    jump: '跳转页码',
    go: '跳转',
    previous: '上一页',
  },
};

function row(role: Role, index: number, charity = false) {
  const common = {
    id: requestID(index),
    route_kind:
      role === 'steward' || charity
        ? index % 2
          ? 'charity_embeddings'
          : 'charity_chat_completions'
        : index % 2
          ? 'openai_embeddings'
          : 'openai_chat_completions',
    phase: 'handler' as const,
    rejection_stage: null,
    rejection_reason: null,
    request_method: null,
    request_path: null,
    caller_result_class: 'success',
    caller_status: 200,
    caller_error_code: null,
    started_at: 1_800_000_000,
    completed_at: 1_800_000_001,
    usage:
      index % 2
        ? { ...usage, uncached_input_tokens: '1', output_tokens: '0', usage_unknown: index === 21 }
        : usage,
  };
  if (role === 'admin')
    return { ...common, user_id: '7', caller_identity: null, attempt_count: '23' };
  if (role === 'steward')
    return {
      ...common,
      user_id: '7',
      caller_identity: { discord_nickname: 'Shared caller', discord_id: '111111111111111111' },
      attempt_count: '23',
    };
  return {
    ...common,
    model: 'saved-model-with-a-long-readable-name',
    ...(charity ? {} : { attempt_count: '23' }),
  };
}

function attempt(role: Role, index: number) {
  return {
    attempt_seq: String(index),
    result_kind: 'response',
    endpoint_key_id: '2',
    endpoint_base_url: 'https://api.example.test/' + 'long-segment/'.repeat(12),
    connector_type: 'openai-compatible',
    upstream_model_id: `upstream-${index}`,
    status_code: 200,
    upstream_code: null,
    diag: null,
    usage,
    started_at: 1_800_000_000,
    completed_at: 1_800_000_001,
    ...(role === 'user' ? { endpoint_note: 'Personal endpoint', key_note: 'Personal key' } : {}),
  };
}

function windowFor(total: number, page: number, size: number) {
  const pages = Math.max(1, Math.ceil(total / size));
  const actual = Math.min(page, pages);
  return {
    page: String(actual),
    page_size: size,
    total_items: String(total),
    total_pages: String(pages),
  };
}

async function prepare(page: Page, role: Role, locale: 'en' | 'zh', width: number) {
  const station = role === 'admin' ? 'admin' : 'user';
  const origin = station === 'admin' ? ADMIN_ORIGIN : USER_ORIGIN;
  const guard = collectConsoleViolations(page);
  await page.setViewportSize({ width, height: 900 });
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.addInitScript((lang) => {
    localStorage.setItem('nb.lang', lang);
    localStorage.setItem('nb.theme', lang === 'zh' ? 'dark' : 'light');
  }, locale);
  await mockPublicConfig(page, station);
  await mockRoleSession(page, station, role === 'steward' ? 'level6' : role);
  if (station === 'user') {
    const session = userSession(role === 'steward' ? 'level6' : 'user');
    session.user.lang = locale;
    for (const path of ['/api/session', '/api/me'])
      await mockJson(page, { origin, method: 'GET', path, body: session });
  } else
    await mockJson(page, {
      origin,
      method: 'GET',
      path: '/admin/api/maintenance',
      body: { enabled: false, revision: '1' },
    });
  await mockJson(page, {
    origin,
    method: 'GET',
    path: station === 'admin' ? '/admin/api/time-zones' : '/api/time-zones',
    body: { version: 'go1.26.6-zoneinfo', zones: ['UTC'] },
  });
  return { origin, guard };
}

for (const scenario of [
  { role: 'admin', locale: 'en', width: 390 },
  { role: 'admin', locale: 'zh', width: 1280 },
  { role: 'user', locale: 'zh', width: 320 },
  { role: 'user', locale: 'en', width: 390 },
  { role: 'steward', locale: 'en', width: 1280 },
  { role: 'steward', locale: 'zh', width: 390 },
] as const)
  test(`${scenario.role} ${scenario.locale} keeps independent log and attempt pages through refresh and return`, async ({
    page,
  }) => {
    const { role, locale } = scenario;
    const { origin, guard } = await prepare(page, role, locale, scenario.width);
    const labels = copy[locale];
    const path =
      role === 'admin' ? '/admin/api/logs' : role === 'steward' ? '/api/steward/logs' : '/api/logs';
    const screenPath = role === 'steward' ? '/steward' : '/logs';
    let total = 21;
    const requests: URL[] = [];
    await page.route('**/*', async (route) => {
      const url = new URL(route.request().url());
      if (url.origin !== origin || ![path, `${path}/${requestID(21)}`].includes(url.pathname))
        return route.fallback();
      expect(route.request().method()).toBe('GET');
      expect(url.searchParams.has('cursor')).toBe(false);
      expect(url.searchParams.has('limit')).toBe(false);
      requests.push(url);
      let body: unknown;
      if (url.pathname === path) {
        expect(url.searchParams.get('status')).toBe('200');
        const meta = windowFor(
          total,
          Number(url.searchParams.get('page')),
          Number(url.searchParams.get('page_size')),
        );
        const start = (Number(meta.page) - 1) * meta.page_size;
        body = {
          data: Array.from({ length: Math.min(meta.page_size, total - start) }, (_, index) =>
            row(role, start + index + 1),
          ),
          next_cursor: null,
          pagination: meta,
        };
      } else {
        expect(url.searchParams.has('attempt_cursor')).toBe(false);
        expect(url.searchParams.has('attempt_limit')).toBe(false);
        const meta = windowFor(
          23,
          Number(url.searchParams.get('attempt_page')),
          Number(url.searchParams.get('attempt_page_size')),
        );
        const start = (Number(meta.page) - 1) * meta.page_size;
        body = {
          request: row(role, 21),
          attempts: {
            data: Array.from({ length: Math.min(meta.page_size, 23 - start) }, (_, index) =>
              attempt(role, start + index + 1),
            ),
            next_cursor: null,
          },
          attempt_pagination: meta,
        };
      }
      await route.fulfill({ contentType: 'application/json', body: JSON.stringify(body) });
    });
    await page.goto(`${origin}${screenPath}?page=3&page_size=10&status=200`);
    await page.getByRole('button', { name: labels.details, exact: true }).click();
    const dialog = page.getByRole('dialog');
    await expect(dialog.locator('.log-attempt')).toHaveCount(20);
    const embeddingLabel =
      locale === 'zh'
        ? role === 'steward'
          ? '公益向量嵌入'
          : '自用向量嵌入'
        : role === 'steward'
          ? 'Charity embedding'
          : 'Personal embedding';
    await expect(page.getByText(embeddingLabel, { exact: true }).first()).toBeVisible();
    await dialog.getByLabel(labels.size).selectOption('10');
    await expect(dialog.locator('.log-attempt')).toHaveCount(10);
    await dialog.getByLabel(labels.jump).fill('3');
    await dialog.getByRole('button', { name: labels.go, exact: true }).click();
    await expect(dialog.locator('.log-attempt')).toHaveCount(3);
    expect(new URL(page.url()).searchParams.get('page')).toBe('3');
    expect(new URL(page.url()).searchParams.get('attempt_page')).toBe('3');
    await page.reload();
    await expect(dialog.locator('.log-attempt')).toHaveCount(3);
    await expect(dialog.getByText('upstream-23', { exact: true })).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
      true,
    );
    if (process.env.NONBIRI_VISUAL_DIR)
      await page.screenshot({
        path: resolve(
          process.env.NONBIRI_VISUAL_DIR,
          `logs-${role}-${locale}-${scenario.width}.png`,
        ),
      });
    await dialog.getByRole('button', { name: labels.close, exact: true }).click();
    await expect(dialog).toHaveCount(0);
    await expect.poll(() => new URL(page.url()).searchParams.has('attempt_page')).toBe(false);
    const restored = new URL(page.url()).searchParams;
    expect(restored.get('page')).toBe('3');
    expect(restored.get('page_size')).toBe('10');
    expect(restored.get('status')).toBe('200');
    expect(restored.has('attempt_page')).toBe(false);
    if (role !== 'user') {
      for (const format of ['csv', 'json']) {
        const link = page.getByRole('link', {
          name: `${locale === 'zh' ? '导出' : 'Export'} ${format.toUpperCase()}`,
        });
        await expect(link).toHaveAttribute('href', `${path}/export.${format}?status=200`);
      }
    }
    await page.getByRole('button', { name: labels.previous, exact: true }).click();
    await expect(page.getByRole('button', { name: labels.details, exact: true })).toHaveCount(10);
    await page.goBack();
    await expect(page.getByRole('button', { name: labels.details, exact: true })).toHaveCount(1);
    total = 5;
    await page.reload();
    await expect(page.getByRole('button', { name: labels.details, exact: true })).toHaveCount(5);
    await expect(page.getByLabel(labels.jump)).toHaveValue('1');
    await expect(page.getByLabel(labels.size)).toHaveValue('10');
    expect(requests.some((url) => url.searchParams.get('attempt_page') === '3')).toBe(true);
    guard.assertNone();
  });

test('ordinary charity log detail exposes no attempt list or attempt pagination', async ({
  page,
}) => {
  const { origin, guard } = await prepare(page, 'user', 'en', 390);
  await mockJson(page, {
    origin,
    method: 'GET',
    path: '/api/logs?page=1&page_size=20',
    body: { data: [row('user', 1, true)], next_cursor: null, pagination: windowFor(1, 1, 20) },
  });
  await mockJson(page, {
    origin,
    method: 'GET',
    path: `/api/logs/${requestID(1)}?attempt_page=1&attempt_page_size=20`,
    body: { request: row('user', 1, true), caller_safe_result: { class: 'success' } },
  });
  await page.goto(`${origin}/logs`);
  await page.getByRole('button', { name: 'Details', exact: true }).click();
  const dialog = page.getByRole('dialog');
  await expect(dialog).toBeVisible();
  await expect(dialog.getByText('Success', { exact: true })).toBeVisible();
  await expect(dialog.locator('.log-attempts')).toHaveCount(0);
  await expect(dialog.getByLabel('Items per page')).toHaveCount(0);
  await expect(dialog.getByText('Personal key', { exact: true })).toHaveCount(0);
  guard.assertNone();
});
