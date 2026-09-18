import { mkdir } from 'node:fs/promises';
import { resolve } from 'node:path';
import { expect, test, type Page } from './test';
import { ADMIN_ORIGIN, USER_ORIGIN } from './ports';
import {
  assertNoSensitiveBrowserPersistence,
  installURLPersistenceObserver,
  mockPublicConfig,
  mockRoleSession,
} from './support';

const NOW = 1_800_000_000;
const PAGE_SIZE = 20;
const SOURCE_COUNT = 21;
const TARGET_SOURCE_INDEX = 20;
const TARGET_DONATION_ID = '7';
const TARGET_KEY_ID = '99';
const ORIGINAL_NOTE = 'Synthetic reviewer-safe note';
const UPDATED_NOTE = 'Updated reviewer-safe note';
const SYNTHETIC_DISCORD_ID = '1'.repeat(18);
const LONG_BASE_URL =
  'https://donor.example.test/v1/tenant/long-segment-long-segment-long-segment-long-segment/keys/21';
const ADMIN_MARKER = 'managed-source-admin-ephemeral-7c4a1f9e';
const STEWARD_MARKER = 'managed-source-steward-ephemeral-5e9c3b7a';
const EVIDENCE_DIR = process.env.NONBIRI_VISUAL_DIR
  ? resolve(process.env.NONBIRI_VISUAL_DIR)
  : null;

type JSONRecord = Record<string, unknown>;
type BrowserContext = Parameters<typeof installURLPersistenceObserver>[0];
type RouteLike = {
  fulfill(options: {
    status: number;
    headers: Record<string, string>;
    body: string;
  }): Promise<void>;
};

interface StationSetup {
  readonly station: 'admin' | 'user';
  readonly role: 'admin' | 'level5';
  readonly origin: string;
  readonly apiPrefix: string;
  readonly locale: 'en' | 'zh';
  readonly theme: 'light' | 'dark';
  readonly width: number;
  readonly marker: string;
  readonly sourceTab: string;
  readonly managementTab: string;
  readonly searchSources: string;
  readonly searchKeys: string;
  readonly scope: string;
  readonly applySearch: string;
  readonly backToSources: string;
  readonly next: string;
  readonly retry: RegExp;
  readonly manageKey: RegExp;
  readonly safeNote: string;
  readonly saveKeyLimits: string;
  readonly returnToList: string;
}

interface RecordedPatch {
  readonly body: JSONRecord;
  readonly idempotencyKey: string;
}

interface ManagedConsoleGuard {
  assertNone(): void;
}

interface ManagedSourceFixture {
  readonly sourceReads: string[];
  readonly sourceKeyReads: string[];
  readonly donationListReads: string[];
  readonly donationDetailReads: string[];
  readonly donationKeyReads: string[];
  readonly patches: RecordedPatch[];
  keyPage2Failures: number;
  patched: boolean;
  install(page: Page): Promise<void>;
}

function sourceKey(index: number): string {
  if (!Number.isInteger(index) || index < 0 || index >= SOURCE_COUNT) {
    throw new Error(`Unsupported synthetic source index: ${index}`);
  }
  // 32 bytes encoded as 43 unpadded base64url characters. The final A has
  // zero low padding bits and therefore passes the canonical source-key rule.
  return `dsg_${String.fromCharCode(65 + index)}${'A'.repeat(42)}`;
}

function safeSource(index: number): JSONRecord {
  return {
    kind: 'custom',
    connector_type: 'openai-compatible',
    base_url:
      index === TARGET_SOURCE_INDEX
        ? LONG_BASE_URL
        : `https://donor.example.test/v1/source-${index + 1}`,
  };
}

function handling(revision: string): JSONRecord {
  return {
    state: 'pending',
    revision,
    processed_at: null,
    processed_by_role: null,
    closed_at: null,
    closed_reason: null,
  };
}

