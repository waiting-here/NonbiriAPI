import { randomUUID } from 'node:crypto';
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { expect, test, type Browser, type BrowserContext } from '@playwright/test';
import type { Action, DrawReceipt, Outcome, Snapshot } from '../../src/user/activities/gacha/types';
import catalog from '../../../internal/gacha/catalog.json' with { type: 'json' };
import type { CardStatistics, PoolStatistics } from '../../src/admin/features/gacha/types';
import en from '../../src/admin/i18n/en.json' with { type: 'json' };

type Cookie = { Name: string; Value: string };
interface Fixture {
  user_url: string;
  admin_url: string;
  admin_cookie: Cookie;
  users: { id: string; level: number; cookie: Cookie }[];
}
async function session(browser: Browser, origin: string, cookie: Cookie, admin = false) {
  const context = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  await context.addCookies([
    {
      name: cookie.Name,
      value: cookie.Value,
      domain: new URL(origin).hostname,
      path: admin ? '/admin' : '/api',
      httpOnly: true,
      sameSite: 'Lax',
    },
  ]);
  await context.addInitScript(() => {
    localStorage.setItem('nb.lang', 'en');
    localStorage.setItem('nb.theme', 'light');
  });
  return context;
}

test('real draws agree with administrator participant counts, card totals and full downloads', async ({
  browser,
}) => {
  const f = JSON.parse(readFileSync(process.env.NONBIRI_AUDIT_BROWSER_STATE!, 'utf8')) as Fixture;
  const players = f.users.filter((user) => user.level === 1).slice(0, 2);
  expect(players).toHaveLength(2);
  const manager = await session(browser, f.admin_url, f.admin_cookie, true);
  const users = await Promise.all(
    players.map((player) => session(browser, f.user_url, player.cookie)),
  );
  const path = '/admin/api/gacha/statistics/pools/permanent';
  try {
    const initial = await manager.request.get(f.admin_url + path + '/cards');
    expect(initial.status()).toBe(200);
    expect((await initial.json()).pool.total_draws).toBe('0');

    async function act(context: BrowserContext, action: Action, key = randomUUID()) {
      const response = await context.request.post(f.user_url + '/api/gacha/actions', {
        headers: { Origin: f.user_url, 'Idempotency-Key': key },
        data: action,
      });
      expect(response.status()).toBe(200);
      const result = (await response.json()) as Outcome;
      expect(result.snapshot).toBeDefined();
      return result;
    }
    const receipts: DrawReceipt[] = [];
    for (const [index, context] of users.entries()) {
      await act(context, { action: 'enter' });
      const action: Action = {
        action: 'draw',
        pool_id: 'permanent',
        quantity: index === 0 ? 10 : 1,
      };
      const key = randomUUID();
      const drawn = await act(context, action, key);
      expect(drawn.draw?.items).toHaveLength(action.quantity);
      receipts.push(drawn.draw!);
      // A browser retry after losing the response must not add another draw or person.
      expect((await act(context, action, key)).draw).toEqual(drawn.draw);
    }
    const expected = new Map<string, { count: number; owners: Set<number>; first: number }>();
    receipts.forEach((receipt, person) => {
      receipt.items.forEach((item) => {
        const row = expected.get(item.card_id) ?? { count: 0, owners: new Set<number>(), first: 0 };
        row.count++;
        row.owners.add(person);
        if (!item.duplicate) row.first++;
        expected.set(item.card_id, row);
      });
    });

    const response = await manager.request.get(f.admin_url + path + '/cards?page_size=100');
    expect(response.status()).toBe(200);
    const result = (await response.json()) as { pool: PoolStatistics; items: CardStatistics[] };
    expect(result.pool.total_draws).toBe('11');
    expect(result.pool.participant_count).toBe('2');
    expect(result.items.length).toBeGreaterThan(20);
    for (const row of result.items) {
      const wanted = expected.get(row.card_id);
      expect(row.count).toBe(String(wanted?.count ?? 0));
      expect(row.participant_count).toBe(String(wanted?.owners.size ?? 0));
      expect(row.first_acquisition_count).toBe(String(wanted?.first ?? 0));
    }
    expect(result.items.reduce((sum, row) => sum + Number(row.count), 0)).toBe(11);

    const page = await manager.newPage();
    const copy = en.admin.gachaStats;
    await page.goto(f.admin_url + '/activities/gacha');
    await page.getByRole('tab', { name: copy.tab, exact: true }).click();
    const summary = page.locator('.gacha-stats-summary');
    await expect(
      summary
        .locator('div')
        .filter({ has: page.getByText(copy.totalDraws, { exact: true }) })
        .locator('dd'),
    ).toHaveText('11');
    await expect(
      summary
        .locator('div')
        .filter({ has: page.getByText(copy.participants, { exact: true }) })
        .locator('dd'),
    ).toHaveText('2');
    await expect(
      page.getByRole('columnheader', { name: copy.firstOwners, exact: true }),
    ).toBeVisible();
    await expect(page.getByRole('img', { name: copy.rarityChart, exact: true })).toBeVisible();
    if (process.env.NONBIRI_VISUAL_DIR) {
      mkdirSync(process.env.NONBIRI_VISUAL_DIR, { recursive: true });
      await page.screenshot({
        path: resolve(process.env.NONBIRI_VISUAL_DIR, 'pool-statistics-real.png'),
        fullPage: true,
      });
    }

    // Filtering the visible table must not silently filter the administrator's download.
    await page
      .getByRole('searchbox', { name: copy.searchCards, exact: true })
      .fill('does-not-match-any-card');
    await page.getByRole('searchbox', { name: copy.searchCards, exact: true }).press('Enter');
    await expect(page.getByText(copy.noCardsTitle, { exact: true })).toBeVisible();
    const downloadEvent = page.waitForEvent('download');
    await page.getByRole('link', { name: copy.downloadJSON, exact: true }).click();
    const download = await downloadEvent;
    const downloadPath = await download.path();
    expect(downloadPath).toBeTruthy();
    const exported = JSON.parse(readFileSync(downloadPath!, 'utf8')) as {
      pool: PoolStatistics;
      cards: CardStatistics[];
    };
    expect(exported.pool).toEqual(result.pool);
    expect(exported.cards).toHaveLength(result.items.length);
    expect(exported.cards.reduce((sum, row) => sum + Number(row.count), 0)).toBe(11);
    expect(exported.cards.every((card) => !('user_id' in card))).toBe(true);
    const csv = await manager.request.get(f.admin_url + path + '/export?format=csv');
    expect(csv.status()).toBe(200);
    const csvText = await csv.text();
    expect(csvText.trim().split('\n')).toHaveLength(result.items.length + 1);
    if (process.env.NONBIRI_VISUAL_DIR) {
      await download.saveAs(resolve(process.env.NONBIRI_VISUAL_DIR, 'pool-statistics-real.json'));
      writeFileSync(resolve(process.env.NONBIRI_VISUAL_DIR, 'pool-statistics-real.csv'), csvText);
    }

    const denied = await users[0]!.request.get(f.admin_url + path + '/export?format=json');
    expect([401, 403]).toContain(denied.status());
  } finally {
    await Promise.all([...users, manager].map((context) => context.close()));
  }
});

