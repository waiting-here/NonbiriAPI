import { gamesSnapshotWire } from '../../src/user/games/common/testFixtures';
import { mkdir, readdir } from 'node:fs/promises';
import { join } from 'node:path';
import { expect, test } from './test';
import { collectConsoleViolations, mockPublicConfig, mockRoleSession } from './support';
import { USER_ORIGIN } from './ports';
import type {
  Action,
  Profile,
  Neighbor,
  NeighborHouse,
} from '../../src/user/games/whale-haven/api';

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
        feed_silver_fish_grams: 200,
        coupon_discount_bp: 2000,
        decor_jelly_lamp_price_pearls: 85,
        decor_coral_tea_price_pearls: 115,
        decor_golden_clam_price_pearls: 168,
        decor_shell_cabinet_price_pearls: 68,
        decor_pearl_clock_price_pearls: 48,
        decor_sea_planter_price_pearls: 58,
        outfit_knit_beret_price_pearls: 150,
        outfit_winter_snow_price_pearls: 185,
        outfit_whale_hoodie_price_pearls: 220,
      },
    },
    social: { open: false, door: 'bay-a', accept_work: false, away: null, guests: [] },
    snapshot: {
      revision: 1,
      interactionAffectionRemaining: 3,
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

test('an oversized serving needs confirmation even in an empty custom bowl', async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem('nb.lang', 'en'));
  await mockRoleSession(page, 'user', 'user');
  await mockPublicConfig(page, 'user');
  const profile = initial();
  profile.snapshot.whale.hasCompletedNaming = true;
  profile.settings.values.trough_grams = 100;
  profile.snapshot.inventory.silver_fish = 1;
  const sent: Action[] = [];
  await page.route(USER_ORIGIN + '/api/games/whale-haven/**', async (route) => {
    if (route.request().method() === 'POST') sent.push(route.request().postDataJSON());
    await route.fulfill({ json: profile });
  });
  await page.goto(USER_ORIGIN + '/games/whale-haven');
  await page.getByRole('button', { name: 'Bag', exact: true }).click();
  await expect(page.getByText(/Refill tickets only use servings that fit/)).toBeVisible();
  await page.getByRole('button', { name: 'Fill the bowl', exact: true }).click();
  const dialog = page.getByRole('alertdialog');
  await expect(dialog).toContainText('100 g');
  await dialog.getByRole('button', { name: 'Cancel' }).click();
  expect(sent).toEqual([]);
});

