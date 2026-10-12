import { resolve } from 'node:path';
import { expect, test } from './test';
import { collectConsoleViolations, mockPublicConfig, mockRoleSession } from './support';
import { USER_ORIGIN } from './ports';
import { loanActivity } from '../fixtures/loans';

const ownedCards = [
  'a-chatgpt',
  'a-claude',
  'a-glm',
  'a-deepseek',
  'a-gemini',
  'g-chatgpt',
  'l-chatgpt',
];
const options = [
  'cover:permanent',
  'cover:i',
  ...ownedCards.map((id) => 'card:' + id),
  'illustration:a',
  'illustration:l',
];
const snapshot = {
  visible_themes: ['a', 'g', 'i', 'l'],
  tickets: { permanent: 2, limited: 2 },
  fragments: { N: 0, R: 0, SR: 0, SSR: 0 },
  credits: { general: '10000', game: '10000' },
  owned: Object.fromEntries(ownedCards.map((id) => [id, { at: 1800000000, source: 'reward' }])),
  pity: {},
  newcomer: true,
  checked_in: false,
  limited_checked_in: false,
  claimed: [],
  redeemed: [],
  month_used: { permanent: 0, limited: 0 },
  donation: '0',
  server_time: 1800000000,
  time_zone: 'UTC',
  pools: [
    {
      id: 'permanent',
      name: '永续智能',
      kind: 'permanent',
      cover: 'permanent',
      featured: [],
      group: 'permanent',
      starts_at: null,
      ends_at: null,
      paused: false,
      published: true,
      revision: 1,
      rates: [0.55, 0.35, 0.09, 0.01],
      rules: {
        weights: [5500, 3500, 900, 100],
        sr_pity: 10,
        ssr_pity: 60,
        sr_featured: 5000,
        ssr_featured: 5000,
        sr_guarantee: true,
        ssr_guarantee: true,
      },
    },
  ],
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
};

