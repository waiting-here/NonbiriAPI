import { resolve } from 'node:path';
import { expect, test } from './test';
import { collectConsoleViolations, mockPublicConfig, mockRoleSession } from './support';
import { USER_ORIGIN } from './ports';
import { loanActivity } from '../fixtures/loans';
import { appearanceFixtures } from './appearance-fixtures';

for (const design of ['celestial', 'wandering', 'amber'])
  for (const mode of ['light', 'dark'])
    for (const width of [1440, 390])
      test(`background appreciation preserves page state ${design} ${mode} ${width}`, async ({
        page,
      }) => {
        const guard = collectConsoleViolations(page);
        await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
        await page.emulateMedia({ reducedMotion: 'reduce' });
        await page.addInitScript((value) => {
          localStorage.setItem('nb.lang', 'zh');
          localStorage.setItem('nb.theme', value);
        }, mode);
        await mockRoleSession(page, 'user', 'user');
        await mockPublicConfig(page, 'user');
        await page.route('**/api/events', (route) =>
          route.fulfill({ contentType: 'text/event-stream', body: ': connected\n\n' }),
        );
        await page.route('**/api/activities', (route) => route.fulfill({ json: loanActivity() }));
        await page.route('**/api/limited-activities', (route) => route.fulfill({ json: [] }));
        await page.route('**/api/gacha/entry', (route) =>
          route.fulfill({ json: { enabled: false, entered: false, pools: [] } }),
        );
        let selected = 'default';
        let unlocked = ['default'];
        let visible = ['default'];
        await page.route('**/api/appearance', (route) => {
          expect(route.request().method()).toBe('GET');
          return route.fulfill({
            json: { selected, unlocked, visible, updated_at: null },
          });
        });
        await page.goto(USER_ORIGIN + '/activities');
        const trigger = page.getByRole('button', { name: '欣赏背景', exact: true });
        await expect(page.locator('.loan-card')).toBeVisible();
        await expect(trigger).toHaveCount(0);
        const appearance = page.getByRole('button', { name: '装扮', exact: true });

        await appearance.click();
        await expect(page.locator('.nb-appearance-option')).toHaveCount(1);
        await expect(page.getByText('获得对应 SP 卡牌后解锁', { exact: true })).toHaveCount(0);
        await page.getByRole('button', { name: '关闭', exact: true }).click();
        visible = ['default', design];
        await page.reload();

        await appearance.click();
        await expect(page.locator('.nb-appearance-option')).toHaveCount(2);
        await expect(page.getByText('获得对应 SP 卡牌后解锁', { exact: true })).toBeVisible();
        await expect(page.locator('.nb-appearance-option input:disabled')).toHaveCount(1);
        await page.getByRole('button', { name: '关闭', exact: true }).click();
        selected = design;
        unlocked = ['default', design];
        await page.reload();
        const amount = page.locator('.loan-card select');
        await amount.selectOption('2');
        if (width < 1024) await appearance.click();
        await trigger.click();
        const view = page.getByRole('dialog', { name: '欣赏背景', exact: true });
        await expect(view).toBeVisible();
        await expect(page.locator('.nb-user-shell')).toBeHidden();
        const back = view.getByRole('button', { name: '返回页面', exact: true });
        await expect(back).toBeFocused();
        await page.keyboard.press('Tab');
        await expect(page.locator('.nb-user-shell :focus')).toHaveCount(0);
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
          true,
        );
        if (process.env.NONBIRI_VISUAL_DIR)
          await page.screenshot({
            path: resolve(
              process.env.NONBIRI_VISUAL_DIR,
              `background-${design}-${mode}-${width}.png`,
            ),
          });
        await back.click();
        await expect(view).toHaveCount(0);
        await expect(page.locator('.nb-user-shell')).toBeVisible();
        await expect(amount).toHaveValue('2');
        await expect(trigger).toBeFocused();
        await trigger.click();
        await page.keyboard.press('Escape');
        await expect(view).toHaveCount(0);
        await expect(trigger).toBeFocused();
        if (process.env.NONBIRI_VISUAL_DIR)
          await page.screenshot({
            path: resolve(
              process.env.NONBIRI_VISUAL_DIR,
              `background-restored-${design}-${mode}-${width}.png`,
            ),
          });
        guard.assertNone();
      });

