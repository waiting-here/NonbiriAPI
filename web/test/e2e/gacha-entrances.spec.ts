import { resolve } from 'node:path';
import { expect, test, type Page } from './test';
import { collectConsoleViolations, mockPublicConfig, mockRoleSession } from './support';
import { USER_ORIGIN } from './ports';
import { loanActivity } from '../fixtures/loans';
import { gachaSnapshot } from '../fixtures/gacha';

type Locator = ReturnType<Page['locator']>;
const defaultRules = {
  weights: [5500, 3500, 900, 100],
  sr_pity: 10,
  ssr_pity: 60,
  sr_featured: 5000,
  ssr_featured: 5000,
  sr_guarantee: true,
  ssr_guarantee: true,
};

const now = 1_800_000_000;
const entrances = [
  {
    id: 'gp_summer',
    name: '夏夜花火',
    name_en: 'Summer Fireworks',
    cover: 'i',
    featured: [
      'i-chatgpt',
      'i-claude',
      'i-glm',
      'i-deepseek',
      'i-gemini',
      'h-chatgpt',
      'h-claude',
      'h-glm',
      'h-deepseek',
      'h-gemini',
    ],
    starts_at: now - 86400,
    ends_at: now + 86400,
  },
  {
    id: 'gp_celestial',
    name: '星月神话',
    name_en: 'Celestial Myth',
    cover: 'l',
    featured: ['l-chatgpt'],
    starts_at: now - 86400,
    ends_at: now + 86400,
  },
];

const ratedEntrance = {
  id: 'gp_sweet',
  name: '糖夜迷藏',
  name_en: 'Sweet Hide-and-Seek',
  cover: 's',
  featured: ['s-chatgpt', 's-claude'],
  starts_at: now - 86400,
  ends_at: now + 86400,
};

async function ratedFixture(page: Page, theme: 'light' | 'dark', disabled = false) {
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.clock.setFixedTime(new Date(now * 1000));
  await page.addInitScript((theme) => {
    localStorage.setItem('nb.lang', 'en');
    localStorage.setItem('nb.theme', theme);
    localStorage.setItem(
      'nb.gacha.preferences',
      JSON.stringify({ music: false, sound: false, reduced: true }),
    );
  }, theme);
  await mockRoleSession(page, 'user', 'user');
  await mockPublicConfig(page, 'user', { artwork_r18_disabled: disabled });
  await page.route('**/api/events', (route) =>
    route.fulfill({ contentType: 'text/event-stream', body: ': connected\n\n' }),
  );
  await page.route('**/api/activities', (route) => route.fulfill({ json: loanActivity() }));
  await page.route('**/api/limited-activities', (route) => route.fulfill({ json: [] }));
  await page.route('**/api/gacha/entry', (route) =>
    route.fulfill({ json: { enabled: true, entered: false, pools: [ratedEntrance] } }),
  );
  const snapshot = gachaSnapshot();
  snapshot.visible_themes.push('s');
  snapshot.pools.push({
    ...snapshot.pools[0],
    ...ratedEntrance,
    kind: 'limited',
    group: ratedEntrance.id,
  });
  await page.route('**/api/gacha/state', (route) => route.fulfill({ json: snapshot }));
  let enters = 0;
  await page.route('**/api/gacha/actions', (route) => {
    expect(route.request().postDataJSON().action).toBe('enter');
    enters++;
    return route.fulfill({ json: { snapshot } });
  });
  return () => enters;
}

async function expectBlurred(cover: Locator) {
  const image = cover.locator('img');
  await expect(image).toBeVisible();
  await expect
    .poll(() =>
      image.evaluate((node) => {
        const blur = getComputedStyle(node).filter.match(/blur\(([\d.]+)px\)/);
        return Number(blur?.[1] ?? 0);
      }),
    )
    .toBeGreaterThan(0);
}