function keyBase(id: string, note: string): JSONRecord {
  return {
    binding_count: '0',
    idle: true,
    id,
    endpoint_key_id: String(1_000 + Number(id)),
    display_head: 'sk-live',
    display_tail: `key-${id}`,
    safe_source: safeSource(TARGET_SOURCE_INDEX),
    physical_enabled: true,
    charity_state: 'available',
    limits: { price: null, calls: null, tokens: null },
    usage: {
      price_used: '0',
      price_inflight: '0',
      calls_used: '0',
      calls_inflight: '0',
      tokens_used: '0',
      tokens_inflight: '0',
    },
    token_reserve: 7,
    authorized_expires_at: null,
    expires_at: null,
    failure_disable_threshold: '10',
    streak: { generation: '1', count: '0', failure_disabled: false },
    ended_reason: null,
    safe_note: note,
    max_concurrency: 2_147_483_647,
    max_rpm: 2_147_483_647,
  };
}

function keySummary(id: string, revision: string, note: string): JSONRecord {
  return {
    ...keyBase(id, note),
    donation_id: TARGET_DONATION_ID,
    key_id: id,
    donation_revision: revision,
    rule_count: '0',
    rules: [],
    handling: handling(revision),
  };
}

function donationKeys(note: string): JSONRecord[] {
  return [
    ...Array.from({ length: PAGE_SIZE }, (_, index) => keyBase(String(index + 1), note)),
    keyBase(TARGET_KEY_ID, note),
  ];
}

function donation(patched: boolean): JSONRecord {
  const revision = patched ? '8' : '7';
  const note = patched ? UPDATED_NOTE : ORIGINAL_NOTE;
  return {
    id: TARGET_DONATION_ID,
    status: 'approved',
    revision,
    handling: handling(revision),
    description: 'Synthetic source-browser donation',
    review_result: {
      decision: 'approve',
      reason: 'Synthetic review approval',
      reviewed_at: NOW - 60,
    },
    keys: donationKeys(note),
    owner: { user_id: '42', discord_id: SYNTHETIC_DISCORD_ID, display_name: 'Synthetic donor' },
    reviewer: { user_id: '42', role: 'admin' },
    created_at: NOW - 120,
    updated_at: patched ? NOW + 1 : NOW,
  };
}

function donationPage(): JSONRecord {
  return {
    data: [],
    next_cursor: null,
    pagination: { page: '1', page_size: PAGE_SIZE, total_items: '0', total_pages: '1' },
  };
}

function page(data: JSONRecord[], pageNumber: number, totalItems: number): JSONRecord {
  return {
    data,
    next_cursor: null,
    pagination: {
      page: String(pageNumber),
      page_size: PAGE_SIZE,
      total_items: String(totalItems),
      total_pages: String(Math.max(1, Math.ceil(totalItems / PAGE_SIZE))),
    },
  };
}

function sourceSummary(index: number): JSONRecord {
  return {
    source_key: sourceKey(index),
    safe_source: safeSource(index),
    donation_count: '1',
    key_count: String(SOURCE_COUNT),
    usable_key_count: String(SOURCE_COUNT),
    pending_donation_count: '0',
  };
}

