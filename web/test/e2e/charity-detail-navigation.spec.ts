import { mkdir } from 'node:fs/promises';
import { resolve } from 'node:path';
import { expect, test, type Page } from './test';
import { ADMIN_ORIGIN, USER_ORIGIN } from './ports';
import {
  assertNoSensitiveBrowserPersistence,
  collectConsoleViolations,
  installURLPersistenceObserver,
  mockPublicConfig,
  mockRoleSession,
} from './support';
import { numberedPage } from './numbered-fixtures';

type BrowserContext = ReturnType<Page['context']>;
type Locator = ReturnType<Page['locator']>;
type RouteHandler = NonNullable<Parameters<Page['route']>[1]>;
type Route = Parameters<RouteHandler>[0];

const NOW = 1_800_000_000;
const PAGE_SIZE = 10;
const DETAIL_DONATION_ID = '20';
const DETAIL_MODEL_ID = '20';
const DETAIL_SOURCE_INDEX = 19;
const DETAIL_SOURCE_KEY = sourceKey(DETAIL_SOURCE_INDEX);
const EVIDENCE_DIR = process.env.NONBIRI_VISUAL_DIR
  ? resolve(process.env.NONBIRI_VISUAL_DIR)
  : null;

type Station = 'admin' | 'user';
type Role = 'admin' | 'level5';
type Frame = 'admin' | 'steward';
type JSONRecord = Record<string, unknown>;
type DetailMode = 'ok' | 'slow' | 'error' | 'forbidden';

interface DetailGate {
  promise: Promise<void>;
  release: () => void;
}

interface FixtureState {
  detailMode: DetailMode;
  detailGate?: DetailGate;
  listGate?: DetailGate;
  detailReads: string[];
  listReads: string[];
  sourceReads: string[];
  sourceKeyReads: string[];
  modelReads: string[];
}

interface StationSetup {
  station: Station;
  role: Role;
  frame: Frame;
  origin: string;
  root: string;
  marker: string;
  consoleGuard: ReturnType<typeof collectConsoleViolations>;
}

function sourceKey(index: number): string {
  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_';
  return 'dsg_' + 'A'.repeat(41) + alphabet[index] + 'A';
}

function safeSource(index: number): JSONRecord {
  return {
    kind: 'custom',
    connector_type: 'openai-compatible',
    base_url: 'https://detail-source-' + (index + 1) + '.example.test/v1',
  };
}

function handling(): JSONRecord {
  return {
    state: 'pending',
    revision: '1',
    processed_at: null,
    processed_by_role: null,
    closed_at: null,
    closed_reason: null,
  };
}

function keyFor(index: number, donationID = String(index + 1)): JSONRecord {
  return {
    id: String(1_000 + index),
    binding_count: '0',
    idle: true,
    endpoint_key_id: String(2_000 + index),
    display_head: 'detail-head',
    display_tail: String(index + 1),
    safe_source: safeSource(index),
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
    token_reserve: 0,
    authorized_expires_at: null,
    expires_at: null,
    streak: { generation: '1', count: '0', failure_disabled: false },
    ended_reason: null,
    safe_note: 'Synthetic detail note',
    max_concurrency: 4,
    max_rpm: 60,
    donation_id: donationID,
    key_id: String(1_000 + index),
    donation_revision: '1',
    rule_count: '0',
    rules: [],
    handling: handling(),
  };
}

function keyForDonation(index: number): JSONRecord {
  const key = keyFor(index);
  const base = { ...key };
  for (const field of [
    'donation_id',
    'key_id',
    'donation_revision',
    'rule_count',
    'rules',
    'handling',
  ]) {
    delete base[field];
  }
  return base;
}

function stateCounts(): JSONRecord {
  return {
    available: '1',
    pending: '0',
    disabled: '0',
    suspended: '0',
    exhausted: '0',
    expired: '0',
    ended: '0',
  };
}

function donationSummary(index: number): JSONRecord {
  const id = String(index + 1);
  return {
    id,
    status: 'approved',
    revision: '1',
    handling: handling(),
    description: 'Reviewable donation ' + id,
    review_result: { decision: 'approve', reason: 'Accepted', reviewed_at: NOW },
    created_at: NOW - index,
    updated_at: NOW,
    key_count: '1',
    state_counts: stateCounts(),
    source_count: '1',
    sources: [safeSource(index)],
    reviewer: { user_id: '9', role: 'admin' },
    owner: { user_id: '42', discord_id: null, display_name: 'Fixture donor ' + id },
  };
}

const LONG_DETAIL_TEXT =
  'A deliberately long detail description that must wrap across the narrow layout. ' +
  'The shared resource remains visible while its detail is open. '.repeat(9);

