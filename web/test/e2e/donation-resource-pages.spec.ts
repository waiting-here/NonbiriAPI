import { mkdir } from 'node:fs/promises';
import { resolve } from 'node:path';
import { expect, test } from './test';
import { USER_ORIGIN } from './ports';
import { collectConsoleViolations, mockPublicConfig, mockRoleSession } from './support';
import en from '../../src/user/i18n/en.json' with { type: 'json' };
import zh from '../../src/user/i18n/zh.json' with { type: 'json' };
import commonEn from '../../src/shared/i18n/common/en.json' with { type: 'json' };
import commonZh from '../../src/shared/i18n/common/zh.json' with { type: 'json' };

const NOW = 1_800_000_000;
const EXPIRY = Date.parse('2030-01-01T05:00:00Z') / 1000;
function endpoint(id: number) {
  return {
    id: String(id),
    connector_type: 'openai-compatible',
    base_url: `https://controlled-${id}.example/v1/${'long-source-'.repeat(8)}`,
    origin: { kind: 'custom' },
    note: `Source ${id}`,
    enabled: true,
    revision: '1',
    key_count: id === 1 ? '21' : '1',
    created_at: NOW,
    updated_at: NOW,
  };
}
function endpointKey(id: number, parent: number, submitted: boolean) {
  return {
    id: String(id),
    endpoint_id: String(parent),
    display_head: `head${id}`,
    display_tail: 'tail',
    note: `Key ${id}`,
    enabled: false,
    force_store_false: false,
    max_concurrency: 2,
    max_rpm: 30,
    suspension_state: 'none',
    revision: '1',
    created_at: NOW,
    updated_at: NOW,
    browse: {
      donation_eligibility:
        submitted && [101, 121, 201].includes(id) ? 'already_donated' : 'eligible',
      model_count: '0',
      binding_count: '0',
      available_binding_count: '0',
      preview: [],
      discovery: {
        state: 'unknown',
        revision: '1',
        result: null,
        safe_class: 'none',
        observed_at: null,
        count: null,
      },
    },
  };
}
function pageWindow(rows: unknown[], params: URLSearchParams) {
  expect(params.has('cursor')).toBe(false);
  expect(params.has('limit')).toBe(false);
  const size = Number(params.get('page_size'));
  expect([10, 20, 50, 100]).toContain(size);
  const total = Math.max(1, Math.ceil(rows.length / size));
  const page = Math.min(Number(params.get('page')), total);
  expect(page).toBeGreaterThanOrEqual(1);
  return {
    data: rows.slice((page - 1) * size, page * size),
    next_cursor: null,
    pagination: {
      page: String(page),
      page_size: size,
      total_items: String(rows.length),
      total_pages: String(total),
    },
  };
}

