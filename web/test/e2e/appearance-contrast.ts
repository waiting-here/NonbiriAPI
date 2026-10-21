import type { Page } from './test';

export interface ContrastFailure {
  selector: string;
  text: string;
  ratio: number;
  required: number;
}

/** CSS boundary estimate; actual raster samples are reported separately below. */
export async function contrastFailures(page: Page): Promise<ContrastFailure[]> {
  return page.evaluate(() => {
    const canvas = document.createElement('canvas');
    canvas.width = canvas.height = 1;
    const context = canvas.getContext('2d', { willReadFrequently: true })!;
    const colors = new Map<string, number[]>();
    const rgba = (value: string): number[] => {
      const cached = colors.get(value);
      if (cached) return cached;
      context.clearRect(0, 0, 1, 1);
      context.fillStyle = '#000';
      context.fillStyle = value;
      context.fillRect(0, 0, 1, 1);
      const [r, g, b, a] = context.getImageData(0, 0, 1, 1).data;
      const color = [r, g, b, a / 255];
      colors.set(value, color);
      return color;
    };
    const linear = (n: number) => {
      n /= 255;
      return n <= 0.04045 ? n / 12.92 : ((n + 0.055) / 1.055) ** 2.4;
    };
    const luminance = (c: number[]) =>
      0.2126 * linear(c[0]) + 0.7152 * linear(c[1]) + 0.0722 * linear(c[2]);
    const over = (top: number[], below: number[]) =>
      [0, 1, 2].map((i) => top[i] * top[3] + below[i] * (1 - top[3]));
    const split = (value: string) => {
      let depth = 0;
      let start = 0;
      const parts: string[] = [];
      for (let i = 0; i < value.length; i++) {
        if (value[i] === '(') depth++;
        else if (value[i] === ')') depth--;
        else if (value[i] === ',' && depth === 0) {
          parts.push(value.slice(start, i).trim());
          start = i + 1;
        }
      }
      parts.push(value.slice(start).trim());
      return parts;
    };
    const stopColor = (value: string) => {
      let depth = 0;
      let end = value.length;
      for (let i = 0; i < value.length; i++) {
        if (value[i] === '(') depth++;
        else if (value[i] === ')') depth--;
        else if (/\s/.test(value[i]) && depth === 0) {
          end = i;
          break;
        }
      }
      const color = value.slice(0, end);
      return CSS.supports('color', color) ? color : null;
    };
    const layerCache = new WeakMap<Element, number[][][]>();
    const layersOf = (element: Element): number[][][] => {
      const cached = layerCache.get(element);
      if (cached) return cached;
      const style = getComputedStyle(element);
      const layers: number[][][] = [];
      for (const image of split(style.backgroundImage)) {
        if (!image.includes('gradient(')) continue;
        const stops = split(image.slice(image.indexOf('(') + 1, -1))
          .map(stopColor)
          .filter((color): color is string => color !== null);
        const colors = stops.map(rgba);
        // Equal-alpha stops can have very different contrast. Also check the interior:
        // medium-grey text can pass both black and white endpoints yet fail between them.
        const space = image.match(/\bin (srgb-linear|srgb|oklab|oklch|lab|lch)\b/)?.[1] ?? 'oklab';
        for (let i = 1; i < stops.length; i++) {
          for (let step = 1; step < 8; step++)
            colors.push(
              rgba(`color-mix(in ${space}, ${stops[i - 1]}, ${stops[i]} ${step * 12.5}%)`),
            );
        }
        if (colors.length) layers.push(colors);
      }
      const color = rgba(style.backgroundColor);
      if (color[3] > 0) layers.push([color]);
      layerCache.set(element, layers);
      return layers;
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
      if (rect.width <= 1 || rect.height <= 1) continue;
      const style = getComputedStyle(element);
      if (style.visibility !== 'visible' || !element.checkVisibility()) continue;
      if (element.closest('.sr-only, .visually-hidden')) continue;
      const layers: number[][][] = [];
      let opacity = 1;
      for (let current: Element | null = element; current; current = current.parentElement) {
        if (current === document.body || current === document.documentElement) break;
        const currentStyle = getComputedStyle(current);
        opacity *= Number(currentStyle.opacity);
        // display: contents boxes paint nothing, even when they carry a background.
        if (currentStyle.display === 'contents') continue;
        layers.push(...layersOf(current));
      }
      if (opacity < 0.05) continue;
      const foreground = rgba(style.color);
      const size = parseFloat(style.fontSize);
      const bold = Number(style.fontWeight) >= 700;
      const required = size >= 24 || (bold && size >= 18.66) ? 3 : 4.5;
      let ratio = Infinity;
      const appearance = document.documentElement.dataset.appearance;
      const decorated = appearance && appearance !== 'default';
      if (!decorated)
        layers.push(...layersOf(document.body), ...layersOf(document.documentElement));
      let backgrounds = decorated
        ? [
            [0, 0, 0],
            [255, 255, 255],
          ]
        : [[255, 255, 255]];
      for (const choices of layers.reverse()) {
        const unique = new Map<string, number[]>();
        for (const background of backgrounds)
          for (const choice of choices) {
            const composite = over(choice, background);
            unique.set(composite.map((n) => n.toFixed(2)).join(','), composite);
          }
        backgrounds = [...unique.values()];
        // Bound large nested-gradient combinations while retaining luminance extremes
        // and interior colours. This is a sampled estimate, not a raster measurement.
        if (backgrounds.length > 129) {
          backgrounds.sort((a, b) => luminance(a) - luminance(b));
          const all = backgrounds;
          backgrounds = Array.from(
            { length: 129 },
            (_, i) => all[Math.round((i * (all.length - 1)) / 128)],
          );
        }
      }
      for (const background of backgrounds) {
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

export interface PixelContrastSample extends ContrastFailure {
  point: [number, number];
  background: number[];
  foreground: number[];
  pixels: number;
}

/**
 * Samples the rendered background in visible DOM text-run rectangles at CSS-pixel scale.
 * Text fill is hidden without changing geometry, so gradients, pseudo-elements, artwork,
 * blur and sibling decorations are included. This checks background pixels, not antialiased
 * glyph edge colours. Offscreen text and animation frames beyond this snapshot are not covered.
 */
export async function pixelContrastSamples(
  page: Page,
  backgroundPath?: string,
): Promise<PixelContrastSample[]> {
  const runs = await page.evaluate(() => {
    const canvas = document.createElement('canvas');
    canvas.width = canvas.height = 1;
    const context = canvas.getContext('2d', { willReadFrequently: true })!;
    const runs = [];
    const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
    for (let node = walker.nextNode(); node; node = walker.nextNode()) {
      const text = node.textContent?.trim();
      const element = node.parentElement;
      if (
        !text ||
        !element ||
        element.closest('[aria-hidden="true"], .sr-only, .visually-hidden, script, style, noscript')
      )
        continue;
      const style = getComputedStyle(element);
      if (style.visibility !== 'visible' || !element.checkVisibility()) continue;
      const box = element.getBoundingClientRect();
      if (box.width <= 1 || box.height <= 1) continue;
      let opacity = 1;
      const clip = { left: 0, top: 0, right: innerWidth, bottom: innerHeight };
      let hidden = false;
      for (let current: Element | null = element; current; current = current.parentElement) {
        const currentStyle = getComputedStyle(current);
        opacity *= Number(currentStyle.opacity);
        if (currentStyle.clipPath === 'inset(50%)') hidden = true;
        const bounds = current.getBoundingClientRect();
        if (/hidden|clip|auto|scroll/.test(currentStyle.overflowX)) {
          clip.left = Math.max(clip.left, bounds.left);
          clip.right = Math.min(clip.right, bounds.right);
        }
        if (/hidden|clip|auto|scroll/.test(currentStyle.overflowY)) {
          clip.top = Math.max(clip.top, bounds.top);
          clip.bottom = Math.min(clip.bottom, bounds.bottom);
        }
      }
      if (hidden || opacity < 0.05) continue;
      const range = document.createRange();
      range.selectNodeContents(node);
      const rects = [...range.getClientRects()]
        .map((r) => ({
          left: Math.max(clip.left, r.left),
          top: Math.max(clip.top, r.top),
          right: Math.min(clip.right, r.right),
          bottom: Math.min(clip.bottom, r.bottom),
        }))
        .filter((r) => r.right > r.left && r.bottom > r.top)
        .filter((r) => {
          const hit = document.elementFromPoint((r.left + r.right) / 2, (r.top + r.bottom) / 2);
          return hit && (element.contains(hit) || hit.contains(element));
        });
      if (!rects.length) continue;
      context.clearRect(0, 0, 1, 1);
      context.fillStyle = style.color;
      context.fillRect(0, 0, 1, 1);
      const foreground = [...context.getImageData(0, 0, 1, 1).data];
      foreground[3] = (foreground[3] / 255) * opacity;
      const size = parseFloat(style.fontSize);
      const required = size >= 24 || (Number(style.fontWeight) >= 700 && size >= 18.66) ? 3 : 4.5;
      runs.push({
        selector:
          element.tagName.toLowerCase() +
          [...element.classList]
            .slice(0, 3)
            .map((name) => '.' + name)
            .join(''),
        text: text.slice(0, 40),
        rects,
        foreground,
        required,
      });
    }
    return runs;
  });
  const hiddenText = await page.addStyleTag({
    content:
      '* { -webkit-text-fill-color: transparent !important; text-shadow: none !important; caret-color: transparent !important; }',
  });
  let background: Buffer;
  try {
    background = await page.screenshot({
      path: backgroundPath,
      type: 'png',
      scale: 'css',
      animations: 'disabled',
    });
  } finally {
    await hiddenText.evaluate((element) => element.parentNode?.removeChild(element));
  }
  return page.evaluate(
    async ({ runs, png }) => {
      const bytes = Uint8Array.from(atob(png), (character) => character.charCodeAt(0));
      const image = await createImageBitmap(new Blob([bytes], { type: 'image/png' }));
      const canvas = document.createElement('canvas');
      canvas.width = image.width;
      canvas.height = image.height;
      const context = canvas.getContext('2d', { willReadFrequently: true })!;
      context.drawImage(image, 0, 0);
      image.close();
      const { data } = context.getImageData(0, 0, canvas.width, canvas.height);
      const linear = (n: number) =>
        n / 255 <= 0.04045 ? n / 255 / 12.92 : ((n / 255 + 0.055) / 1.055) ** 2.4;
      const luminance = (c: number[]) =>
        0.2126 * linear(c[0]) + 0.7152 * linear(c[1]) + 0.0722 * linear(c[2]);
      return runs
        .map((run) => {
          let ratio = Infinity;
          let point: [number, number] = [0, 0];
          let background: number[] = [];
          let pixels = 0;
          for (const rect of run.rects) {
            for (let y = Math.ceil(rect.top); y < Math.min(canvas.height, rect.bottom); y += 2)
              for (let x = Math.ceil(rect.left); x < Math.min(canvas.width, rect.right); x += 2) {
                const offset = (y * canvas.width + x) * 4;
                const rgb = [...data.slice(offset, offset + 3)];
                const shown = rgb.map(
                  (n, i) => run.foreground[i] * run.foreground[3] + n * (1 - run.foreground[3]),
                );
                const a = luminance(shown),
                  b = luminance(rgb);
                const current = (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05);
                pixels++;
                if (current < ratio) {
                  ratio = current;
                  point = [x, y];
                  background = rgb;
                }
              }
          }
          return {
            selector: run.selector,
            text: run.text,
            foreground: run.foreground,
            required: run.required,
            ratio,
            point,
            background,
            pixels,
          };
        })
        .filter((sample) => sample.pixels > 0);
    },
    { runs, png: background.toString('base64') },
  );
}
