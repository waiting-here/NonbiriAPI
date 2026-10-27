import { mkdir, readFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { expect, test, type Page } from './test';
import { collectConsoleViolations, mockPublicConfig, mockRoleSession } from './support';
import { ADMIN_ORIGIN } from './ports';
import { gachaSnapshot } from '../fixtures/gacha';
import { statisticsPage } from '../fixtures/gacha-statistics';
import { shopStatisticsCards, shopStatisticsRarities } from '../fixtures/gacha-shop-statistics';
import en from '../../src/admin/i18n/en.json' with { type: 'json' };
import zh from '../../src/admin/i18n/zh.json' with { type: 'json' };

const longEnglish =
  'Complete mystery-shop card name with Wide WWW lettering and every original word preserved '.repeat(
    2,
  );
const longChinese = '神秘商店完整中文卡牌名称应当在导出图表中完整换行保留'.repeat(3);
const maximum = '340282366920938463463374607431768211455';
const firstCount = (BigInt(maximum) - 8n).toString();
const maximumDisplay = '340,282,366,920,938,463,463,374,607,431,768,211,455';
const exportCards = shopStatisticsCards.map((card, index) =>
  index
    ? card
    : {
        ...card,
        name: longChinese,
        name_en: longEnglish,
        count: firstCount,
        participant_count: firstCount,
        first_acquisition_count: firstCount,
      },
);
const summary = {
  total_redemptions: maximum,
  participant_count: (BigInt(maximum) - 4n).toString(),
};
const rarities = shopStatisticsRarities.map((row, index) =>
  index ? row : { ...row, count: firstCount, first_acquisition_count: firstCount },
);
async function setup(
  page: Page,
  theme: string,
  locale: 'en' | 'zh' = 'en',
  mode: 'normal' | 'fail' | 'empty' = 'normal',
) {
  await page.addInitScript(
    ({ theme, locale }) => {
      localStorage.setItem('nb.theme', theme);
      localStorage.setItem('nb.lang', locale);
    },
    { theme, locale },
  );
  await mockRoleSession(page, 'admin', 'admin');
  await mockPublicConfig(page, 'admin');
  const snapshot = gachaSnapshot();
  await page.route('**/admin/api/gacha', (route) =>
    route.fulfill({
      json: {
        entry_enabled: true,
        rules: snapshot.pools[0]!.rules,
        shop: snapshot.shop,
        pools: [],
        presets: [],
        revision: 1,
        server_time: snapshot.server_time,
        time_zone: 'UTC',
      },
    }),
  );
  const requests: URL[] = [];
  let failures = 0;
  await page.route('**/admin/api/gacha/statistics/shop/**', (route) => {
    const url = new URL(route.request().url());
    requests.push(url);
    const cards = mode === 'empty' ? [] : exportCards;
    const totals = mode === 'empty' ? { total_redemptions: '0', participant_count: '0' } : summary;
    const rarity_totals = mode === 'empty' ? [] : rarities;
    // Exhaust the shared query client's automatic retry before testing manual retry.
    if (mode === 'fail' && failures++ < 2) {
      return route.fulfill({
        status: 503,
        json: { error: { code: 'internal_error', message: 'Shop statistics unavailable' } },
      });
    }
    const q = (url.searchParams.get('q') ?? '').toLowerCase();
    let filtered = cards.filter((card) =>
      (card.name + card.name_en + card.card_id).toLowerCase().includes(q),
    );
    if (url.searchParams.get('sort') === 'name')
      filtered = [...filtered].sort((a, b) => a.name_en.localeCompare(b.name_en));
    return route.fulfill({
      json: {
        summary: totals,
        ...statisticsPage(filtered, url.searchParams),
        rarity_totals,
        top_cards: mode === 'empty' ? [] : exportCards.slice(0, 2),
      },
    });
  });
  await page.goto(ADMIN_ORIGIN + '/activities/gacha?gacha_view=shop-statistics');
  const catalogs = (locale === 'en' ? en : zh).admin;
  await expect(
    page.getByRole('tab', { name: catalogs.gachaShopStats.tab, exact: true }),
  ).toHaveAttribute('aria-selected', 'true');
  return { copy: catalogs.gachaShopStats, common: catalogs.gachaStats, requests };
}
async function capture(page: Page, name: string) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  if (process.env.NONBIRI_VISUAL_DIR) {
    await mkdir(process.env.NONBIRI_VISUAL_DIR, { recursive: true });
    await page.screenshot({
      path: resolve(process.env.NONBIRI_VISUAL_DIR, name + '.png'),
      fullPage: true,
    });
    await page.screenshot({
      path: resolve(process.env.NONBIRI_VISUAL_DIR, name + '-viewport.png'),
    });
  }
}
async function download(page: Page, action: () => Promise<unknown>, filename: string) {
  const waiting = page.waitForEvent('download');
  await action();
  const result = await waiting;
  if (process.env.NONBIRI_VISUAL_DIR) {
    await mkdir(process.env.NONBIRI_VISUAL_DIR, { recursive: true });
    await result.saveAs(resolve(process.env.NONBIRI_VISUAL_DIR, filename));
  }
  return readFile((await result.path())!);
}
for (const theme of ['light', 'dark'])
  for (const width of [1440, 390]) {
    test('shop card statistics and complete exports ' + theme + ' ' + width, async ({ page }) => {
      const guard = collectConsoleViolations(page);
      await page.setViewportSize({ width, height: 900 });
      const { copy, common, requests } = await setup(page, theme);
      const panel = page.getByRole('region', { name: copy.title, exact: true });
      await expect(
        panel.locator('.gacha-stats-summary').getByLabel(maximumDisplay, { exact: true }),
      ).toBeVisible();
      await expect(panel.getByRole('img', { name: copy.rarityChart, exact: true })).toBeVisible();
      const prefix = 'gacha-shop-statistics-' + theme + '-' + width;
      await capture(page, prefix);
      const table = panel.getByRole('table', { name: copy.cardsTitle, exact: true });
      await expect(table.getByRole('columnheader')).toHaveCount(4);
      await expect(
        table.getByRole('columnheader', { name: common.firstOwners, exact: true }),
      ).toHaveCount(0);
      await panel.getByRole('button', { name: 'Next', exact: true }).click();
      await expect(table.getByText('historical-card', { exact: true }).first()).toBeVisible();
      await expect(table.getByText(common.unspecifiedRarity, { exact: true })).toBeVisible();
      await panel.getByRole('searchbox', { name: common.searchCards, exact: true }).fill('silver');
      await panel
        .getByRole('form', { name: common.cardFilters, exact: true })
        .getByRole('button', { name: common.search, exact: true })
        .click();
      await expect(table.getByText('Limited silver moon', { exact: true })).toBeVisible();
      await panel.getByLabel(common.cardOrder, { exact: true }).selectOption('name');
      await expect
        .poll(() =>
          requests.some(
            (url) =>
              url.searchParams.get('q') === 'silver' &&
              url.searchParams.get('page') === '1' &&
              url.searchParams.get('sort') === 'name',
          ),
        )
        .toBe(true);
      // Real attachment responses and complete rows are covered by the audit fixture.
      for (const [format, label] of [
        ['json', common.downloadJSON],
        ['csv', common.downloadCSV],
      ])
        await expect(panel.getByRole('link', { name: label, exact: true })).toHaveAttribute(
          'href',
          '/admin/api/gacha/statistics/shop/export?format=' + format,
        );
      const svg = (
        await download(
          page,
          () => panel.getByRole('button', { name: common.downloadSVG, exact: true }).last().click(),
          prefix + '.svg',
        )
      ).toString('utf8');
      expect(
        await page.evaluate(
          (source) =>
            new DOMParser()
              .parseFromString(source, 'image/svg+xml')
              .querySelector('.chart-row-label')?.textContent,
          svg,
        ),
      ).toBe(longEnglish);
      expect(svg).toContain('width="1600"');
      const png = await download(
        page,
        () => panel.getByRole('button', { name: common.downloadPNG, exact: true }).last().click(),
        prefix + '-chart.png',
      );
      expect(png.readUInt32BE(16)).toBe(1600);
      expect(png.subarray(0, 8)).toEqual(Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]));
      await page.getByRole('tab', { name: 'Limited pools', exact: true }).click();
      await expect(page).not.toHaveURL(/gacha_view=shop-statistics/);
      await page.goBack();
      await expect(page.getByRole('tab', { name: copy.tab, exact: true })).toHaveAttribute(
        'aria-selected',
        'true',
      );
      await expect(
        panel.getByRole('searchbox', { name: common.searchCards, exact: true }),
      ).toHaveValue('silver');
      await page.reload();
      await expect(page.getByRole('tab', { name: copy.tab, exact: true })).toHaveAttribute(
        'aria-selected',
        'true',
      );
      await expect(panel.getByLabel(common.cardOrder, { exact: true })).toHaveValue('name');
      await expect(table.getByText('Limited silver moon', { exact: true })).toBeVisible();
      await panel.scrollIntoViewIfNeeded();
      await capture(page, prefix + '-restored');
      guard.assertNone();
    });
  }
