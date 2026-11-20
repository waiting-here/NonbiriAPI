import { gamesSnapshotWire } from '../../src/user/games/common/testFixtures';
import { mkdir, readdir } from 'node:fs/promises';
import { join } from 'node:path';
import { expect, test } from './test';
import { collectConsoleViolations, mockPublicConfig, mockRoleSession } from './support';
import { USER_ORIGIN } from './ports';
import type { Action, Profile, Neighbor } from '../../src/user/games/whale-haven/api';

const neighbor: Neighbor = {
  id: 'bay-b',
  anonymousCode: 'bay-b',
  uid: 0,
  publicNickname: 'Hidden identity',
  gameProfilePublic: false,
  whaleName: '雨滴',
  whaleOutfit: 'winter_snow',
  oceanTitle: '蓝湾',
  troughGrams: 100,
  troughFeedType: 'silver_fish',
  whaleSatiety: 80,
  whaleAtHome: true,
  equippedDecor: [],
  pinned: false,
};
function initial(): Profile {
  return {
    settings_revision: '7',
    timezone_offset_minutes: 480,
    settings: {
      enabled: true,
      exchange_enabled: true,
      values: {
        affection_per_level: 100,
        bank_base_pearls: 25,
        trough_grams: 360,
        lot_pearls: 5,
        lot_gross_milli: 5000000,
        platform_cut_bp: 200,
        welfare_cut_bp: 400,
        thursday_cut_bp: 800,
        guest_wage_bp: 2000,
        daily_exchange_pearls: 75,
        wallet_cap_milli: 300000000,
        conch_cost_shards: 5,
        daily_social_pearls: 5,
        daily_conch_shards: 10,
        stove_window_seconds: 172800,
        stove_required_grams: 180,
        feed_silver_fish_price_milli: 19000,
      },
    },
    social: { open: false, door: 'bay-a', accept_work: false, away: null, guests: [] },
    snapshot: {
      revision: 1,
      lastTickAt: Date.now(),
      playerNickname: 'A',
      playerAnonymousCode: 'bay-a',
      gameProfilePublic: false,
      wallet: {
        generalMilli: 1000,
        gameMilli: 100000,
        pearls: 15,
        conchShards: 0,
        siteDayKey: '2026-11-19',
        dailyPearlExchanged: 0,
        dailySocialPearlsGained: 0,
        dailyConchShardsGained: 0,
      },
      whale: {
        name: '小鲸鱼',
        hasCompletedNaming: false,
        satiety: 80,
        mood: 70,
        affection: 1,
        outfit: 'maid_classic',
        location: { kind: 'home' },
        tempEmotion: null,
      },
      inventory: { silver_fish: 0, sweet_shrimp: 0, berry_teacake: 0 },
      home: {
        troughGrams: 0,
        troughFeedType: 'silver_fish',
        unclaimedPearls: 0,
        pearlProgress: 0,
        ownedDecor: [],
        equippedDecor: [],
        ownedOutfits: ['maid_classic'],
      },
      guestVisit: null,
      guestVisits: [],
      neighbors: [],
      footprints: {},
      logs: [],
    },
  };
}
for (const width of [1440, 390]) {
  for (const theme of ['light', 'dark']) {
    test(
      'Whale Haven adoption, privacy, visits and exchange ' + width + ' ' + theme,
      async ({ page }, info) => {
        const evidence = process.env.NONBIRI_VISUAL_DIR;
        if (evidence) await mkdir(evidence, { recursive: true });
        const capture = (name: string) =>
          page.screenshot({
            path: evidence
              ? join(evidence, 'whale-' + width + '-' + theme + '-' + name + '.png')
              : info.outputPath(name + '.png'),
          });
        await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
        await page.addInitScript((value) => {
          localStorage.setItem('nb.theme', value);
          localStorage.setItem('nb.lang', 'zh');
        }, theme);
        await mockRoleSession(page, 'user', 'user');
        await mockPublicConfig(page, 'user');
        const errors = collectConsoleViolations(page);
        const profile = initial(),
          commands: Action[] = [],
          sheets: string[] = [];
        page.on('request', (r) => {
          if (r.url().includes('/i2v/')) sheets.push(r.url());
        });
        let exchanged = 0;
        const quoted: string[] = [];
        await page.route(USER_ORIGIN + '/api/games/whale-haven/**', async (route) => {
          const req = route.request();
          const path = new URL(req.url()).pathname;
          if (req.method() === 'POST' && !path.endsWith('/exchange/quote'))
            expect(req.headers()['idempotency-key']).toBeTruthy();
          if (path.endsWith('/profile')) return route.fulfill({ json: profile });
          if (path.endsWith('/neighbors/search'))
            return route.fulfill({ json: { items: [neighbor] } });
          const body = req.postDataJSON();
          if (path.endsWith('/exchange/quote')) {
            quoted.push(body.quantity);
            const lots = BigInt(body.quantity);
            return route.fulfill({
              json: {
                quantity: body.quantity,
                max_lots: String(Math.floor(profile.snapshot.wallet.pearls / 5)),
                profile_revision: String(profile.snapshot.revision),
                settings_revision: '7',
                source_pearls: String(5n * lots),
                gross_general_milli: String(5000000n * lots),
                platform_milli: String(100000n * lots),
                welfare_milli: String(200000n * lots),
                thursday_milli: String(400000n * lots),
                net_general_milli: String(4300000n * lots),
                general_wallet_cap_milli: '300000000',
                daily_source_remaining: '75',
                wallet_cap_blocked: false,
              },
            });
          }
          expect(body.expected_profile_revision).toBe(String(profile.snapshot.revision));
          expect(body.expected_settings_revision).toBe('7');
          profile.snapshot.revision++;
          if (path.endsWith('/exchange')) {
            exchanged++;
            profile.snapshot.wallet.pearls -= 5;
            profile.snapshot.wallet.dailyPearlExchanged += 5;
            profile.snapshot.wallet.generalMilli =
              Number(profile.snapshot.wallet.generalMilli) + 4300000;
            return route.fulfill({ json: profile });
          }
          commands.push(body);
          if (body.type === 'adopt') {
            profile.snapshot.whale.hasCompletedNaming = true;
            profile.snapshot.whale.name = body.name;
            profile.social.open = body.open;
          }
          if (body.type === 'buy_feed') {
            profile.snapshot.wallet.gameMilli =
              Number(profile.snapshot.wallet.gameMilli) - 19000 * body.quantity;
            profile.snapshot.inventory.silver_fish += body.quantity;
          }
          if (body.type === 'send_visit')
            profile.social.away = {
              target: neighbor.id,
              label: '匿名饲养员 #bay-b',
              working: false,
              allow_hire: body.allowHire,
              started_at_ms: Date.now(),
            };
          if (body.type === 'recall_home') profile.social.away = null;
          if (body.type === 'set_open') profile.social.open = body.open;
          return route.fulfill({ json: profile });
        });
        await page.goto(USER_ORIGIN + (width === 390 ? '/whale-haven' : '/games/whale-haven'));
        await expect(page.getByLabel('开放小筑，允许互相串门')).toBeChecked();
        await expect(page.getByRole('button', { name: /确认叫/ })).toBeInViewport();
        expect(sheets).toEqual([]);
        await capture('adopt');
        await page.getByRole('button', { name: /确认叫/ }).click();
        await expect(page.getByRole('navigation', { name: '海湾功能菜单栏' })).toBeVisible();
        await expect(page.locator('.whale-rig-canvas').first()).toBeVisible();
        expect(
          sheets.every(
            (url) => url.includes('/whale-maid/') && !/sleeping|worker|waving|jumping/.test(url),
          ),
        ).toBe(true);
        await expect(page.getByRole('button', { name: '设置', exact: true })).toBeInViewport({
          ratio: 1,
        });
        await capture('cabin');
        const dock = page.getByRole('navigation', { name: '海湾功能菜单栏' });
        await dock.getByRole('button', { name: '背包' }).click();
        await page.getByRole('spinbutton', { name: '购买份数（1–99）' }).fill('3');
        await page.getByRole('button', { name: /购买 ·/ }).click();
        const buy = page.getByRole('alertdialog');
        await expect(buy).toContainText('×3');
        await expect(buy).toContainText('57');
        await buy.getByRole('button', { name: '确认', exact: true }).click();
        await expect
          .poll(() => commands.filter((c) => c.type === 'buy_feed'))
          .toMatchObject([{ quantity: 3, feedId: 'silver_fish' }]);
        await page.getByRole('button', { name: '收起菜单面板' }).click();
        await dock.getByRole('button', { name: '串门' }).click();
        await expect(page.getByText('Hidden identity')).toHaveCount(0);
        await page.getByRole('button', { name: '去串门', exact: true }).click();
        await page.getByLabel('本次允许主人雇佣帮工').check();
        await page.getByRole('button', { name: '出发', exact: true }).click();
        await expect(page.getByRole('button', { name: '召回并结算' })).toBeVisible();
        expect(commands.find((a) => a.type === 'send_visit')).toMatchObject({
          allowHire: true,
          target: 'bay-b',
        });
        await page.getByRole('button', { name: '召回并结算' }).click();
        await page.getByLabel('开放小筑，允许互相串门').click();
        await page.getByRole('button', { name: '关闭并结算' }).click();
        await expect(page.getByText('开放自己的小筑后，就能查找同样开放的邻居。')).toBeVisible();
        await page.getByRole('button', { name: '收起菜单面板' }).click();
        await dock.getByRole('button', { name: '鲸币' }).click();
        await expect(page.getByText(/最多可兑/)).toContainText('3 手');
        expect(quoted).toEqual(['0']);
        await page.getByRole('button', { name: '查看报价' }).click();
        await expect(page.getByRole('alertdialog')).toContainText('4,300');
        expect(exchanged).toBe(0);
        await capture('quote');
        await page.getByRole('button', { name: '确认兑换' }).click();
        await expect.poll(() => exchanged).toBe(1);
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
          true,
        );
        errors.assertNone();
      },
    );
  }
}

