import { resolve } from 'node:path';
import { readFileSync } from 'node:fs';
import { expect, test, type Page } from './test';
import { mockPublicConfig, mockRoleSession } from './support';
import { USER_ORIGIN } from './ports';
import { gachaSnapshot } from '../fixtures/gacha';
import type { Card } from '../../src/user/activities/gacha/types';
import { loanActivity } from '../fixtures/loans';
import type { MomentRegistration } from '../../src/user/activities/gacha/moment-frame';
import { specialCards } from '../../src/user/activities/gacha/special-scenes';
import { heartThiefStance } from '../../src/user/activities/gacha/romance/heart-thief-stance';

const { cards } = JSON.parse(readFileSync(resolve('../internal/gacha/catalog.json'), 'utf8')) as {
  cards: Card[];
};

const { cards: compositions } = JSON.parse(
  readFileSync(resolve('public/assets/gacha/composition.json'), 'utf8'),
) as { cards: Record<string, { moment_layers?: MomentRegistration }> };
// Exercise each renderer and top-padding layout once; artwork review covers every adopted image.
const expandedExamples = new Set([
  'c-gemini',
  'g-claude',
  'g-gemini',
  'l-chatgpt',
  'b-glm',
  'g-glm',
  'f-gemini',
  'f-chatgpt',
  'e-glm',
  'e-claude',
  'c-glm',
  'g-deepseek',
  's-chatgpt',
  's-claude',
  't-claude',
  't-deepseek',
]);
const targets = [
  ...new Set([
    ...Object.keys(specialCards),
    ...cards
      .filter((card) => {
        const layers = compositions[card.id]?.moment_layers;
        return expandedExamples.has(card.id) && (layers?.expansion?.hero || layers?.scene?.path);
      })
      .map((card) => card.id),
  ]),
];

async function openAppreciation(page: Page, width: number) {
  if (width === 390) await page.getByRole('button', { name: '装扮', exact: true }).click();
  await page.getByRole('button', { name: '欣赏背景', exact: true }).click();
  await expect(page.getByRole('dialog', { name: '欣赏背景', exact: true })).toBeVisible();
}

async function closeAppreciation(page: Page, width: number) {
  await page.keyboard.press('Escape');
  await expect(page.getByRole('dialog', { name: '欣赏背景', exact: true })).not.toBeVisible();
  if (width === 390) {
    const settings = page.getByRole('dialog', { name: '装扮', exact: true });
    await settings.getByRole('button', { name: '关闭', exact: true }).click();
  }
}

