import { mkdirSync } from 'node:fs';
import { resolve } from 'node:path';
import { expect, test } from './test';
import { USER_ORIGIN } from './ports';
import { appearanceFixtures } from './appearance-fixtures';

test.use({ deviceScaleFactor: 2, viewport: { width: 800, height: 480 } });

test('retries the current scene at highest quality, pauses in place and restores the system preference', async ({
  page,
}) => {
  test.setTimeout(90_000);
  await appearanceFixtures(page, 'celestial');
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.addInitScript(() => {
    localStorage.setItem('nb.lang', 'en');
    localStorage.setItem('nb.appearance-quality', 'highest');
  });
  let reject = true;
  await page.route('**/assets/gacha/appearances/layers/hires/**', (route) =>
    reject ? route.abort() : route.continue(),
  );
  await page.goto(USER_ORIGIN + '/activities');
  const canvas = page.locator('.nb-art-stage canvas');
  const still = page.locator('.nb-art-stage .nb-appearance-still');
  await expect(canvas).toHaveAttribute('data-ready', 'false');
  expect(await still.evaluate((image) => (image as HTMLImageElement).currentSrc)).toContain(
    '/hires/',
  );
  await page.getByRole('button', { name: 'Appearance', exact: true }).click();
  const sheet = page.getByRole('dialog', { name: 'Appearance', exact: true });
  await sheet.getByText('Full motion', { exact: true }).click();
  await expect(sheet.getByRole('status')).toContainText('Showing the still artwork');
  reject = false;
  await sheet.getByRole('button', { name: 'Retry animation' }).click();
  await expect(canvas).toHaveAttribute('data-ready', 'true', { timeout: 25_000 });
  await sheet.getByRole('button', { name: 'View background' }).click();
  const view = page.getByRole('dialog', { name: 'View background' });
  const phase = async () => Number(await canvas.getAttribute('data-motion-time'));
  await expect.poll(phase, { timeout: 40_000 }).toBeGreaterThan(8);
  await view.getByRole('button', { name: 'Pause', exact: true }).click();
  const paused = await phase();
  const box = await canvas.boundingBox();
  expect(
    await canvas.evaluate((node) => [
      (node as HTMLCanvasElement).width,
      (node as HTMLCanvasElement).height,
    ]),
  ).toEqual([Math.round(box!.width * 2), Math.round(box!.height * 2)]);
  // Several real refreshes elapse while the canvas retains its exact painted phase.
  await page.evaluate(
    () =>
      new Promise<void>((done) =>
        requestAnimationFrame(() =>
          requestAnimationFrame(() => requestAnimationFrame(() => done())),
        ),
      ),
  );
  expect(await phase()).toBe(paused);
  if (process.env.NONBIRI_VISUAL_DIR) {
    mkdirSync(process.env.NONBIRI_VISUAL_DIR, { recursive: true });
    await page.screenshot({ path: resolve(process.env.NONBIRI_VISUAL_DIR, 'highest-paused.png') });
  }
  await view.getByRole('button', { name: 'Resume', exact: true }).click();
  await expect.poll(phase).toBeGreaterThan(paused);
  await page.keyboard.press('Escape');
  await expect(sheet.getByRole('button', { name: 'View background' })).toBeFocused();
  await sheet.getByText('Follow system', { exact: true }).click();
  await expect(canvas).toHaveAttribute('data-ready', 'false');
  await expect(still).toBeVisible();
  await sheet.getByRole('button', { name: 'Close', exact: true }).click();
  await page.setViewportSize({ width: 1440, height: 900 });
  if (process.env.NONBIRI_VISUAL_DIR)
    await page.screenshot({ path: resolve(process.env.NONBIRI_VISUAL_DIR, 'desktop-static.png') });
});