test('tailor coupon savings agree on the shelf, at confirmation and after purchase', async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.addInitScript(() => localStorage.setItem('nb.lang', 'zh'));
  await mockRoleSession(page, 'user', 'user');
  await mockPublicConfig(page, 'user');
  const profile = initial();
  const { home, wallet, whale } = profile.snapshot;
  whale.hasCompletedNaming = true;
  wallet.pearls = 100;
  const buffs = {
    autoRefillEnabled: false,
    autoRefillTickets: 0,
    aromaCharges: 0,
    aromaActiveGrams: 0,
    wardrobeDiscountCoupons: 1,
  };
  home.buffs = buffs;
  profile.settings.exchange_enabled = false;
  profile.settings.values.decor_shell_cabinet_price_pearls = 1;
  profile.settings.values.decor_pearl_clock_price_pearls = 0;
  const sent: Action[] = [];
  await page.route(USER_ORIGIN + '/api/games/whale-haven/**', async (route) => {
    if (route.request().method() === 'POST') {
      const action = route.request().postDataJSON();
      expect(action).toMatchObject({
        type: 'toggle_decor',
        decorId: 'jelly_lamp',
        expected_profile_revision: '1',
      });
      sent.push(action);
      home.ownedDecor.push('jelly_lamp');
      home.equippedDecor.push('jelly_lamp');
      buffs.wardrobeDiscountCoupons = 0;
      wallet.pearls = 32;
      profile.snapshot.revision++;
    }
    await route.fulfill({ json: profile });
  });
  await page.goto(USER_ORIGIN + '/games/whale-haven');
  await page.getByRole('button', { name: '角色', exact: true }).click();
  const outfit = page.getByRole('article').filter({ hasText: '鲸鱼贝雷帽针织裙' });
  const outfitPrice = '原价 150 枚 · 1 张裁缝券抵 30 枚 · 实付 120 枚小鲸币';
  const dialog = page.getByRole('alertdialog');
  await expect(outfit).toContainText(outfitPrice);
  await outfit.getByRole('button', { name: '解锁衣装' }).click();
  await expect(dialog).toContainText(outfitPrice);
  await dialog.getByRole('button', { name: '取消' }).click();
  await page.getByRole('button', { name: '收起菜单面板' }).click();
  await page.getByRole('button', { name: '鲸币', exact: true }).click();
  for (const [name, total] of [
    ['珍珠舷窗挂钟', 0],
    ['贝壳橡木矮柜', 1],
  ] as const) {
    const item = page.getByRole('listitem').filter({ hasText: name });
    const kept = `实付 ${total} 枚小鲸币 · 裁缝券保留`;
    await expect(item).toContainText(kept);
    await item.getByRole('button', { name: '解锁家具' }).click();
    await expect(dialog).toContainText(kept);
    await dialog.getByRole('button', { name: '取消' }).click();
  }
  expect(sent).toHaveLength(0);
  const lamp = page.getByRole('listitem').filter({ hasText: '幽蓝数据水母夜灯' });
  const lampPrice = '原价 85 枚 · 1 张裁缝券抵 17 枚 · 实付 68 枚小鲸币';
  await expect(lamp).toContainText(lampPrice);
  await lamp.getByRole('button', { name: '解锁家具' }).click();
  await expect(dialog).toContainText(lampPrice);
  await dialog.getByRole('button', { name: '确认', exact: true }).click();
  await expect(
    page.getByRole('status').filter({ hasText: '已解锁并布置幽蓝数据水母夜灯' }),
  ).toContainText(lampPrice);
  await expect(lamp.getByRole('button', { name: '收起', exact: true })).toBeVisible();
  expect(sent).toHaveLength(1);
  await page.getByRole('button', { name: '收起菜单面板' }).click();
  await page.getByRole('button', { name: '角色', exact: true }).click();
  await expect(outfit).toContainText('实付 150 枚小鲸币');
  await expect(outfit).not.toContainText('裁缝券');
  await expect(page.getByText('32 小鲸币', { exact: true })).toBeVisible();
});

test('a visiting keeper sees hiring and meal state consistently in both havens', async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.addInitScript(() => localStorage.setItem('nb.lang', 'zh'));
  await mockRoleSession(page, 'user', 'user');
  await mockPublicConfig(page, 'user');
  const profile = initial();
  profile.snapshot.whale.hasCompletedNaming = true;
  profile.social.open = true;
  profile.snapshot.neighbors = [neighbor];
  profile.snapshot.whale.location = {
    kind: 'visiting',
    neighborId: neighbor.id,
    startedAt: Date.now(),
  };
  profile.social.away = {
    target: neighbor.id,
    label: '门牌 bay-b',
    allow_hire: true,
    working: false,
    started_at_ms: Date.now(),
  };
  await page.route(USER_ORIGIN + '/api/games/whale-haven/**', (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith('/neighbors/house'))
      return route.fulfill({
        json: {
          house: neighbor,
          guests: [
            {
              whaleName: '小鲸鱼',
              whaleOutfit: 'maid_classic',
              stage: profile.social.away?.working ? 'working' : 'eating',
              mine: true,
            },
            { whaleName: '小满', whaleOutfit: 'winter_snow', stage: 'working', mine: false },
          ],
        },
      });
    return route.fulfill({
      json: path.endsWith('/neighbors/search') ? { items: [neighbor] } : profile,
    });
  });
  await page.goto(USER_ORIGIN + '/games/whale-haven');
  await expect(page.getByText(/已吃 0 克 · 餐食奖励预计 0 枚/)).toBeVisible();
  await page.getByRole('button', { name: '去邻居家看她' }).click();
  await expect(page.getByText('小鲸鱼 · 串门用餐')).toBeVisible();
  await expect(page.getByText('小满 · 正在帮工')).toBeVisible();
  profile.social.away.working = true;
  profile.social.away.work_ends_at_ms = Date.now() + 120000;
  await page.getByRole('button', { name: '串门', exact: true }).click();
  await page.getByRole('button', { name: '刷新串门状态' }).click();
  await expect(page.getByRole('heading', { name: '在外帮工' })).toBeVisible();
  await expect(page.getByText(/预计收工/)).toBeVisible();
  await expect(page.getByText('小鲸鱼 · 正在帮工')).toBeAttached();
  await expect(page.getByText('小鲸鱼 · 串门用餐')).not.toBeAttached();
  await page.getByRole('button', { name: '首页', exact: true }).click();
  await expect(page.getByText('在外帮工', { exact: true })).toBeVisible();
  await expect(page.getByText('工资按帮工产出的 20% 结算')).toBeVisible();
});

