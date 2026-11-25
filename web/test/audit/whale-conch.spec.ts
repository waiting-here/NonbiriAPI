import { randomUUID } from 'node:crypto';
import { mkdirSync, readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { expect, test } from '@playwright/test';
import type { Profile } from '../../src/user/games/whale-haven/api';

type Cookie = { Name: string; Value: string };
type Fixture = {
  admin_url: string;
  user_url: string;
  admin_cookie: Cookie;
  users: { level: number; cookie: Cookie }[];
};

test('conch draws explain eaten candy and stored items after reloading the haven', async ({
  browser,
  request,
}) => {
  const fixture: Fixture = JSON.parse(
    readFileSync(process.env.NONBIRI_AUDIT_BROWSER_STATE!, 'utf8'),
  );
  const owner = fixture.users.find((user) => user.level === 1)!.cookie;
  async function api(path: string, method = 'GET', data?: unknown) {
    const admin = path.startsWith('/admin/');
    const origin = admin ? fixture.admin_url : fixture.user_url;
    const cookie = admin ? fixture.admin_cookie : owner;
    const result = await request.fetch(origin + path, {
      method,
      data,
      headers: {
        Origin: origin,
        Cookie: `${cookie.Name}=${cookie.Value}`,
        'Idempotency-Key': randomUUID(),
      },
    });
    expect(result.status(), path).toBe(200);
    return result.json();
  }
  const games = '/admin/api/games/config';
  const haven = '/admin/api/games/whale-haven/config';
  const apiBase = '/api/games/whale-haven';
  const originalGames = await api(games);
  const original = await api(haven);
  let revision: string = original.revision;
  const code = 'CONCH-' + randomUUID().slice(0, 8);
  try {
    if (!originalGames.master_enabled)
      await api(games, 'PATCH', {
        expected_revision: originalGames.revision,
        master_enabled: true,
      });
    await api('/admin/api/rewards/create', 'POST', {
      kind: 'code',
      title: 'Conch draw fixture',
      code,
      enabled: true,
      quota: 1,
      rewards: [{ asset: 'whale:conch_shards', quantity: '10' }],
      conditions: { op: 'all' },
    });
    await api('/api/rewards/redeem', 'POST', { code });
    for (const language of ['zh', 'en'] as const) {
      const candy = language === 'zh';
      const settings = await api(haven, 'PATCH', {
        expected_revision: revision,
        enabled: true,
        values: {
          conch_cost_shards: 5,
          candy_mood: 92,
          candy_affection: 7,
          conch_auto_refill_weight: 0,
          conch_aroma_boost_weight: 0,
          conch_wardrobe_coupon_weight: candy ? 0 : 1,
          conch_affection_candy_weight: candy ? 1 : 0,
        },
      });
      revision = settings.revision;
      const before: Profile = await api(apiBase + '/profile');
      if (!before.snapshot.whale.hasCompletedNaming)
        await api(apiBase + '/actions', 'POST', {
          type: 'adopt',
          name: 'Fin',
          open: false,
          expected_profile_revision: String(before.snapshot.revision),
          expected_settings_revision: revision,
        });
      const width = candy ? 390 : 1440;
      const context = await browser.newContext({
        viewport: { width, height: 900 },
        reducedMotion: 'reduce',
      });
      try {
        await context.addCookies([
          {
            name: owner.Name,
            value: owner.Value,
            domain: new URL(fixture.user_url).hostname,
            path: '/api',
          },
        ]);
        await context.addInitScript((lang) => localStorage.setItem('nb.lang', lang), language);
        const page = await context.newPage();
        await page.goto(fixture.user_url + '/games/whale-haven');
        await page.getByRole('button', { name: candy ? '背包' : 'Bag', exact: true }).click();
        const machine = page.locator('.conch-gacha-card');
        await expect(machine).toContainText(
          candy ? '心情变为 92，羁绊 +7' : 'Mood becomes 92 and Bond XP increases by 7',
        );
        const draw = machine.getByRole('button', { name: candy ? '转动扭蛋' : 'Draw an item' });
        await draw.click();
        const response = page.waitForResponse(
          (r) =>
            r.request().method() === 'POST' && new URL(r.url()).pathname === apiBase + '/actions',
        );
        await page
          .getByRole('alertdialog')
          .getByRole('button', { name: candy ? '确认' : 'Confirm', exact: true })
          .click();
        const result: Profile = await (await response).json();
        expect(result.conch_reward).toBe(candy ? 'affection_candy' : 'wardrobe_coupon');
        expect(result.snapshot.whale.tempEmotion?.line).toContain(
          candy ? '海盐牛奶糖' : '深海裁缝海螺券',
        );
        const disposition = candy
          ? '小鲸已当场吃掉。'
          : 'You can find it under Conch draws and boosts in your bag.';
        const name = candy ? '海盐牛奶糖' : 'Tailor coupon';
        await expect(page.getByRole('status').filter({ hasText: name })).toContainText(disposition);
        await expect(machine).toContainText(
          candy ? '最近抽到海盐牛奶糖' : 'Last draw · Tailor coupon',
        );
        await expect(machine).toContainText(disposition);
        if (!candy) await expect(machine).toContainText('Tailor coupon ×1');
        const evidence = process.env.NONBIRI_VISUAL_DIR;
        if (evidence) {
          mkdirSync(evidence, { recursive: true });
          await page.screenshot({ path: resolve(evidence, `conch-receipt-${width}.png`) });
          for (const theme of ['light', 'dark']) {
            await page.evaluate((v) => {
              localStorage.setItem('nb.theme', v);
            }, theme);
            await page.reload();
            await expect(page.locator('.whale-haven')).toHaveAttribute('data-whale-theme', theme);
            await page.getByRole('button', { name: candy ? '背包' : 'Bag', exact: true }).click();
            await machine.getByRole('heading').scrollIntoViewIfNeeded();
            await expect(machine).toContainText(disposition);
            await page.screenshot({ path: resolve(evidence, `conch-bag-${width}-${theme}.png`) });
          }
        }
        await page.reload();
        await page.getByRole('button', { name: candy ? '动态' : 'Activity', exact: true }).click();
        const timeline = page.locator('.deck-timeline--activity');
        const entry = timeline.getByRole('listitem').filter({
          has: page.getByRole('heading', {
            name: candy ? '海螺扭蛋 · 海盐牛奶糖' : 'Conch draw · Tailor coupon',
          }),
        });
        await expect(entry).toContainText(disposition);
        if (!candy) await expect(timeline).toContainText('Your whale ate it right away.');
        await expect(timeline).not.toContainText(/affection_candy|wardrobe_coupon/);
        expect(
          await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1),
        ).toBe(true);
        if (evidence)
          await page.screenshot({ path: resolve(evidence, `conch-activity-${width}.png`) });
      } finally {
        await context.close();
      }
    }
  } finally {
    if (revision !== original.revision)
      await api(haven, 'PATCH', {
        expected_revision: revision,
        enabled: original.enabled,
        values: original.values,
      });
    if (!originalGames.master_enabled)
      await api(games, 'PATCH', {
        expected_revision: (await api(games)).revision,
        master_enabled: false,
      });
  }
});
