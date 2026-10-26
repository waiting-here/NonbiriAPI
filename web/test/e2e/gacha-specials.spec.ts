import { resolve } from 'node:path';
import { readFileSync } from 'node:fs';
import { expect, test } from './test';
import { mockPublicConfig, mockRoleSession } from './support';
import { USER_ORIGIN } from './ports';
import { gachaSnapshot } from '../fixtures/gacha';
import type { Card } from '../../src/user/activities/gacha/types';
import { specialCards } from '../../src/user/activities/gacha/special-scenes';

const { cards } = JSON.parse(readFileSync(resolve('../internal/gacha/catalog.json'), 'utf8')) as {
  cards: Card[];
};

for (const theme of ['light', 'dark'])
  for (const width of [1440, 390])
    for (const [id, appearance] of Object.entries(specialCards)) {
      for (const scene of ['collection', 'appearance', 'moment'] as const)
        test(`special ${scene} ${id} ${theme} ${width}`, async ({ page }) => {
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
          await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
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
          await mockPublicConfig(page, 'user');
          const state = gachaSnapshot();
          state.visible_themes = [
            'g',
            ...Object.keys(specialCards).map((id) => cards.find((card) => card.id === id)!.theme),
          ];
          for (const id of Object.keys(specialCards))
            state.owned[id] = { at: state.server_time, source: 'shop' };
          await page.route('**/api/events', (route) =>
            route.fulfill({ contentType: 'text/event-stream', body: ': connected\n\n' }),
          );
          await page.route('**/api/gacha/state', (route) => route.fulfill({ json: state }));
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
                moments: [
                  'cover:permanent',
                  ...Object.keys(specialCards).map((id) => 'card:' + id),
                ],
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
          await page.goto(USER_ORIGIN + '/activities/gacha');
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
              expect(bounds!.y).toBeGreaterThan(68);
              const contentRatio = appearance === 'heart-thief' ? 1448 / 1488 : 1;
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
            await page.getByRole('button', { name: '欣赏背景', exact: true }).click();
            if (process.env.NONBIRI_VISUAL_DIR)
              await page.screenshot({
                path: resolve(
                  process.env.NONBIRI_VISUAL_DIR,
                  `appearance-${id}-${theme}-${width}.png`,
                ),
              });
            await page.keyboard.press('Escape');
            await page.emulateMedia({ reducedMotion: 'reduce' });
            await expect(page.locator('.nb-appearance-backdrop')).toHaveAttribute(
              'data-ready',
              'false',
            );
          }
          if (scene === 'moment') {
            const hero = page.locator('.nb-moment-character img');
            await expect(hero).toHaveAttribute('src', `/assets/gacha/motion/${id}/hero.webp`);
            await expect
              .poll(() =>
                hero.evaluate(
                  (image: HTMLImageElement) => image.complete && image.naturalWidth > 0,
                ),
              )
              .toBe(true);
            await page.emulateMedia({ reducedMotion: 'no-preference' });
            await expect(page.locator('.nb-moment-character canvas')).toHaveAttribute(
              'data-ready',
              'true',
              { timeout: 15_000 },
            );
            await page.getByRole('button', { name: '欣赏背景', exact: true }).click();
            if (process.env.NONBIRI_VISUAL_DIR)
              await page.screenshot({
                path: resolve(process.env.NONBIRI_VISUAL_DIR, `moment-${id}-${theme}-${width}.png`),
              });
            await page.keyboard.press('Escape');
            await page.emulateMedia({ reducedMotion: 'reduce' });
          }
          await expect.poll(() => mutations).toBe(1);
          expect(errors).toEqual([]);
        });
    }