function donationDetail(index: number): JSONRecord {
  const summary = donationSummary(index);
  return {
    id: summary.id,
    status: summary.status,
    revision: summary.revision,
    handling: summary.handling,
    description: LONG_DETAIL_TEXT,
    review_result: summary.review_result,
    keys: [keyForDonation(index)],
    owner: summary.owner,
    reviewer: summary.reviewer,
    created_at: summary.created_at,
    updated_at: summary.updated_at,
  };
}

function model(index: number): JSONRecord {
  const id = String(index + 1);
  return {
    route_strategy: 'expiry_weighted',
    id,
    provider: 'DetailProvider',
    model: 'detail-model-' + id,
    full_name: '[公益]DetailProvider/detail-model-' + id,
    enabled: true,
    allowed_levels: [1, 2, 3, 4, 5],
    public_description: LONG_DETAIL_TEXT,
    pricing: { mode: 'per_request', user_price: '1', donor_reward: '0' },
    discount: { enabled: false, percent: 0, start_at: null, end_at: null },
    flatten_tool_calls: false,
    revision: '1',
    binding_revision: '0',
    binding_count: '0',
    rolling_success: { sample_count: '0', success_count: '0', percent: null },
    created_at: NOW - index,
    updated_at: NOW,
  };
}

function sourceSummary(index: number): JSONRecord {
  return {
    source_key: sourceKey(index),
    safe_source: safeSource(index),
    donation_count: '1',
    key_count: '1',
    usable_key_count: '1',
    pending_donation_count: '0',
  };
}

function sourceKeyPageItem(index: number): JSONRecord {
  return keyFor(index, String(index + 1));
}

function detailGate(): DetailGate {
  let release!: () => void;
  const promise = new Promise<void>((resolvePromise) => {
    release = resolvePromise;
  });
  return { promise, release };
}

function stationConfig(station: Station): {
  station: Station;
  role: Role;
  frame: Frame;
  origin: string;
  root: string;
  marker: string;
} {
  return station === 'admin'
    ? {
        station,
        role: 'admin',
        frame: 'admin',
        origin: ADMIN_ORIGIN,
        root: '/admin/api',
        marker: 'detail-browser-admin-marker-8f2a',
      }
    : {
        station,
        role: 'level5',
        frame: 'steward',
        origin: USER_ORIGIN,
        root: '/api/steward',
        marker: 'detail-browser-steward-marker-4b7c',
      };
}

async function prepareStation(
  context: BrowserContext,
  page: Page,
  station: Station,
  width = 1_280,
): Promise<StationSetup> {
  const config = stationConfig(station);
  const consoleGuard = collectConsoleViolations(page);
  await installURLPersistenceObserver(context, [config.marker]);
  await page.setViewportSize({ width, height: 900 });
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.addInitScript(() => {
    localStorage.setItem('nb.lang', 'en');
    localStorage.setItem('nb.theme', 'light');
  });
  await mockPublicConfig(page, station);
  await mockRoleSession(page, station, config.role);
  return { ...config, consoleGuard };
}

async function fulfillJSON(route: Route, value: unknown, status = 200): Promise<void> {
  await route.fulfill({
    status,
    headers: { 'content-type': 'application/json', 'cache-control': 'no-store' },
    body: JSON.stringify(value),
  });
}

function listRows(): JSONRecord[] {
  return Array.from({ length: 21 }, (_, index) => donationSummary(index));
}

function modelRows(): JSONRecord[] {
  return Array.from({ length: 21 }, (_, index) => model(index));
}

function sourceRows(): JSONRecord[] {
  return Array.from({ length: 21 }, (_, index) => sourceSummary(index));
}