function createManagedSourceFixture(setup: StationSetup): ManagedSourceFixture {
  const fixture: ManagedSourceFixture = {
    sourceReads: [],
    sourceKeyReads: [],
    donationListReads: [],
    donationDetailReads: [],
    donationKeyReads: [],
    patches: [],
    keyPage2Failures: 0,
    patched: false,
    async install(pageObject) {
      await pageObject.route('**/*', async (route) => {
        const request = route.request();
        const url = new URL(request.url());
        if (url.origin !== setup.origin) {
          await route.fallback();
          return;
        }

        const sourceListPath = `${setup.apiPrefix}/donation-sources`;
        const sourceKeysPath = `${sourceListPath}/${sourceKey(TARGET_SOURCE_INDEX)}/keys`;
        const donationListPath = `${setup.apiPrefix}/donations`;
        const donationDetailPath = `${donationListPath}/${TARGET_DONATION_ID}`;
        const donationKeysPath = `${donationDetailPath}/keys`;
        const donationKeyPatchPath = `${donationKeysPath}/${TARGET_KEY_ID}`;
        const supportPaths =
          setup.station === 'admin'
            ? new Set([
                '/admin/api/branding',
                '/admin/api/config',
                '/admin/api/session',
                '/admin/api/time-zones',
                '/admin/api/donations/badge',
              ])
            : new Set([
                '/api/config',
                '/api/session',
                '/api/me',
                '/api/time-zones',
                '/api/steward/donations/badge',
              ]);
        const businessPath =
          url.pathname === sourceListPath ||
          url.pathname === sourceKeysPath ||
          url.pathname === donationListPath ||
          url.pathname === donationDetailPath ||
          url.pathname === donationKeysPath ||
          url.pathname === donationKeyPatchPath;
        if (!businessPath) {
          if (url.pathname.startsWith(setup.apiPrefix) && !supportPaths.has(url.pathname)) {
            throw new Error(`Unexpected business request: ${request.method()} ${url.href}`);
          }
          await route.fallback();
          return;
        }

        const pageNumber = url.searchParams.get('page');
        const pageSize = url.searchParams.get('page_size');
        const pagedPath =
          url.pathname === sourceListPath ||
          url.pathname === sourceKeysPath ||
          url.pathname === donationListPath ||
          url.pathname === donationKeysPath;
        if (pagedPath && pageSize !== String(PAGE_SIZE)) {
          throw new Error(`Unexpected business page size: ${request.method()} ${url.href}`);
        }

        if (url.pathname === sourceListPath) {
          if (request.method() !== 'GET' || (pageNumber !== '1' && pageNumber !== '2')) {
            throw new Error(`Unexpected source list request: ${request.method()} ${url.href}`);
          }
          const q = url.searchParams.get('q') ?? '';
          const scope = url.searchParams.get('scope') ?? '';
          if (!['', 'source-needle'].includes(q) || !['active', 'all'].includes(scope)) {
            throw new Error(`Unexpected source filters: ${url.href}`);
          }
          fixture.sourceReads.push(url.search);
          await fulfillJSON(
            route,
            page(
              pageNumber === '1'
                ? Array.from({ length: PAGE_SIZE }, (_, index) => sourceSummary(index))
                : [sourceSummary(TARGET_SOURCE_INDEX)],
              Number(pageNumber),
              SOURCE_COUNT,
            ),
          );
          return;
        }

        if (url.pathname === sourceKeysPath) {
          if (request.method() !== 'GET' || (pageNumber !== '1' && pageNumber !== '2')) {
            throw new Error(`Unexpected source-key request: ${request.method()} ${url.href}`);
          }
          const q = url.searchParams.get('q') ?? '';
          const scope = url.searchParams.get('scope') ?? '';
          if (!['', 'key-needle'].includes(q) || scope !== 'all') {
            throw new Error(`Unexpected source-key filters: ${url.href}`);
          }
          fixture.sourceKeyReads.push(url.search);
          if (pageNumber === '2' && fixture.keyPage2Failures === 0) {
            fixture.keyPage2Failures += 1;
            await fulfillJSON(
              route,
              {
                error: {
                  code: 'temporary_failure',
                  message: 'Synthetic retryable fixture failure.',
                },
              },
              503,
            );
            return;
          }
          const revision = fixture.patched ? '8' : '7';
          const note = fixture.patched ? UPDATED_NOTE : ORIGINAL_NOTE;
          await fulfillJSON(
            route,
            page(
              pageNumber === '1'
                ? Array.from({ length: PAGE_SIZE }, (_, index) =>
                    keySummary(String(index + 1), revision, note),
                  )
                : [keySummary(TARGET_KEY_ID, revision, note)],
              Number(pageNumber),
              SOURCE_COUNT,
            ),
          );
          return;
        }

        if (url.pathname === donationListPath) {
          if (request.method() !== 'GET' || pageNumber !== '1') {
            throw new Error(`Unexpected donation list request: ${request.method()} ${url.href}`);
          }
          fixture.donationListReads.push(url.search);
          await fulfillJSON(route, donationPage());
          return;
        }

        if (url.pathname === donationDetailPath) {
          if (request.method() !== 'GET' || url.search) {
            throw new Error(`Unexpected donation detail request: ${request.method()} ${url.href}`);
          }
          fixture.donationDetailReads.push(url.search);
          await fulfillJSON(route, donation(fixture.patched));
          return;
        }

        if (url.pathname === donationKeysPath) {
          if (request.method() !== 'GET' || (pageNumber !== '1' && pageNumber !== '2')) {
            throw new Error(
              `Unexpected donation-key page request: ${request.method()} ${url.href}`,
            );
          }
          fixture.donationKeyReads.push(url.search);
          const revision = fixture.patched ? '8' : '7';
          const note = fixture.patched ? UPDATED_NOTE : ORIGINAL_NOTE;
          await fulfillJSON(
            route,
            page(
              pageNumber === '1'
                ? Array.from({ length: PAGE_SIZE }, (_, index) =>
                    keySummary(String(index + 1), revision, note),
                  )
                : [keySummary(TARGET_KEY_ID, revision, note)],
              Number(pageNumber),
              SOURCE_COUNT,
            ),
          );
          return;
        }

        if (request.method() !== 'PATCH') {
          throw new Error(
            `Unexpected donation-key mutation method: ${request.method()} ${url.href}`,
          );
        }
        const rawBody = request.postData() ?? '{}';
        fixture.patches.push({
          body: JSON.parse(rawBody) as JSONRecord,
          idempotencyKey: request.headers()['idempotency-key'] ?? '',
        });
        fixture.patched = true;
        await fulfillJSON(route, donation(true));
      });
    },
  };
  return fixture;
}

