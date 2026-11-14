const width = 1672;
const skyHeight = 380;
const height = 941;
const files = ['clear', 'sky-mask', 'high-clouds', 'low-clouds'] as const;

export function tomorrowSkySources(dark: boolean) {
  const base = `/assets/gacha/motion/r-chatgpt/sky-${dark ? 'dark' : 'light'}/`;
  return files.map((name) => ({
    name: `tomorrow-sky-${name}`,
    path: `${base}${name}.webp`,
    rect: [0, 0, width, skyHeight] as const,
  }));
}

const smooth = (value: number) => {
  const t = Math.max(0, Math.min(1, value));
  return t * t * (3 - 2 * t);
};

/** The sky moves behind a fixed foliage/roof matte, before the leaf plates. */
export async function createTomorrowSky(
  source: HTMLImageElement,
  dark: boolean,
  signal: AbortSignal,
) {
  const owned: HTMLImageElement[] = [];
  const surfaces = [height, skyHeight, skyHeight].map((h) => {
    const canvas = document.createElement('canvas');
    canvas.width = width;
    canvas.height = h;
    return canvas;
  });
  let disposed = false;
  const controller = new AbortController();
  const dispose = () => {
    if (disposed) return;
    disposed = true;
    controller.abort();
    signal.removeEventListener('abort', dispose);
    for (const image of owned) {
      image.onload = image.onerror = null;
      image.src = '';
    }
    for (const canvas of surfaces) canvas.width = canvas.height = 0;
  };
  signal.addEventListener('abort', dispose, { once: true });
  const load = (url: string) =>
    new Promise<HTMLImageElement>((resolve, reject) => {
      const image = new Image();
      owned.push(image);
      const finish = () => {
        controller.signal.removeEventListener('abort', abort);
        image.onload = image.onerror = null;
      };
      const abort = () => {
        finish();
        reject(new DOMException('Cancelled', 'AbortError'));
      };
      image.onload = () => {
        finish();
        resolve(image);
      };
      image.onerror = () => {
        finish();
        reject(new Error('sky-image-unavailable'));
      };
      controller.signal.addEventListener('abort', abort, { once: true });
      if (controller.signal.aborted) abort();
      else image.src = url;
    });
  try {
    if (signal.aborted) throw new DOMException('Cancelled', 'AbortError');
    const [clear, mask, high, low] = await Promise.all(
      tomorrowSkySources(dark).map((item) => load(item.path)),
    );
    if (controller.signal.aborted) throw new DOMException('Cancelled', 'AbortError');
    const [scene, sky, clouds] = surfaces.map((canvas) => canvas.getContext('2d'));
    if (!scene || !sky || !clouds) throw new Error('sky-renderer-unavailable');

    function deck(image: HTMLImageElement, seconds: number, cycle: number, distance: number) {
      const phase = (((seconds % cycle) + cycle) % cycle) / cycle;
      const reset = smooth((phase - 0.84) / 0.16);
      // Complementary premultiplied coverage avoids a dim band during renewal.
      clouds!.globalAlpha = 1 - reset;
      clouds!.drawImage(image, distance * phase, 0);
      if (reset) {
        clouds!.globalAlpha = reset;
        clouds!.drawImage(image, distance * (phase - 1), 0);
      }
    }

    function birds(seconds: number) {
      const phase = ((seconds % 83) + 83) % 83;
      sky!.save();
      sky!.strokeStyle = dark ? '#182444' : '#345378';
      sky!.lineWidth = 0.9;
      sky!.lineCap = 'round';
      for (let i = 0; i < 3; i++) {
        const age = phase - 9 - i * 1.45;
        if (age <= 0 || age >= 27) continue;
        const fade = smooth(age / 1.5) * (1 - smooth((age - 24) / 3));
        const x = 1410 - age * 35;
        const y = 112 + i * 16 + age * 1.45 + Math.sin(age * 0.31 + i) * 3;
        const wing = Math.sin(age * 5.2 + i * 0.8) * 1.8;
        sky!.globalAlpha = fade * 0.66;
        sky!.beginPath();
        sky!.moveTo(x - 3.1, y - wing);
        sky!.quadraticCurveTo(x - 1.4, y - 1.5, x, y);
        sky!.quadraticCurveTo(x + 1.4, y - 1.5, x + 3.1, y - wing);
        sky!.stroke();
      }
      sky!.restore();
    }

    return {
      dispose,
      draw(seconds: number) {
        if (disposed) return surfaces[0];
        clouds.globalAlpha = 1;
        clouds.clearRect(0, 0, width, skyHeight);
        clouds.globalCompositeOperation = 'lighter';
        deck(high, seconds, 109, 48);
        deck(low, seconds, 163, 29);
        clouds.globalAlpha = 1;
        sky.globalCompositeOperation = 'copy';
        sky.drawImage(clear, 0, 0);
        sky.globalCompositeOperation = 'source-over';
        sky.drawImage(surfaces[2], 0, 0);
        birds(seconds);
        sky.globalCompositeOperation = 'destination-in';
        sky.drawImage(mask, 0, 0);
        scene.globalCompositeOperation = 'copy';
        scene.drawImage(source, 0, 0, width, height);
        scene.globalCompositeOperation = 'source-over';
        scene.drawImage(surfaces[1], 0, 0);
        return surfaces[0];
      },
    };
  } catch (error) {
    dispose();
    throw error;
  }
}