async function installManagementRoutes(
  page: Page,
  station: Station,
  state: FixtureState,
): Promise<void> {
  const config = stationConfig(station);
  const rows = listRows();
  const models = modelRows();
  const sources = sourceRows();
  await page.route('**/*', async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (url.origin !== config.origin || request.method() !== 'GET') {
      await route.fallback();
      return;
    }
    const path = url.pathname;
    if (path === config.root + '/donations') {
      state.listReads.push(url.search);
      await state.listGate?.promise;
      await fulfillJSON(route, numberedPage(rows, url.searchParams));
      return;
    }
    const donationMatch = path.match(new RegExp('^' + config.root + '/donations/([1-9][0-9]*)$'));
    if (donationMatch) {
      state.detailReads.push(url.search);
      if (state.detailMode === 'slow') await state.detailGate?.promise;
      if (state.detailMode === 'error') {
        await fulfillJSON(
          route,
          { error: { code: 'fixture_detail_error', message: 'Synthetic detail error.' } },
          200,
        );
        return;
      }
      if (state.detailMode === 'forbidden') {
        await fulfillJSON(
          route,
          { error: { code: 'forbidden', message: 'Synthetic capability loss.' } },
          403,
        );
        return;
      }
      await fulfillJSON(route, donationDetail(Number(donationMatch[1]) - 1));
      return;
    }
    const donationKeysMatch = path.match(
      new RegExp('^' + config.root + '/donations/([1-9][0-9]*)/keys$'),
    );
    if (donationKeysMatch) {
      const index = Number(donationKeysMatch[1]) - 1;
      await fulfillJSON(route, numberedPage([keyForDonation(index)], url.searchParams));
      return;
    }
    if (path === config.root + '/charity-models') {
      state.modelReads.push(url.search);
      await fulfillJSON(route, numberedPage(models, url.searchParams));
      return;
    }
    const modelMatch = path.match(new RegExp('^' + config.root + '/charity-models/([1-9][0-9]*)$'));
    if (modelMatch) {
      state.detailReads.push(url.search);
      if (state.detailMode === 'slow') await state.detailGate?.promise;
      if (state.detailMode === 'error') {
        await fulfillJSON(
          route,
          { error: { code: 'fixture_detail_error', message: 'Synthetic detail error.' } },
          500,
        );
        return;
      }
      if (state.detailMode === 'forbidden') {
        await fulfillJSON(
          route,
          { error: { code: 'forbidden', message: 'Synthetic capability loss.' } },
          403,
        );
        return;
      }
      await fulfillJSON(route, model(Number(modelMatch[1]) - 1));
      return;
    }
    if (path.match(new RegExp('^' + config.root + '/charity-models/[1-9][0-9]*/bindings$'))) {
      await fulfillJSON(route, { bindings: [], binding_revision: '0' });
      return;
    }
    if (
      path.match(new RegExp('^' + config.root + '/charity-models/[1-9][0-9]*/binding-candidates$'))
    ) {
      await fulfillJSON(route, numberedPage([], url.searchParams));
      return;
    }
    if (path === config.root + '/donation-sources') {
      state.sourceReads.push(url.search);
      await fulfillJSON(route, numberedPage(sources, url.searchParams));
      return;
    }
    if (path === '/api/steward/logs') {
      await fulfillJSON(route, numberedPage([], url.searchParams));
      return;
    }
    const sourceKeysMatch = path.match(
      new RegExp('^' + config.root + '/donation-sources/(dsg_[A-Za-z0-9_-]{43})/keys$'),
    );
    if (sourceKeysMatch) {
      state.sourceKeyReads.push(url.search);
      const index = sources.findIndex(
        (entry) => entry.source_key === decodeURIComponent(sourceKeysMatch[1]),
      );
      if (index < 0) {
        await fulfillJSON(route, { error: { code: 'not_found', message: 'Unknown source.' } }, 404);
        return;
      }
      await fulfillJSON(route, numberedPage([sourceKeyPageItem(index)], url.searchParams));
      return;
    }
    await route.fallback();
  });
}

function donationListRow(page: Page, id = DETAIL_DONATION_ID): Locator {
  return page.locator('.ops-table tbody tr').filter({
    has: page.getByText(id, { exact: true }),
  });
}

function modelListRow(page: Page, id = DETAIL_MODEL_ID): Locator {
  return page.locator('.ops-table tbody tr').filter({
    has: page.getByText('[公益]DetailProvider/detail-model-' + id, { exact: true }),
  });
}

function sourceListItem(page: Page): Locator {
  return page.locator('.charity-source-browser__source').filter({
    hasText: 'detail-source-' + (DETAIL_SOURCE_INDEX + 1) + '.example.test',
  });
}

async function assertDetailFocusAndViewport(
  page: Page,
  target: Locator,
  options: { requireFocus?: boolean; requireViewport?: boolean } = {},
): Promise<void> {
  await expect(target).toBeVisible();
  if (options.requireFocus !== false) {
    await expect
      .poll(async () =>
        target.evaluate((element) => {
          const rect = element.getBoundingClientRect();
          return {
            focused: document.activeElement === element,
            top: rect.top,
            width: rect.width,
            right: rect.right,
            viewport: innerWidth,
          };
        }),
      )
      .toMatchObject({ focused: true });
  }
  const metrics = await target.evaluate((element) => {
    const rect = element.getBoundingClientRect();
    return {
      top: rect.top,
      right: rect.right,
      viewportWidth: innerWidth,
      viewportHeight: innerHeight,
    };
  });
  if (options.requireViewport !== false) {
    expect(metrics.top, 'detail top must be in the viewport').toBeGreaterThanOrEqual(-2);
    expect(metrics.top, 'detail top must enter the viewport after navigation').toBeLessThan(
      metrics.viewportHeight,
    );
  }
  expect(metrics.right, 'detail must fit the viewport').toBeLessThanOrEqual(
    metrics.viewportWidth + 1,
  );
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
}

async function assertListButtonFocus(button: Locator): Promise<void> {
  await expect
    .poll(async () => button.evaluate((element) => document.activeElement === element))
    .toBe(true);
}

async function assertURLState(
  page: Page,
  expected: { pageParam: string; pageSizeParam: string; page: string; pageSize: string },
): Promise<void> {
  const params = new URL(page.url()).searchParams;
  expect(params.get(expected.pageParam)).toBe(expected.page);
  expect(params.get(expected.pageSizeParam)).toBe(expected.pageSize);
}