test('an observer sees guests, an empty room and a closed door on the same visit', async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.addInitScript(() => localStorage.setItem('nb.lang', 'zh'));
  await mockRoleSession(page, 'user', 'user');
  await mockPublicConfig(page, 'user');
  const profile = initial();
  profile.snapshot.whale.hasCompletedNaming = true;
  profile.social.open = true;
  const house: NeighborHouse = {
    house: { ...neighbor, whaleAtHome: false },
    guests: [
      { whaleName: '海星', whaleOutfit: 'maid_classic', stage: 'eating', mine: false },
      { whaleName: '月牙', whaleOutfit: 'winter_snow', stage: 'working', mine: false },
    ],
  };
  let closed = false;
  await page.route(USER_ORIGIN + '/api/games/whale-haven/**', (route) => {
    const url = new URL(route.request().url());
    if (url.pathname.endsWith('/neighbors/search'))
      return route.fulfill({ json: { items: [neighbor] } });
    if (url.pathname.endsWith('/neighbors/house')) {
      expect(url.searchParams.get('door')).toBe(neighbor.id);
      return closed
        ? route.fulfill({
            status: 404,
            json: { error: { code: 'not_found', message: '小屋未开放或已无法访问。' } },
          })
        : route.fulfill({ json: house });
    }
    return route.fulfill({ json: profile });
  });
  await page.goto(USER_ORIGIN + '/games/whale-haven');
  const button = (name: string) => page.getByRole('button', { name, exact: true });
  await button('串门').click();
  await button('看看小筑').click();
  const guests = page.getByRole('group', { name: '来访客鲸' });
  await expect(guests.getByText('海星 · 串门用餐')).toBeInViewport();
  await expect(guests.getByText('月牙 · 正在帮工')).toBeInViewport();
  await expect(guests.getByRole('button')).toHaveCount(0);
  await expect(page.getByText('客位已满')).toBeVisible();
  const frontTag = await guests.getByText('海星 · 串门用餐').boundingBox();
  const bowlBadge = await page.locator('.diegetic-prop--right .diegetic-prop__badge').boundingBox();
  expect(frontTag!.y + frontTag!.height).toBeLessThanOrEqual(bowlBadge!.y);
  await expect(page.getByText('屋主鲸暂时不在家。')).toBeVisible();
  house.guests = [];
  await button('串门').click();
  await button('刷新串门状态').click();
  await expect(page.getByText('暂无客鲸来访')).toBeAttached();
  await expect(guests.locator('figure')).toHaveCount(0);
  closed = true;
  await button('刷新串门状态').click();
  await expect(page.getByRole('alert')).toBeVisible();
  await expect(guests).toHaveCount(0);
  await button('回到我的小筑').click();
  await expect(button('唠嗑')).toBeVisible();
});

