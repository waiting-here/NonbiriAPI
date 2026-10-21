import { mkdirSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { expect, test, type Page } from './test';
import { USER_ORIGIN } from './ports';
import { ALL_APPEARANCES, appearanceFixtures } from './appearance-fixtures';

const PAGES: [string, string][] = [
  ['/', 'home'],
  ['/charity?tab=donations', 'donations'],
  ['/keys', 'keys'],
  ['/endpoints', 'services'],
  ['/models', 'models'],
  ['/account', 'account'],
  ['/activities', 'activities'],
  ['/rewards', 'rewards'],
  ['/credits', 'credits'],
  ['/logs', 'logs'],
  ['/steward?tab=maintenance', 'steward'],
  ['/privacy', 'privacy'],
];

export interface ContrastFailure {
  selector: string;
  text: string;
  ratio: number;
  required: number;
}

/**
 * Measures every visible text run against the worst artwork behind it (pure black and pure
 * white). Ancestor backgrounds are composited in paint order; gradient glass uses its weakest
 * stop. Blur is ignored, so results are conservative.
 */
export async function contrastFailures(page: Page): Promise<ContrastFailure[]> {
  return page.evaluate(() => {
    const canvas = document.createElement('canvas');
    canvas.width = canvas.height = 1;
    const context = canvas.getContext('2d', { willReadFrequently: true })!;
    const rgba = (value: string): number[] => {
      context.clearRect(0, 0, 1, 1);
      context.fillStyle = '#000';
      context.fillStyle = value;
      context.fillRect(0, 0, 1, 1);
      const [r, g, b, a] = context.getImageData(0, 0, 1, 1).data;
      return [r, g, b, a / 255];
    };
    const root = getComputedStyle(document.documentElement);
    const edge = root.getPropertyValue('--nb-appearance-glass-edge').trim();
    const linear = (n: number) => {
      n /= 255;
      return n <= 0.04045 ? n / 12.92 : ((n + 0.055) / 1.055) ** 2.4;
    };
    const luminance = (c: number[]) =>
      0.2126 * linear(c[0]) + 0.7152 * linear(c[1]) + 0.0722 * linear(c[2]);
    const over = (top: number[], below: number[]) =>
      [0, 1, 2].map((i) => top[i] * top[3] + below[i] * (1 - top[3]));
    const layerOf = (element: Element): number[] | null => {
      const style = getComputedStyle(element);
      const image = style.backgroundImage;
      if (image.includes('gradient')) {
        const stops = [...image.matchAll(/(?:rgba?|color)\([^)]*\)/g)].map((m) => rgba(m[0]));
        // Chromium may serialize glass as unresolved color-mix(); use the resolved edge token.
        if (edge && image.includes('color-mix')) stops.push(rgba(edge));
        const weakest = stops.sort((a, b) => a[3] - b[3])[0];
        if (weakest && weakest[3] > 0) return weakest;
      }
      const color = rgba(style.backgroundColor);
      return color[3] > 0 ? color : null;
    };
    const describe = (element: Element) => {
      const classes = [...element.classList].slice(0, 3).join('.');
      const parent = element.parentElement;
      const parentClasses = parent ? [...parent.classList].slice(0, 2).join('.') : '';
      return (
        (parent
          ? parent.tagName.toLowerCase() + (parentClasses ? '.' + parentClasses : '') + ' > '
          : '') +
        element.tagName.toLowerCase() +
        (classes ? '.' + classes : '')
      );
    };
    const failures: ContrastFailure[] = [];
    const seen = new Set<Element>();
    const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
    for (let node = walker.nextNode(); node; node = walker.nextNode()) {
      const text = (node.textContent ?? '').trim();
      const element = node.parentElement;
      if (!text || !element || seen.has(element)) continue;
      seen.add(element);
      if (element.closest('[aria-hidden="true"], script, style, noscript, dialog:not([open])'))
        continue;
      const rect = element.getBoundingClientRect();
      if (rect.width < 1 || rect.height < 1) continue;
      const style = getComputedStyle(element);
      if (style.visibility !== 'visible') continue;
      if (element.closest('.sr-only, .visually-hidden')) continue;
      const layers: number[][] = [];
      let opacity = 1;
      for (let current: Element | null = element; current; current = current.parentElement) {
        if (current === document.body || current === document.documentElement) break;
        const currentStyle = getComputedStyle(current);
        opacity *= Number(currentStyle.opacity);
        // display: contents boxes paint nothing, even when they carry a background.
        if (currentStyle.display === 'contents') continue;
        const layer = layerOf(current);
        if (layer) layers.push(layer);
      }
      if (opacity < 0.05) continue;
      const foreground = rgba(style.color);
      const size = parseFloat(style.fontSize);
      const bold = Number(style.fontWeight) >= 700;
      const required = size >= 24 || (bold && size >= 18.66) ? 3 : 4.5;
      let ratio = Infinity;
      for (const artwork of [0, 255]) {
        let background = [artwork, artwork, artwork];
        for (const layer of layers.reverse()) background = over(layer, background);
        layers.reverse();
        const shown = over([...foreground.slice(0, 3), foreground[3] * opacity], background);
        const a = luminance(shown);
        const b = luminance(background);
        ratio = Math.min(ratio, (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05));
      }
      if (ratio < required)
        failures.push({
          selector: describe(element),
          text: text.slice(0, 40),
          ratio: Math.round(ratio * 100) / 100,
          required,
        });
    }
    return failures;
  });
}

const enabled = Boolean(process.env.NONBIRI_APPEARANCE_AUDIT);
const designs = ALL_APPEARANCES.filter((design) => design !== 'default');

for (const design of designs)
  for (const mode of ['light', 'dark'])
    for (const width of [1440, 390])
      test(`appearance contrast ${design} ${mode} ${width}`, async ({ page }) => {
        test.skip(!enabled, 'Set NONBIRI_APPEARANCE_AUDIT to run the contrast audit.');
        test.setTimeout(180_000);
        await page.setViewportSize({ width, height: 900 });
        await page.emulateMedia({ reducedMotion: 'reduce' });
        await page.addInitScript((mode) => {
          localStorage.setItem('nb.lang', 'zh');
          localStorage.setItem('nb.theme', mode);
        }, mode);
        await appearanceFixtures(page, design);
        const report: Record<string, ContrastFailure[]> = {};
        for (const [path, label] of PAGES) {
          await page.goto(USER_ORIGIN + path);
          await expect(page.locator('main h1').first()).toBeVisible();
          await page.waitForTimeout(150);
          report[label] = await contrastFailures(page);
        }
        const directory = process.env.NONBIRI_APPEARANCE_AUDIT!;
        mkdirSync(directory, { recursive: true });
        writeFileSync(
          resolve(directory, `${design}-${mode}-${width}.json`),
          JSON.stringify(report, null, 2) + '\n',
        );
        if (process.env.NONBIRI_APPEARANCE_AUDIT_STRICT)
          expect(Object.values(report).flat()).toEqual([]);
      });