async function saveScreenshot(page: Page, name: string): Promise<void> {
  if (!EVIDENCE_DIR) return;
  await mkdir(EVIDENCE_DIR, { recursive: true });
  await page.screenshot({ path: resolve(EVIDENCE_DIR, name + '.png'), fullPage: true });
  await page.screenshot({ path: resolve(EVIDENCE_DIR, name + '-viewport.png') });
}

async function assertStationClean(page: Page, setup: StationSetup): Promise<void> {
  await assertNoSensitiveBrowserPersistence(page, [setup.marker]);
  setup.consoleGuard.assertNone();
}

async function exerciseDonationNavigation(
  page: Page,
  setup: StationSetup,
  state: FixtureState,
): Promise<void> {
  const path =
    setup.station === 'admin'
      ? '/charity?handling=pending&q=reviewable&donations_page=2&donations_page_size=10'
      : '/steward?tab=charity&charity_section=donations&handling=pending&q=reviewable&donations_page=2&donations_page_size=10';
  await page.goto(setup.origin + path);
  const row = donationListRow(page);
  const openButton = row.getByRole('button', { name: 'Review', exact: true });
  await expect(openButton).toBeVisible();
  expect(
    await openButton.evaluate((button) => {
      const range = document.createRange();
      range.selectNodeContents(button);
      return range.getClientRects().length;
    }),
    'the short review label must stay on one line',
  ).toBe(1);
  await openButton.click();
  await expect
    .poll(() => new URL(page.url()).searchParams.get('donation_id'))
    .toBe(DETAIL_DONATION_ID);
  await expect(
    page.getByRole('heading', { name: 'Donation #' + DETAIL_DONATION_ID }),
  ).toBeVisible();
  const detailTarget = page.locator('.ops-detail-target');
  await expect(detailTarget).toHaveCount(1);
  await assertDetailFocusAndViewport(page, detailTarget);
  await assertURLState(page, {
    pageParam: 'donations_page',
    pageSizeParam: 'donations_page_size',
    page: '2',
    pageSize: String(PAGE_SIZE),
  });
  expect(state.listReads).toContain('?handling=pending&q=reviewable&page=2&page_size=10');
  expect(state.detailReads.length).toBeGreaterThanOrEqual(1);
  await saveScreenshot(page, setup.station + '-donation-detail-1280-light-en');

  await page.reload();
  await expect(
    page.getByRole('heading', { name: 'Donation #' + DETAIL_DONATION_ID }),
  ).toBeVisible();
  // A reload must preserve the selected detail and focus. The browser keeps its
  // scroll restoration position, so viewport entry is asserted on the actual
  // list-to-detail actions below rather than treating refresh as a new action.
  await assertDetailFocusAndViewport(page, page.locator('.ops-detail-target'), {
    requireViewport: false,
  });

  await page.goBack();
  await expect(page).toHaveURL(/donations_page=2/);
  const mobileListButton = donationListRow(page).getByRole('button', {
    name: 'Review',
    exact: true,
  });
  await expect(mobileListButton).toBeVisible();
  await page.setViewportSize({ width: 390, height: 844 });
  await mobileListButton.click();
  await expect(
    page.getByRole('heading', { name: 'Donation #' + DETAIL_DONATION_ID }),
  ).toBeVisible();
  const mobileDetailTarget = page.locator('.ops-detail-target');
  await expect(mobileDetailTarget).toHaveCount(1);
  await assertDetailFocusAndViewport(page, mobileDetailTarget);
  await saveScreenshot(page, setup.station + '-donation-detail-390-light-en');

  await page.goBack();
  await expect(page).toHaveURL(/donations_page=2/);
  const returnedButton = donationListRow(page).getByRole('button', {
    name: 'Review',
    exact: true,
  });
  await expect(returnedButton).toBeVisible();
  await assertListButtonFocus(returnedButton);
  await assertURLState(page, {
    pageParam: 'donations_page',
    pageSizeParam: 'donations_page_size',
    page: '2',
    pageSize: String(PAGE_SIZE),
  });

  await page.goForward();
  await expect(
    page.getByRole('heading', { name: 'Donation #' + DETAIL_DONATION_ID }),
  ).toBeVisible();
  await assertDetailFocusAndViewport(page, page.locator('.ops-detail-target'));
  await assertURLState(page, {
    pageParam: 'donations_page',
    pageSizeParam: 'donations_page_size',
    page: '2',
    pageSize: String(PAGE_SIZE),
  });
  await assertStationClean(page, setup);
}

type DonationArrivalOrder = 'list-first' | 'detail-first';

