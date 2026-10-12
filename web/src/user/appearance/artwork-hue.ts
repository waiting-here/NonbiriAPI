import { useEffect, type RefObject } from 'react';

export function artworkHue(pixels: Uint8ClampedArray): number {
  let x = 0,
    y = 0,
    weight = 0;
  for (let i = 0; i < pixels.length; i += 4) {
    if (pixels[i + 3] < 128) continue;
    const r = pixels[i] / 255,
      g = pixels[i + 1] / 255,
      b = pixels[i + 2] / 255;
    const high = Math.max(r, g, b),
      low = Math.min(r, g, b),
      chroma = high - low;
    if (chroma < 0.08 || high < 0.15 || low > 0.9) continue;
    const sector =
      high === r ? (g - b) / chroma : high === g ? (b - r) / chroma + 2 : (r - g) / chroma + 4;
    const angle = (sector * Math.PI) / 3;
    x += Math.cos(angle) * chroma;
    y += Math.sin(angle) * chroma;
    weight += chroma;
  }
  if (!weight || Math.hypot(x, y) / weight < 0.1) return 215;
  return Math.round(((Math.atan2(y, x) * 180) / Math.PI + 360) % 360);
}

export function useArtworkHue(image: RefObject<HTMLImageElement | null>, artwork: string) {
  useEffect(() => {
    const node = image.current;
    if (!node) return;
    const root = document.documentElement;
    const update = () => {
      if (!node.naturalWidth) return;
      try {
        const canvas = document.createElement('canvas');
        canvas.width = canvas.height = 32;
        const context = canvas.getContext('2d', { willReadFrequently: true });
        if (!context) return;
        context.drawImage(node, 0, 0, 32, 32);
        const hue = artworkHue(context.getImageData(0, 0, 32, 32).data);
        root.style.setProperty('--nb-moment-hue', hue + 'deg');
      } catch {
        root.style.removeProperty('--nb-moment-hue');
      }
    };
    node.addEventListener('load', update);
    if (node.complete) update();
    return () => {
      node.removeEventListener('load', update);
      root.style.removeProperty('--nb-moment-hue');
    };
  }, [image, artwork]);
}