test('real shop card redemptions agree with administrator totals and exclude ticket exchanges', async ({
  browser,
}) => {
  const f = JSON.parse(readFileSync(process.env.NONBIRI_AUDIT_BROWSER_STATE!, 'utf8')) as Fixture;
  const players = f.users.filter((user) => user.level === 1).slice(0, 2);
  const manager = await session(browser, f.admin_url, f.admin_cookie, true);
  const users = await Promise.all(
    players.map((player) => session(browser, f.user_url, player.cookie)),
  );
  const root = '/admin/api/gacha/statistics/shop';
  interface ShopData {
    summary: { total_redemptions: string; participant_count: string };
    items: CardStatistics[];
  }
  async function readShop(): Promise<ShopData> {
    const response = await manager.request.get(f.admin_url + root + '/cards?page_size=100');
    expect(response.status()).toBe(200);
    return response.json();
  }
  async function post<T>(
    context: BrowserContext,
    origin: string,
    path: string,
    data: unknown,
    key: string = randomUUID(),
  ): Promise<T> {
    const response = await context.request.post(origin + path, {
      headers: { Origin: origin, 'Idempotency-Key': key },
      data,
    });
    expect(response.status()).toBe(200);
    return response.json();
  }
  const act = (context: BrowserContext, data: Action, key?: string) =>
    post<Outcome>(context, f.user_url, '/api/gacha/actions', data, key);
  try {
    const before = await readShop();
    const states: Snapshot[] = [];
    for (const context of users) states.push((await act(context, { action: 'enter' })).snapshot);
    const redeemable = catalog.cards.filter(
      (card) => card.source === 'shop' && states.every((state) => !state.owned[card.id]),
    );
    expect(redeemable.length).toBeGreaterThanOrEqual(3);
    const [first, second, rewardOnly] = redeemable;
    const newParticipants = states.filter(
      (state) => !Object.values(state.owned).some((card) => card.source === 'shop'),
    ).length;
    const campaign = await post<{ code: string }>(
      manager,
      f.admin_url,
      '/admin/api/rewards/create',
      {
        kind: 'code',
        title: 'Synthetic shop statistics supplies',
        enabled: true,
        conditions: { op: 'all' },
        rewards: [
          { asset: 'gacha:shard:N', quantity: String(states[0]!.shop.chibi_n * 3) },
          { asset: 'gacha:shard:R', quantity: String(states[0]!.shop.chibi_r * 3) },
          { asset: 'gacha:card:' + rewardOnly!.id, quantity: '1' },
        ],
      },
    );
    for (const context of users)
      await post(context, f.user_url, '/api/rewards/redeem', { code: campaign.code });
    expect((await readShop()).summary).toEqual(before.summary);

    for (const [index, context] of users.entries()) {
      const action: Action = {
        action: 'redeem',
        product: first!.id,
        revision: states[index]!.shop.revision,
      };
      const key = randomUUID();
      expect((await act(context, action, key)).snapshot.owned[first!.id]!.source).toBe('shop');
      expect((await act(context, action, key)).snapshot.owned[first!.id]!.source).toBe('shop');
    }
    await act(users[0]!, {
      action: 'redeem',
      product: second!.id,
      revision: states[0]!.shop.revision,
    });
    // Exchanging remaining shards for a ticket must not enter the card statistics.
    await act(users[0]!, {
      action: 'redeem',
      product: 'ticket-n',
      revision: states[0]!.shop.revision,
    });
    const after = await readShop();
    expect(after.summary.total_redemptions).toBe(
      (BigInt(before.summary.total_redemptions) + 3n).toString(),
    );
    expect(after.summary.participant_count).toBe(
      (BigInt(before.summary.participant_count) + BigInt(newParticipants)).toString(),
    );
    for (const [id, increment] of [
      [first!.id, 2],
      [second!.id, 1],
      [rewardOnly!.id, 0],
    ] as const) {
      const previous = before.items.find((card) => card.card_id === id)?.count ?? '0';
      const row = after.items.find((card) => card.card_id === id)!;
      expect(row.count).toBe((BigInt(previous) + BigInt(increment)).toString());
      expect(row.participant_count).toBe(row.count);
      expect(row.first_acquisition_count).toBe(row.count);
    }

    const page = await manager.newPage();
    const copy = en.admin.gachaShopStats;
    await page.goto(f.admin_url + '/activities/gacha');
    await page.getByRole('tab', { name: copy.tab, exact: true }).click();
    const summary = page.locator('.gacha-stats-summary');
    await expect(
      summary
        .locator('div')
        .filter({ has: page.getByText(copy.totalRedemptions, { exact: true }) })
        .locator('dd'),
    ).toHaveText(after.summary.total_redemptions);
    await expect(
      summary
        .locator('div')
        .filter({ has: page.getByText(copy.participants, { exact: true }) })
        .locator('dd'),
    ).toHaveText(after.summary.participant_count);
    await page.reload();
    await expect(page.getByRole('tab', { name: copy.tab, exact: true })).toHaveAttribute(
      'aria-selected',
      'true',
    );
    await expect(page.getByRole('img', { name: copy.rarityChart, exact: true })).toBeVisible();
    if (process.env.NONBIRI_VISUAL_DIR) {
      mkdirSync(process.env.NONBIRI_VISUAL_DIR, { recursive: true });
      await page.screenshot({
        path: resolve(process.env.NONBIRI_VISUAL_DIR, 'shop-statistics-real.png'),
        fullPage: true,
      });
    }
    const pending = page.waitForEvent('download');
    await page.getByRole('link', { name: en.admin.gachaStats.downloadJSON, exact: true }).click();
    const download = await pending;
    const exported = JSON.parse(readFileSync((await download.path())!, 'utf8')) as {
      summary: ShopData['summary'];
      cards: CardStatistics[];
    };
    expect(exported.summary).toEqual(after.summary);
    expect(exported.cards).toHaveLength(after.items.length);
    const csvPending = page.waitForEvent('download');
    await page.getByRole('link', { name: en.admin.gachaStats.downloadCSV, exact: true }).click();
    const csvDownload = await csvPending;
    const csvText = readFileSync((await csvDownload.path())!, 'utf8');
    expect(csvText.trim().split('\n')).toHaveLength(after.items.length + 1);
    expect(csvText.split('\n')[0].trim()).toBe(
      'total_redemptions,shop_participant_count,card_id,name,name_en,rarity,count,participant_count,first_acquisition_count',
    );
    if (process.env.NONBIRI_VISUAL_DIR) {
      await download.saveAs(resolve(process.env.NONBIRI_VISUAL_DIR, 'shop-statistics-real.json'));
      await csvDownload.saveAs(resolve(process.env.NONBIRI_VISUAL_DIR, 'shop-statistics-real.csv'));
    }
    const denied = await users[0]!.request.get(f.admin_url + root + '/export?format=json');
    expect([401, 403]).toContain(denied.status());
  } finally {
    await Promise.all([...users, manager].map((context) => context.close()));
  }
});