async function fulfillJSON(route: RouteLike, value: unknown, status = 200): Promise<void> {
  await route.fulfill({
    status,
    headers: { 'content-type': 'application/json', 'cache-control': 'no-store' },
    body: JSON.stringify(value),
  });
}

async function saveScreenshot(page: Page, name: string): Promise<void> {
  if (!EVIDENCE_DIR) return;
  await mkdir(EVIDENCE_DIR, { recursive: true });
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.screenshot({ path: resolve(EVIDENCE_DIR, `${name}.png`), fullPage: true });
}

async function prepareStation(context: BrowserContext, page: Page, setup: StationSetup) {
  const consoleGuard = collectManagedConsoleViolations(page);
  await installURLPersistenceObserver(context, [setup.marker]);
  await page.setViewportSize({ width: setup.width, height: 900 });
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.addInitScript(
    ({ locale, theme }) => {
      localStorage.setItem('nb.lang', locale);
      localStorage.setItem('nb.theme', theme);
    },
    { locale: setup.locale, theme: setup.theme },
  );
  await mockPublicConfig(page, setup.station);
  await mockRoleSession(page, setup.station, setup.role);
  return consoleGuard;
}

function collectManagedConsoleViolations(page: Page): ManagedConsoleGuard {
  const violations: Array<{ type: string; length: number }> = [];
  const expectedTransient503 =
    'Failed to load resource: the server responded with a status of 503 (Service Unavailable)';
  page.on('console', (message) => {
    if (
      (message.type() === 'error' || message.type() === 'warning') &&
      message.text() !== expectedTransient503
    ) {
      violations.push({ type: message.type(), length: message.text().length });
    }
  });
  page.on('pageerror', (error) =>
    violations.push({ type: 'pageerror', length: error.message.length }),
  );
  return {
    assertNone() {
      expect(violations, 'console violations are represented only by type and length').toEqual([]);
    },
  };
}