test('hall only advertises an open Whale Haven and loads motion after arrival', async ({
  page,
}) => {
  await mockRoleSession(page, 'user', 'user');
  await mockPublicConfig(page, 'user');
  await page.route(USER_ORIGIN + '/api/games', (route) =>
    route.fulfill({ json: gamesSnapshotWire() }),
  );
  let enabled = false;
  await page.route(USER_ORIGIN + '/api/games/whale-haven/availability', (route) =>
    route.fulfill({ json: { enabled, exchange_enabled: enabled } }),
  );
  const art: string[] = [];
  page.on('request', (req) => {
    if (req.url().includes('/assets/whale-haven')) art.push(req.url());
  });
  await page.goto(USER_ORIGIN + '/games');
  await expect(page.locator('.game-center-card').first()).toBeVisible();
  await expect(page.locator('a[href="/games/whale-haven"]')).toHaveCount(0);
  expect(art).toEqual([]);
  enabled = true;
  await page.reload();
  await expect(page.locator('a[href="/games/whale-haven"]')).toBeVisible();
  expect(art.some((url) => url.includes('/pet/') || url.includes('/rig/'))).toBe(false);
});

test('user station serves decodable whale artwork and admin build omits the pack', async ({
  page,
  request,
}) => {
  const art = USER_ORIGIN + '/assets/whale-haven/';
  const portrait = await page.goto(art + 'sprites/whale-calm.webp');
  expect(portrait?.status()).toBe(200);
  expect(portrait?.headers()['content-type']).toBe('image/webp');
  await expect
    .poll(() => page.evaluate(() => document.images[0]?.naturalWidth ?? 0))
    .toBeGreaterThan(0);

  for (const path of ['backgrounds/cabin-mobile.webp', 'pet/whale-maid/i2v/idle.webp']) {
    const response = await request.head(art + path);
    expect(response.ok()).toBe(true);
    expect(response.headers()['content-type']).toBe('image/webp');
  }
  const adminAssets = await readdir(new URL('../../dist/admin/assets/', import.meta.url));
  expect(adminAssets).not.toContain('whale-haven');
});

