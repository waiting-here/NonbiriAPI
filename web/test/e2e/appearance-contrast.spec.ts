import { creditDisplayFixtures } from './credit-display-fixtures';
import { mkdirSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { expect, test } from './test';
import { USER_ORIGIN } from './ports';
import { ALL_APPEARANCES, appearanceFixtures } from './appearance-fixtures';
import { contrastFailures, pixelContrastSamples } from './appearance-contrast';

const PAGES = [
  ['/', 'home', '.core-checkin-card'],
  ['/charity?tab=donations', 'donations', '.economy-donation-list'],
  ['/keys', 'keys', '.nb-panel'],
  ['/endpoints', 'services', '.nb-panel'],
  ['/models', 'models', '.nb-page-header'],
  ['/account', 'account', '.core-account-preferences'],
  ['/activities', 'activities', '.loan-card'],
  ['/rewards', 'rewards', '.reward-form'],
  ['/credits', 'credits', '.credit-history__table tbody tr'],
  ['/logs', 'logs', '.nb-table-wrap'],
  ['/steward?tab=maintenance', 'steward', '.nb-panel--danger'],
  ['/privacy', 'privacy', '.legal-layout__body'],
];

const enabled = Boolean(process.env.NONBIRI_APPEARANCE_AUDIT);
const designs = ALL_APPEARANCES.filter((design) => design !== 'default');

for (const intensity of ['vivid', 'balanced', 'focus'])
  for (const design of designs)
    for (const mode of ['light', 'dark'])
      for (const width of [1440, 390])
        test(`appearance contrast ${intensity} ${design} ${mode} ${width}`, async ({ page }) => {
          test.skip(!enabled, 'Set NONBIRI_APPEARANCE_AUDIT to run the contrast audit.');
          test.setTimeout(180_000);
          await page.setViewportSize({ width, height: 900 });
          await page.emulateMedia({ reducedMotion: 'reduce' });
          await page.addInitScript(
            ({ mode, intensity }) => {
              localStorage.setItem('nb.lang', 'zh');
              localStorage.setItem('nb.theme', mode);
              localStorage.setItem('nb.appearance-intensity', intensity);
            },
            { mode, intensity },
          );
          await appearanceFixtures(page, design);
          const directory = process.env.NONBIRI_APPEARANCE_AUDIT!;
          const name = `${intensity}-${design}-${mode}-${width}`;
          const images = resolve(directory, name);
          mkdirSync(images, { recursive: true });
          const pages: Record<string, unknown> = {};
          const failures: unknown[] = [];
          for (const [path, label, ready] of PAGES) {
            await page.goto(USER_ORIGIN + path);
            await expect(page.locator('main h1').first()).toBeVisible();
            await expect(page.locator(ready).first()).toBeVisible();
            await expect(page.locator('html')).toHaveAttribute(
              'data-appearance-intensity',
              intensity,
            );
            if (path !== '/privacy')
              await expect(page.locator('html')).toHaveAttribute('data-appearance', design);
            await page
              .locator(
                'img.nb-appearance-still, img.nb-amber-still-character, .nb-moment-backdrop img, .nb-tomorrow-still img',
              )
              .evaluateAll(async (images) => {
                await Promise.all(images.map((image) => (image as HTMLImageElement).decode()));
              });
            await page.evaluate(() => document.fonts.ready);
            const estimate = await contrastFailures(page);
            await page.screenshot({
              path: resolve(images, label + '.jpg'),
              type: 'jpeg',
              quality: 65,
              scale: 'css',
              animations: 'disabled',
            });
            const samples = await pixelContrastSamples(
              page,
              resolve(images, label + '-background.png'),
            );
            expect(
              samples.length,
              `${name}/${label} must contain rendered text samples`,
            ).toBeGreaterThan(0);
            const raster = samples.filter((sample) => sample.ratio < sample.required);
            pages[label] = {
              appliedAppearance: await page.locator('html').getAttribute('data-appearance'),
              cssEstimateFailures: estimate,
              pixelFailures: raster,
              pixelSamples: samples,
            };
            failures.push(
              ...estimate.map((failure) => ({ page: label, method: 'css-estimate', ...failure })),
              ...raster.map((failure) => ({ page: label, method: 'raster-sample', ...failure })),
            );
          }
          writeFileSync(
            resolve(directory, name + '.json'),
            JSON.stringify({ intensity, design, mode, width, pages }, null, 2) + '\n',
          );
          if (process.env.NONBIRI_APPEARANCE_AUDIT_STRICT) expect(failures).toEqual([]);
        });

const CREDIT_PAGES = [
  ['/', 'home', '.home-wallet'],
  ['/credits', 'credits', '.credit-history__table'],
  ['/games/fishing', 'game', '.fishing-controls'],
] as const;

// Include default surfaces as well as every appearance for the new fractional ink.
for (const intensity of ['vivid', 'balanced', 'focus'])
  for (const design of ALL_APPEARANCES)
    for (const mode of ['light', 'dark'])
      for (const width of [1440, 390])
        test(`credit fractions ${intensity} ${design} ${mode} ${width}`, async ({ page }) => {
          test.skip(!enabled, 'Set NONBIRI_APPEARANCE_AUDIT to run the contrast audit.');
          test.setTimeout(120_000);
          await page.setViewportSize({ width, height: 900 });
          await page.emulateMedia({ reducedMotion: 'reduce' });
          await page.addInitScript(
            ({ mode, intensity }) => {
              localStorage.setItem('nb.lang', 'zh');
              localStorage.setItem('nb.theme', mode);
              localStorage.setItem('nb.appearance-intensity', intensity);
            },
            { mode, intensity },
          );
          await creditDisplayFixtures(page, design);
          const directory = process.env.NONBIRI_APPEARANCE_AUDIT!;
          mkdirSync(directory, { recursive: true });
          const name = `credit-${intensity}-${design}-${mode}-${width}`;
          const evidence: Record<string, unknown> = {};
          for (const [path, label, selector] of CREDIT_PAGES) {
            await page.goto(USER_ORIGIN + path);
            const region = page.locator(selector).first();
            await expect(region).toBeVisible();
            await expect(region.locator('.credit-number__fraction').first()).toBeVisible();
            await region.evaluate((element) => element.scrollIntoView({ block: 'center' }));
            await page.evaluate(() => document.fonts.ready);
            if (label === 'credits') {
              const signs = await page.locator('.credit-change').evaluateAll((elements) =>
                elements.map((element) => ({
                  text: element.textContent,
                  color: getComputedStyle(element).color,
                  integerColor: getComputedStyle(element.querySelector('.credit-number')!).color,
                })),
              );
              expect(signs.some((value) => value.text === '+0.001')).toBe(true);
              expect(signs.some((value) => value.text === '−0.001')).toBe(true);
              expect(signs.every((value) => value.color === value.integerColor)).toBe(true);
            }
            const samples = (await pixelContrastSamples(page)).filter((sample) =>
              sample.selector.includes('.credit-number__fraction'),
            );
            expect(
              samples.length,
              name + '/' + label + ' actual fractional samples',
            ).toBeGreaterThan(0);
            expect(
              samples.filter((sample) => sample.ratio < 4.5),
              name + '/' + label,
            ).toEqual([]);
            evidence[label] = samples;
            expect(
              await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth),
            ).toBe(true);
            if (intensity === 'balanced' && (design === 'default' || design === 'amber'))
              await page.screenshot({ path: resolve(directory, name + '-' + label + '.png') });
          }
          writeFileSync(
            resolve(directory, name + '.json'),
            JSON.stringify(evidence, null, 2) + String.fromCharCode(10),
          );
        });