async function assertNoHorizontalOverflow(page: Page): Promise<void> {
  const layout = await page.evaluate(() => ({
    width: innerWidth,
    scrollWidth: document.documentElement.scrollWidth,
    overflowing: [...document.querySelectorAll<HTMLElement>('body *')]
      .filter(
        (element) =>
          element.getClientRects().length && element.getBoundingClientRect().right > innerWidth + 1,
      )
      .slice(-20)
      .map((element) => ({
        tag: element.tagName,
        className: element.className,
        right: element.getBoundingClientRect().right,
      })),
  }));
  expect(layout.scrollWidth, JSON.stringify(layout)).toBeLessThanOrEqual(layout.width);
  expect(layout.overflowing, JSON.stringify(layout)).toEqual([]);
  const boundedSelectors = [
    '.charity-source-browser__source-address',
    '.charity-source-browser__key-heading p',
    '.ops-subcard > p',
    '.ops-kv dd',
  ];
  for (const selector of boundedSelectors) {
    expect(
      await page
        .locator(selector)
        .evaluateAll((elements) =>
          elements
            .filter((element) => element.getClientRects().length)
            .every((element) => element.scrollWidth <= element.clientWidth + 1),
        ),
      `content overflow for ${selector}`,
    ).toBe(true);
  }
}

async function assertStationPresentation(
  page: Page,
  setup: StationSetup,
  consoleGuard: ManagedConsoleGuard,
): Promise<void> {
  await expect(page.locator('html')).toHaveAttribute(
    'lang',
    setup.locale === 'zh' ? 'zh-CN' : 'en',
  );
  await expect(page.locator('html')).toHaveAttribute('data-theme', setup.theme);
  await assertNoHorizontalOverflow(page);
  await assertNoSensitiveBrowserPersistence(page, [setup.marker]);
  consoleGuard.assertNone();
}

function setupFor(station: 'admin' | 'user'): StationSetup {
  if (station === 'admin') {
    return {
      station,
      role: 'admin',
      origin: ADMIN_ORIGIN,
      apiPrefix: '/admin/api',
      locale: 'en',
      theme: 'light',
      width: 1_280,
      marker: ADMIN_MARKER,
      sourceTab: 'Browse by source',
      managementTab: '',
      searchSources: 'Search sources',
      searchKeys: 'Search keys',
      scope: 'Source scope',
      applySearch: 'Search',
      backToSources: 'Back to sources',
      next: 'Next',
      retry: /^Retry$/,
      manageKey: /^Manage key #99$/,
      safeNote: 'Review note',
      saveKeyLimits: 'Save key limits',
      returnToList: 'Return to list',
    };
  }
  return {
    station,
    role: 'level5',
    origin: USER_ORIGIN,
    apiPrefix: '/api/steward',
    locale: 'zh',
    theme: 'dark',
    width: 390,
    marker: STEWARD_MARKER,
    sourceTab: '按来源查看',
    managementTab: '公益管理',
    searchSources: '搜索来源',
    searchKeys: '搜索密钥',
    scope: '来源范围',
    applySearch: '搜索',
    backToSources: '返回来源列表',
    next: '下一页',
    retry: /^重试$/,
    manageKey: /^管理密钥 #99$/,
    safeNote: '审核备注',
    saveKeyLimits: '保存密钥限制',
    returnToList: '返回列表',
  };
}

test.afterEach(async ({ page }, testInfo) => {
  if (testInfo.status === testInfo.expectedStatus || page.isClosed() || !EVIDENCE_DIR) return;
  try {
    await saveScreenshot(page, `failure-${testInfo.title.replace(/[^A-Za-z0-9_-]+/g, '_')}`);
  } catch {
    // Keep the original assertion failure when a page is already unusable.
  }
});