for (const theme of ['light', 'dark'] as const)
  for (const width of [1440, 390]) {
    test(`gacha entrances and direct pool navigation ${theme} ${width}`, async ({ page }) => {
      const guard = collectConsoleViolations(page);
      await page.setViewportSize({ width, height: 900 });
      await page.emulateMedia({ reducedMotion: 'reduce' });
      await page.clock.setFixedTime(new Date(now * 1000));
      await page.addInitScript(
        ({ theme }) => {
          localStorage.setItem('nb.lang', 'zh');
          localStorage.setItem('nb.theme', theme);
          localStorage.setItem(
            'nb.gacha.preferences',
            JSON.stringify({ music: false, sound: false, reduced: true }),
          );
        },
        { theme },
      );
      await mockRoleSession(page, 'user', 'user');
      await mockPublicConfig(page, 'user');
      await page.route('**/api/events', (route) =>
        route.fulfill({ contentType: 'text/event-stream', body: ': connected\n\n' }),
      );
      await page.route('**/api/activities', (route) => route.fulfill({ json: loanActivity() }));
      await page.route('**/api/limited-activities', (route) => route.fulfill({ json: [] }));
      let enabled = false;
      await page.route('**/api/gacha/entry', (route) =>
        route.fulfill({ json: { enabled, entered: false, pools: enabled ? entrances : [] } }),
      );
      const snapshot = {
        visible_themes: ['a', 'g', 'i', 'l'],
        tickets: { permanent: 10, limited: 10 },
        fragments: { N: 0, R: 0, SR: 0, SSR: 0 },
        credits: { general: '-1', game: '10000' },
        owned: {},
        pity: {},
        newcomer: true,
        checked_in: false,
        limited_checked_in: false,
        claimed: [],
        redeemed: [],
        month_used: { permanent: 0, limited: 0 },
        donation: '0',
        server_time: now,
        time_zone: 'UTC+08:00',
        pools: [
          {
            id: 'permanent',
            name: '永续智能',
            kind: 'permanent',
            cover: 'permanent',
            featured: [],
            starts_at: null,
            ends_at: null,
          },
          ...entrances.map((pool) => ({ ...pool, kind: 'limited' })),
        ].map((pool) => ({
          ...pool,
          group: pool.id,
          rules: defaultRules,
          paused: false,
          published: true,
          revision: 1,
          rates: [0.51317544, 0.32656619, 0.13817568, 0.02208269],
        })),
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
      await page.route('**/api/gacha/state', (route) => route.fulfill({ json: snapshot }));
      let enters = 0;
      await page.route('**/api/gacha/actions', (route) => {
        const { action } = route.request().postDataJSON();
        if (action === 'enter') enters++;
        else if (action === 'check-in') {
          snapshot.checked_in = true;
          snapshot.tickets.permanent++;
        } else if (action === 'limited-check-in') {
          snapshot.limited_checked_in = true;
          snapshot.tickets.limited++;
        } else throw new Error('Unexpected action ' + action);
        return route.fulfill({ json: { snapshot } });
      });
      await page.goto(USER_ORIGIN + '/activities');
      await expect(page.locator('.loan-card')).toBeVisible();
      await expect(page.locator('.gacha-entry, .gacha-pool-entry')).toHaveCount(0);
      expect(enters).toBe(0);
      enabled = true;
      await page.reload();
      const main = page.locator('.gacha-entry');
      await expect(main).toBeVisible();
      await expect(page.locator('.gacha-pool-entry')).toHaveCount(2);
      const grid = (await page.locator('.economy-activities-grid').boundingBox())!;
      const loan = (await page.locator('.loan-card').boundingBox())!;
      const entry = (await main.boundingBox())!;
      expect(entry.y).toBeGreaterThanOrEqual(loan.y + loan.height);
      expect(Math.abs(entry.x - grid.x)).toBeLessThan(2);
      expect(Math.abs(entry.width - grid.width)).toBeLessThan(2);
      await expect(main.locator('img')).toHaveAttribute(
        'src',
        '/assets/gacha/covers/permanent.webp',
      );
      await expect(main).toContainText('捐赠回馈');
      await expect(main).toContainText('主题装扮');
      await expect(main).not.toContainText('限定奖池');
      const special = page
        .locator('.gacha-pool-entry')
        .filter({ has: page.getByRole('heading', { name: '星月神话', exact: true }) });
      await expect(special).toContainText('ChatGPT');
      await expect(special.locator('.gacha-pool-entry__schedule')).toBeVisible();
      await expect(special.locator('img')).toHaveAttribute('src', '/assets/gacha/covers/l.webp');
      expect(enters).toBe(0);
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      );
      if (process.env.NONBIRI_VISUAL_DIR) {
        await page.screenshot({
          path: resolve(process.env.NONBIRI_VISUAL_DIR, `gacha-entries-${theme}-${width}.png`),
          fullPage: true,
        });
      }
      await special.click();
      await expect(page).toHaveURL(USER_ORIGIN + '/activities/gacha?pool=gp_celestial');
      await expect(page.locator('#gacha-pool-title')).toHaveText('星月神话');
      expect(enters).toBe(1);
      await page.getByRole('button', { name: '签到 · 常驻券 +1', exact: true }).click();
      await expect(page.getByRole('button', { name: '常驻券已领取', exact: true })).toBeDisabled();
      await page.getByRole('button', { name: '签到 · 限定券 +1', exact: true }).click();
      await expect(page.getByRole('button', { name: '限定券已领取', exact: true })).toBeDisabled();
      expect(snapshot.tickets).toEqual({ permanent: 11, limited: 11 });
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      );
      if (process.env.NONBIRI_VISUAL_DIR) {
        await page.screenshot({
          path: resolve(process.env.NONBIRI_VISUAL_DIR, `gacha-daily-${theme}-${width}.png`),
          fullPage: true,
        });
        await page.screenshot({
          path: resolve(
            process.env.NONBIRI_VISUAL_DIR,
            `gacha-daily-${theme}-${width}-viewport.png`,
          ),
        });
      }
      guard.assertNone();
      await page.getByTitle('兑换常驻抽奖券', { exact: true }).click();
      const purchase = page.getByRole('dialog', { name: '兑换抽奖券' });
      await purchase.getByRole('spinbutton', { name: '兑换数量' }).fill('1');
      await expect(purchase).toContainText('-1 积分');
      await expect(purchase.getByRole('button', { name: '兑换 1 张' })).toBeEnabled();
      await purchase.getByRole('tab', { name: '限定抽奖券' }).click();
      await expect(purchase.getByRole('button', { name: '积分不足' })).toBeDisabled();
      if (process.env.NONBIRI_VISUAL_DIR) {
        await page.screenshot({
          path: resolve(
            process.env.NONBIRI_VISUAL_DIR,
            `gacha-negative-wallet-${theme}-${width}.png`,
          ),
        });
      }
      guard.assertNone();
    });
  }