for (const theme of ['light', 'dark'])
  for (const width of [1440, 1920, 390])
    for (const id of targets) {
      const card = cards.find((card) => card.id === id)!;
      const animated = card.rarity === 'SSR' || card.rarity === 'SP';
      const appearance = specialCards[id] ?? 'heart-moment';
      const scenes = specialCards[id]
        ? (['collection', 'appearance', 'moment'] as const)
        : (['moment'] as const);
      for (const scene of scenes) {
        if (width === 1920 && scene !== 'moment') continue;
        test(`special ${scene} ${id} ${theme} ${width}`, async ({ page }) => {
          // Wide special scenes cover animation, appreciation and reduced-motion cleanup together.
          if (scene === 'moment' && width === 1920 && specialCards[id]) test.setTimeout(60_000);
          const errors: string[] = [];
          page.on('pageerror', (error) => errors.push(error.message));
          page.on('console', (message) => {
            // Headless screenshots read GPU pixels and produce this driver diagnostic.
            if (
              message.type() === 'warning' &&
              /^\[\.WebGL-0x[0-9a-f]+\]GL Driver Message \(OpenGL, Performance, GL_CLOSE_PATH_NV, High\): GPU stall due to ReadPixels(?: \(this message will no longer repeat\))?$/.test(
                message.text(),
              )
            )
              return;
            if (message.type() === 'error' || message.type() === 'warning')
              errors.push(message.type());
          });
          await page.setViewportSize({
            width,
            height: width === 390 ? 844 : width === 1920 ? 1080 : 900,
          });
          await page.emulateMedia({ reducedMotion: 'reduce' });
          await page.addInitScript((theme) => {
            localStorage.setItem('nb.lang', 'zh');
            localStorage.setItem('nb.theme', theme);
            localStorage.setItem(
              'nb.gacha.preferences',
              JSON.stringify({ music: false, sound: false, reduced: true }),
            );
          }, theme);
          await mockRoleSession(page, 'user', 'user');
          await mockPublicConfig(page, 'user', { artwork_r18_disabled: card.theme !== 's' });
          const state = gachaSnapshot();
          state.visible_themes = [
            'g',
            ...targets.map((id) => cards.find((card) => card.id === id)!.theme),
          ];
          for (const id of targets) state.owned[id] = { at: state.server_time, source: 'shop' };
          await page.route('**/api/events', (route) =>
            route.fulfill({ contentType: 'text/event-stream', body: ': connected\n\n' }),
          );
          await page.route('**/api/gacha/state', (route) => route.fulfill({ json: state }));
          await page.route('**/api/activities', (route) => route.fulfill({ json: loanActivity() }));
          await page.route('**/api/limited-activities', (route) => route.fulfill({ json: [] }));
          await page.route('**/api/gacha/entry', (route) =>
            route.fulfill({ json: { enabled: true, entered: true, pools: [] } }),
          );
          const selected =
            scene === 'appearance' ? appearance : scene === 'moment' ? 'heart-moment' : 'default';
          const moment = scene === 'moment' ? 'card:' + id : 'cover:permanent';
          await page.route('**/api/appearance', (route) =>
            route.fulfill({
              json: {
                selected,
                visible: ['default', 'heart-moment', ...Object.values(specialCards)],
                unlocked: ['default', 'heart-moment', ...Object.values(specialCards)],
                updated_at: null,
                moment,
                moments: ['cover:permanent', ...targets.map((id) => 'card:' + id)],
              },
            }),
          );
          let mutations = 0;
          await page.route('**/api/gacha/actions', (route) => {
            const action = route.request().postDataJSON().action;
            expect(action).toBe('enter');
            mutations++;
            return route.fulfill({ json: { snapshot: state } });
          });
          await page.goto(USER_ORIGIN + (scene === 'moment' ? '/activities' : '/activities/gacha'));
          if (scene === 'collection') {
            await page.getByRole('tab', { name: /^我的图鉴/ }).click();
            const card = cards.find((value) => value.id === id)!;
            await page.getByRole('button', { name: card.name + ', 已珍藏', exact: true }).click();
            const portrait = page.locator('.gacha-spotlight__stage img');
            await expect(portrait).toBeVisible();
            await expect
              .poll(() =>
                portrait.evaluate(
                  (image: HTMLImageElement) => image.complete && image.naturalWidth > 0,
                ),
              )
              .toBe(true);
            if (process.env.NONBIRI_VISUAL_DIR)
              await page.screenshot({
                path: resolve(
                  process.env.NONBIRI_VISUAL_DIR,
                  `special-${id}-${theme}-${width}.png`,
                ),
              });
            await page.getByRole('button', { name: '回看初遇', exact: true }).click();
            await expect(page.locator('.gacha-draw-stage')).toHaveAttribute(
              'data-phase',
              'prelude',
            );
            await page.keyboard.press('Escape');
            await expect(page.locator('.gacha-cinema')).toHaveCount(0);
            // Reopen through keyboard activation after focus returns from the cinema.
            await page
              .getByRole('button', { name: card.name + ', 已珍藏', exact: true })
              .press('Enter');
            await page.getByRole('button', { name: '回看重逢', exact: true }).click();
            await expect(page.getByText('重逢回忆', { exact: true })).toBeVisible();
            expect(
              await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth),
            ).toBe(true);
            await page.getByRole('button', { name: '关闭回看', exact: true }).click();
          }
          if (scene === 'appearance') {
            const still = page
              .locator(
                appearance === 'tomorrow'
                  ? '.nb-tomorrow-still .tomorrow-art__still img'
                  : '.nb-appearance-still',
              )
              .first();
            await expect(still).toHaveAttribute(
              'src',
              appearance === 'tomorrow'
                ? `/assets/gacha/motion/r-chatgpt/background-${theme}.webp`
                : `/assets/gacha/appearances/${appearance}-${theme}.webp`,
            );
            await expect
              .poll(() =>
                still.evaluate(
                  (image: HTMLImageElement) => image.complete && image.naturalWidth > 0,
                ),
              )
              .toBe(true);
            if (
              appearance === 'amber' ||
              appearance === 'elopement' ||
              appearance === 'heart-thief' ||
              appearance === 'mirrorsea'
            ) {
              const character = page.locator(
                appearance === 'amber'
                  ? '.nb-amber-still-character'
                  : appearance === 'mirrorsea'
                    ? '.nb-mirror-still-character'
                    : '.nb-romance-still-character',
              );
              await expect
                .poll(() =>
                  character.evaluate(
                    (image: HTMLImageElement, expectedWidth: number) =>
                      image.complete &&
                      (expectedWidth
                        ? image.naturalWidth === expectedWidth
                        : image.naturalWidth > 0),
                    appearance === 'amber'
                      ? 1037
                      : appearance === 'elopement'
                        ? 1024
                        : appearance === 'heart-thief'
                          ? 1086
                          : 0,
                  ),
                )
                .toBe(true);
              const bounds = await character.boundingBox();
              const visibleTop = await character.evaluate((image: HTMLImageElement) => {
                const canvas = document.createElement('canvas');
                canvas.width = image.naturalWidth;
                canvas.height = image.naturalHeight;
                const context = canvas.getContext('2d')!;
                context.drawImage(image, 0, 0);
                const pixels = context.getImageData(0, 0, canvas.width, canvas.height).data;
                let first = 0;
                while (first < canvas.width * canvas.height && pixels[first * 4 + 3] <= 8) first++;
                return Math.floor(first / canvas.width) / canvas.height;
              });
              expect(bounds!.y + bounds!.height * visibleTop).toBeGreaterThan(68);
              // The rotated plate extends below the supporting shoe for cloth motion.
              const contentRatio =
                appearance === 'heart-thief'
                  ? heartThiefStance.anchor[1] / heartThiefStance.plateHeight
                  : 1;
              expect(bounds!.y + bounds!.height * contentRatio).toBeLessThanOrEqual(
                width === 390 ? 844 : 900,
              );
              if (appearance === 'mirrorsea') {
                for (const selector of [
                  '.nb-mirror-still-reflection img',
                  '.nb-mirror-still-light',
                ])
                  await expect
                    .poll(() =>
                      page
                        .locator(selector)
                        .evaluate(
                          (image: HTMLImageElement) => image.complete && image.naturalWidth > 0,
                        ),
                    )
                    .toBe(true);
              }
            }
            await page.emulateMedia({ reducedMotion: 'no-preference' });
            await expect(page.locator('.nb-appearance-backdrop')).toHaveAttribute(
              'data-ready',
              'true',
              { timeout: 15_000 },
            );
            await openAppreciation(page, width);
            if (process.env.NONBIRI_VISUAL_DIR)
              await page.screenshot({
                path: resolve(
                  process.env.NONBIRI_VISUAL_DIR,
                  `appearance-${id}-${theme}-${width}.png`,
                ),
              });
            await closeAppreciation(page, width);
            await page.emulateMedia({ reducedMotion: 'reduce' });
            await expect(page.locator('.nb-appearance-backdrop')).toHaveAttribute(
              'data-ready',
              'false',
            );
          }
          if (scene === 'moment') {
            // The gacha page has game layout; desktop reserve must also be checked on a normal page.
            await expect(page.locator('.loan-card')).toBeVisible();
            await expect(page.locator('.nb-user-shell')).not.toHaveAttribute('data-layout', 'game');
            const layers = compositions[id]?.moment_layers;
            const expansion = layers?.expansion;
            const hero = page.locator('.nb-moment-character__hero');
            const source = expansion?.hero ?? 'motion/' + id + '/hero.webp';
            await expect(hero).toHaveAttribute('src', '/assets/gacha/' + source);
            await hero.evaluate((image: HTMLImageElement) => image.decode());
            const registeredScene = page.locator('.nb-moment-character__scene');
            if (layers?.scene) {
              const scene = layers.scene;
              const backdropImage = page.locator('.nb-moment-backdrop__scene');
              const sceneImage = scene.viewport ? backdropImage : registeredScene;
              await expect(sceneImage).toHaveAttribute('src', '/assets/gacha/' + scene.path);
              await sceneImage.evaluate((image: HTMLImageElement) => image.decode());
              const background = (await sceneImage.boundingBox())!;
              const backdrop = (await backdropImage.boundingBox())!;
              const stage = (await page.locator('.nb-art-stage').boundingBox())!;
              expect(backdrop.x).toBeLessThanOrEqual(stage.x + 1);
              expect(backdrop.y).toBeLessThanOrEqual(stage.y + 1);
              expect(backdrop.x + backdrop.width).toBeGreaterThanOrEqual(stage.x + stage.width - 1);
              expect(backdrop.y + backdrop.height).toBeGreaterThanOrEqual(
                stage.y + stage.height - 1,
              );
              if (scene.viewport) {
                await expect(registeredScene).toHaveCount(0);
                await expect(backdropImage).toHaveCSS('filter', 'none');
                await expect(backdropImage).toHaveCSS('transform', 'none');
                const shadow = page.locator('.nb-moment-character__shadow');
                if (scene.viewport.shadow) {
                  await expect(shadow).toHaveAttribute(
                    'src',
                    '/assets/gacha/' + scene.viewport.shadow,
                  );
                  await shadow.evaluate((image: HTMLImageElement) => image.decode());
                } else await expect(shadow).toHaveCount(0);
              }
              const character = (await page.locator('.nb-moment-character').boundingBox())!;
              const placement = scene.viewport?.placement;
              if (placement) {
                const [left, top, right, bottom] = placement.bounds;
                expect(character.x + (left * character.width) / 1024).toBeGreaterThanOrEqual(-1);
                expect(character.x + (right * character.width) / 1024).toBeLessThanOrEqual(
                  stage.x + stage.width + 1,
                );
                expect(character.y + (top * character.height) / 1536).toBeGreaterThanOrEqual(67);
                expect(character.y + (bottom * character.height) / 1536).toBeLessThanOrEqual(
                  stage.y + stage.height + 1,
                );
                const ground = character.y + (placement.anchor[1] * character.height) / 1536;
                expect(
                  Math.abs(ground - stage.y - stage.height * (placement.floor ?? 0.95)),
                ).toBeLessThan(1);
              } else {
                const original = scene.originalRegion;
                expect(
                  Math.abs(
                    background.x + (background.width * original.x) / scene.canvas[0] - character.x,
                  ),
                ).toBeLessThan(1);
                expect(
                  Math.abs(
                    background.y + (background.height * original.y) / scene.canvas[1] - character.y,
                  ),
                ).toBeLessThan(1);
                expect(
                  Math.abs((background.width * original.width) / scene.canvas[0] - character.width),
                ).toBeLessThan(1);
              }
            }
            const frame = await hero.boundingBox();
            expect(frame).not.toBeNull();
            if ((expansion || layers?.scene) && width >= 1280) {
              const placement = layers?.scene?.viewport?.placement;
              const character = (await page.locator('.nb-moment-character').boundingBox())!;
              const visibleLeft = placement
                ? character.x + (placement.bounds[0] * character.width) / 1024
                : frame!.x;
              if (!placement) {
                expect(frame!.x).toBeGreaterThanOrEqual(-1);
                expect(frame!.x + frame!.width).toBeLessThanOrEqual(width + 1);
              }
              if (expansion && !layers?.scene?.viewport) {
                const transparentTop =
                  (frame!.height * (expansion.contentTop ?? 0)) / expansion.canvas[1];
                expect(frame!.y + transparentTop).toBeGreaterThanOrEqual(67);
                expect(frame!.y + frame!.height).toBeLessThanOrEqual(
                  page.viewportSize()!.height + 1,
                );
              }
              const main = await page.locator('main').evaluate((element) => {
                const bounds = element.getBoundingClientRect();
                const style = getComputedStyle(element);
                const left = parseFloat(style.paddingLeft),
                  right = parseFloat(style.paddingRight);
                return { right: bounds.right - right, width: bounds.width - left - right };
              });
              expect(main.right).toBeLessThanOrEqual(visibleLeft - 31);
              expect(main.width).toBeGreaterThan(640);
            }
            if (process.env.NONBIRI_VISUAL_DIR)
              await page.screenshot({
                path: resolve(
                  process.env.NONBIRI_VISUAL_DIR,
                  'moment-static-' + id + '-' + theme + '-' + width + '.png',
                ),
              });
            const surface = page.locator('.nb-moment-character canvas');
            if (animated) {
              await expect(surface).toHaveAttribute('data-ready', 'false');
              await page.emulateMedia({ reducedMotion: 'no-preference' });
              await expect(surface).toHaveAttribute('data-ready', 'true', { timeout: 15_000 });
              const animated = await surface.boundingBox();
              expect(animated).not.toBeNull();
              for (const axis of ['x', 'y', 'width', 'height'] as const)
                expect(Math.abs(animated![axis] - frame![axis])).toBeLessThan(1);
            } else await expect(surface).toHaveCount(0);
            if (process.env.NONBIRI_VISUAL_DIR)
              await page.screenshot({
                path: resolve(
                  process.env.NONBIRI_VISUAL_DIR,
                  'moment-page-' + id + '-' + theme + '-' + width + '.png',
                ),
              });
            await openAppreciation(page, width);
            const full = (await page.locator('.nb-art-stage').boundingBox())!;
            const viewport = page.viewportSize()!;
            expect(full.x).toBeCloseTo(0, 0);
            expect(full.y).toBeCloseTo(0, 0);
            expect(full.width).toBeCloseTo(viewport.width, 0);
            expect(full.height).toBeCloseTo(viewport.height, 0);
            if (process.env.NONBIRI_VISUAL_DIR)
              await page.screenshot({
                path: resolve(
                  process.env.NONBIRI_VISUAL_DIR,
                  'moment-' + id + '-' + theme + '-' + width + '.png',
                ),
              });
            await closeAppreciation(page, width);
            await page.emulateMedia({ reducedMotion: 'reduce' });
            if (animated) await expect(surface).toHaveAttribute('data-ready', 'false');
            await expect(hero).toBeVisible();
            expect(
              await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth),
            ).toBe(true);
          }
          await expect.poll(() => mutations).toBe(scene === 'moment' ? 0 : 1);
          expect(errors).toEqual([]);
        });
      }
    }
