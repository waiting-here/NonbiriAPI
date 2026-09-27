import { mkdir } from 'node:fs/promises';
import { resolve } from 'node:path';
import { USER_ORIGIN } from './ports';
import { mockJson, mockPublicConfig, mockRoleSession } from './support';
import { expect, test } from './test';

const viewports = [
  { width: 320, locale: 'en' },
  { width: 390, locale: 'en' },
  { width: 480, locale: 'en' },
  { width: 768, locale: 'en' },
  { width: 1440, locale: 'en' },
  { width: 390, locale: 'zh' },
] as const;

for (const { width, locale } of viewports) {
  test(`daily check-in choice remains readable at ${width}px in ${locale}`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 });
    await page.emulateMedia({ reducedMotion: 'reduce' });
    await page.addInitScript((language) => localStorage.setItem('nb.lang', language), locale);
    await mockPublicConfig(page, 'user');
    await mockRoleSession(page, 'user', 'user');
    for (const asset of ['general', 'game'] as const) {
      await mockJson(page, {
        origin: USER_ORIGIN,
        method: 'GET',
        path: asset === 'game' ? '/api/checkin/game' : '/api/checkin',
        body: {
          enabled: true,
          mutually_exclusive: true,
          blocked_by_other_checkin: asset === 'game',
          asset_type: asset,
          checked_in_today: asset === 'general',
          balance: '9000000000000',
          award_min: '0.001',
          award_max: '123456789.123',
          balance_cap: '0',
        },
      });
    }
    await mockJson(page, {
      origin: USER_ORIGIN,
      method: 'GET',
      path: '/api/home/game-summary',
      body: { continue: [], pending_results: [] },
    });
    await mockJson(page, {
      origin: USER_ORIGIN,
      method: 'GET',
      path: '/api/announcements?limit=100',
      body: { data: [], next_cursor: null },
    });

    await page.goto(USER_ORIGIN);
    const general = page.locator('.core-checkin-card').filter({
      hasText: locale === 'zh' ? '通用积分签到' : 'General-credit check-in',
    });
    const game = page.locator('.core-checkin-card').filter({
      hasText: locale === 'zh' ? '游戏积分签到' : 'Game-credit check-in',
    });
    await expect(page.getByRole('note')).toContainText(
      locale === 'zh' ? '每天只能选一种签到' : 'Choose one check-in each site day',
    );
    await expect(general.getByText(locale === 'zh' ? '已签到' : 'Checked in', { exact: true }))
      .toBeVisible();
    await expect(game.getByText(locale === 'zh' ? '已领取另一种签到' : 'Other check-in claimed'))
      .toBeVisible();
    await expect(general.getByRole('button', { name: locale === 'zh' ? '立即签到' : 'Check in' }))
      .toBeDisabled();
    await expect(game.getByRole('button', { name: locale === 'zh' ? '立即签到' : 'Check in' }))
      .toBeDisabled();
    await expect(page.locator('.core-checkin-grid > .core-card')).toHaveCount(2);

    const layout = await page.evaluate(() => ({
      viewport: window.innerWidth,
      scroll: document.documentElement.scrollWidth,
    }));
    expect(layout.scroll).toBeLessThanOrEqual(layout.viewport + 1);
    const generalBox = await general.boundingBox();
    const gameBox = await game.boundingBox();
    expect(generalBox).not.toBeNull();
    expect(gameBox).not.toBeNull();
    if (width === 320) {
      expect(gameBox!.y).toBeGreaterThan(generalBox!.y);
    } else {
      expect(Math.abs(gameBox!.y - generalBox!.y)).toBeLessThanOrEqual(1);
    }

    const screenshotDirectory = process.env.NONBIRI_CHECKIN_SCREENSHOT_DIR;
    if (screenshotDirectory) {
      await mkdir(screenshotDirectory, { recursive: true });
      await page.screenshot({
        path: resolve(screenshotDirectory, `checkin-${width}${locale === 'zh' ? '-zh' : ''}.png`),
        fullPage: true,
      });
    }
  });
}