async function exerciseSourceToDonationNavigation(
  page: Page,
  setup: StationSetup,
  state: FixtureState,
  order: DonationArrivalOrder,
): Promise<void> {
  const path =
    '/charity?charity_section=sources&source_scope=active&sources_page=2&sources_page_size=10' +
    '&donations_page=2&donations_page_size=10';
  await page.goto(setup.origin + path);

  const sourceButton = sourceListItem(page).getByRole('button');
  await expect(sourceButton).toBeVisible();
  await sourceButton.click();
  await expect
    .poll(() => new URL(page.url()).searchParams.get('source_key'))
    .toBe(DETAIL_SOURCE_KEY);
  const sourceDetail = page.locator('.charity-source-browser__keys.ops-detail-target');
  await expect(sourceDetail).toBeVisible();
  await expect(sourceDetail.getByRole('heading', { name: /detail-source-20/ })).toBeVisible();

  const manageKey = sourceDetail.getByRole('button', {
    name: 'Manage key #1019',
    exact: true,
  });
  await expect(manageKey).toBeVisible();
  await manageKey.click();
  await expect
    .poll(() => new URL(page.url()).searchParams.get('donation_id'))
    .toBe(DETAIL_DONATION_ID);
  await expect(page).toHaveURL(/charity_section=donations/);

  const detailTarget = page.locator('.ops-detail-target');
  await expect(detailTarget).toHaveCount(1);
  if (order === 'list-first') {
    await expect.poll(() => state.listReads.length).toBeGreaterThan(0);
    await expect(donationListRow(page)).toBeVisible();
    await expect.poll(() => state.detailReads.length).toBeGreaterThan(0);
    state.detailGate!.release();
  } else {
    await expect.poll(() => state.detailReads.length).toBeGreaterThan(0);
    await expect.poll(() => state.listReads.length).toBeGreaterThan(0);
    await expect(donationListRow(page)).toHaveCount(0);
    await expect(
      detailTarget.getByRole('heading', { name: 'Donation #' + DETAIL_DONATION_ID }),
    ).toBeVisible();
    state.listGate!.release();
  }

  await expect(donationListRow(page)).toBeVisible();
  const donationHeading = detailTarget.getByRole('heading', {
    name: 'Donation #' + DETAIL_DONATION_ID,
  });
  const returnToList = detailTarget.getByRole('button', {
    name: 'Return to list',
    exact: true,
  });
  await expect(donationHeading).toBeVisible();
  await expect(returnToList).toBeVisible();
  await expect(donationHeading).toBeInViewport();
  await expect(returnToList).toBeInViewport();
  await assertDetailFocusAndViewport(page, detailTarget);
  await assertURLState(page, {
    pageParam: 'donations_page',
    pageSizeParam: 'donations_page_size',
    page: '2',
    pageSize: String(PAGE_SIZE),
  });
  expect(state.listReads).toContain('?page=2&page_size=10');

  if (order === 'detail-first') {
    const manualScroll = await page.evaluate(() => {
      window.scrollTo({ top: document.documentElement.scrollHeight, behavior: 'instant' });
      return window.scrollY;
    });
    expect(manualScroll).toBeGreaterThan(0);
    await page.evaluate(
      () =>
        new Promise<void>((resolve) => {
          requestAnimationFrame(() => requestAnimationFrame(() => resolve()));
        }),
    );
    expect(await page.evaluate(() => window.scrollY)).toBe(manualScroll);
  }

  await returnToList.click();
  await expect(page).toHaveURL(/charity_section=sources/);
  await expect(page).not.toHaveURL(/donation_id=/);
  const returnedByButton = page.locator('.charity-source-browser__keys.ops-detail-target');
  await expect(returnedByButton).toBeVisible();
  await expect(returnedByButton.getByRole('heading', { name: /detail-source-20/ })).toBeVisible();
  await assertDetailFocusAndViewport(page, returnedByButton);
  await assertURLState(page, {
    pageParam: 'sources_page',
    pageSizeParam: 'sources_page_size',
    page: '2',
    pageSize: String(PAGE_SIZE),
  });

  await page.goto(
    setup.origin +
      '/charity?charity_section=donations&donation_id=20&donation_from=sources' +
      '&source_key=' +
      encodeURIComponent(DETAIL_SOURCE_KEY) +
      '&sources_page=2&sources_page_size=10&donations_page=2&donations_page_size=10',
  );
  await expect(page).toHaveURL(/charity_section=donations/);
  await expect(page).toHaveURL(/donation_id=20/);
  await expect(
    page.getByRole('heading', { name: 'Donation #' + DETAIL_DONATION_ID }),
  ).toBeVisible();

  await page.reload();
  await expect(
    page.getByRole('heading', { name: 'Donation #' + DETAIL_DONATION_ID }),
  ).toBeVisible();
  await assertDetailFocusAndViewport(page, page.locator('.ops-detail-target'), {
    requireViewport: false,
  });
  await assertURLState(page, {
    pageParam: 'donations_page',
    pageSizeParam: 'donations_page_size',
    page: '2',
    pageSize: String(PAGE_SIZE),
  });

  await page.goBack();
  await expect(page).toHaveURL(/charity_section=sources/);
  await expect(page).not.toHaveURL(/donation_id=/);
  const returnedSourceDetail = page.locator('.charity-source-browser__keys.ops-detail-target');
  await expect(returnedSourceDetail).toBeVisible();
  await expect(
    returnedSourceDetail.getByRole('heading', { name: /detail-source-20/ }),
  ).toBeVisible();
  await assertDetailFocusAndViewport(page, returnedSourceDetail);
  await assertURLState(page, {
    pageParam: 'sources_page',
    pageSizeParam: 'sources_page_size',
    page: '2',
    pageSize: String(PAGE_SIZE),
  });
  await assertStationClean(page, setup);
}

