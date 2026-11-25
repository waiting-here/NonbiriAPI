import { endpointScope } from '../fixtures/resourceScopes';
import { mkdir } from 'node:fs/promises';
import { resolve } from 'node:path';
import { expect, test } from './test';
import { USER_ORIGIN } from './ports';
import { collectConsoleViolations, mockPublicConfig, mockRoleSession } from './support';
import en from '../../src/user/i18n/en.json' with { type: 'json' };
import zh from '../../src/user/i18n/zh.json' with { type: 'json' };
import commonEn from '../../src/shared/i18n/common/en.json' with { type: 'json' };
import commonZh from '../../src/shared/i18n/common/zh.json' with { type: 'json' };
import { createOptions } from './connection-fixtures';
import { charityControlCopy } from '../../src/shared/components/charityControlCopy';

const NOW = 1_800_000_000;
const BROWSER_ZONE = 'America/New_York';
const LOCAL_EXPIRY = '2030-01-01T00:00:00';
const EXPIRY = Date.parse(LOCAL_EXPIRY + '-05:00') / 1000;
function endpoint(id: number) {
  return {
    ...endpointScope,
    id: String(id),
    connection_id: String(id),
    native_services: ['chat_completions', 'responses'],
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
    credential_id: String(id + 1000),
    credential_available: true,
    credential_version: '1',
    grant_revision: '1',
    native_services: ['chat_completions', 'responses'],
    discovery_source_id: null,
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
  test.use({ timezoneId: BROWSER_ZONE });
  for (const [width, locale, theme] of [
    [320, 'en', 'light'],
    [390, 'zh', 'dark'],
    [1440, 'en', 'light'],
  ] as const) {
    test(`retains every selected key and local expiry across pages at ${width} ${locale}`, async ({
      page,
    }) => {
      const copy = (locale === 'en' ? en : zh).user.charity;
      const workflows = (locale === 'en' ? en : zh).user.workflows;
      const common = (locale === 'en' ? commonEn : commonZh).common;
      const controls = charityControlCopy(locale);
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
        if (url.pathname === '/api/endpoint-create-options') {
          expect(request.method()).toBe('GET');
          expect(url.search).toBe('');
          return route.fulfill({ json: createOptions });
        }
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
          expect(request.method()).toBe('GET');
          expect(url.searchParams.size).toBe(2);
          expect(url.searchParams.get('local')).toBe(LOCAL_EXPIRY);
          expect(url.searchParams.get('time_zone')).toBe(BROWSER_ZONE);
          return route.fulfill({
            json: {
              instant: EXPIRY,
              local: LOCAL_EXPIRY,
              time_zone: BROWSER_ZONE,
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
            keys: {
              endpoint_key_id: string;
              expires_at: number | null;
            }[];
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
                smart_backoff: null,
                streak: { generation: '1', count: '0', failure_disabled: false },
                ended_reason: null,
              })),
            },
          });
        }
        return route.fallback();
      });
      await page.goto(`${USER_ORIGIN}/charity?tab=models&q=catalog-search`);
      await page.getByRole('tab', { name: copy.tabs.donate, exact: true }).click();
      await expect(page).toHaveURL(
        (url) => url.searchParams.get('tab') === 'donate' && !url.searchParams.has('q'),
      );
      const openPicker = page.getByRole('button', {
        name: copy.presentation.openPicker,
        exact: true,
      });
      const picker = page
        .getByRole('region', { name: copy.presentation.chooseKeys, exact: true })
        .filter({
          has: page.getByRole('heading', { name: copy.resourcePicker.title, exact: true }),
        });
      const sources = picker.locator('section').filter({
        has: page.getByRole('heading', { name: copy.resourcePicker.endpointTitle, exact: true }),
      });
      const firstServiceKeys = picker.locator('section').filter({
        has: page.getByRole('heading', {
          name: copy.resourcePicker.keyTitle.replace('{{endpoint}}', 'Source 1'),
          exact: true,
        }),
      });
      await openPicker.click();
      await expect(
        picker.getByText(common.smartBackoff.automaticHelp, { exact: true }),
      ).toBeVisible();
      await sources.getByRole('button', { name: /^Source 1 / }).click();
      await firstServiceKeys.getByRole('checkbox', { name: /^Key 101 / }).check();
      await firstServiceKeys.getByRole('button', { name: common.next, exact: true }).click();
      await firstServiceKeys.getByRole('checkbox', { name: /^Key 121 / }).check();
      await picker
        .getByRole('button', { name: copy.resourcePicker.backToEndpoints, exact: true })
        .click();
      await expect(firstServiceKeys).toBeHidden();
      await sources.getByRole('button', { name: common.next, exact: true }).click();
      await sources.getByRole('button', { name: /^Source 21 / }).click();
      await picker.getByRole('checkbox', { name: /^Key 201 / }).check();
      const selected = picker.getByRole('region', {
        name: copy.resourcePicker.selectedTitle,
        exact: true,
      });
      await expect(selected.getByRole('checkbox')).toHaveCount(0);
      await picker.getByRole('button', { name: common.close, exact: true }).click();
      await expect(picker).toBeHidden();

      const composer = page.getByRole('region', { name: copy.submitDonation, exact: true });
      await composer
        .getByRole('listitem')
        .filter({ hasText: 'Source 1 · Key 101' })
        .getByText(workflows.donationOptions, { exact: true })
        .click();
      const expiry = composer.getByLabel(copy.keyExpiryFor.replace('{{key}}', 'head101…tail'), {
        exact: true,
      });
      const resolved = page.waitForResponse((response) => {
        const url = new URL(response.url());
        return url.origin === USER_ORIGIN && url.pathname === '/api/time/resolve';
      });
      await expiry.fill(LOCAL_EXPIRY.slice(0, 16));
      expect((await resolved).ok()).toBe(true);
      const description = composer.getByRole('textbox', {
        name: copy.donationDescription,
        exact: true,
      });
      await description.fill('Cross-page contribution');
      await composer
        .getByRole('checkbox', { name: copy.ownershipAuthorization, exact: true })
        .check();
      await expect(page).toHaveURL((url) => url.searchParams.get('endpoint_page') === '2');
      await page.getByRole('tab', { name: copy.tabs.models, exact: true }).click();
      await expect(page).toHaveURL(
        (url) => url.searchParams.get('tab') === 'models' && !url.searchParams.has('endpoint_page'),
      );
      await page.getByRole('tab', { name: copy.tabs.donate, exact: true }).click();
      await expect(description).toHaveValue('Cross-page contribution');
      await expect(expiry).toHaveValue(LOCAL_EXPIRY.slice(0, 16));
      await openPicker.click();
      await expect(page).toHaveURL((url) => !url.searchParams.has('endpoint_page'));
      for (const key of ['head101…tail', 'head121…tail', 'head201…tail']) {
        await expect(selected.getByText(key, { exact: true })).toBeVisible();
      }
      await expect(sources.getByRole('button', { name: /^Source 1 / })).toBeVisible();
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      );
      for (const navigation of await picker
        .getByRole('navigation', { name: common.pagination })
        .all()) {
        for (const control of await navigation
          .locator('button:visible, input:visible, select:visible')
          .all()) {
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
      const submit = composer.getByRole('button', { name: copy.submit, exact: true });
      await picker
        .getByRole('button', { name: copy.presentation.selectionDone, exact: true })
        .click();
      await expect(expiry).toHaveValue(LOCAL_EXPIRY.slice(0, 16));
      await submit.click();
      await expect(composer.getByRole('alert').filter({ hasText: controls.thanks })).toBeVisible();
      expect(posts).toHaveLength(0);
      const thanks = composer.getByRole('radiogroup', { name: controls.thanks, exact: true });
      await thanks.getByText(controls.no, { exact: true }).click();
      await expect(thanks.getByRole('radio', { name: controls.no, exact: true })).toBeChecked();
      await submit.click();
      await expect(page.getByText(copy.submitted, { exact: true })).toBeVisible();
      expect(posts).toEqual([
        {
          description: 'Cross-page contribution',
          ownership_authorized: true,
          discord_public_thanks: false,
          keys: [
            {
              endpoint_key_id: '101',
              native_services: ['chat_completions', 'responses', 'decisions', 'embeddings'],
              credential_id: '1101',
              credential_version: '1',
              grant_revision: '1',
              expires_at: EXPIRY,
              failure_disable_threshold: '10',
            },
            {
              endpoint_key_id: '121',
              native_services: ['chat_completions', 'responses', 'decisions', 'embeddings'],
              credential_id: '1121',
              credential_version: '1',
              grant_revision: '1',
              expires_at: null,
              failure_disable_threshold: '10',
            },
            {
              endpoint_key_id: '201',
              native_services: ['chat_completions', 'responses', 'decisions', 'embeddings'],
              credential_id: '1201',
              credential_version: '1',
              grant_revision: '1',
              expires_at: null,
              failure_disable_threshold: '10',
            },
          ],
        },
      ]);
      await openPicker.click();
      await expect(
        selected.getByText(copy.resourcePicker.noSelected, { exact: true }),
      ).toBeVisible();
      for (const pathname of ['/api/endpoints', '/api/endpoints/1/keys']) {
        expect(
          reads.some((path) => {
            const url = new URL(path, USER_ORIGIN);
            return url.pathname === pathname && url.searchParams.get('page') === '2';
          }),
        ).toBe(true);
      }
      errors.assertNone();
    });
  }
});
