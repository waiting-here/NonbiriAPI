import { mkdir } from 'node:fs/promises';
import { resolve } from 'node:path';
import { expect, test, type Page } from './test';
import { USER_ORIGIN } from './ports';
import {
  assertNoSensitiveBrowserPersistence,
  collectConsoleViolations,
  installURLPersistenceObserver,
  mockJson,
  mockPublicConfig,
  mockRoleSession,
} from './support';

const NOW = 1_800_000_000;
const EVIDENCE_DIR = process.env.NONBIRI_VISUAL_DIR
  ? resolve(process.env.NONBIRI_VISUAL_DIR)
  : null;

type JSONRecord = Record<string, unknown>;
type BrowserContext = Parameters<typeof installURLPersistenceObserver>[0];

interface CatalogFixtureModel extends JSONRecord {
  id: string;
  provider: string;
  model: string;
  full_name: string;
  public_description: string;
  enabled: boolean;
  allowed_levels: number[];
  level_allowed: boolean;
  availability: 'level_denied' | 'no_usable_key' | 'available';
  currently_available: boolean;
}

interface CatalogFixture {
  readonly models: readonly CatalogFixtureModel[];
  readonly requests: string[];
}

const MODEL_DEFINITIONS: ReadonlyArray<
  readonly [string, readonly number[], boolean, boolean, string]
> = [
  ['1', [1, 2, 3, 4, 5], true, true, 'alpha'],
  ['2', [1, 3, 5], true, true, 'beta'],
  ['3', [3], false, true, 'denied-available'],
  ['4', [3], false, false, 'denied-unavailable'],
  ['5', [1, 2], true, false, 'unavailable'],
  ['6', [1, 4, 5], true, true, 'gamma'],
  ['7', [1, 3], true, true, 'needle-available'],
  ['8', [2, 4], false, true, 'delta-denied'],
  ['9', [1, 3, 4], true, false, 'needle-unavailable'],
  ['10', [1, 2, 3], true, true, 'epsilon'],
  ['11', [1, 5], true, true, 'zeta'],
  ['12', [2], false, true, 'eta-denied'],
  ['13', [1, 2, 3], true, true, 'theta'],
  ['14', [1, 3], true, true, 'iota'],
  ['15', [1, 4], true, true, 'kappa'],
  ['16', [1, 2], true, false, 'lambda'],
  ['17', [3, 5], false, true, 'mu-denied'],
  ['18', [3, 5], false, false, 'nu-denied'],
  ['19', [1, 3, 5], true, true, 'xi'],
  ['20', [1, 2, 4], true, true, 'omicron'],
  ['21', [2], false, true, 'pi-denied'],
  ['22', [1, 2, 3], true, false, 'rho'],
  ['23', [1, 3, 4], true, true, 'sigma'],
  ['24', [1, 5], true, true, 'tau'],
  ['25', [1, 2, 3, 4, 5], true, true, 'upsilon'],
  ['26', [3], false, true, 'phi-denied'],
];

function capabilityModel(id: string, model: string): JSONRecord {
  return {
    id,
    provider: 'provider',
    model,
    full_name: `[公益]provider/${model}`,
    pricing: {
      mode: 'per_request',
      user_price_milli: '3000',
      discounted_user_price_milli: '2400',
      user_prices_milli: null,
      discounted_user_prices_milli: null,
    },
    discount: {
      enabled: true,
      percent: 80,
      start_at: NOW - 60,
      end_at: NOW + 3_600,
    },
  };
}

function catalogModel(
  id: string,
  allowedLevels: readonly number[],
  levelAllowed: boolean,
  currentlyAvailable: boolean,
  name: string,
): CatalogFixtureModel {
  if (levelAllowed !== allowedLevels.includes(1))
    throw new Error('Catalog fixture must match the L1 session.');
  return {
    ...capabilityModel(id, `model-${id.padStart(2, '0')}`),
    public_description: `Fixture ${name} public description`,
    enabled: true,
    allowed_levels: [...allowedLevels],
    level_allowed: levelAllowed,
    availability: levelAllowed
      ? currentlyAvailable
        ? 'available'
        : 'no_usable_key'
      : 'level_denied',
    currently_available: currentlyAvailable,
  } as CatalogFixtureModel;
}

function createCatalogFixture(): CatalogFixture {
  return {
    models: MODEL_DEFINITIONS.map(([id, levels, allowed, available, name]) =>
      catalogModel(id, levels, allowed, available, name),
    ),
    requests: [],
  };
}