async function exerciseSourceNavigation(
  page: Page,
  setup: StationSetup,
  state: FixtureState,
): Promise<void> {
  const path =
    setup.station === 'admin'
      ? '/charity?charity_section=sources&source_scope=active&source_q=detail&sources_page=2&sources_page_size=10'
      : '/steward?tab=charity&charity_section=sources&source_scope=active&source_q=detail&sources_page=2&sources_page_size=10';
  await page.goto(setup.origin + path);
  const source = sourceListItem(page);
  const sourceButton = source.getByRole('button');
  await expect(sourceButton).toBeVisible();
  await sourceButton.click();
  await expect
    .poll(() => new URL(page.url()).searchParams.get('source_key'))
    .toBe(DETAIL_SOURCE_KEY);
  const detailTarget = page.locator('.charity-source-browser__keys.ops-detail-target');
  await expect(detailTarget).toBeVisible();
  const bindingFilter = detailTarget.getByRole('combobox', {
    name: 'Binding filter',
    exact: true,
  });
  await expect(bindingFilter).toHaveValue('');
  await expect(bindingFilter.locator('option')).toHaveText([
    'All binding states',
    'Idle only',
    'Bound only',
  ]);
  await expect(detailTarget.getByRole('heading', { name: /detail-source-20/ })).toBeVisible();
  await assertDetailFocusAndViewport(page, detailTarget);
  await assertURLState(page, {
    pageParam: 'sources_page',
    pageSizeParam: 'sources_page_size',
    page: '2',
    pageSize: String(PAGE_SIZE),
  });
  expect(state.sourceReads).toContain('?q=detail&scope=active&page=2&page_size=10');
  expect(state.sourceKeyReads.length).toBeGreaterThanOrEqual(1);
  await saveScreenshot(page, setup.station + '-source-detail-1280-light-en');

  await page.goBack();
  await expect(page).not.toHaveURL(/source_key=/);
  await page.setViewportSize({ width: 390, height: 844 });
  const mobileSourceButton = sourceListItem(page).getByRole('button');
  await expect(mobileSourceButton).toBeVisible();
  await mobileSourceButton.click();
  await expect
    .poll(() => new URL(page.url()).searchParams.get('source_key'))
    .toBe(DETAIL_SOURCE_KEY);
  await expect(detailTarget).toBeVisible();
  await assertDetailFocusAndViewport(page, detailTarget);
  await saveScreenshot(page, setup.station + '-source-detail-390-light-en');

  await page.goBack();
  await expect(page).not.toHaveURL(/source_key=/);
  const returnedSourceButton = sourceListItem(page).getByRole('button');
  await expect(returnedSourceButton).toBeVisible();
  await assertListButtonFocus(returnedSourceButton);
  await assertURLState(page, {
    pageParam: 'sources_page',
    pageSizeParam: 'sources_page_size',
    page: '2',
    pageSize: String(PAGE_SIZE),
  });
  await page.goForward();
  await expect(detailTarget).toBeVisible();
  await assertDetailFocusAndViewport(page, detailTarget, { requireViewport: false });
  await assertURLState(page, {
    pageParam: 'sources_page',
    pageSizeParam: 'sources_page_size',
    page: '2',
    pageSize: String(PAGE_SIZE),
  });
  await assertStationClean(page, setup);
}

