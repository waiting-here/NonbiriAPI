import { randomBytes, randomUUID } from 'node:crypto';
import { readFileSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { expect, test, type BrowserContext } from '@playwright/test';
import type { Configuration, Outcome, Snapshot } from '../../src/user/activities/gacha/types';
import type { AppearanceState } from '../../src/user/appearance/context';
import { contrastFailures } from '../e2e/appearance-contrast';

test('Tomorrow unlocks through its limited pool, collection reward and account appearance', async ({
  browser,
}) => {
  test.setTimeout(150_000);
  const fixture = JSON.parse(readFileSync(process.env.NONBIRI_AUDIT_BROWSER_STATE!, 'utf8'));
  const player = fixture.users.find((user: { level: number }) => user.level === 1);
  const user = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  const admin = await browser.newContext();
  for (const [context, origin, cookie, cookiePath] of [
    [user, fixture.user_url, player.cookie, '/api'],
    [admin, fixture.admin_url, fixture.admin_cookie, '/admin'],
  ] as const)
    await context.addCookies([
      {
        name: cookie.Name,
        value: cookie.Value,
        domain: new URL(origin).hostname,
        path: cookiePath,
        httpOnly: true,
        sameSite: 'Lax',
      },
    ]);
  await user.addInitScript(() => {
    localStorage.setItem('nb.lang', 'en');
    localStorage.setItem('nb.theme', 'light');
    localStorage.setItem(
      'nb.gacha.preferences',
      JSON.stringify({ music: false, sound: false, reduced: true }),
    );
  });
  async function api<T>(
    context: BrowserContext,
    origin: string,
    path: string,
    data?: unknown,
  ): Promise<T> {
    const response = await context.request.fetch(origin + path, {
      method: data === undefined ? 'GET' : 'POST',
      data,
      headers: { Origin: origin, 'Idempotency-Key': randomUUID() },
    });
    expect(response.status(), path + ': ' + (await response.text())).toBe(200);
    return response.json();
  }
  const readAppearance = () => api<AppearanceState>(user, fixture.user_url, '/api/appearance');
  const act = async (data: unknown) => {
    const result = await api<Outcome>(user, fixture.user_url, '/api/gacha/actions', data);
    expect(result).toHaveProperty('snapshot');
    return result;
  };
  const name = 'Tomorrow, Still Unwritten';
  try {
    const config = await api<Configuration>(admin, fixture.admin_url, '/admin/api/gacha');
    const preset = config.presets.find((item) => item.id === 'tomorrow')!;
    expect(preset.pool.published).toBe(false);
    expect(preset.pool.starts_at).toBeNull();
    expect(preset.pool.ends_at).toBeNull();
    expect(preset.pool.featured).toEqual(['r-chatgpt']);
    expect((await readAppearance()).unlocked).not.toContain('tomorrow');
    const pool = {
      ...preset.pool,
      id: 'gp_' + randomBytes(16).toString('base64url'),
      name: 'Tomorrow encounter',
      published: true,
      starts_at: Math.floor(Date.now() / 1000) - 60,
      ends_at: Math.floor(Date.now() / 1000) + 3600,
    };
    await api(admin, fixture.admin_url, '/admin/api/gacha', {
      kind: 'pool',
      expected_revision: 0,
      pool,
    });
    const userPath = '/admin/api/users/' + player.id;
    const account = await api<{ revision: string }>(admin, fixture.admin_url, userPath);
    const budget = await admin.request.patch(fixture.admin_url + userPath, {
      headers: { Origin: fixture.admin_url, 'Idempotency-Key': randomUUID() },
      data: {
        mode: 'economy',
        expected_revision: account.revision,
        target: 'balance',
        direction: 'increase',
        amount: '2000000',
        reason: 'Synthetic SP draw budget',
      },
    });
    expect(budget.status()).toBe(200);
    let state = (await act({ action: 'enter' })).snapshot;
    expect(state.visible_themes).toContain('r');
    for (let i = 0; i < 2; i++)
      state = (
        await act({ action: 'buy', ticket: 'limited', quantity: 90, revision: state.shop.revision })
      ).snapshot;
    let draws = 0;
    while (!state.owned['r-chatgpt'] && draws < 180) {
      const result = await act({ action: 'draw', pool_id: pool.id, quantity: 10 });
      expect(result.draw?.items).toHaveLength(10);
      state = result.snapshot;
      draws += 10;
    }
    expect(state.owned['r-chatgpt'].source).toBe('draw');
    const unlocked = await readAppearance();
    expect(unlocked.unlocked).toContain('tomorrow');
    expect(unlocked.selected).toBe('default');
    expect(unlocked.moments).toEqual(
      expect.arrayContaining(['card:r-chatgpt', 'illustration:r', 'cover:r']),
    );
    const page = await user.newPage();
    const errors: string[] = [];
    page.on('pageerror', (error) => errors.push(error.message));
    await page.emulateMedia({ reducedMotion: 'reduce' });
    await page.goto(fixture.user_url + '/activities/gacha');
    await page.getByRole('tab', { name: /^Collection/ }).click();
    await page.getByRole('button', { name: name + ' · ChatGPT, Collected', exact: true }).click();
    await expect(page.locator('.gacha-spotlight__stage img')).toHaveAttribute(
      'src',
      '/assets/gacha/cards/r-chatgpt.webp',
    );
    await page.getByRole('button', { name: 'Replay first encounter', exact: true }).click();
    await expect(page.locator('.gacha-draw-prelude')).toHaveAttribute('data-card-id', 'r-chatgpt');
    await page.keyboard.press('Escape');
    await page.getByRole('tab', { name: 'Theme memories', exact: true }).click();
    const collection = page
      .locator('.gacha-theme')
      .filter({ has: page.getByRole('heading', { name, exact: true }) });
    await expect(collection.getByText('Appearance unlocked', { exact: true })).toBeVisible();
    await collection.getByRole('button', { name: 'Claim reward', exact: true }).click();
    await expect(collection.getByRole('button', { name: 'Claimed', exact: true })).toBeDisabled();
    const claimed = await api<Snapshot>(user, fixture.user_url, '/api/gacha/state');
    expect(claimed.claimed).toContain('r');
    expect(claimed.tickets.limited).toBe(state.tickets.limited + 1);
    expect(Number(claimed.credits.general) - Number(state.credits.general)).toBe(10000);
    expect(Number(claimed.donation) - Number(state.donation)).toBe(10);
    await collection.getByRole('button', { name: 'View ' + name, exact: true }).click();
    await expect(
      page.getByRole('dialog').locator('img[src="/assets/gacha/groups/r.webp"]'),
    ).toBeVisible();
    await page.keyboard.press('Escape');
    await page.getByRole('button', { name: 'Appearance', exact: true }).click();
    await page.getByRole('radio', { name: new RegExp(name) }).check();
    await page.getByRole('button', { name: 'Apply appearance', exact: true }).click();
    await expect(page.locator('html')).toHaveAttribute('data-appearance', 'tomorrow');
    expect((await readAppearance()).selected).toBe('tomorrow');
    const contrast: unknown[] = [];
    for (const width of [1440, 390]) {
      await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
      for (const theme of ['light', 'dark']) {
        await collection.scrollIntoViewIfNeeded();
        await page.evaluate((theme) => {
          document.documentElement.dataset.theme = theme;
        }, theme);
        const source = page.locator('.nb-tomorrow-still .tomorrow-art__still img').first();
        await expect(source).toHaveAttribute(
          'src',
          '/assets/gacha/motion/r-chatgpt/background-' + theme + '.webp',
        );
        await expect
          .poll(() =>
            source.evaluate((image: HTMLImageElement) => image.complete && image.naturalWidth > 0),
          )
          .toBe(true);
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
          true,
        );
        contrast.push(
          ...(await contrastFailures(page)).map((failure) => ({ width, theme, ...failure })),
        );
        if (process.env.NONBIRI_VISUAL_DIR)
          await page.screenshot({
            path: resolve(process.env.NONBIRI_VISUAL_DIR, `tomorrow-${theme}-${width}.png`),
          });
        await page.emulateMedia({ reducedMotion: 'no-preference' });
        await expect(page.locator('.nb-appearance-backdrop')).toHaveAttribute(
          'data-ready',
          'true',
          { timeout: 15000 },
        );
        await page.emulateMedia({ reducedMotion: 'reduce' });
        await expect(page.locator('.nb-appearance-backdrop')).toHaveAttribute(
          'data-ready',
          'false',
        );
      }
    }
    await page.getByRole('button', { name: 'Open navigation', exact: true }).click();
    await page.getByRole('button', { name: 'Appearance', exact: true }).click();
    await page.getByRole('radio', { name: /Nonbiri/ }).check();
    await page.getByRole('button', { name: 'Apply appearance', exact: true }).click();
    await expect(page.locator('html')).toHaveAttribute('data-appearance', 'default');
    expect((await readAppearance()).unlocked).toContain('tomorrow');
    if (process.env.NONBIRI_VISUAL_DIR)
      writeFileSync(
        resolve(process.env.NONBIRI_VISUAL_DIR, 'real-flow.json'),
        JSON.stringify(
          { draws, claim: true, appearance: 'tomorrow', restored: 'default', contrast, errors },
          null,
          2,
        ) + '\n',
      );
    expect(contrast).toEqual([]);
    expect(errors).toEqual([]);
  } finally {
    await user.close();
    await admin.close();
  }
});