function catalogCapability(fixture: CatalogFixture): JSONRecord {
  return {
    state: 'available',
    models: fixture.models.map(({ id, provider, model, full_name, pricing, discount }) => ({
      id,
      provider,
      model,
      full_name,
      pricing,
      discount,
    })),
    donation_intake: 'open',
    server_now: NOW,
  };
}

function matchesCatalogRequest(model: CatalogFixtureModel, url: URL): boolean {
  const query = url.searchParams.get('q');
  if (
    query &&
    !`${model.full_name} ${model.public_description}`.toLowerCase().includes(query.toLowerCase())
  ) {
    return false;
  }
  const allowedForMe = url.searchParams.get('allowed_for_me');
  if (allowedForMe === 'true' && !model.level_allowed) return false;
  if (allowedForMe === 'false' && model.level_allowed) return false;
  const allowedLevel = url.searchParams.get('allowed_level');
  if (allowedLevel && !model.allowed_levels.includes(Number(allowedLevel))) return false;
  const currentlyAvailable = url.searchParams.get('currently_available');
  if (currentlyAvailable === 'true' && !model.currently_available) return false;
  if (currentlyAvailable === 'false' && model.currently_available) return false;
  return true;
}

function catalogPage(fixture: CatalogFixture, url: URL): JSONRecord {
  const pageSize = Number(url.searchParams.get('page_size') ?? '20');
  const requestedPage = Number(url.searchParams.get('page') ?? '1');
  const filtered = fixture.models.filter((model) => matchesCatalogRequest(model, url));
  const totalPages = Math.max(1, Math.ceil(filtered.length / pageSize));
  const page = Math.min(requestedPage, totalPages);
  const start = (page - 1) * pageSize;
  return {
    models: filtered.slice(start, start + pageSize),
    pagination: {
      page: String(page),
      page_size: pageSize,
      total_items: String(filtered.length),
      total_pages: String(totalPages),
    },
    donation_intake: 'open',
    server_now: NOW,
  };
}

async function installCatalogFixture(
  context: BrowserContext,
  page: Page,
  fixture: CatalogFixture,
  locale: 'en' | 'zh',
  theme: 'light' | 'dark',
  width: number,
  forbiddenToken: string,
) {
  const consoleGuard = collectConsoleViolations(page);
  await installURLPersistenceObserver(context, [forbiddenToken]);
  await page.setViewportSize({ width, height: 900 });
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.addInitScript(
    ({ selectedLocale, selectedTheme }) => {
      localStorage.setItem('nb.lang', selectedLocale);
      localStorage.setItem('nb.theme', selectedTheme);
    },
    { selectedLocale: locale, selectedTheme: theme },
  );
  await mockPublicConfig(page, 'user');
  await mockRoleSession(page, 'user', 'user');
  await mockJson(page, {
    origin: USER_ORIGIN,
    method: 'GET',
    path: '/api/donations?limit=100',
    body: { data: [], next_cursor: null },
  });
  await page.route('**/*', async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (
      request.method() !== 'GET' ||
      url.origin !== USER_ORIGIN ||
      url.pathname !== '/api/charity/models'
    ) {
      await route.fallback();
      return;
    }
    if (url.searchParams.get('view') !== 'catalog') {
      await route.fulfill({
        status: 200,
        headers: { 'content-type': 'application/json', 'cache-control': 'no-store' },
        body: JSON.stringify(catalogCapability(fixture)),
      });
      return;
    }
    fixture.requests.push(`${url.pathname}${url.search}`);
    await route.fulfill({
      status: 200,
      headers: { 'content-type': 'application/json', 'cache-control': 'no-store' },
      body: JSON.stringify(catalogPage(fixture, url)),
    });
  });
  return { consoleGuard, forbiddenToken, locale, theme };
}

async function saveScreenshot(page: Page, name: string): Promise<void> {
  if (!EVIDENCE_DIR) return;
  await mkdir(EVIDENCE_DIR, { recursive: true });
  await page.screenshot({ path: resolve(EVIDENCE_DIR, `${name}.png`) });
}

async function assertAppliedPresentation(page: Page, count: number): Promise<void> {
  const chips = page.locator('.economy-catalog-filters .nb-chip');
  await expect(chips).toHaveCount(count);
  for (const chip of await chips.all()) {
    await expect(chip).toBeVisible();
    await expect(chip.getByRole('button')).toHaveAccessibleName(/Remove|移除/);
    const box = await chip.getByRole('button').boundingBox();
    expect(box && box.width >= 40 && box.height >= 40).toBeTruthy();
  }
}

async function openFilters(page: Page) {
  const disclosure = page.locator('.economy-catalog-filters .nb-filter__more');
  if (!(await disclosure.evaluate((element) => element.hasAttribute('open')))) {
    await disclosure.locator('summary').click();
  }
}