async function exerciseModelNavigation(
  page: Page,
  setup: StationSetup,
  state: FixtureState,
): Promise<void> {
  const path =
    setup.station === 'admin'
      ? '/charity?charity_section=models&model_q=detail&model_enabled=true&models_page=2&models_page_size=10'
      : '/steward?tab=charity&charity_section=models&model_q=detail&model_enabled=true&models_page=2&models_page_size=10';
  await page.goto(setup.origin + path);
  const row = modelListRow(page);
  const manageButton = row.getByRole('button', { name: 'Manage', exact: true });
  await expect(manageButton).toBeVisible();
  await manageButton.click();
  await expect
    .poll(() => new URL(page.url()).searchParams.get('charity_model'))
    .toBe(DETAIL_MODEL_ID);
  const detailTarget = page.locator('.ops-detail-target');
  await expect(detailTarget).toHaveCount(1);
  await expect(
    detailTarget.getByRole('heading', {
      name: '[公益]DetailProvider/detail-model-20',
      exact: true,
    }),
  ).toBeVisible();
  await assertDetailFocusAndViewport(page, detailTarget);
  await assertURLState(page, {
    pageParam: 'models_page',
    pageSizeParam: 'models_page_size',
    page: '2',
    pageSize: String(PAGE_SIZE),
  });
  expect(state.modelReads).toContain('?q=detail&enabled=true&page=2&page_size=10');
  await saveScreenshot(page, setup.station + '-model-detail-1280-light-en');

  await page.goBack();
  await expect(page).not.toHaveURL(/charity_model=/);
  await page.setViewportSize({ width: 390, height: 844 });
  const mobileManageButton = modelListRow(page).getByRole('button', {
    name: 'Manage',
    exact: true,
  });
  await expect(mobileManageButton).toBeVisible();
  await mobileManageButton.click();
  await expect
    .poll(() => new URL(page.url()).searchParams.get('charity_model'))
    .toBe(DETAIL_MODEL_ID);
  await expect(detailTarget).toBeVisible();
  await assertDetailFocusAndViewport(page, detailTarget);
  await saveScreenshot(page, setup.station + '-model-detail-390-light-en');

  await page.goBack();
  await expect(page).not.toHaveURL(/charity_model=/);
  const returnedManageButton = modelListRow(page).getByRole('button', {
    name: 'Manage',
    exact: true,
  });
  await expect(returnedManageButton).toBeVisible();
  await assertListButtonFocus(returnedManageButton);
  await assertURLState(page, {
    pageParam: 'models_page',
    pageSizeParam: 'models_page_size',
    page: '2',
    pageSize: String(PAGE_SIZE),
  });
  await page.goForward();
  await expect(detailTarget).toBeVisible();
  await assertDetailFocusAndViewport(page, detailTarget, { requireViewport: false });
  await assertURLState(page, {
    pageParam: 'models_page',
    pageSizeParam: 'models_page_size',
    page: '2',
    pageSize: String(PAGE_SIZE),
  });
  await assertStationClean(page, setup);
}

function initialState(overrides: Partial<FixtureState> = {}): FixtureState {
  return {
    detailMode: 'ok',
    detailReads: [],
    listReads: [],
    sourceReads: [],
    sourceKeyReads: [],
    modelReads: [],
    ...overrides,
  };
}

for (const station of ['admin', 'user'] as const) {
  test(
    station + ' keeps donation detail reachable across pagination, refresh, and browser navigation',
    async ({ context, page }) => {
      const setup = await prepareStation(context, page, station);
      const state = initialState();
      await installManagementRoutes(page, station, state);
      await exerciseDonationNavigation(page, setup, state);
    },
  );

  test(
    station + ' returns focus from a source-group detail to the paginated source row',
    async ({ context, page }) => {
      const setup = await prepareStation(context, page, station);
      const state = initialState();
      await installManagementRoutes(page, station, state);
      await exerciseSourceNavigation(page, setup, state);
    },
  );

  test(
    station + ' returns focus from a charity-model detail to its paginated row',
    async ({ context, page }) => {
      const setup = await prepareStation(context, page, station);
      const state = initialState();
      await installManagementRoutes(page, station, state);
      await exerciseModelNavigation(page, setup, state);
    },
  );
}

for (const scenario of [
  { order: 'list-first' as const, width: 1_280 },
  { order: 'detail-first' as const, width: 390 },
]) {
  test(`source key to donation detail remains positioned when ${scenario.order} response arrives at ${scenario.width}px`, async ({
    context,
    page,
  }) => {
    const setup = await prepareStation(context, page, 'admin', scenario.width);
    const state = initialState(
      scenario.order === 'list-first'
        ? { detailMode: 'slow', detailGate: detailGate() }
        : { listGate: detailGate() },
    );
    await installManagementRoutes(page, 'admin', state);
    await exerciseSourceToDonationNavigation(page, setup, state, scenario.order);
  });
}

