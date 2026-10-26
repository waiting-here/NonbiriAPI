import { randomUUID } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { expect, test, type Browser, type BrowserContext } from '@playwright/test';
import type { Action, Outcome, Snapshot } from '../../src/user/activities/gacha/types';
import type { Campaign, Redemption } from '../../src/shared/rewards/api';

type Cookie = { Name: string; Value: string };
interface Fixture {
  user_url: string;
  admin_url: string;
  users: { id: string; level: number; cookie: Cookie }[];
  admin_cookie: Cookie;
}
function fixture(): Fixture {
  return JSON.parse(readFileSync(process.env.NONBIRI_AUDIT_BROWSER_STATE!, 'utf8')) as Fixture;
}
async function session(browser: Browser, origin: string, cookie: Cookie, admin = false) {
  const context = await browser.newContext({ viewport: { width: 1280, height: 900 } });
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
    localStorage.setItem(
      'nb.gacha.preferences',
      JSON.stringify({ music: false, sound: false, reduced: true }),
    );
  });
  return context;
}
async function api(
  context: BrowserContext,
  origin: string,
  path: string,
  method = 'GET',
  data?: unknown,
) {
  return context.request.fetch(origin + path, {
    method,
    data,
    headers: { Origin: origin, 'Idempotency-Key': randomUUID() },
  });
}
function milli(credits: string) {
  const negative = credits.startsWith('-');
  const [whole, fraction = ''] = credits.replace(/^-/, '').split('.');
  const amount = BigInt(whole) * 1000n + BigInt(fraction.padEnd(3, '0'));
  return negative ? -amount : amount;
}
function credits(amount: bigint) {
  const unsigned = amount < 0n ? -amount : amount;
  const fraction = (unsigned % 1000n).toString().padStart(3, '0').replace(/0+$/, '');
  return (
    (amount < 0n ? '-' : '') + (unsigned / 1000n).toString() + (fraction ? '.' + fraction : '')
  );
}

test('daily bundles persist real ticket grants, game-first payments and independent purchase limits', async ({
  browser,
}) => {
  const f = fixture();
  const player = f.users.find((user) => user.level === 1)!;
  const user = await session(browser, f.user_url, player.cookie);
  const admin = await session(browser, f.admin_url, f.admin_cookie, true);
  const userPath = '/admin/api/users/' + player.id;
  async function readState(): Promise<Snapshot> {
    const response = await api(user, f.user_url, '/api/gacha/state');
    expect(response.status()).toBe(200);
    return response.json();
  }
  async function adminUser() {
    const response = await api(admin, f.admin_url, userPath);
    expect(response.status()).toBe(200);
    return response.json() as Promise<{ revision: string; balance: string; game_balance: string }>;
  }
  async function setWallet(target: 'balance' | 'game_balance', desired: string) {
    const current = await adminUser();
    const delta = milli(desired) - milli(current[target]);
    if (delta === 0n) return;
    const response = await api(admin, f.admin_url, userPath, 'PATCH', {
      mode: 'economy',
      expected_revision: current.revision,
      target,
      direction: delta > 0n ? 'increase' : 'decrease',
      amount: credits(delta < 0n ? -delta : delta),
      reason: 'Synthetic bundle payment setup',
    });
    expect(response.status()).toBe(200);
  }
  const original = await adminUser();
  try {
    await setWallet('game_balance', '15000.001');
    const page = await user.newPage();
    const errors: string[] = [];
    page.on('pageerror', (error) => errors.push(error.message));
    await page.goto(f.user_url + '/activities/gacha');
    await page.getByRole('tab', { name: 'Mystery shop', exact: true }).click();
    const shop = page.getByRole('region', { name: 'Bulk bargains', exact: true });
    const before = await readState();
    expect(milli(before.credits.general)).toBeGreaterThanOrEqual(4_999_999n);
    expect(before.credits.game).toBe('15000.001');
    expect(before.redeemed).not.toContain('bulk-permanent');
    expect(before.redeemed).not.toContain('bulk-limited');
    await expect(shop.getByText('Remaining today: 1/1 bundles', { exact: true })).toHaveCount(2);

    async function buy(product: string, label: string) {
      const offer = shop
        .locator('.gacha-offer')
        .filter({ has: page.getByRole('heading', { name: label, exact: true }) });
      const receipt = page.waitForResponse((response) => {
        if (
          new URL(response.url()).pathname !== '/api/gacha/actions' ||
          response.request().method() !== 'POST'
        )
          return false;
        const input = response.request().postDataJSON() as Action;
        return input.action === 'redeem' && input.product === product;
      });
      await offer.getByRole('button', { name: 'Buy', exact: true }).click();
      const response = await receipt;
      expect(response.status()).toBe(200);
      const result = (await response.json()) as Outcome;
      expect(result.snapshot).toBeDefined();
      await expect(
        offer.getByRole('button', { name: 'Purchased today', exact: true }),
      ).toBeDisabled();
      return result.snapshot;
    }

    const standard = await buy('bulk-permanent', 'Standard ticket ×10');
    expect(standard.tickets).toEqual({
      permanent: before.tickets.permanent + 10,
      limited: before.tickets.limited,
    });
    expect(standard.credits).toEqual({ general: before.credits.general, game: '5000.001' });
    expect(standard.redeemed).toContain('bulk-permanent');
    expect(standard.redeemed).not.toContain('bulk-limited');
    expect((await readState()).credits).toEqual(standard.credits);
    await expect(shop.getByRole('button', { name: 'Buy', exact: true })).toBeEnabled();
    const denied = await api(user, f.user_url, '/api/gacha/actions', 'POST', {
      action: 'redeem',
      product: 'bulk-permanent',
      revision: before.shop.revision,
    });
    expect(denied.status()).toBe(200);
    expect(await denied.json()).toEqual({ rejection: 'daily-limit' });
    const unchanged = await readState();
    expect(unchanged.tickets).toEqual(standard.tickets);
    expect(unchanged.credits).toEqual(standard.credits);

    const limited = await buy('bulk-limited', 'Limited ticket ×10');
    expect(limited.tickets).toEqual({
      permanent: before.tickets.permanent + 10,
      limited: before.tickets.limited + 10,
    });
    expect(limited.credits).toEqual({
      general: credits(milli(before.credits.general) - 4_999_999n),
      game: '0',
    });
    expect(limited.redeemed).toEqual(expect.arrayContaining(['bulk-permanent', 'bulk-limited']));
    await expect(shop.getByRole('button', { name: 'Purchased today', exact: true })).toHaveCount(2);
    await page.reload();
    await page.getByRole('tab', { name: 'Mystery shop', exact: true }).click();
    await expect(shop.getByText('Remaining today: 0/1 bundles', { exact: true })).toHaveCount(2);
    const buttons = shop.getByRole('button', { name: 'Purchased today', exact: true });
    await expect(buttons).toHaveCount(2);
    await expect(buttons.nth(0)).toBeDisabled();
    await expect(buttons.nth(1)).toBeDisabled();
    const persisted = await readState();
    expect(persisted.tickets).toEqual(limited.tickets);
    expect(persisted.credits).toEqual(limited.credits);
    const limitedAgain = await api(user, f.user_url, '/api/gacha/actions', 'POST', {
      action: 'redeem',
      product: 'bulk-limited',
      revision: persisted.shop.revision,
    });
    expect(limitedAgain.status()).toBe(200);
    expect(await limitedAgain.json()).toEqual({ rejection: 'daily-limit' });
    const afterRejection = await readState();
    expect(afterRejection.tickets).toEqual(limited.tickets);
    expect(afterRejection.credits).toEqual(limited.credits);
    expect(errors).toEqual([]);
  } finally {
    try {
      await setWallet('balance', original.balance);
      await setWallet('game_balance', original.game_balance);
    } finally {
      await user.close();
      await admin.close();
    }
  }
});