for (const moment of ['cover:permanent', 'illustration:a']) {
  test('keeps a single fixed workspace painting for ' + moment, async ({ page }) => {
    await appearanceFixtures(page, 'heart-moment', moment);
    await page.addInitScript(() => localStorage.setItem('nb.lang', 'en'));
    await page.goto(USER_ORIGIN + '/');
    const painting = page.locator('.nb-moment-backdrop__scene');
    await expect(page.locator('.nb-moment-backdrop')).toHaveAttribute('data-moment', moment);
    await expect(page.locator('.nb-art-stage img, .nb-art-stage canvas')).toHaveCount(1);
    await painting.evaluate((image: HTMLImageElement) => image.decode());

    for (const width of [1600, 1180, 390]) {
      await page.setViewportSize({ width, height: 800 });
      await page.evaluate(() => window.scrollTo(0, 0));
      await expect(painting).toHaveCSS('object-fit', 'cover');
      await expect(page.locator('.nb-art-stage')).toHaveCSS('position', 'fixed');
      const box = (await painting.boundingBox())!;
      const header = (await page.locator('.nb-user-header').boundingBox())!;
      expect(box.y).toBeCloseTo(header.y + header.height, 0);
      expect(box.width).toBe(width);
      expect(box.y + box.height).toBe(800);
      await page.evaluate(() => window.scrollTo(0, document.body.scrollHeight));
      await expect.poll(() => page.evaluate(() => scrollY)).toBeGreaterThan(0);
      expect(await painting.boundingBox()).toEqual(box);
      if (process.env.NONBIRI_VISUAL_DIR)
        await page.screenshot({
          path: resolve(
            process.env.NONBIRI_VISUAL_DIR,
            moment.split(':')[0] + '-' + width + '.png',
          ),
        });
      if (width < 1120) await page.getByRole('button', { name: 'Appearance', exact: true }).click();
      await page.getByRole('button', { name: 'View background', exact: true }).click();
      await expect(painting).toHaveCSS('object-fit', 'contain');
      expect(await painting.boundingBox()).toEqual({ x: 0, y: 0, width, height: 800 });
      await page.getByRole('button', { name: 'Return to page', exact: true }).click();
      if (width < 1120) await page.keyboard.press('Escape');
    }
  });
}

for (const mode of ['light', 'dark']) {
  test('keeps the Hello Tomorrow crown below the actual header in ' + mode, async ({ page }) => {
    await appearanceFixtures(page, 'tomorrow');
    await page.emulateMedia({ reducedMotion: 'reduce' });
    await page.addInitScript((mode) => {
      localStorage.setItem('nb.lang', 'en');
      localStorage.setItem('nb.theme', mode);
    }, mode);
    await page.goto(USER_ORIGIN + '/activities');
    const hero = page.locator('.nb-tomorrow-still img').last();
    await hero.evaluate((img: HTMLImageElement) => img.decode());
    // The first opaque crown pixels are at row 8 in both 1024 × 1536 source plates.
    const header = page.locator('.nb-user-header');
    for (const [width, height] of [
      [1600, 900],
      [2560, 600],
      [1440, 1400],
      [390, 844],
    ]) {
      await page.setViewportSize({ width, height });
      await expect
        .poll(async () => {
          const rect = (await hero.boundingBox())!;
          const bar = (await header.boundingBox())!;
          return rect.y + (rect.height * 8) / 1536 - (bar.y + bar.height);
        })
        .toBeGreaterThan(0);
      if (process.env.NONBIRI_VISUAL_DIR)
        await page.screenshot({
          path: resolve(process.env.NONBIRI_VISUAL_DIR, 'tomorrow-' + mode + '-' + width + '.png'),
        });
    }
    await page.setViewportSize({ width: 1440, height: 800 });
    // Text scaling or wrapped controls can make the bar taller than the minimum 4.25rem.
    await header.evaluate((el) => {
      el.style.minHeight = '104px';
    });
    const stage = page.locator('.nb-art-stage');
    await expect.poll(async () => (await stage.boundingBox())?.y).toBe(104);
    let fail = true;
    await page.route('**/motion/r-chatgpt/body.webp', (route) =>
      fail ? route.abort() : route.continue(),
    );
    await page.getByRole('button', { name: 'Appearance', exact: true }).click();
    const sheet = page.getByRole('dialog', { name: 'Appearance', exact: true });
    await sheet.getByText('Full motion', { exact: true }).click();
    await expect(sheet.getByRole('status')).toContainText('Showing the still artwork');
    await expect(hero).toBeVisible();
    const still = (await hero.boundingBox())!;
    expect(still.y + (still.height * 8) / 1536).toBeGreaterThan(104);
    fail = false;
    await sheet.getByRole('button', { name: 'Retry animation' }).click();
    const canvas = stage.locator('canvas');
    await expect(canvas).toHaveCount(1);
    await expect(canvas).toHaveAttribute('data-ready', 'true', { timeout: 25_000 });
    await expect(hero).toBeHidden();
    expect(await canvas.boundingBox()).toEqual(await stage.boundingBox());
    await sheet.getByRole('button', { name: 'Close', exact: true }).click();
    await expect
      .poll(async () => Number(await canvas.getAttribute('data-time')))
      .toBeGreaterThan(0);
    if (process.env.NONBIRI_VISUAL_DIR)
      await page.screenshot({
        path: resolve(process.env.NONBIRI_VISUAL_DIR, 'tomorrow-' + mode + '-motion.png'),
      });
  });
}