test('semantic donation detail navigation leaves the trigger and enters the viewport', async ({
  context,
  page,
}) => {
  const setup = await prepareStation(context, page, 'admin');
  const state = initialState();
  await installManagementRoutes(page, 'admin', state);
  await page.goto(ADMIN_ORIGIN + '/charity?donations_page=2&donations_page_size=10');
  const openButton = donationListRow(page).getByRole('button', { name: 'Review', exact: true });
  await expect(openButton).toBeVisible();
  await openButton.click();
  const heading = page.getByRole('heading', { name: 'Donation #' + DETAIL_DONATION_ID });
  await expect(heading).toBeVisible();
  const behavior = await page.evaluate(() => {
    const matchingHeading = Array.from(document.querySelectorAll('h1, h2, h3, h4')).find(
      (element) => element.textContent?.trim() === 'Donation #20',
    );
    const rect = matchingHeading?.getBoundingClientRect();
    const active = document.activeElement;
    return {
      headingTop: rect?.top ?? Number.POSITIVE_INFINITY,
      headingBottom: rect?.bottom ?? Number.NEGATIVE_INFINITY,
      viewport: innerHeight,
      focusInList:
        active instanceof HTMLElement && active.closest('tbody')?.contains(active) === true,
    };
  });
  expect(
    behavior.headingTop,
    'semantic detail heading must enter the viewport',
  ).toBeGreaterThanOrEqual(-2);
  expect(behavior.headingTop, 'semantic detail heading must be visible after opening').toBeLessThan(
    behavior.viewport,
  );
  expect(behavior.headingBottom, 'semantic detail heading must have a visible box').toBeGreaterThan(
    behavior.headingTop,
  );
  expect(behavior.focusInList, 'focus must leave the list trigger').toBe(false);
  await assertStationClean(page, setup);
});

test('slow detail loading defers focus until the complete detail layout is ready', async ({
  context,
  page,
}) => {
  const setup = await prepareStation(context, page, 'admin');
  const state = initialState({ detailMode: 'slow', detailGate: detailGate() });
  await installManagementRoutes(page, 'admin', state);
  await page.goto(
    ADMIN_ORIGIN + '/charity?handling=pending&donations_page=2&donations_page_size=10',
  );
  const openButton = donationListRow(page).getByRole('button', { name: 'Review', exact: true });
  await expect(openButton).toBeVisible();
  await openButton.click();
  const detailTarget = page.locator('.ops-detail-target');
  await expect(detailTarget).toHaveCount(1);
  await expect
    .poll(() => detailTarget.evaluate((element) => document.activeElement === element))
    .toBe(false);
  state.detailGate!.release();
  await expect(
    page.getByRole('heading', { name: 'Donation #' + DETAIL_DONATION_ID }),
  ).toBeVisible();
  await assertDetailFocusAndViewport(page, detailTarget);
  await assertStationClean(page, setup);
});

test('detail errors remain reachable and retry can recover the selected resource', async ({
  context,
  page,
}) => {
  const setup = await prepareStation(context, page, 'admin');
  const state = initialState({ detailMode: 'error' });
  await installManagementRoutes(page, 'admin', state);
  await page.goto(ADMIN_ORIGIN + '/charity?donations_page=2&donations_page_size=10');
  await donationListRow(page).getByRole('button', { name: 'Review', exact: true }).click();
  const detailTarget = page.locator('.ops-detail-target');
  await expect(detailTarget).toHaveCount(1);
  await assertDetailFocusAndViewport(page, detailTarget);
  await expect(detailTarget.getByRole('alert')).toBeVisible();
  state.detailMode = 'ok';
  await detailTarget.getByRole('button', { name: 'Retry', exact: true }).click();
  await expect(
    detailTarget.getByRole('heading', { name: 'Donation #' + DETAIL_DONATION_ID }),
  ).toBeVisible();
  await assertDetailFocusAndViewport(page, detailTarget, { requireFocus: false });
  await assertStationClean(page, setup);
});

test('steward detail permission loss clears the visible private projection', async ({
  context,
  page,
}) => {
  const setup = await prepareStation(context, page, 'user');
  const state = initialState({ detailMode: 'forbidden' });
  await installManagementRoutes(page, 'user', state);
  const consoleMessages: { type: string; text: string }[] = [];
  const forbiddenResponses: string[] = [];
  page.on('console', (message) => {
    if (message.type() === 'error' || message.type() === 'warning') {
      consoleMessages.push({ type: message.type(), text: message.text() });
    }
  });
  page.on('response', (response) => {
    const url = new URL(response.url());
    if (response.status() === 403 && url.origin === USER_ORIGIN) {
      forbiddenResponses.push(url.pathname);
    }
  });
  await page.goto(
    USER_ORIGIN +
      '/steward?tab=charity&charity_section=donations&donations_page=2&donations_page_size=10',
  );
  await donationListRow(page).getByRole('button', { name: 'Review', exact: true }).click();
  await expect.poll(() => state.detailReads.length).toBeGreaterThan(0);
  await expect(page).toHaveURL(/tab=logs/);
  await expect(page.getByRole('heading', { name: 'Donation #' + DETAIL_DONATION_ID })).toHaveCount(
    0,
  );
  await expect(page.getByText('Synthetic detail note', { exact: true })).toHaveCount(0);
  await assertNoSensitiveBrowserPersistence(page, [setup.marker]);
  expect(forbiddenResponses).toEqual(['/api/steward/donations/20']);
  expect(consoleMessages).toHaveLength(1);
  expect(consoleMessages[0]).toMatchObject({ type: 'error' });
  expect(consoleMessages[0].text).toMatch(/Failed to load resource:.*403/);
});
