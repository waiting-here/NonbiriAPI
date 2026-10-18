import { mkdirSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { expect, test } from './test';
import { mockPublicConfig, mockRoleSession } from './support';
import { USER_ORIGIN } from './ports';
import { loanActivity } from '../fixtures/loans';

for (const width of [1440, 390]) {
  test(`amber appearance layers and quality transitions ${width}`, async ({ page }) => {
    test.setTimeout(120_000);
    const errors: string[] = [];
    page.on('pageerror', (error) => errors.push(error.message));
    page.on('console', (message) => {
      if (
        message.type() === 'warning' &&
        /^\[\.WebGL-0x[0-9a-f]+\]GL Driver Message \(OpenGL, Performance, GL_CLOSE_PATH_NV, High\): GPU stall due to ReadPixels(?: \(this message will no longer repeat\))?$/.test(
          message.text(),
        )
      )
        return;
      if (message.type() === 'error' || message.type() === 'warning') errors.push(message.type());
    });
    const sizes = new Map<string, number>();
    const transfers: Promise<void>[] = [];
    page.on('response', (response) => {
      const path = new URL(response.url()).pathname;
      if (!path.startsWith('/assets/gacha/')) return;
      transfers.push(
        response.body().then((body) => {
          expect(response.ok(), path).toBe(true);
          sizes.set(path, body.length);
        }),
      );
    });
    const samples: Record<string, unknown> = {};
    const record = async (stage: string) => {
      await Promise.all(transfers);
      samples[stage] = {
        uniqueResponseBodyBytes: [...sizes.values()].reduce((sum, value) => sum + value, 0),
        resources: Object.fromEntries(sizes),
      };
    };
    await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
    await page.emulateMedia({ reducedMotion: 'reduce', colorScheme: 'light' });
    await page.addInitScript(() => {
      localStorage.setItem('nb.lang', 'zh');
      localStorage.setItem('nb.theme', 'system');
    });
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
    await page.route('**/api/appearance', (route) =>
      route.fulfill({
        json: {
          selected: 'amber',
          visible: ['default', 'amber'],
          unlocked: ['default', 'amber'],
          updated_at: null,
        },
      }),
    );
    await page.goto(USER_ORIGIN + '/activities');
    const still = page.locator('.nb-amber-still-character');
    await expect
      .poll(() => still.evaluate((image: HTMLImageElement) => image.complete && image.naturalWidth))
      .toBe(1037);
    const before = await still.boundingBox();
    await record('staticLight');
    const canvas = page.locator('.nb-appearance-backdrop');
    await page.emulateMedia({ reducedMotion: 'no-preference' });
    await expect(canvas).toHaveAttribute('data-ready', 'true', { timeout: 15_000 });
    await record('animatedLight');
    // Lifecycle regression is independent of the test runner's rendering speed.
    const lightTime = Number(await canvas.getAttribute('data-time'));
    await expect
      .poll(async () => Number(await canvas.getAttribute('data-time')))
      .toBeGreaterThan(lightTime + 0.2);
    // Exercise the visibility lifecycle without relying on headless tab activation.
    const hiddenTime = await page.evaluate(() => {
      Object.defineProperty(document, 'hidden', { configurable: true, value: true });
      document.dispatchEvent(new Event('visibilitychange'));
      return document.querySelector('.nb-appearance-backdrop')?.getAttribute('data-time');
    });
    expect(hiddenTime).toBeTruthy();
    // A bounded observation interval proves that no background frames keep advancing.
    await page.waitForTimeout(250);
    await expect(canvas).toHaveAttribute('data-time', hiddenTime!);
    await page.evaluate(() => {
      Reflect.deleteProperty(document, 'hidden');
      document.dispatchEvent(new Event('visibilitychange'));
    });
    await expect
      .poll(async () => Number(await canvas.getAttribute('data-time')))
      .toBeGreaterThan(Number(hiddenTime) + 0.2);
    await page.emulateMedia({ reducedMotion: 'reduce' });
    await expect(canvas).toHaveAttribute('data-ready', 'false');
    expect(await still.boundingBox()).toEqual(before);
    if (process.env.NONBIRI_VISUAL_DIR)
      await page.screenshot({
        path: resolve(process.env.NONBIRI_VISUAL_DIR, `amber-static-light-${width}.png`),
      });
    await page.emulateMedia({ colorScheme: 'dark', reducedMotion: 'no-preference' });
    await expect(still).toHaveAttribute('src', /appearance-hero-dark\.webp$/);
    await expect(canvas).toHaveAttribute('data-ready', 'true', { timeout: 15_000 });
    await record('animatedDark');
    const darkTime = Number(await canvas.getAttribute('data-time'));
    await expect
      .poll(async () => Number(await canvas.getAttribute('data-time')))
      .toBeGreaterThan(darkTime + 0.2);
    if (process.env.NONBIRI_VISUAL_DIR)
      await page.screenshot({
        path: resolve(process.env.NONBIRI_VISUAL_DIR, `amber-animated-dark-${width}.png`),
      });
    await page.setViewportSize({ width: 3840, height: 2160 });
    await expect
      .poll(() => sizes.has('/assets/gacha/hires/motion/o-claude/background-dark.webp'))
      .toBe(true);
    await expect(canvas).toHaveAttribute('data-ready', 'true', { timeout: 15_000 });
    await record('highDark');
    if (process.env.NONBIRI_VISUAL_DIR) {
      mkdirSync(process.env.NONBIRI_VISUAL_DIR, { recursive: true });
      writeFileSync(
        resolve(process.env.NONBIRI_VISUAL_DIR, `amber-transfer-${width}.json`),
        JSON.stringify(samples, null, 2) + '\n',
      );
    }
    expect(errors).toEqual([]);
  });
}