for (const theme of ['light', 'dark'] as const)
  for (const width of [1440, 390]) {
    test(`rated gacha cover toggles without navigation ${theme} ${width}`, async ({ page }) => {
      const guard = collectConsoleViolations(page);
      await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
      const enters = await ratedFixture(page, theme);
      await page.goto(USER_ORIGIN + '/activities');
      const entry = page.locator('.gacha-pool-entry').filter({
        has: page.getByRole('heading', { name: ratedEntrance.name_en, exact: true }),
      });
      const cover = entry.getByRole('button');
      const text = entry.getByRole('link');
      await expect(cover).toHaveAccessibleName('Reveal R-18 cover: ' + ratedEntrance.name_en);
      await expect(cover).toHaveAttribute('aria-pressed', 'false');
      await expect(cover.locator('img')).toHaveAttribute('src', '/assets/gacha/covers/s.webp');
      await expectBlurred(cover);
      await expect(entry.locator('.gacha-pool-entry__hint')).toBeVisible();
      await expect(text).toHaveAttribute('href', '/activities/gacha?pool=' + ratedEntrance.id);
      if (process.env.NONBIRI_VISUAL_DIR) {
        await entry.screenshot({
          path: resolve(
            process.env.NONBIRI_VISUAL_DIR,
            `gacha-rated-blurred-${theme}-${width}.png`,
          ),
        });
      }
      await cover.click();
      await expect(cover).toHaveAttribute('aria-pressed', 'true');
      await expect(cover).toHaveAccessibleName('Blur R-18 cover: ' + ratedEntrance.name_en);
      await expect(cover.locator('img')).toHaveCSS('filter', 'none');
      if (process.env.NONBIRI_VISUAL_DIR) {
        await entry.screenshot({
          path: resolve(
            process.env.NONBIRI_VISUAL_DIR,
            `gacha-rated-revealed-${theme}-${width}.png`,
          ),
        });
      }
      await expect(page).toHaveURL(USER_ORIGIN + '/activities');
      expect(enters()).toBe(0);
      await cover.press('Enter');
      await expect(cover).toHaveAttribute('aria-pressed', 'false');
      await expectBlurred(cover);
      await expect(page).toHaveURL(USER_ORIGIN + '/activities');
      expect(enters()).toBe(0);
      await cover.click();
      await expect(cover).toHaveAttribute('aria-pressed', 'true');
      await page.reload();
      await expect(cover).toHaveAttribute('aria-pressed', 'false');
      await expectBlurred(cover);
      expect(enters()).toBe(0);
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      );
      await cover.click();
      await expect(cover).toHaveAttribute('aria-pressed', 'true');
      await text.focus();
      await expect(text).toBeFocused();
      await text.press('Enter');
      await expect(page).toHaveURL(USER_ORIGIN + '/activities/gacha?pool=' + ratedEntrance.id);
      await expect(page.locator('#gacha-pool-title')).toHaveText(ratedEntrance.name_en);
      expect(enters()).toBe(1);
      await page.getByRole('link', { name: 'Back to activities', exact: true }).click();
      await expect(page).toHaveURL(USER_ORIGIN + '/activities');
      await expect(cover).toHaveAttribute('aria-pressed', 'false');
      await expectBlurred(cover);
      expect(enters()).toBe(1);
      guard.assertNone();
    });
  }