for (const mode of ['light', 'dark'])
  for (const width of [1440, 390])
    test('heartfelt moments selection and persistence ' + mode + ' ' + width, async ({ page }) => {
      const guard = collectConsoleViolations(page);
      await page.setViewportSize({ width, height: 900 });
      await page.emulateMedia({ reducedMotion: 'reduce' });
      await page.addInitScript((mode) => {
        localStorage.setItem('nb.lang', 'zh');
        localStorage.setItem('nb.theme', mode);
        localStorage.setItem(
          'nb.gacha.preferences',
          JSON.stringify({ music: false, sound: false, reduced: true }),
        );
      }, mode);
      await mockRoleSession(page, 'user', 'user');
      await mockPublicConfig(page, 'user');
      await page.route('**/api/events', (route) =>
        route.fulfill({ contentType: 'text/event-stream', body: ': connected\n\n' }),
      );
      await page.route('**/api/activities', (route) => route.fulfill({ json: loanActivity() }));
      await page.route('**/api/limited-activities', (route) => route.fulfill({ json: [] }));
      await page.route('**/api/gacha/entry', (route) =>
        route.fulfill({ json: { enabled: true, entered: true, pools: [] } }),
      );
      await page.route('**/api/gacha/state', (route) => route.fulfill({ json: snapshot }));
      let enters = 0;
      await page.route('**/api/gacha/actions', (route) => {
        expect(route.request().postDataJSON().action).toBe('enter');
        enters++;
        return route.fulfill({ json: { snapshot } });
      });
      const appearance = {
        selected: 'default',
        moment: 'cover:permanent',
        moments: options,
        unlocked: ['default', 'heart-moment', 'celestial'],
        visible: ['default', 'heart-moment', 'celestial'],
        updated_at: 1800000000,
      };
      const saves: unknown[] = [];
      await page.route('**/api/appearance', (route) => {
        if (route.request().method() === 'POST') {
          const input = route.request().postDataJSON();
          saves.push(input);
          appearance.selected = input.selected;
          if (input.moment) appearance.moment = input.moment;
        }
        return route.fulfill({ json: appearance });
      });
      const openAppearance = async () => {
        if (width < 1024) {
          const navigation = page.locator('button[aria-controls="user-navigation"]');
          await expect(navigation).toBeVisible();
          if ((await navigation.getAttribute('aria-expanded')) === 'false')
            await navigation.click();
        }
        await page.getByRole('button', { name: '外观', exact: true }).click();
      };
      await page.goto(USER_ORIGIN + '/activities');
      await openAppearance();
      await page.locator('input[name="appearance"][value="heart-moment"]').check();
      await page.getByRole('button', { name: '应用装扮', exact: true }).click();
      await expect(page.locator('.nb-moment-backdrop')).toHaveAttribute(
        'data-moment',
        'cover:permanent',
      );
      expect(enters).toBe(0);
      await page.goto(USER_ORIGIN + '/activities/gacha');
      await page.getByRole('tab', { name: /^我的图鉴/ }).click();
      await page.getByRole('tab', { name: '心动时刻', exact: true }).click();
      const picker = page.locator('.nb-moment-picker');
      await expect(picker.getByRole('radio')).toHaveCount(options.length);
      await expect(picker.locator('input[value="card:b-chatgpt"]')).toHaveCount(0);
      await picker.locator('input[value="card:l-chatgpt"]').check();
      await expect(picker.locator('.nb-moment-picker__copy small')).toContainText('专属');
      await picker.getByRole('button', { name: '设为心动时刻', exact: true }).click();
      await expect(page.locator('.nb-moment-backdrop')).toHaveAttribute(
        'data-moment',
        'card:l-chatgpt',
      );
      await expect(page.locator('.nb-moment-character canvas')).toHaveAttribute(
        'data-ready',
        'false',
      );
      expect(saves.at(-1)).toEqual({ selected: 'heart-moment', moment: 'card:l-chatgpt' });
      if (process.env.NONBIRI_VISUAL_DIR) {
        await picker.scrollIntoViewIfNeeded();
        await page.screenshot({
          path: resolve(
            process.env.NONBIRI_VISUAL_DIR,
            'moment-picker-' + mode + '-' + width + '.png',
          ),
        });
      }
      await picker.getByRole('tab', { name: '卡牌', exact: true }).click();
      await expect(picker.getByRole('radio')).toHaveCount(ownedCards.length);
      await picker.getByRole('textbox').fill('不存在的素材');
      await expect(picker.getByRole('radio')).toHaveCount(0);
      await picker.getByRole('textbox').fill('ChatGPT');
      await expect(picker.getByRole('radio')).toHaveCount(3);
      await page.goto(USER_ORIGIN + '/activities');
      await expect(page.locator('.nb-moment-backdrop')).toHaveAttribute(
        'data-moment',
        'card:l-chatgpt',
      );
      await openAppearance();
      await page.locator('input[name="appearance"][value="default"]').check();
      await page.getByRole('button', { name: '应用装扮', exact: true }).click();
      await expect(page.locator('.nb-moment-backdrop')).toHaveCount(0);
      await openAppearance();
      await page.locator('input[name="appearance"][value="heart-moment"]').check();
      await page.getByRole('button', { name: '应用装扮', exact: true }).click();
      await page.reload();
      await expect(page.locator('.nb-moment-backdrop')).toHaveAttribute(
        'data-moment',
        'card:l-chatgpt',
      );
      await page.getByRole('button', { name: '欣赏背景', exact: true }).click();
      await expect(page.getByRole('dialog', { name: '欣赏背景', exact: true })).toBeVisible();
      await expect(page.locator('.nb-user-shell')).toBeHidden();
      await expect
        .poll(() =>
          page
            .locator('.nb-moment-backdrop img')
            .evaluateAll((images) =>
              images.every(
                (image) =>
                  (image as HTMLImageElement).complete &&
                  (image as HTMLImageElement).naturalWidth > 0,
              ),
            ),
        )
        .toBe(true);
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      );
      if (process.env.NONBIRI_VISUAL_DIR)
        await page.screenshot({
          path: resolve(
            process.env.NONBIRI_VISUAL_DIR,
            'moment-background-' + mode + '-' + width + '.png',
          ),
        });
      await page.getByRole('button', { name: '返回页面', exact: true }).click();
      await expect(page.locator('.loan-card')).toBeVisible();
      if (process.env.NONBIRI_VISUAL_DIR)
        await page.screenshot({
          path: resolve(
            process.env.NONBIRI_VISUAL_DIR,
            'moment-page-' + mode + '-' + width + '.png',
          ),
        });
      guard.assertNone();
    });
