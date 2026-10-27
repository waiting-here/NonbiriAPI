import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { expect, test, type Page } from './test';
import { collectConsoleViolations, mockPublicConfig, mockRoleSession } from './support';
import { ADMIN_ORIGIN } from './ports';
import { gachaSnapshot } from '../fixtures/gacha';
import {
  statisticsCards,
  statisticsPage,
  statisticsPool,
  statisticsRarities,
  removedStatisticsPool,
} from '../fixtures/gacha-statistics';
import en from '../../src/admin/i18n/en.json' with { type: 'json' };
import zh from '../../src/admin/i18n/zh.json' with { type: 'json' };

const longCardEnglish =
  'A complete long card name with Wide WWW lettering and every original word preserved '.repeat(2);
const longCardChinese = '完整中文卡牌名称应当在导出图表中完整换行展示'.repeat(3);
const longPoolEnglish =
  'Permanent pool with its complete original period and descriptive name '.repeat(2);
const longPoolChinese = '完整奖池名称与原始期次在导出文件中保留'.repeat(4);
const hugeTotal = '340282366920938463463374607431768211455';
const hugeCardCount = (BigInt(hugeTotal) - 993n).toString();
const hugeDisplay = '340,282,366,920,938,463,463,374,607,431,768,211,455';
const hugeCardDisplay = '340,282,366,920,938,463,463,374,607,431,768,210,462';
const exportRarities = statisticsRarities.map((row, index) =>
  index === 0 ? { ...row, count: hugeCardCount } : row,
);
const exportCards = statisticsCards.map((card, index) =>
  index === 0
    ? { ...card, name: longCardChinese, name_en: longCardEnglish, count: hugeCardCount }
    : card,
);
const exportPool = {
  ...statisticsPool,
  name: longPoolChinese,
  name_en: longPoolEnglish,
  total_draws: hugeTotal,
};
async function setup(page: Page, theme: string, locale: 'en' | 'zh' = 'en', failCards = false) {
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
  await page.route('**/admin/api/gacha/statistics/**', async (route) => {
    const url = new URL(route.request().url());
    requests.push(url);
    if (url.pathname.endsWith('/pools')) {
      const q = (url.searchParams.get('q') ?? '').toLowerCase();
      const pools = [exportPool, removedStatisticsPool].filter((pool) =>
        (pool.name + pool.name_en + pool.id).toLowerCase().includes(q),
      );
      return route.fulfill({ json: statisticsPage(pools, url.searchParams) });
    }
    const removed = url.pathname.includes('/gp_removed/');
    const pool = removed ? removedStatisticsPool : exportPool;
    const all = removed
      ? [
          {
            ...exportCards[0]!,
            rarity: null,
            count: '0',
            participant_count: '0',
            first_acquisition_count: '0',
          },
        ]
      : exportCards;
    const rarities = removed
      ? [{ rarity: null, count: '0', first_acquisition_count: '0' }]
      : exportRarities;
    if (url.pathname.endsWith('/cards')) {
      // Exhaust the shared query client's automatic retry before testing manual retry.
      if (failCards && failures++ < 2) {
        return route.fulfill({
          status: 503,
          json: { error: { code: 'internal_error', message: 'Statistics unavailable' } },
        });
      }
      const q = (url.searchParams.get('q') ?? '').toLowerCase();
      const filtered = all.filter((card) =>
        (card.name + card.name_en + card.card_id).toLowerCase().includes(q),
      );
      return route.fulfill({
        json: {
          pool,
          ...statisticsPage(filtered, url.searchParams),
          rarity_totals: rarities,
          top_cards: removed ? [] : exportCards.slice(0, 2),
        },
      });
    }
    return route.fallback();
  });
  await page.goto(ADMIN_ORIGIN + '/activities/gacha');
  const copy = (locale === 'en' ? en : zh).admin.gachaStats;
  await page.getByRole('tab', { name: copy.tab, exact: true }).click();
  return { copy, requests };
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
for (const theme of ['light', 'dark'])
  for (const width of [1440, 390]) {
    test(
      'administrator pool charts, table and complete exports ' + theme + ' ' + width,
      async ({ page }) => {
        const guard = collectConsoleViolations(page);
        await page.setViewportSize({ width, height: 900 });
        const { copy, requests } = await setup(page, theme);
        const details = page.getByRole('region', {
          name: copy.selectedPoolStatistics,
          exact: true,
        });
        await expect(
          details.locator('.gacha-stats-summary').getByLabel(hugeDisplay, { exact: true }),
        ).toBeVisible();
        await expect(
          details.getByRole('img', { name: copy.rarityChart, exact: true }),
        ).toBeVisible();
        await details.scrollIntoViewIfNeeded();
        await capture(page, 'gacha-statistics-' + theme + '-' + width);
        await details.getByRole('button', { name: 'Next', exact: true }).click();
        await expect
          .poll(() =>
            requests.some(
              (url) =>
                url.pathname.endsWith('/permanent/cards') && url.searchParams.get('page') === '2',
            ),
          )
          .toBe(true);
        const search = details.getByRole('searchbox', { name: copy.searchCards, exact: true });
        await search.fill('silver');
        await details
          .getByRole('form', { name: copy.cardFilters, exact: true })
          .getByRole('button', { name: copy.search, exact: true })
          .click();
        await expect(
          details
            .getByRole('table', { name: copy.cardsTitle, exact: true })
            .getByText('Silver moon', { exact: true }),
        ).toBeVisible();
        // Native attachment responses are exercised against the real backend in audit.
        // Here, verify that table filtering never enters either complete export URL.
        for (const [format, label] of [
          ['json', copy.downloadJSON],
          ['csv', copy.downloadCSV],
        ])
          await expect(details.getByRole('link', { name: label, exact: true })).toHaveAttribute(
            'href',
            '/admin/api/gacha/statistics/pools/permanent/export?format=' + format,
          );
        const svgWait = page.waitForEvent('download');
        await details.getByRole('button', { name: copy.downloadSVG, exact: true }).last().click();
        const svg = await readFile((await (await svgWait).path())!, 'utf8');
        expect(svg).toContain(hugeCardDisplay);
        expect(
          await page.evaluate(
            (source) =>
              new DOMParser()
                .parseFromString(source, 'image/svg+xml')
                .querySelector('.chart-row-label')?.textContent,
            svg,
          ),
        ).toBe(longCardEnglish);
        expect(
          await page.evaluate(
            (source) =>
              [
                ...new DOMParser()
                  .parseFromString(source, 'image/svg+xml')
                  .querySelectorAll('.chart-pool-name'),
              ]
                .map((node) => node.textContent)
                .join(''),
            svg,
          ),
        ).toBe(longPoolEnglish);
        const pngWait = page.waitForEvent('download');
        await details.getByRole('button', { name: copy.downloadPNG, exact: true }).last().click();
        const png = await readFile((await (await pngWait).path())!);
        expect(png.readUInt32BE(16)).toBe(1600);
        expect(png.subarray(0, 8)).toEqual(Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]));
        if (process.env.NONBIRI_VISUAL_DIR) {
          const prefix = resolve(process.env.NONBIRI_VISUAL_DIR, 'export-' + theme + '-' + width);
          await writeFile(prefix + '.svg', svg);
          await writeFile(prefix + '.png', png);
        }
        guard.assertNone();
      },
    );
  }