test('disabled R-18 policy keeps the banner placeholder while its text link still opens the pool', async ({
  page,
}) => {
  const guard = collectConsoleViolations(page);
  const enters = await ratedFixture(page, 'dark', true);
  await page.goto(USER_ORIGIN + '/activities');
  const entry = page.locator('.gacha-pool-entry').filter({
    has: page.getByRole('heading', { name: ratedEntrance.name_en, exact: true }),
  });
  const cover = entry.getByRole('button');
  for (const width of [1440, 390]) {
    await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
    await expect(cover).toBeDisabled();
    await expect(cover).toHaveAccessibleName('R-18 cover unavailable: ' + ratedEntrance.name_en);
    await expect(cover).toHaveAttribute('aria-pressed', 'false');
    await expect(cover.locator('.nb-artwork-placeholder')).toBeVisible();
    await expect(entry.locator('img')).toHaveCount(0);
    await cover.evaluate((button: HTMLButtonElement) => button.click());
    await expect(cover).toHaveAttribute('aria-pressed', 'false');
    await expect(entry.locator('img')).toHaveCount(0);
    await expect(page).toHaveURL(USER_ORIGIN + '/activities');
    expect(enters()).toBe(0);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
      true,
    );
  }
  await entry.getByRole('link').click();
  await expect(page).toHaveURL(USER_ORIGIN + '/activities/gacha?pool=' + ratedEntrance.id);
  await expect(page.locator('#gacha-pool-title')).toHaveText(ratedEntrance.name_en);
  await expect(page.locator('.gacha-banner__art img')).toHaveCount(0);
  expect(enters()).toBe(1);
  guard.assertNone();
});

