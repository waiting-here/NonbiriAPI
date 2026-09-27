import { expect, test } from './test';
import { ADMIN_ORIGIN } from './ports';
import { collectConsoleViolations, mockJson, mockPublicConfig, mockRoleSession } from './support';

const base = '/admin/api/limited-activities/fat-fish';
const level = {
  id: 'ffl_first', title: 'First level', description: 'Fixture', revision: '1',
  created_at: 1, updated_at: 1,
};
const period = {
  id: 'ffp_first', title: 'Autumn period', description: 'Fixture', state: 'draft',
  visible: false, paused: false, past_public: false,
  starts_at: 1_917_000_000, ends_at: 1_919_000_000,
  revision: '1', leaderboard_final: false,
  nodes: [{ id: 'ffn_first', period_id: 'ffp_first', title: 'First node', description: '',
    map_x: 200, map_y: 200, order: 0, revision: '1', version_id: 'ffv_first', hidden: false,
    amounts: { unlock_cost: '0', ticket_price: '0', first_clear_reward: '0', star_rewards: ['0', '0', '0'] } }],
};

test('admin level and period editors remain usable at desktop and mobile widths', async ({ page }) => {
  const consoleGuard = collectConsoleViolations(page);
  await page.addInitScript(() => localStorage.setItem('nb.lang', 'en'));
  await mockPublicConfig(page, 'admin');
  await mockRoleSession(page, 'admin', 'admin');
  await mockJson(page, { origin: ADMIN_ORIGIN, method: 'GET', path: `${base}/levels?page=1`,
    body: { items: [level], page: 1, page_size: 20, has_more: false } });
  await mockJson(page, { origin: ADMIN_ORIGIN, method: 'GET', path: `${base}/periods?page=1`,
    body: { items: [period], page: 1, page_size: 20, has_more: false } });
  await mockJson(page, { origin: ADMIN_ORIGIN, method: 'GET', path: `${base}/periods/${period.id}`, body: period });
  await mockJson(page, { origin: ADMIN_ORIGIN, method: 'GET', path: `${base}/periods/${period.id}/validate`,
    body: { publishable: false, reachable: ['ffn_first'], unreachable: [], missing_playtests: ['ffn_first'] } });
  await mockJson(page, { origin: ADMIN_ORIGIN, method: 'GET', path: '/examples/fatfish/manifest.json',
    body: { format: 'nonbiri-fatfish-examples', version: 1, license: 'AGPL-3.0', source: 'Fixtures',
      examples: Array.from({ length: 8 }, (_, index) => ({ id: `example_${index}`, title: `Example ${index + 1}`,
        url: `/examples/fatfish/0${index + 1}-example.fatfish.json`, content_hash: 'a'.repeat(64) })) } });
  await page.goto(`${ADMIN_ORIGIN}/limited-activities/fat-fish`);
  await expect(page.getByRole('heading', { name: 'Level directory' })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Add fish' })).toBeVisible();
  await expect(page.getByLabel('Fat Fish level map')).toBeVisible();
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.getByRole('button', { name: 'Add hazard' })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.getByLabel('Title').fill('Unsaved example');
  expect(await page.evaluate(() => {
    const event = new Event('beforeunload', { cancelable: true });
    window.dispatchEvent(event);
    return event.defaultPrevented;
  })).toBe(true);
  await Promise.all([
    page.waitForEvent('dialog').then((dialog) => dialog.dismiss()),
    page.getByRole('button', { name: 'Periods and nodes' }).click(),
  ]);
  await expect(page.getByRole('heading', { name: 'Level directory' })).toBeVisible();
  await expect(page.getByLabel('Title')).toHaveValue('Unsaved example');
  await Promise.all([
    page.waitForEvent('dialog').then((dialog) => dialog.accept()),
    page.getByRole('button', { name: 'Periods and nodes' }).click(),
  ]);
  await expect(page.getByRole('heading', { name: 'Period directory' })).toBeVisible();
  await page.getByRole('button', { name: /Autumn period · draft/ }).click();
  await expect(page.getByRole('button', { name: /First node/ })).toBeVisible();
  await page.getByRole('button', { name: 'Check and preview publish conditions' }).click();
  await expect(page.getByText(/Missing one-star playtest.*First node/)).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.setViewportSize({ width: 1280, height: 900 });
  await expect(page.getByLabel('Draggable node layout')).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  consoleGuard.assertNone();
});

test('anonymous visitors cannot load the administrator editor data', async ({ page }) => {
  const requests: string[] = [];
  page.on('request', (request) => { if (request.url().includes('/limited-activities/fat-fish/')) requests.push(request.url()); });
  await mockPublicConfig(page, 'admin');
  await mockRoleSession(page, 'admin', 'anonymous');
  await page.goto(`${ADMIN_ORIGIN}/limited-activities/fat-fish`);
  await expect(page.getByRole('heading', { name: /sign in|log in/i })).toBeVisible();
  expect(requests).toEqual([]);
});