test.describe('donation resource submission', () => {
  test.use({ timezoneId: 'America/New_York' });
  for (const [width, locale, theme] of [
    [320, 'en', 'light'],
    [390, 'zh', 'dark'],
  ] as const) {
    test(`retains every selected key and local expiry across pages at ${width} ${locale}`, async ({
      page,
    }) => {
      const copy = (locale === 'en' ? en : zh).user.charity;
      const common = (locale === 'en' ? commonEn : commonZh).common;
      const errors = collectConsoleViolations(page);
      await page.setViewportSize({ width, height: 900 });
      await page.emulateMedia({ reducedMotion: 'reduce' });
      await page.addInitScript(
        ({ locale, theme }) => {
          localStorage.setItem('nb.lang', locale);
          localStorage.setItem('nb.theme', theme);
        },
        { locale, theme },
      );
      await mockPublicConfig(page, 'user');
      await mockRoleSession(page, 'user', 'user');
      let submitted = false;
      const posts: unknown[] = [];
      const reads: string[] = [];
      await page.route('**/*', async (route) => {
        const request = route.request();
        const url = new URL(request.url());
        if (url.origin !== USER_ORIGIN) return route.fallback();
        if (url.pathname === '/api/charity/leaderboard')
          return route.fulfill({
            json: {
              as_of: NOW,
              statistics_start: NOW,
              window: 'history',
              rows: [],
              me: null,
              pagination: { page: '1', page_size: 20, total_items: '0', total_pages: '1' },
            },
          });
        if (url.pathname === '/api/charity/models')
          return route.fulfill({
            json: url.searchParams.has('page')
              ? {
                  models: [],
                  pagination: { page: '1', page_size: 20, total_items: '0', total_pages: '1' },
                  donation_intake: 'open',
                  server_now: NOW,
                }
              : { state: 'no_models', models: [], donation_intake: 'open', server_now: NOW },
          });
        if (url.pathname === '/api/time/resolve') {
          expect(url.searchParams.get('local')).toBe('2030-01-01T00:00:00');
          expect(url.searchParams.get('time_zone')).toBe('America/New_York');
          return route.fulfill({
            json: {
              instant: EXPIRY,
              local: '2030-01-01T00:00:00',
              time_zone: 'America/New_York',
              offset_seconds: -18000,
              adjustment: 'none',
            },
          });
        }
        if (url.pathname.startsWith('/api/endpoints')) {
          expect(request.method()).toBe('GET');
          reads.push(url.pathname + url.search);
          if (url.pathname === '/api/endpoints')
            return route.fulfill({
              json: pageWindow(
                Array.from({ length: 21 }, (_, index) => endpoint(index + 1)),
                url.searchParams,
              ),
            });
          const keyMatch = /^\/api\/endpoints\/(1|21)\/keys$/.exec(url.pathname);
          if (keyMatch) {
            const parent = Number(keyMatch[1]);
            const rows =
              parent === 1
                ? Array.from({ length: 21 }, (_, index) =>
                    endpointKey(index + 101, parent, submitted),
                  )
                : [endpointKey(201, parent, submitted)];
            return route.fulfill({ json: pageWindow(rows, url.searchParams) });
          }
          const match = /^\/api\/endpoints\/(1|21)$/.exec(url.pathname);
          if (match) return route.fulfill({ json: endpoint(Number(match[1])) });
          throw new Error(`Unexpected endpoint request: ${url.pathname}`);
        }
        if (url.pathname === '/api/donations') {
          expect(request.method()).toBe('POST');
          const body = request.postDataJSON() as {
            keys: { endpoint_key_id: string; expires_at: number | null }[];
            description: string;
          };
          posts.push(body);
          submitted = true;
          return route.fulfill({
            json: {
              id: '1',
              status: 'pending',
              revision: '1',
              description: body.description,
              review_result: null,
              created_at: NOW,
              updated_at: NOW,
              keys: body.keys.map((key, index) => ({
                id: String(index + 1),
                endpoint_key_id: key.endpoint_key_id,
                display_head: `head${key.endpoint_key_id}`,
                display_tail: 'tail',
                safe_source: {
                  kind: 'custom',
                  base_url: endpoint(key.endpoint_key_id === '201' ? 21 : 1).base_url,
                  connector_type: 'openai-compatible',
                },
                physical_enabled: false,
                charity_state: 'pending',
                limits: { price: null, calls: null, tokens: null },
                usage: {
                  price_used: '0',
                  price_inflight: '0',
                  calls_used: '0',
                  calls_inflight: '0',
                  tokens_used: '0',
                  tokens_inflight: '0',
                },
                token_reserve: 0,
                expires_at: key.expires_at,
                failure_disable_threshold: '10',
                streak: { generation: '1', count: '0', failure_disabled: false },
                ended_reason: null,
              })),
            },
          });
        }
        return route.fallback();
      });
      await page.goto(`${USER_ORIGIN}/charity?tab=donate`);
      const picker = page.locator('.donation-resource-picker');
      const sources = picker.locator('.donation-resource-picker__section').first();
      await sources.getByRole('button', { name: /^Source 1 / }).click();
      const keys = picker.locator('.donation-resource-picker__section').nth(1);
      await keys.getByRole('checkbox', { name: /^Key 101 / }).check();
      const expiry = picker.locator('input[type="datetime-local"]');
      await expiry.fill('2030-01-01T00:00');
      await keys.getByRole('button', { name: common.next, exact: true }).click();
      await keys.getByRole('checkbox', { name: /^Key 121 / }).check();
      await sources.getByRole('button', { name: common.next, exact: true }).click();
      await sources.getByRole('button', { name: /^Source 21 / }).click();
      await keys.getByRole('checkbox', { name: /^Key 201 / }).check();
      await page
        .getByRole('textbox', { name: copy.donationDescription, exact: true })
        .fill('Cross-page contribution');
      await page.getByRole('checkbox', { name: copy.ownershipAuthorization, exact: true }).check();
      await page.getByRole('tab', { name: copy.tabs.models, exact: true }).click();
      await page.getByRole('tab', { name: copy.tabs.donate, exact: true }).click();
      await expect(picker.locator('input[type="datetime-local"]').first()).toHaveValue(
        '2030-01-01T00:00',
      );
      await expect(page).toHaveURL(/endpoint_page=2/);
      await expect(sources.getByRole('button', { name: /^Source 1 / })).toHaveCount(0);
      await expect(picker.getByText('head101…tail', { exact: true })).toBeVisible();
      await expect(picker.getByText('head121…tail', { exact: true })).toBeVisible();
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      );
      for (const navigation of await picker
        .getByRole('navigation', { name: common.pagination })
        .all()) {
        for (const control of await navigation.locator('button, input, select').all()) {
          const box = await control.boundingBox();
          expect(box && box.width > 0 && box.x >= 0 && box.x + box.width <= width).toBeTruthy();
        }
      }
      if (process.env.NONBIRI_VISUAL_DIR) {
        await mkdir(process.env.NONBIRI_VISUAL_DIR, { recursive: true });
        await page.screenshot({
          path: resolve(
            process.env.NONBIRI_VISUAL_DIR,
            `donation-resources-${width}-${locale}-${theme}.png`,
          ),
          fullPage: true,
        });
      }
      const submit = page.getByRole('button', { name: copy.submit, exact: true });
      await page
        .getByRole('combobox', {
          name: locale === 'en' ? 'Accept a public Discord thank-you' : '是否接受 Discord 公屏感谢',
        })
        .selectOption('no');
      await expect(submit).toBeEnabled();
      await submit.click();
      await expect(page.getByText(copy.submitted, { exact: true })).toBeVisible();
      expect(posts).toEqual([
        {
          description: 'Cross-page contribution',
          ownership_authorized: true,
          discord_public_thanks: false,
          keys: [
            { endpoint_key_id: '101', expires_at: EXPIRY, failure_disable_threshold: '10' },
            { endpoint_key_id: '121', expires_at: null, failure_disable_threshold: '10' },
            { endpoint_key_id: '201', expires_at: null, failure_disable_threshold: '10' },
          ],
        },
      ]);
      await expect(picker.getByText(copy.resourcePicker.noSelected, { exact: true })).toBeVisible();
      expect(reads.some((path) => path.includes('page=2'))).toBe(true);
      expect(reads.every((path) => !path.includes('limit='))).toBe(true);
      errors.assertNone();
    });
  }
});