test('an English keeper can see the blocked exchange and keep the pending wage separate', async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.addInitScript(() => localStorage.setItem('nb.lang', 'en'));
  await mockRoleSession(page, 'user', 'user');
  await mockPublicConfig(page, 'user');
  const profile = initial();
  profile.snapshot.whale.hasCompletedNaming = true;
  Object.assign(profile.snapshot.wallet, {
    generalMilli: '299999999',
    pendingSocialPearls: 2,
    pendingConchShards: 3,
    wageProgress: 0.4,
  });
  const requests: string[] = [];
  await page.route(USER_ORIGIN + '/api/games/whale-haven/**', async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith('/profile')) return route.fulfill({ json: profile });
    expect(path).toMatch(/\/exchange\/quote$/);
    requests.push(route.request().postDataJSON().quantity);
    return route.fulfill({
      json: {
        quantity: '0',
        max_lots: '0',
        settings_revision: '7',
        profile_revision: '1',
        source_pearls: '0',
        gross_general_milli: '0',
        platform_milli: '0',
        welfare_milli: '0',
        thursday_milli: '0',
        net_general_milli: '0',
        general_wallet_cap_milli: '300000000',
        daily_source_remaining: '75',
        wallet_cap_blocked: true,
      },
    });
  });
  await page.goto(USER_ORIGIN + '/games/whale-haven');
  await page
    .getByRole('navigation', { name: 'Haven navigation' })
    .getByRole('button', { name: 'Coins' })
    .click();
  await expect(page.getByText('Available now', { exact: false })).toContainText('0 lots');
  await expect(page.getByText('Remaining today', { exact: false })).toContainText('75 whale coins');
  await expect(page.getByText('One lot would take', { exact: false })).toBeVisible();
  await expect(page.getByText('Pending settlement', { exact: false })).toContainText('40%');
  await expect(page.getByRole('button', { name: 'Review quote' })).toBeDisabled();
  expect(requests).toEqual(['0']);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});