test('code duplicates fund a permanent SSR exchange without an open limited pool', async ({
  browser,
}) => {
  const f = fixture();
  const player = f.users.find((user) => user.level === 2)!;
  const user = await session(browser, f.user_url, player.cookie);
  const admin = await session(browser, f.admin_url, f.admin_cookie, true);
  try {
    const created = await api(admin, f.admin_url, '/admin/api/rewards/create', 'POST', {
      kind: 'code',
      title: 'Synthetic card reunion',
      enabled: true,
      conditions: { op: 'all' },
      rewards: [
        { asset: 'gacha:card:g-chatgpt', quantity: '3' },
        { asset: 'gacha:shard:SSR', quantity: '1' },
      ],
    });
    expect(created.status()).toBe(200);
    const campaign = (await created.json()) as Campaign;
    for (const alreadyClaimed of [false, true]) {
      const response = await api(user, f.user_url, '/api/rewards/redeem', 'POST', {
        code: campaign.code,
      });
      expect(response.status()).toBe(200);
      const receipt = (await response.json()) as Redemption;
      expect(receipt.already_claimed).toBe(alreadyClaimed);
      expect(receipt.results[0]).toEqual({
        asset: 'gacha:card:g-chatgpt',
        quantity: '3',
        granted: '1',
        converted: { asset: 'gacha:shard:SSR', quantity: '2' },
      });
    }
    const page = await user.newPage();
    await page.goto(f.user_url + '/activities/gacha');
    await page.getByRole('tab', { name: 'Mystery shop', exact: true }).click();
    const beforeResponse = await api(user, f.user_url, '/api/gacha/state');
    expect(beforeResponse.status()).toBe(200);
    const before = (await beforeResponse.json()) as Snapshot;
    expect(before.pools.filter((pool) => pool.kind === 'limited')).toHaveLength(0);
    expect(before.fragments.SSR).toBe(3);
    await expect(page.getByRole('heading', { name: 'Permanent SSR', exact: true })).toBeVisible();
    const offer = page.locator('.gacha-shop-card').filter({
      has: page.getByRole('button', { name: 'Origin echo · Claude', exact: true }),
    });
    await expect(offer.getByText('Cost: 3 SSR shards', { exact: true })).toBeVisible();
    const completed = page.waitForResponse(
      (response) =>
        new URL(response.url()).pathname === '/api/gacha/actions' &&
        response.request().postDataJSON()?.product === 'g-claude',
    );
    await offer.getByRole('button', { name: 'Add to collection', exact: true }).click();
    const response = await completed;
    expect(response.status()).toBe(200);
    const outcome = (await response.json()) as Outcome;
    expect(outcome.snapshot.fragments.SSR).toBe(0);
    expect(outcome.snapshot.owned['g-claude'].source).toBe('shop');
    await expect(offer.getByRole('button', { name: 'Collected', exact: true })).toBeDisabled();
    await page.reload();
    await page.getByRole('tab', { name: 'Mystery shop', exact: true }).click();
    await expect(offer.getByRole('button', { name: 'Collected', exact: true })).toBeDisabled();
    const persisted = await api(user, f.user_url, '/api/gacha/state');
    expect(persisted.status()).toBe(200);
    const state = (await persisted.json()) as Snapshot;
    expect(state.fragments.SSR).toBe(0);
    expect(state.owned['g-claude']).toEqual(outcome.snapshot.owned['g-claude']);
  } finally {
    await user.close();
    await admin.close();
  }
});
