import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { expect, test, type Page } from './test';
import { mockPublicConfig, mockRoleSession } from './support';
import { USER_ORIGIN } from './ports';
import { loanActivity } from '../fixtures/loans';
import type { Card } from '../../src/user/activities/gacha/types';
import type { MomentRegistration } from '../../src/user/activities/gacha/moment-frame';

const { cards } = JSON.parse(readFileSync(resolve('../internal/gacha/catalog.json'), 'utf8')) as {
  cards: Card[];
};
const { cards: compositions } = JSON.parse(
  readFileSync(resolve('public/assets/gacha/composition.json'), 'utf8'),
) as { cards: Record<string, { moment_layers?: MomentRegistration }> };
const examples = new Set([
  'a-gemini',
  'c-chatgpt',
  'c-glm',
  'e-glm',
  'f-chatgpt',
  'g-gemini',
  'h-chatgpt',
  'r-chatgpt',
  's-chatgpt',
  's-claude',
]);
const viewports = [
  { width: 1920, height: 1080 },
  { width: 1920, height: 600 },
  // A 1920x600 desktop at 125% zoom has this CSS viewport.
  { width: 1536, height: 480 },
  { width: 1440, height: 900 },
  { width: 1280, height: 600 },
  { width: 1024, height: 600 },
  { width: 390, height: 844 },
  { width: 390, height: 600 },
];

async function fixtures(page: Page, id: string, theme: string) {
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.addInitScript((theme) => {
    localStorage.setItem('nb.lang', 'zh');
    localStorage.setItem('nb.theme', theme);
  }, theme);
  await mockRoleSession(page, 'user', 'user');
  await mockPublicConfig(page, 'user', { artwork_r18_disabled: false });
  await page.route('**/api/events', (route) =>
    route.fulfill({ contentType: 'text/event-stream', body: ': connected\n\n' }),
  );
  await page.route('**/api/activities', (route) => route.fulfill({ json: loanActivity() }));
  await page.route('**/api/limited-activities', (route) => route.fulfill({ json: [] }));
  await page.route('**/api/appearance', (route) =>
    route.fulfill({
      json: {
        selected: 'heart-moment',
        moment: 'card:' + id,
        moments: ['cover:permanent', 'card:' + id],
        visible: ['default', 'heart-moment'],
        unlocked: ['default', 'heart-moment'],
        updated_at: null,
      },
    }),
  );
}

async function visibleTop(page: Page) {
  return page
    .locator(
      '.nb-moment-character > img:not(.nb-moment-character__plate, .nb-moment-character__scene)',
    )
    .evaluate((image: HTMLImageElement) => {
      const canvas = document.createElement('canvas');
      canvas.width = image.naturalWidth;
      canvas.height = image.naturalHeight;
      const context = canvas.getContext('2d')!;
      context.drawImage(image, 0, 0);
      const pixels = context.getImageData(0, 0, canvas.width, canvas.height).data;
      let first = 0;
      while (first < canvas.width * canvas.height && pixels[first * 4 + 3] <= 8) first++;
      const top = Math.floor(first / canvas.width);
      return { top: top / canvas.height, sourceTop: top, sourceHeight: canvas.height };
    });
}