test('selecting the second guest opens its own controls and respects consent', async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem('nb.lang', 'zh'));
  await mockRoleSession(page, 'user', 'user');
  await mockPublicConfig(page, 'user');
  const profile = initial();
  profile.snapshot.whale.hasCompletedNaming = true;
  profile.social.open = true;
  const second = { ...neighbor, id: 'bay-c', whaleName: '青空' };
  profile.snapshot.neighbors = [neighbor, second];
  profile.snapshot.guestVisits = [neighbor, second].map((n, i) => ({
    neighborId: n.id,
    slotIndex: i as 0 | 1,
    arrivedAt: Date.now(),
    eatenGrams: 0,
    stage: 'eating',
  }));
  profile.social.guests = [neighbor, second].map((n, i) => ({
    neighbor_id: n.id,
    label: n.id,
    whale_name: n.whaleName,
    whale_outfit: 'maid_classic',
    allow_hire: i === 0,
    working: false,
    started_at_ms: Date.now(),
  }));
  await page.route(USER_ORIGIN + '/api/games/whale-haven/**', (route) =>
    route.fulfill({
      json: route.request().url().includes('neighbors/search') ? { items: [] } : profile,
    }),
  );
  await page.goto(USER_ORIGIN + '/games/whale-haven');
  await page.getByRole('button', { name: '查看客鲸青空，只来串门' }).press('Enter');
  await expect(page.getByRole('heading', { name: '青空 · bay-c' })).toBeFocused();
  const card = page
    .getByRole('article')
    .filter({ has: page.getByRole('heading', { name: '青空 · bay-c' }) });
  await expect(card.getByRole('button', { name: '温和送回' })).toBeVisible();
  await expect(card.getByRole('button', { name: '雇佣帮工' })).toHaveCount(0);
});

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
          if (body.type === 'use_feed') {
            profile.snapshot.inventory.silver_fish--;
            profile.snapshot.home.troughGrams = Math.min(
              profile.settings.values.trough_grams,
              profile.snapshot.home.troughGrams + profile.settings.values.feed_silver_fish_grams,
            );
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
        const servings = page.getByRole('spinbutton', { name: /购买份数/ });
        const buyFeed = page.getByRole('button', { name: '购买口粮' });
        await servings.fill('100');
        await expect(buyFeed).toBeDisabled();
        await expect(page.getByText('请输入 1–99 的整数。')).toBeVisible();
        await servings.fill('3');
        const item = page.getByRole('region', { name: '当前道具' });
        await expect(item).toContainText('合计 57 游戏积分');
        await item.scrollIntoViewIfNeeded();
        const field = (await servings.boundingBox())!;
        const button = (await buyFeed.boundingBox())!;
        expect(button.height).toBeGreaterThanOrEqual(40);
        expect(button.height).toBeLessThanOrEqual(field.height + 8);
        expect(await item.evaluate((node) => node.scrollWidth <= node.clientWidth)).toBe(true);
        await capture('backpack');
        await buyFeed.click();
        const buy = page.getByRole('alertdialog');
        await expect(buy).toContainText('×3');
        await expect(buy).toContainText('57');
        await buy.getByRole('button', { name: '确认', exact: true }).click();
        await expect
          .poll(() => commands.filter((c) => c.type === 'buy_feed'))
          .toMatchObject([{ quantity: 3, feedId: 'silver_fish' }]);
        await expect(page.getByRole('status')).toContainText('已购入 热腾腾大碗白米饭 ×3');
        const fills = () => commands.filter((command) => command.type === 'use_feed');
        const fill = item.getByRole('button', { name: '盛入饭盆' });
        await fill.click();
        await expect(item).toContainText('库存 2 份');
        expect(fills()).toMatchObject([{ maxSpillGrams: 0 }]);
        await fill.click();
        const spill = page.getByRole('alertdialog', { name: '饭碗装不下整份口粮' });
        await expect(spill).toContainText('预计最多会浪费 40 克');
        await expect(spill.getByRole('button', { name: '取消' })).toBeFocused();
        await capture('refill');
        await spill.getByRole('button', { name: '取消' }).click();
        await expect(fill).toBeFocused();
        await expect(item).toContainText('库存 2 份');
        expect(fills()).toHaveLength(1);
        await page.getByRole('button', { name: '收起菜单面板' }).click();
        await page.getByTitle(/点击一键添饭/).click();
        await expect(spill).toContainText('这份口粮有 200 克');
        await spill.getByRole('button', { name: '仍然添饭' }).click();
        await expect(page.getByLabel('余粮', { exact: true })).toHaveText('360g');
        expect(fills()).toMatchObject([{ maxSpillGrams: 0 }, { maxSpillGrams: 40 }]);
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

test('a cold headpat keeps idle frames moving and plays the decoded action from frame zero', async ({
  page,
}, info) => {
  await mockRoleSession(page, 'user', 'user');
  await mockPublicConfig(page, 'user');
  await page.emulateMedia({ reducedMotion: 'no-preference' });
  await page.addInitScript(() => {
    localStorage.setItem('nb.lang', 'zh');
    const draw = CanvasRenderingContext2D.prototype.drawImage;
    CanvasRenderingContext2D.prototype.drawImage = function (image: CanvasImageSource, ...coords: number[]) {
      const [x, y] = coords;
      if (
        image instanceof HTMLImageElement &&
        this.canvas.classList.contains('whale-rig-canvas') &&
        /\/(idle|waving)\.webp/.test(image.src)
      ) {
        const name = image.src.includes('/waving.') ? 'wave' : 'idle';
        const trace = this.canvas.dataset;
        const cell = String(x) + ',' + String(y);
        if (trace[name] !== cell) {
          trace[name] = cell;
          trace[name + 'Count'] = String(Number(trace[name + 'Count'] ?? '0') + 1);
          trace[name + 'First'] ??= cell;
        }
      }
      return Reflect.apply(draw, this, [image, ...coords]);
    };
  });
  const profile = initial();
  profile.snapshot.whale.hasCompletedNaming = true;
  profile.snapshot.whale.petLookEnabled = false;
  profile.snapshot.whale.petRoamEnabled = false;
  await page.route(USER_ORIGIN + '/api/games/whale-haven/**', (route) =>
    route.fulfill({ json: profile }),
  );
  let release!: () => void;
  const decoded = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route('**/i2v/waving.webp', async (route) => {
    await decoded;
    await route.continue();
  });
  await page.goto(USER_ORIGIN + '/games/whale-haven');
  const canvas = page.locator('canvas.whale-rig-canvas').first();
  await expect(canvas).toBeVisible();
  const requested = page.waitForRequest('**/i2v/waving.webp');
  await page.getByRole('button', { name: /摸摸头（/ }).click();
  await requested;
  const before = Number(await canvas.getAttribute('data-idle-count'));
  // A single loop is 4.45 s. A delayed decode must not consume its lifetime.
  await expect
    .poll(async () => Number(await canvas.getAttribute('data-idle-count')), { timeout: 8000 })
    .toBeGreaterThan(before + 106);
  release();
  await expect(canvas).toHaveAttribute('data-wave-first', '0,0');
  await expect
    .poll(async () => Number(await canvas.getAttribute('data-wave-count')))
    .toBeGreaterThan(4);
  const evidence = process.env.NONBIRI_VISUAL_DIR;
  if (evidence) {
    await mkdir(evidence, { recursive: true });
    await canvas.screenshot({ path: join(evidence, 'headpat-cold-decoded.png') });
  }
  await expect
    .poll(async () => Number(await canvas.getAttribute('data-wave-count')), { timeout: 8000 })
    .toBeGreaterThan(85);
  await info.attach('decoded-visual-frames', {
    body: String(await canvas.getAttribute('data-wave-count')),
    contentType: 'text/plain',
  });
});

test('menu position takes effect in place and routine interactions reply in the cabin', async ({
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.addInitScript(() => localStorage.setItem('nb.lang', 'zh'));
  await mockRoleSession(page, 'user', 'user');
  await mockPublicConfig(page, 'user');
  const profile = initial();
  profile.snapshot.whale.hasCompletedNaming = true;
  let failChat = false;
  let releaseHead: (() => void) | undefined;
  const heldHead = new Promise<void>((resolve) => {
    releaseHead = resolve;
  });
  profile.settings.values.affection_per_level = 250;
  profile.snapshot.whale.affection = 120;
  await page.route(USER_ORIGIN + '/api/games/whale-haven/**', async (route) => {
    if (route.request().method() === 'GET') return route.fulfill({ json: profile });
    const { type } = route.request().postDataJSON();
    if (type === 'interact') await heldHead;
    if (type === 'buy_feed')
      return route.fulfill({
        status: 409,
        json: { error: { code: 'conflict', message: '游戏积分不足。' } },
      });
    if (type === 'chat' && failChat)
      return route.fulfill({
        status: 409,
        json: { error: { code: 'conflict', message: '小鲸正在串门，先召回她吧。' } },
      });
    profile.snapshot.revision++;
    if (
      (type === 'chat' || type === 'interact') &&
      profile.snapshot.interactionAffectionRemaining > 0
    ) {
      profile.snapshot.interactionAffectionRemaining--;
      profile.snapshot.whale.affection += type === 'chat' ? 2 : 3;
    }
    profile.snapshot.whale.tempEmotion = {
      expression: 'cheerful',
      line: type === 'chat' ? '听我讲个海湾冷笑话！' : '摸摸头，好开心！',
      expiresAt: Date.now() + 30000,
    };
    return route.fulfill({ json: profile });
  });
  await page.goto(USER_ORIGIN + '/games/whale-haven');
  const button = (name: string) => page.getByRole('button', { name, exact: true });
  const panel = page.getByRole('complementary', { name: '背包与投喂' });
  const servings = panel.getByRole('spinbutton', { name: /购买份数/ });
  await button('设置').click();
  await button('将菜单移到底部').click();
  await expect(panel.getByRole('heading', { name: '背包与投喂' })).toBeFocused();
  const bottom = (await panel.boundingBox())!;
  await servings.fill('7');
  await button('将菜单移到右侧').click();
  await expect.poll(async () => (await panel.boundingBox())!.x).toBeGreaterThan(bottom.x + 80);
  await expect(servings).toHaveValue('7');
  await button('设置').click();
  await button('将菜单移到底部').click();
  await expect(servings).toHaveValue('7');
  await button('购买口粮').click();
  const order = page.getByRole('alertdialog');
  await order.getByRole('button', { name: '确认', exact: true }).click();
  await expect(order.getByRole('alert')).toHaveText('游戏积分不足。');
  await order.getByRole('button', { name: '取消', exact: true }).click();
  await expect(servings).toHaveValue('7');
  await page.keyboard.press('Escape');
  await expect(button('背包')).toBeFocused();
  await page.reload();
  await button('背包').click();
  await expect(button('将菜单移到右侧')).toBeVisible();
  await button('收起菜单面板').click();
  const chat = button('唠嗑/冷笑话');
  await chat.click();
  await expect(page.getByRole('button', { name: /听我讲个海湾冷笑话/ })).toBeVisible();
  await expect(button('关闭提示')).toHaveCount(0);
  const waving = page.waitForRequest((request) => /waving.*\.webp/.test(request.url()));
  await page.getByRole('button', { name: /摸摸头（/ }).click();
  await waving;
  await expect(page.getByRole('button', { name: /摸摸头，好开心/ })).toHaveCount(0);
  releaseHead?.();
  await expect(page.getByRole('button', { name: /摸摸头，好开心/ })).toBeVisible();
  await expect(button('关闭提示')).toHaveCount(0);
  failChat = true;
  await chat.click();
  await expect(page.getByRole('alert')).toHaveText(/先召回/);
  failChat = false;
  await chat.click();
  await expect(page.getByRole('button', { name: /听我讲个海湾冷笑话/ })).toBeVisible();
  await expect(page.getByText('今天的三次互动羁绊奖励已领完，仍可继续互动。')).toBeVisible();
  await button('关闭提示').click();
  await page.getByRole('button', { name: /摸摸头（/ }).click();
  await expect(page.getByRole('button', { name: /摸摸头，好开心/ })).toBeVisible();
  await expect(button('关闭提示')).toHaveCount(0);
  await page.setViewportSize({ width: 390, height: 844 });
  const status = page.getByTitle('点击展开鲸鱼娘详细状态、改名与试衣间快捷入口');
  await status.click();
  await expect(
    page.getByText('今日免费互动的羁绊经验已领完，明天恢复。仍可继续互动。'),
  ).toBeVisible();
  await expect(page.getByText('127 / 250')).toBeVisible();
  const meter = page.locator('.stat-meter__bar').filter({ has: page.locator('[style*="50.8%"]') });
  await expect(meter).toBeVisible();
  await status.click();
  await button('设置').click();
  await expect(page.getByRole('group', { name: '小屋设置' })).toBeVisible();
  await expect(page.getByRole('button', { name: /将菜单移/ })).toHaveCount(0);
});

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
  await expect(page.getByText('Progress toward next wage coin', { exact: false })).toContainText(
    '40%',
  );
  await expect(page.getByRole('button', { name: 'Review quote' })).toBeDisabled();
  expect(requests).toEqual(['0']);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});