test('Chinese shop statistics retries a read and exports full Chinese names', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 900 });
  const { copy, common } = await setup(page, 'dark', 'zh', 'fail');
  const panel = page.getByRole('region', { name: copy.title, exact: true });
  await expect(panel.getByRole('alert')).toBeVisible();
  await capture(page, 'gacha-shop-statistics-error-zh');
  await panel.getByRole('button', { name: '重试', exact: true }).click();
  await expect(panel.getByRole('table', { name: copy.cardsTitle, exact: true })).toBeVisible();
  const svg = (
    await download(
      page,
      () => panel.getByRole('button', { name: common.downloadSVG, exact: true }).last().click(),
      'gacha-shop-statistics-zh.svg',
    )
  ).toString('utf8');
  expect(
    await page.evaluate(
      (source) =>
        new DOMParser().parseFromString(source, 'image/svg+xml').querySelector('.chart-row-label')
          ?.textContent,
      svg,
    ),
  ).toBe(longChinese);
  const png = await download(
    page,
    () => panel.getByRole('button', { name: common.downloadPNG, exact: true }).last().click(),
    'gacha-shop-statistics-zh-chart.png',
  );
  expect(png.readUInt32BE(16)).toBe(1600);
  await capture(page, 'gacha-shop-statistics-zh');
});
test('an empty shop keeps zero summaries and complete download access', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 900 });
  const { copy, common } = await setup(page, 'light', 'zh', 'empty');
  const panel = page.getByRole('region', { name: copy.title, exact: true });
  await expect(panel.getByRole('heading', { name: copy.noCardsTitle, exact: true })).toBeVisible();
  await expect(panel.getByText(copy.noRedemptions, { exact: true })).toBeVisible();
  await expect(panel.getByRole('link', { name: common.downloadJSON, exact: true })).toBeVisible();
  await capture(page, 'gacha-shop-statistics-empty-zh');
});