for (const theme of ['light', 'dark'])
  for (const card of cards.filter((card) => theme === 'light' || examples.has(card.id)))
    test(`moment visible head ${card.id} ${theme}`, async ({ page }) => {
      const errors: string[] = [];
      page.on('pageerror', (error) => errors.push(error.message));
      await page.setViewportSize(viewports[0]);
      await fixtures(page, card.id, theme);
      await page.goto(USER_ORIGIN + '/activities');
      const hero = page.locator(
        '.nb-moment-character > img:not(.nb-moment-character__plate, .nb-moment-character__scene)',
      );
      await expect(page.locator('.nb-moment-backdrop')).toHaveAttribute(
        'data-moment',
        'card:' + card.id,
      );
      const expansion = compositions[card.id]?.moment_layers?.expansion;
      const source =
        expansion?.hero ??
        (card.rarity === 'SSR' || card.rarity === 'SP'
          ? 'motion/' + card.id + '/hero.webp'
          : 'layers/' + card.id + '.webp');
      await expect(hero).toHaveAttribute('src', '/assets/gacha/' + source);
      await hero.evaluate((image: HTMLImageElement) => image.decode());
      const alpha = await visibleTop(page);
      if (expansion?.contentTop !== undefined) {
        expect(Number.isInteger(expansion.contentTop)).toBe(true);
        expect(expansion.contentTop).toBeGreaterThanOrEqual(0);
        expect(expansion.contentTop).toBeLessThan(expansion.originalRegion.y);
        expect((expansion.contentTop * alpha.sourceHeight) / expansion.canvas[1]).toBeLessThan(
          alpha.sourceTop,
        );
      }
      for (const viewport of viewports) {
        await page.setViewportSize(viewport);
        const bounds = (await hero.boundingBox())!;
        const top = bounds.y + bounds.height * alpha.top;
        expect(top, `${card.id} at ${viewport.width}x${viewport.height}`).toBeGreaterThanOrEqual(
          67,
        );
        expect(top).toBeLessThan(viewport.height * 0.7);
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
          true,
        );
        if (
          process.env.NONBIRI_VISUAL_DIR &&
          examples.has(card.id) &&
          ((viewport.width === 1440 && viewport.height === 900) ||
            (viewport.width === 1920 && viewport.height === 600) ||
            (viewport.width === 390 && viewport.height === 844))
        )
          await page.screenshot({
            path: resolve(
              process.env.NONBIRI_VISUAL_DIR,
              `fit-${card.id}-${theme}-${viewport.width}x${viewport.height}.png`,
            ),
          });
      }
      if (card.id === 's-claude' || card.id === 'c-glm') {
        await page.setViewportSize({ width: 1920, height: 600 });
        await page.getByRole('button', { name: '欣赏背景', exact: true }).click();
        const frame = (await hero.boundingBox())!;
        expect(frame.y + frame.height * alpha.top).toBeGreaterThanOrEqual(67);
        await page.keyboard.press('Escape');
        // A game page uses no reserved desktop column and must obey the same height bound.
        await page.goto(USER_ORIGIN + '/activities/gacha');
        await expect(page.locator('.nb-user-shell')).toHaveAttribute('data-layout', 'game');
        await expect(hero).toHaveAttribute('src', '/assets/gacha/' + source);
        await hero.evaluate((image: HTMLImageElement) => image.decode());
        const game = (await hero.boundingBox())!;
        expect(game.y + game.height * alpha.top).toBeGreaterThanOrEqual(67);
      }
      expect(errors).toEqual([]);
    });

// Sample one complete ten-second motion cycle, including the two generic sway extrema.
for (const id of ['g-gemini', 's-chatgpt', 's-claude'])
  test('animated moment head remains visible ' + id, async ({ page }) => {
    test.setTimeout(45_000);
    await page.setViewportSize({ width: 1920, height: 600 });
    await fixtures(page, id, 'dark');
    await page.goto(USER_ORIGIN + '/activities');
    const surface = page.locator('.nb-moment-character canvas');
    await page.emulateMedia({ reducedMotion: 'no-preference' });
    await expect(surface).toHaveAttribute('data-ready', 'true', { timeout: 15_000 });
    const tops: number[] = [];
    for (let sample = 0; sample < 21; sample++) {
      tops.push(
        await surface.evaluate((canvas: HTMLCanvasElement) => {
          const pixels = canvas
            .getContext('2d')!
            .getImageData(0, 0, canvas.width, canvas.height).data;
          let first = 0;
          while (first < canvas.width * canvas.height && pixels[first * 4 + 3] <= 8) first++;
          const bounds = canvas.getBoundingClientRect();
          return bounds.y + (Math.floor(first / canvas.width) * bounds.height) / canvas.height;
        }),
      );
      if (sample < 20) await page.waitForTimeout(500);
    }
    expect(Math.min(...tops)).toBeGreaterThanOrEqual(67);
    expect(Math.max(...tops)).toBeLessThan(420);
  });