async function assertNoHorizontalOverflow(page: Page): Promise<void> {
  const layout = await page.evaluate(() => ({
    viewport: innerWidth,
    documentWidth: document.documentElement.scrollWidth,
    bodyWidth: document.body.scrollWidth,
  }));
  expect(layout.documentWidth, JSON.stringify(layout)).toBeLessThanOrEqual(layout.viewport);
  expect(layout.bodyWidth, JSON.stringify(layout)).toBeLessThanOrEqual(layout.viewport);
}

for (const locale of ['zh', 'en'] as const) {
  test(`catalog spans the content area and moves the charity board to donations ${locale}`, async ({
    context,
    page,
  }) => {
    const fixture = createCatalogFixture();
    const setup = await installCatalogFixture(
      context,
      page,
      fixture,
      locale,
      locale === 'zh' ? 'dark' : 'light',
      1440,
      `catalog-layout-${locale}-ephemeral`,
    );
    await page.goto(`${USER_ORIGIN}/charity?allowed_for_me=all&currently_available=all`);
    await expect(page.locator('.charity-model-name')).toHaveCount(20);
    const catalog = page.locator('.economy-catalog-card');
    const summary = page.locator('.economy-catalog-pagination');
    for (const width of [320, 390, 768, 1440, 1920, 2560, 3766]) {
      await page.setViewportSize({ width, height: 1000 });
      await assertNoHorizontalOverflow(page);
      const box = (await catalog.boundingBox())!;
      const notice = (await page.locator('.charity-catalog-workspace > .nb-fold').boundingBox())!;
      expect(notice.y + notice.height).toBeLessThanOrEqual(box.y);
      expect(Math.abs(notice.width - box.width)).toBeLessThan(2);
      await expect(page.locator('.progression-ranking')).toBeHidden();
      const list = (await page.locator('.economy-catalog-results > .nb-table-wrap').boundingBox())!;
      expect(list.x + list.width).toBeLessThanOrEqual(box.x + box.width);
      await page.evaluate(() => scrollTo(0, 0));
      if ([390, 1440, 3766].includes(width))
        await saveScreenshot(page, `charity-layout-${locale}-${width}`);
    }
    await summary.getByRole('button', { name: /Next|下一页/ }).click();
    await expect(page.locator('.charity-model-name')).toHaveCount(6);
    await expect(summary).toContainText('26');
    await expect(summary.getByRole('button', { name: /Next|下一页/ })).toBeDisabled();
    setup.consoleGuard.assertNone();
  });
}

