import { resolve } from 'node:path';
import { expect, test, type Page } from './test';
import { collectConsoleViolations, mockPublicConfig, mockRoleSession } from './support';
import { ADMIN_ORIGIN } from './ports';
import type { AdminInput, Configuration, Pool } from '../../src/user/activities/gacha/types';

const srCards = ['h-chatgpt', 'h-claude', 'h-glm', 'h-deepseek', 'h-gemini'];
const ssrCards = ['i-chatgpt', 'i-claude', 'i-glm', 'i-deepseek', 'i-gemini'];
const rules: Configuration['rules'] = {
  weights: [5500, 3500, 900, 100],
  sr_pity: 10,
  ssr_pity: 60,
  sr_featured: 5000,
  ssr_featured: 5000,
  sr_guarantee: true,
  ssr_guarantee: true,
};

async function setup(page: Page, theme = 'light') {
  await page.addInitScript((theme) => {
    localStorage.setItem('nb.lang', 'zh');
    localStorage.setItem('nb.theme', theme);
    document.addEventListener(
      'submit',
      () => {
        const root = document.documentElement;
        root.dataset.gachaSubmits = String(Number(root.dataset.gachaSubmits ?? 0) + 1);
      },
      true,
    );
  }, theme);
  await mockRoleSession(page, 'admin', 'admin');
  await mockPublicConfig(page, 'admin');
  const pool: Pool = {
    id: 'gp_summer',
    name: '夏夜花火',
    kind: 'limited',
    cover: 'i',
    group: '',
    rules,
    featured: [...srCards, ...ssrCards],
    starts_at: null,
    ends_at: null,
    paused: false,
    published: false,
    revision: 0,
    rates: [0.55, 0.35, 0.09, 0.01],
  };
  const configuration: Configuration = {
    entry_enabled: true,
    rules,
    shop: {
      permanent_price: '10000',
      limited_price: '10000',
      n_ticket: 20,
      r_ticket: 10,
      sr_ticket: 5,
      ssr_tickets: 1,
      permanent_cap: 20,
      limited_cap: 10,
      sr_card: 10,
      ssr_card: 3,
      sp_card: 9,
      chibi_n: 30,
      chibi_r: 15,
      revision: 1,
    },
    pools: [],
    presets: [{ id: 'gt_summer', name: '夏日配置', revision: 1, pool }],
    revision: 1,
    server_time: 1_800_000_000,
    time_zone: 'UTC',
  };
  const writes: AdminInput[] = [];
  await page.route('**/admin/api/gacha', async (route) => {
    if (route.request().method() === 'POST') {
      writes.push(route.request().postDataJSON() as AdminInput);
    }
    await route.fulfill({ json: configuration });
  });
  await page.goto(ADMIN_ORIGIN + '/activities/gacha');
  await page.getByRole('tab', { name: '配置模板', exact: true }).click();
  return writes;
}

async function expectNoSubmit(page: Page) {
  expect(await page.locator('html').getAttribute('data-gacha-submits')).toBeNull();
}

async function capture(page: Page, name: string) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  if (process.env.NONBIRI_VISUAL_DIR)
    await page.screenshot({ path: resolve(process.env.NONBIRI_VISUAL_DIR, name + '.png') });
}

for (const theme of ['light', 'dark'] as const)
  for (const width of [1440, 390]) {
    test(`gacha selections wait for explicit save ${theme} ${width}`, async ({ page }) => {
      const guard = collectConsoleViolations(page);
      await page.setViewportSize({ width, height: 900 });
      const writes = await setup(page, theme);
      await page.getByRole('button', { name: '读取并微调', exact: true }).click();
      const dialog = page.getByRole('dialog', { name: '限定奖池', exact: true });
      await dialog.getByLabel('奖池名称', { exact: true }).fill('夏夜微调');
      await dialog.getByRole('button', { name: '星光收藏册', exact: true }).click();
      const sr = dialog
        .locator('.gacha-admin-roster')
        .filter({ has: page.getByRole('heading', { name: '万象化身 · SR', exact: true }) });
      const ssr = dialog
        .locator('.gacha-admin-roster')
        .filter({ has: page.getByRole('heading', { name: '夏夜花火 · SSR', exact: true }) });
      const toggle = sr.getByRole('button', { name: '全选／取消本组', exact: true });
      await expect(sr.getByRole('checkbox', { checked: true })).toHaveCount(5);
      await toggle.click();
      await expectNoSubmit(page);
      await expect(sr.getByRole('checkbox', { checked: true })).toHaveCount(0);
      await expect(ssr.getByRole('checkbox', { checked: true })).toHaveCount(5);
      await toggle.click();
      await expect(sr.getByRole('checkbox', { checked: true })).toHaveCount(5);
      await toggle.press('Enter');
      await expect(sr.getByRole('checkbox', { checked: true })).toHaveCount(0);
      await expectNoSubmit(page);
      expect(writes).toEqual([]);
      await sr.scrollIntoViewIfNeeded();
      await capture(page, `gacha-selection-${theme}-${width}`);
      const save = dialog.getByRole('button', { name: '保存草稿', exact: true });
      await save.scrollIntoViewIfNeeded();
      await capture(page, `gacha-save-${theme}-${width}`);
      await save.click();
      await expect(dialog).toBeHidden();
      expect(writes).toHaveLength(1);
      expect(writes[0]).toMatchObject({
        kind: 'pool',
        expected_revision: 0,
        pool: { name: '夏夜微调', cover: 'entry', published: false, featured: ssrCards },
      });
      await expect(page.locator('html')).toHaveAttribute('data-gacha-submits', '1');

      await page.getByRole('button', { name: '编辑模板', exact: true }).click();
      const template = page.getByRole('dialog', { name: '编辑配置模板', exact: true });
      await template
        .locator('.gacha-admin-roster')
        .filter({ has: page.getByRole('heading', { name: '万象化身 · SR', exact: true }) })
        .getByRole('button', { name: '全选／取消本组', exact: true })
        .click();
      await expect(page.locator('html')).toHaveAttribute('data-gacha-submits', '1');
      expect(writes).toHaveLength(1);
      await template.getByRole('button', { name: '保存模板', exact: true }).click();
      await expect(template).toBeHidden();
      expect(writes).toHaveLength(2);
      expect(writes[1]).toMatchObject({
        kind: 'preset',
        expected_revision: 1,
        preset: { id: 'gt_summer', pool: { featured: ssrCards } },
      });
      await expect(page.locator('html')).toHaveAttribute('data-gacha-submits', '2');
      guard.assertNone();
    });
  }

test('saving a gacha template does not also submit a pool', async ({ page }) => {
  const guard = collectConsoleViolations(page);
  const writes = await setup(page);
  await page.getByRole('button', { name: '读取并微调', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: '限定奖池', exact: true });
  await dialog.getByLabel('奖池名称', { exact: true }).fill('新模板');
  await dialog.getByRole('button', { name: '另存为配置模板', exact: true }).click();
  await expect(dialog).toBeHidden();
  await expectNoSubmit(page);
  expect(writes).toHaveLength(1);
  expect(writes[0]).toMatchObject({
    kind: 'preset',
    expected_revision: 0,
    preset: { name: '新模板', pool: { name: '新模板', featured: [...srCards, ...ssrCards] } },
  });
  guard.assertNone();
});