test('administrator statistics retry and removed zero pool are readable in Chinese', async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 900 });
  const { copy } = await setup(page, 'dark', 'zh', true);
  const details = page.getByRole('region', { name: copy.selectedPoolStatistics, exact: true });
  await expect(details.getByRole('alert')).toBeVisible();
  await capture(page, 'gacha-statistics-error-zh');
  await details.getByRole('button', { name: '重试', exact: true }).click();
  await expect(details.getByRole('table', { name: copy.cardsTitle, exact: true })).toBeVisible();
  for (const [extension, label] of [
    ['svg', copy.downloadSVG],
    ['png', copy.downloadPNG],
  ]) {
    const pending = page.waitForEvent('download');
    await details.getByRole('button', { name: label, exact: true }).last().click();
    const file = await pending;
    if (process.env.NONBIRI_VISUAL_DIR)
      await file.saveAs(resolve(process.env.NONBIRI_VISUAL_DIR, 'export-zh.' + extension));
  }
  const pools = page.getByRole('table', { name: copy.poolsTitle, exact: true });
  await pools
    .getByRole('row')
    .filter({ hasText: '已结束期次' })
    .getByRole('button', { name: copy.viewCards, exact: true })
    .click();
  await expect(details.getByText(copy.noDraws, { exact: true })).toBeVisible();
  await expect(
    details
      .getByRole('table', { name: copy.cardsTitle, exact: true })
      .getByText(copy.unspecifiedRarity, { exact: true }),
  ).toBeVisible();
  await capture(page, 'gacha-statistics-zero-zh');
});