test('catalog filters use a counted complete sample and restore URL-backed state', async ({
  context,
  page,
}) => {
  const fixture = createCatalogFixture();
  const setup = await installCatalogFixture(
    context,
    page,
    fixture,
    'en',
    'light',
    1_280,
    'catalog-filters-functional-ephemeral',
  );
  await page.goto(`${USER_ORIGIN}/charity`);

  const level = page.getByRole('combobox', { name: 'Allowed for level', exact: true });
  const access = page.getByRole('combobox', { name: 'Your access', exact: true });
  const availability = page.getByRole('combobox', {
    name: 'Currently available',
    exact: true,
  });
  const search = page.getByRole('searchbox', { name: 'Search model name or public description' });
  const pageSize = page.getByRole('combobox', { name: 'Items per page', exact: true });
  const clear = page.getByRole('button', { name: 'Clear filters', exact: true });
  const defaults = async () => {
    await openFilters(page);
    await level.selectOption('all');
    await access.selectOption('true');
    await availability.selectOption('true');
  };
  await openFilters(page);

  await expect(page.locator('.charity-model-name')).toHaveCount(14);
  await expect(level).toHaveValue('all');
  await expect(access).toHaveValue('true');
  await expect(availability).toHaveValue('true');
  await assertAppliedPresentation(page, 2);
  expect(fixture.requests).toContain(
    '/api/charity/models?view=catalog&page=1&page_size=20&allowed_for_me=true&currently_available=true',
  );

  await level.selectOption('3');
  await expect(page.locator('.charity-model-name')).toHaveCount(9);
  await expect(access).toHaveValue('true');
  await expect(availability).toHaveValue('true');
  await expect(fixture.requests.at(-1)).toBe(
    '/api/charity/models?view=catalog&page=1&page_size=20&allowed_for_me=true&allowed_level=3&currently_available=true',
  );

  await access.selectOption('false');
  await expect(page.getByText('[公益]provider/model-03', { exact: true })).toBeVisible();
  await expect(page.locator('.charity-model-name')).toHaveCount(3);
  await expect(level).toHaveValue('3');
  await expect(availability).toHaveValue('true');
  await expect(fixture.requests.at(-1)).toBe(
    '/api/charity/models?view=catalog&page=1&page_size=20&allowed_for_me=false&allowed_level=3&currently_available=true',
  );

  await availability.selectOption('false');
  await expect(page.getByText('[公益]provider/model-04', { exact: true })).toBeVisible();
  await expect(page.locator('.charity-model-name')).toHaveCount(2);
  await expect(level).toHaveValue('3');
  await expect(access).toHaveValue('false');
  await expect(fixture.requests.at(-1)).toBe(
    '/api/charity/models?view=catalog&page=1&page_size=20&allowed_for_me=false&allowed_level=3&currently_available=false',
  );

  await clear.click();
  await expect(level).toHaveValue('all');
  await expect(access).toHaveValue('all');
  await expect(availability).toHaveValue('all');
  await assertAppliedPresentation(page, 0);
  await defaults();
  await expect(page.locator('.charity-model-name')).toHaveCount(14);
  await expect(level).toHaveValue('all');
  await expect(access).toHaveValue('true');
  await expect(availability).toHaveValue('true');
  await assertAppliedPresentation(page, 2);

  await level.selectOption('3');
  await expect(page.locator('.charity-model-name')).toHaveCount(9);
  await level.selectOption('all');
  await expect(access).toHaveValue('true');
  await expect(availability).toHaveValue('true');
  await expect(page.locator('.charity-model-name')).toHaveCount(14);
  await access.selectOption('all');
  await expect(page.locator('.charity-model-name')).toHaveCount(20);
  await availability.selectOption('all');
  await expect(page.locator('.charity-model-name')).toHaveCount(20);
  await expect(page.getByText('26 items', { exact: true })).toBeVisible();
  expect(fixture.requests).toContain('/api/charity/models?view=catalog&page=1&page_size=20');

  await defaults();
  await expect(page.locator('.charity-model-name')).toHaveCount(14);
  await search.fill('needle');
  await search.press('Enter');
  await expect(page.getByText('[公益]provider/model-07', { exact: true })).toBeVisible();
  await expect(page.locator('.charity-model-name')).toHaveCount(1);
  await expect(page).toHaveURL(/q=needle/);
  const searchURL = page.url();
  await page.goBack();
  await expect(page).not.toHaveURL(/q=needle/);
  await expect(search).toHaveValue('');
  await expect(page.locator('.charity-model-name')).toHaveCount(14);
  await page.goForward();
  await expect(page).toHaveURL(searchURL);
  await expect(page.getByText('[公益]provider/model-07', { exact: true })).toBeVisible();
  await page.reload();
  await expect(page.getByText('[公益]provider/model-07', { exact: true })).toBeVisible();

  await search.fill('');
  await search.press('Enter');
  await expect(page).not.toHaveURL(/q=needle/);
  await expect(search).toHaveValue('');
  await expect(page.locator('.charity-model-name')).toHaveCount(14);
  await openFilters(page);
  await access.selectOption('all');
  await expect(page.locator('.charity-model-name')).toHaveCount(20);
  await availability.selectOption('all');
  await pageSize.selectOption('10');
  await expect(page.locator('.charity-model-name')).toHaveCount(10);
  await expect(page.getByText('26 items', { exact: true })).toBeVisible();
  await page
    .getByRole('navigation', { name: 'Pagination', exact: true })
    .getByRole('button', { name: 'Next', exact: true })
    .click();
  await expect(page.getByText('[公益]provider/model-11', { exact: true })).toBeVisible();
  await expect(page).toHaveURL(/page=2&page_size=10/);
  expect(fixture.requests.at(-1)).toBe('/api/charity/models?view=catalog&page=2&page_size=10');
  await page.reload();
  await expect(page.getByText('[公益]provider/model-11', { exact: true })).toBeVisible();
  await page.goBack();
  await expect(page.getByText('[公益]provider/model-01', { exact: true })).toBeVisible();
  await expect(page).toHaveURL(/page=1&page_size=10/);
  await page.goForward();
  await expect(page.getByText('[公益]provider/model-11', { exact: true })).toBeVisible();

  await assertNoHorizontalOverflow(page);
  await assertNoSensitiveBrowserPersistence(page, [setup.forbiddenToken]);
  setup.consoleGuard.assertNone();
});