for (const language of ['zh', 'en'] as const)
  for (const theme of ['light', 'dark'] as const)
    for (const width of [1440, 390]) {
      test(`upcoming limited pool ${language} ${theme} ${width}`, async ({ page }) => {
        const guard = collectConsoleViolations(page);
        await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
        await page.emulateMedia({ reducedMotion: 'reduce' });
        await page.clock.setFixedTime(new Date(now * 1000));
        await page.addInitScript(
          ({ language, theme }) => {
            localStorage.setItem('nb.lang', language);
            localStorage.setItem('nb.theme', theme);
            localStorage.setItem(
              'nb.gacha.preferences',
              JSON.stringify({ music: false, sound: false, reduced: true }),
            );
          },
          { language, theme },
        );
        await mockRoleSession(page, 'user', 'user');
        await mockPublicConfig(page, 'user');
        await page.route('**/api/events', (route) =>
          route.fulfill({ contentType: 'text/event-stream', body: ': connected\n\n' }),
        );
        await page.route('**/api/activities', (route) => route.fulfill({ json: loanActivity() }));
        await page.route('**/api/limited-activities', (route) => route.fulfill({ json: [] }));

        const snapshot = gachaSnapshot();
        const future = {
          ...snapshot.pools[0],
          id: 'gp_future',
          name: '未来奖池',
          name_en: 'Future pool',
          kind: 'limited' as const,
          cover: 'i',
          group: 'gp_future',
          featured: ['i-chatgpt'],
          starts_at: now + 86400,
          ends_at: now + 172800,
        };
        snapshot.visible_themes.push('i');
        snapshot.tickets.limited = 10;
        snapshot.pools.push(future);
        await page.route('**/api/gacha/entry', (route) =>
          route.fulfill({ json: { enabled: true, entered: false, pools: [future] } }),
        );
        await page.route('**/api/gacha/state', (route) => route.fulfill({ json: snapshot }));
        await page.route('**/api/gacha/actions', (route) => {
          expect(route.request().postDataJSON().action).toBe('enter');
          return route.fulfill({ json: { snapshot } });
        });

        await page.goto(USER_ORIGIN + '/activities/gacha?pool=' + future.id);
        await expect(page.locator('#gacha-pool-title')).toHaveText(
          language === 'en' ? future.name_en : future.name,
        );
        await expect(page.locator('.gacha-kicker')).toHaveText(
          language === 'en' ? 'LIMITED · NOT STARTED' : '限定召唤 · 尚未开始',
        );
        const start = await page.evaluate(
          ({ timestamp, locale }) =>
            new Intl.DateTimeFormat(locale, {
              year: 'numeric',
              month: '2-digit',
              day: '2-digit',
              hour: '2-digit',
              minute: '2-digit',
            }).format(new Date(timestamp * 1000)),
          { timestamp: future.starts_at, locale: language === 'en' ? 'en-US' : 'zh-CN' },
        );
        await expect(page.locator('.gacha-banner__time').first()).toHaveText(
          (language === 'en' ? 'Starts ' : '开始时间 ') + start,
        );
        await expect(page.locator('.gacha-draw-actions__notice')).toHaveText(
          language === 'en'
            ? 'Pool not started yet. Summoning unavailable.'
            : '奖池尚未开始，暂不可召唤。',
        );
        const once = page.getByRole('button', {
          name: language === 'en' ? /Summon once/ : /召唤一次/,
        });
        const ten = page.getByRole('button', {
          name: language === 'en' ? /Summon ten/ : /十次召唤/,
        });
        await expect(once).toBeDisabled();
        await expect(ten).toBeDisabled();
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
          true,
        );

        await page.locator('.gacha-banner').evaluate((element) => {
          element.scrollIntoView({ block: 'start', behavior: 'instant' });
        });
        await expect(page.locator('.gacha-kicker')).toBeInViewport();
        await expect(page.locator('.gacha-banner__time').first()).toBeInViewport();
        await expect(page.locator('.gacha-draw-actions__notice')).toBeInViewport();
        await expect(once).toBeInViewport();
        await expect(ten).toBeInViewport();
        if (process.env.NONBIRI_VISUAL_DIR) {
          await page
            .locator('.gacha-banner__back, .gacha-hero-card img')
            .evaluateAll((images) =>
              Promise.all(images.map((image) => (image as HTMLImageElement).decode())),
            );
          await page.screenshot({
            path: resolve(
              process.env.NONBIRI_VISUAL_DIR,
              `gacha-upcoming-${language}-${theme}-${width}.png`,
            ),
            animations: 'disabled',
          });
        }
        guard.assertNone();
      });
    }