async function exerciseManagedSourceBrowser(
  context: BrowserContext,
  page: Page,
  setup: StationSetup,
): Promise<void> {
  const consoleGuard = await prepareStation(context, page, setup);
  const fixture = createManagedSourceFixture(setup);
  await fixture.install(page);

  if (setup.station === 'admin') {
    await page.goto(`${setup.origin}/charity`);
    await page.getByRole('tab', { name: setup.sourceTab, exact: true }).click();
  } else {
    await page.goto(`${setup.origin}/steward?tab=charity`);
    await page.getByRole('tab', { name: setup.managementTab, exact: true }).click();
    await page.getByRole('tab', { name: setup.sourceTab, exact: true }).click();
  }

  const browser = page.locator('.charity-source-browser');
  await expect(browser).toBeVisible();
  await expect(
    page.getByRole('heading', { name: /按来源查看公益资源|Browse charity by source/ }),
  ).toBeVisible();
  await expect.poll(() => fixture.donationListReads.length).toBeGreaterThan(0);
  await expect.poll(() => fixture.sourceReads.length).toBeGreaterThan(0);

  const sourceSection = page.locator('.charity-source-browser__sources');
  const sourceSearch = page.getByRole('searchbox', { name: setup.searchSources, exact: true });
  await sourceSearch.fill('source-needle');
  await browser
    .locator('.charity-source-browser__filters form')
    .getByRole('button', { name: setup.applySearch, exact: true })
    .click();
  await expect.poll(() => fixture.sourceReads.length).toBeGreaterThan(1);
  expect(fixture.sourceReads.at(-1)).toContain('q=source-needle');
  expect(fixture.sourceReads.at(-1)).toContain('page=1');
  await page.getByRole('combobox', { name: setup.scope, exact: true }).selectOption('all');
  await expect.poll(() => fixture.sourceReads.length).toBeGreaterThan(2);
  expect(fixture.sourceReads.at(-1)).toContain('q=source-needle');
  expect(fixture.sourceReads.at(-1)).toContain('scope=all');
  await expect(page.getByRole('combobox', { name: setup.scope, exact: true })).toHaveValue('all');

  const sourcePager = sourceSection.locator('nav.page-pagination');
  await sourcePager.getByRole('button', { name: setup.next, exact: true }).click();
  await expect.poll(() => fixture.sourceReads.length).toBeGreaterThan(3);
  expect(fixture.sourceReads.at(-1)).toContain('q=source-needle');
  expect(fixture.sourceReads.at(-1)).toContain('scope=all');
  expect(fixture.sourceReads.at(-1)).toContain('page=2');
  const targetSource = sourceSection.locator('.charity-source-browser__source-button');
  await expect(targetSource).toHaveCount(1);
  await expect(targetSource).not.toHaveAttribute('aria-current', 'true');
  await expect(targetSource).toContainText('21');
  await expect(sourceSection.locator('.charity-source-browser__source-address')).toContainText(
    LONG_BASE_URL,
  );
  await targetSource.click();
  await expect(targetSource).toHaveAttribute('aria-current', 'true');

  const keysSection = page.locator('.charity-source-browser__keys');
  await expect(keysSection).toBeVisible();
  await expect.poll(() => fixture.sourceKeyReads.length).toBeGreaterThan(0);
  expect(fixture.sourceKeyReads.at(-1)).toContain('scope=all');
  expect(fixture.sourceKeyReads.at(-1)).toContain('page=1');
  await keysSection.getByRole('button', { name: setup.backToSources, exact: true }).click();
  await expect(page).toHaveURL(/charity_section=sources/);
  const cancelledURL = new URL(page.url());
  expect(cancelledURL.searchParams.get('source_q')).toBe('source-needle');
  expect(cancelledURL.searchParams.get('source_scope')).toBe('all');
  expect(cancelledURL.searchParams.get('sources_page')).toBe('2');
  expect(cancelledURL.searchParams.get('source_key')).toBeNull();
  expect(cancelledURL.searchParams.get('source_keys_page')).toBeNull();
  expect(fixture.patches).toHaveLength(0);
  await targetSource.click();
  // Reselecting the same source may reuse the still-valid page-one query from
  // before cancellation. The visible key pane is the contract; search below
  // proves a new query still carries the restored source context.
  await expect(keysSection).toBeVisible();
  expect(fixture.sourceKeyReads.at(-1)).toContain('page=1');
  const keySearch = page.getByRole('searchbox', { name: setup.searchKeys, exact: true });
  await keySearch.fill('key-needle');
  await keysSection
    .locator('form')
    .getByRole('button', { name: setup.applySearch, exact: true })
    .click();
  await expect.poll(() => fixture.sourceKeyReads.length).toBeGreaterThan(1);
  expect(fixture.sourceKeyReads.at(-1)).toContain('q=key-needle');
  expect(fixture.sourceKeyReads.at(-1)).toContain('scope=all');
  expect(fixture.sourceKeyReads.at(-1)).toContain('page=1');

  const keyPager = keysSection.locator('nav.page-pagination');
  await keyPager.getByRole('button', { name: setup.next, exact: true }).click();
  await expect.poll(() => fixture.keyPage2Failures).toBe(1);
  await expect(keysSection.getByRole('alert')).toBeVisible();
  await expect(keySearch).toHaveValue('key-needle');
  expect(fixture.patches).toHaveLength(0);
  await expect(keysSection.getByRole('button', { name: setup.manageKey })).toHaveCount(0);
  await expect(keysSection.getByRole('button', { name: setup.retry })).toBeVisible();
  await keysSection.getByRole('button', { name: setup.retry }).click();
  await expect.poll(() => fixture.sourceKeyReads.length).toBeGreaterThan(3);
  expect(fixture.sourceKeyReads.at(-1)).toContain('q=key-needle');
  expect(fixture.sourceKeyReads.at(-1)).toContain('page=2');
  await expect(keysSection.getByRole('button', { name: setup.manageKey })).toBeVisible();
  await expect(keySearch).toHaveValue('key-needle');
  await expect(keysSection.locator('.nb-key-limits__summary')).toContainText('2147483647');
  await assertNoHorizontalOverflow(page);
  await saveScreenshot(page, `${setup.station}-source-page-2-${setup.locale}-${setup.theme}`);

  await keysSection.getByRole('button', { name: setup.manageKey }).click();
  await expect(page).toHaveURL(/charity_section=donations/);
  const detailURL = new URL(page.url());
  expect(detailURL.searchParams.get('donation_id')).toBe(TARGET_DONATION_ID);
  expect(detailURL.searchParams.get('donation_from')).toBe('sources');
  expect(detailURL.searchParams.get('source_q')).toBe('source-needle');
  expect(detailURL.searchParams.get('source_scope')).toBe('all');
  expect(detailURL.searchParams.get('sources_page')).toBe('2');
  expect(detailURL.searchParams.get('sources_page_size')).toBe('20');
  expect(detailURL.searchParams.get('source_key')).toBe(sourceKey(TARGET_SOURCE_INDEX));
  expect(detailURL.searchParams.get('source_key_q')).toBe('key-needle');
  expect(detailURL.searchParams.get('source_keys_page')).toBe('2');
  expect(detailURL.searchParams.get('source_keys_page_size')).toBe('20');
  await expect.poll(() => fixture.donationDetailReads.length).toBeGreaterThan(0);
  await expect.poll(() => fixture.donationKeyReads.length).toBeGreaterThan(0);
  await expect.poll(() => new URL(page.url()).searchParams.get('donation_keys_page')).toBe('2');
  expect(new URL(page.url()).searchParams.get('donation_keys_page_size')).toBeNull();
  expect(fixture.donationKeyReads.at(-1)).toContain('page=2');
  expect(fixture.donationKeyReads.at(-1)).toContain('page_size=20');
  await expect(page.getByRole('heading', { name: /捐赠 #7|Donation #7/ })).toBeVisible();
  const note = page.getByLabel(setup.safeNote, { exact: true });
  await expect(note).toHaveValue(ORIGINAL_NOTE);
  await expect(page.getByText('Synthetic donor', { exact: false })).toBeVisible();
  await expect(page.getByText(SYNTHETIC_DISCORD_ID, { exact: false })).toBeVisible();

  await note.fill(UPDATED_NOTE);
  await expect(page.getByRole('button', { name: setup.saveKeyLimits, exact: true })).toBeVisible();
  await page.getByRole('button', { name: setup.saveKeyLimits, exact: true }).click();
  await expect.poll(() => fixture.patches.length).toBe(1);
  expect(fixture.patches[0]).toEqual({
    body: {
      expected_revision: '7',
      price_limit: null,
      calls_limit: null,
      tokens_limit: null,
      token_reserve: 7,
      safe_note: UPDATED_NOTE,
      expires_at: null,
    },
    idempotencyKey: expect.stringMatching(/^[A-Za-z0-9_-]{22}$/),
  });
  await expect(note).toHaveValue(UPDATED_NOTE);
  await expect.poll(() => fixture.donationDetailReads.length).toBeGreaterThan(1);
  expect(fixture.patches).toHaveLength(1);
  await saveScreenshot(
    page,
    `${setup.station}-managed-key-readback-${setup.locale}-${setup.theme}`,
  );
  await page.reload();
  await expect(page.getByLabel(setup.safeNote, { exact: true })).toHaveValue(UPDATED_NOTE);
  await expect.poll(() => fixture.donationDetailReads.length).toBeGreaterThan(2);
  expect(new URL(page.url()).searchParams.get('donation_keys_page')).toBe('2');
  expect(new URL(page.url()).searchParams.get('donation_keys_page_size')).toBeNull();
  await assertNoHorizontalOverflow(page);
  expect(fixture.patches).toHaveLength(1);

  await page.getByRole('button', { name: setup.returnToList, exact: true }).click();
  await expect(page).toHaveURL(/charity_section=sources/);
  const returnURL = new URL(page.url());
  expect(returnURL.searchParams.get('source_q')).toBe('source-needle');
  expect(returnURL.searchParams.get('source_scope')).toBe('all');
  expect(returnURL.searchParams.get('sources_page')).toBe('2');
  expect(returnURL.searchParams.get('sources_page_size')).toBe('20');
  expect(returnURL.searchParams.get('source_key')).toBe(sourceKey(TARGET_SOURCE_INDEX));
  expect(returnURL.searchParams.get('source_key_q')).toBe('key-needle');
  expect(returnURL.searchParams.get('source_keys_page')).toBe('2');
  expect(returnURL.searchParams.get('source_keys_page_size')).toBe('20');
  await expect(
    page.locator('.charity-source-browser__source-button[aria-current="true"]'),
  ).toHaveCount(1);
  await expect(
    page.locator('.charity-source-browser__key').filter({ hasText: `key-${TARGET_KEY_ID}` }),
  ).toBeVisible();
  await expect.poll(() => fixture.sourceReads.length).toBeGreaterThan(4);
  await expect.poll(() => fixture.sourceKeyReads.length).toBeGreaterThan(4);
  expect(fixture.sourceReads.at(-1)).toContain('page=2');
  expect(fixture.sourceKeyReads.at(-1)).toContain('q=key-needle');
  expect(fixture.sourceKeyReads.at(-1)).toContain('page=2');
  expect(fixture.patches).toHaveLength(1);
  await assertStationPresentation(page, setup, consoleGuard);
  await saveScreenshot(page, `${setup.station}-source-return-${setup.locale}-${setup.theme}`);
}

test('administrator can browse a paged source, edit one key, and return with context', async ({
  context,
  page,
}) => {
  await exerciseManagedSourceBrowser(context, page, setupFor('admin'));
});

test('level-five steward can browse a paged source with complete donor review information', async ({
  context,
  page,
}) => {
  await exerciseManagedSourceBrowser(context, page, setupFor('user'));
});