for (const locale of ['en', 'zh'] as const) {
  for (const theme of ['light', 'dark'] as const) {
    for (const width of [390, 768, 1440] as const) {
      test(`catalog applied filters remain readable at ${width}px (${locale}, ${theme})`, async ({
        context,
        page,
      }) => {
        const fixture = createCatalogFixture();
        const setup = await installCatalogFixture(
          context,
          page,
          fixture,
          locale,
          theme,
          width,
          `catalog-filters-${locale}-${theme}-${width}-ephemeral`,
        );
        await page.goto(`${USER_ORIGIN}/charity`);

        const copy =
          locale === 'zh'
            ? {
                level: '某等级可访问',
                access: '本人访问权限',
                availability: '当前是否可用',
                applied: '已应用',
              }
            : {
                level: 'Allowed for level',
                access: 'Your access',
                availability: 'Currently available',
                applied: 'Applied',
              };
        const level = page.getByRole('combobox', { name: copy.level, exact: true });
        const access = page.getByRole('combobox', { name: copy.access, exact: true });
        const availability = page.getByRole('combobox', {
          name: copy.availability,
          exact: true,
        });
        await expect(page.locator('.charity-model-name').first()).toBeVisible();
        await openFilters(page);
        await expect(level).toHaveValue('all');
        await expect(access).toHaveValue('true');
        await expect(availability).toHaveValue('true');
        await assertAppliedPresentation(page, 2);

        await level.selectOption('3');
        await expect(level).toHaveValue('3');
        await assertAppliedPresentation(page, 3);
        await assertNoHorizontalOverflow(page);
        await expect(page.locator('html')).toHaveAttribute(
          'lang',
          locale === 'zh' ? 'zh-CN' : 'en',
        );
        await expect(page.locator('html')).toHaveAttribute('data-theme', theme);
        await saveScreenshot(page, `charity-catalog-${locale}-${theme}-${width}`);
        await assertNoSensitiveBrowserPersistence(page, [setup.forbiddenToken]);
        setup.consoleGuard.assertNone();
      });
    }
  }
}

for (const [width, locale, theme] of [
  [1440, 'en', 'light'],
  [768, 'en', 'dark'],
  [390, 'zh', 'dark'],
] as const) {
  test(`catalog empty recovery shows mixed prices at ${width} ${locale}`, async ({
    context,
    page,
  }) => {
    const tokenModel = catalogModel('2', [1, 3], true, false, 'token');
    tokenModel.pricing = {
      mode: 'per_token',
      user_price_milli: null,
      discounted_user_price_milli: null,
      user_prices_milli: {
        uncached_input: '1234',
        cache_write_input: '2468',
        cache_read_input: '3702',
        output: '4936',
      },
      discounted_user_prices_milli: {
        uncached_input: '617',
        cache_write_input: '1234',
        cache_read_input: '1851',
        output: '2468',
      },
    };
    tokenModel.discount = { enabled: true, percent: 50, start_at: NOW - 60, end_at: NOW + 3600 };
    const fixture: CatalogFixture = {
      models: [catalogModel('1', [1, 3], true, false, 'request'), tokenModel],
      requests: [],
    };
    const setup = await installCatalogFixture(
      context,
      page,
      fixture,
      locale,
      theme,
      width,
      'mixed-price-ephemeral',
    );
    await page.goto(`${USER_ORIGIN}/charity`);
    const showAll = page.getByRole('button', {
      name: locale === 'en' ? 'Show all models' : '显示全部模型',
      exact: true,
    });
    await expect(showAll).toBeVisible();
    await saveScreenshot(page, `catalog-empty-${width}-${locale}-${theme}`);
    await showAll.click();
    await expect(page.locator('.charity-model-name')).toHaveCount(2);
    const rows = page.locator('.economy-catalog-results tbody tr');
    await expect(rows).toHaveCount(2);
    await expect(rows.nth(0).locator('.charity-prices-compact')).toContainText('2.4');
    await expect(rows.nth(1).locator('.charity-prices-compact')).toContainText('0.617');
    await expect(rows.nth(1).locator('.charity-prices-compact')).toContainText('1.234');
    await expect(rows.nth(1).locator('.charity-prices-compact')).toContainText('1.851');
    await expect(rows.nth(1).locator('.charity-prices-compact')).toContainText('2.468');
    await assertNoHorizontalOverflow(page);
    await page.evaluate(() => scrollTo(0, 0));
    if (EVIDENCE_DIR)
      await page.screenshot({
        path: resolve(EVIDENCE_DIR, `catalog-mixed-${width}-${locale}-${theme}.png`),
        fullPage: true,
      });
    await page.locator('.charity-model-name').nth(1).click();
    await expect(
      page.getByRole('region', { name: tokenModel.full_name, exact: true }),
    ).toBeVisible();
    await assertNoHorizontalOverflow(page);
    setup.consoleGuard.assertNone();
  });
}
