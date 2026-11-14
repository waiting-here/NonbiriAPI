import type { ClassicCard, Environment, Rect, ScenicPart } from './types';

const wx = 1024;
const hy = 1536;
const fixed = (base: string, parts: readonly ScenicPart[]): Environment => ({ base, parts });
const stock = (id: string, file: string) => 'motion/' + id + '/parts/' + file + '.webp';
const base = (id: string) => stock(id, 'scenery');
const flow = (
  id: string,
  name: string,
  rect: Rect,
  effect: ScenicPart['effect'],
  color?: string,
): ScenicPart => ({ path: stock(id, name), rect, effect, color });
const spirit = (
  id: string,
  name: string,
  rect: Rect,
  amplitude: readonly [number, number],
  rotation: number,
  period: number,
  delay: number,
  front = false,
): ScenicPart => ({
  path: stock(id, name),
  rect,
  front,
  motion: {
    kind: 'rigid',
    pivot: [rect[0] + rect[2] * 0.5, rect[1] + rect[3] * 0.36],
    amplitude,
    rotation,
    period,
    delay,
  },
});

const summer = fixed(base('i-chatgpt'), [
  flow('i-chatgpt', 'sky', [0, 0, 1024, 405], 'fireworks', '#ffd8ab'),
  flow('i-chatgpt', 'river', [39, 784, 955, 361], 'river', '#ffcf88'),
  flow('i-chatgpt', 'lanterns', [0, 137, 1024, 541], 'flame', '#ffdb9f'),
  spirit('i-chatgpt', 'foliage', [0, 0, 1024, 112], [0.4, 1.7], 0.0012, 13.4, 1.3),
]);
const theater = fixed(base('j-chatgpt'), [
  flow('j-chatgpt', 'moon-and-stars', [250, 58, 521, 285], 'sky', '#f4dda4'),
  flow('j-chatgpt', 'stage-lights', [5, 321, 1005, 753], 'flame', '#ffcf93'),
  spirit('j-chatgpt', 'orrery', [0, 178, 127, 231], [0, 0], 0.018, 11.7, 0.5),
  spirit('j-chatgpt', 'hanging-star', [934, 242, 90, 261], [0, 0], 0.026, 9.4, 1.3),
  spirit('j-chatgpt', 'petals', [786, 1345, 210, 152], [9, 12], 0.027, 13.8, -0.4, true),
]);
export const environments: Record<ClassicCard, Environment> = {
  'g-chatgpt': fixed(base('g-chatgpt'), [
    spirit('g-chatgpt', 'left-cloudbank', [33, 180, 478, 506], [7, 2], 0, 28.5, -0.5),
    spirit('g-chatgpt', 'right-cloudbank', [586, 288, 391, 441], [-10, 3], 0, 36, 0.3),
    flow('g-chatgpt', 'sunlight', [50, 126, 331, 454], 'flame', '#fff4cf'),
    {
      ...spirit(
        'g-chatgpt',
        'cloud-companion',
        [104, 339, 133, 133],
        [10, 6],
        0.042,
        7.4,
        0.8,
        true,
      ),
      path: 'motion/g-chatgpt/cloud.webp',
    },
    {
      ...spirit(
        'g-chatgpt',
        'cloud-companion',
        [794, 332, 215, 215],
        [8, 10],
        0.052,
        8.6,
        1.0,
        true,
      ),
      path: 'motion/g-chatgpt/cloud.webp',
    },
    {
      ...spirit(
        'g-chatgpt',
        'cloud-companion',
        [51, 864, 205, 205],
        [12, 10],
        0.05,
        10.2,
        1.7,
        true,
      ),
      path: 'motion/g-chatgpt/cloud.webp',
    },
  ]),
  'g-claude': fixed(base('g-claude'), [
    flow('g-claude', 'candlelight', [0, 22, 1024, 853], 'flame', '#ffe3a7'),
    spirit('g-claude', 'left-paper', [17, 98, 203, 166], [12, 10], 0.06, 10, 0.4),
    spirit('g-claude', 'right-paper', [843, 365, 91, 137], [7, 18], 0.08, 8.4, 0),
    spirit('g-claude', 'butterfly', [198, 134, 101, 101], [10, 12], 0.19, 6, 0.2),
  ]),
  'g-glm': fixed(base('g-glm'), [
    flow('g-glm', 'waterfalls', [267, 204, 694, 526], 'cascade', '#e1f7ff'),
    flow('g-glm', 'diagram-light', [0, 115, 901, 1390], 'sky', '#aed5ff'),
    spirit('g-glm', 'flower-branch', [0, 0, 487, 205], [1.8, 3.5], 0.0045, 14.6, 0.4),
  ]),
  'g-deepseek': fixed(base('g-deepseek'), [
    { ...spirit('g-deepseek', 'whale', [410, 114, 550, 346], [24, 6], 0.012, 19.4, 0), swim: true },
    { path: stock('g-deepseek', 'center-column'), rect: [548, 0, 118, 713] },
    flow('g-deepseek', 'caustics', [350, 3, 674, 1345], 'cascade', '#aeeaff'),
    spirit('g-deepseek', 'left-bubble', [0, 287, 118, 192], [1.5, 8], 0.015, 7.6, 0.4),
    spirit('g-deepseek', 'right-bubble', [705, 449, 111, 104], [4, 10], 0.02, 11.2, 0.5),
    flow('g-deepseek', 'sconces', [65, 141, 930, 550], 'flame', '#ffdfad'),
  ]),
  'g-gemini': fixed(base('g-gemini'), [
    flow('g-gemini', 'spiral-galaxy', [614, 0, 410, 195], 'sky', '#e1cbff'),
    {
      path: stock('g-gemini', 'orrery-ring'),
      rect: [867, 184, 157, 227],
      motion: {
        kind: 'rigid',
        pivot: [1012, 301],
        amplitude: [0, 0],
        rotation: 0.105,
        period: 13.8,
        delay: 0,
      },
    },
    { path: stock('g-gemini', 'orrery-crossband'), rect: [762, 145, 262, 277] },
    flow('g-gemini', 'orrery-glints', [758, 140, 266, 305], 'sky', '#fff0b0'),
    flow('g-gemini', 'floor-glints', [8, 547, 1008, 988], 'river', '#ffedcc'),
  ]),
  'i-chatgpt': summer,
  'i-claude': summer,
  'i-glm': summer,
  'i-deepseek': summer,
  'i-gemini': summer,
  'j-chatgpt': theater,
  'j-claude': theater,
  'j-glm': theater,
  'j-deepseek': theater,
  'j-gemini': theater,
};

export function createScenery(definition: Environment, images: readonly HTMLImageElement[]) {
  const [background, ...sprites] = images;
  const scratch = document.createElement('canvas');
  const overlay = scratch.getContext('2d');
  if (!overlay) throw new Error('renderer-unavailable');
  return {
    paint(ctx: CanvasRenderingContext2D, time: number, front: boolean, paintSwimmer?: () => void) {
      ctx.save();
      ctx.scale(ctx.canvas.width / wx, ctx.canvas.height / hy);
      if (!front && background) ctx.drawImage(background, 0, 0, wx, hy);
      for (const [i, part] of definition.parts.entries()) {
        if (!!part.front !== front) continue;
        const image = sprites[i]!;
        const [x, y, width, height] = part.rect;
        if (part.swim) {
          paintSwimmer?.();
        } else if (part.effect === 'fireworks') {
          paintFireworks(ctx, image, time);
        } else if (part.effect) {
          if (scratch.width !== width || scratch.height !== height) {
            scratch.width = width;
            scratch.height = height;
          }
          overlay.clearRect(0, 0, width, height);
          overlay.globalCompositeOperation = 'source-over';
          overlay.drawImage(image, 0, 0, width, height);
          overlay.globalCompositeOperation = 'source-in';
          const energy = 0.5 + Math.sin(time * 0.87) * 0.5;
          if (part.effect === 'flame' || part.effect === 'sky') {
            overlay.fillStyle = part.color ?? '#fff4d9';
            overlay.fillRect(0, 0, width, height);
            ctx.globalAlpha =
              part.effect === 'flame' ? 0.02 + energy * 0.12 : 0.035 + energy * 0.13;
          } else {
            const period = part.effect === 'river' ? 55 : 92;
            overlay.globalCompositeOperation = 'source-over';
            overlay.clearRect(0, 0, width, height);
            for (let a = -period; a < height + period; a += period) {
              const y0 = a + ((time * (part.effect === 'river' ? 7 : 32)) % period);
              const fade = overlay.createLinearGradient(0, y0, 0, y0 + period);
              fade.addColorStop(0, 'transparent');
              fade.addColorStop(0.46, part.color ?? '#e6f7ff');
              fade.addColorStop(1, 'transparent');
              overlay.fillStyle = fade;
              overlay.fillRect(0, y0, width, period);
            }
            overlay.globalCompositeOperation = 'destination-in';
            overlay.drawImage(image, 0, 0, width, height);
            ctx.globalAlpha = 0.13;
          }
          ctx.globalCompositeOperation = 'screen';
          ctx.drawImage(scratch, x, y);
          ctx.globalCompositeOperation = 'source-over';
          ctx.globalAlpha = 1;
        } else {
          ctx.save();
          const motion = part.motion;
          if (motion) {
            const phase = (time * Math.PI * 2) / motion.period + motion.delay;
            const [px, py] = motion.pivot;
            ctx.translate(
              px + motion.amplitude[0] * Math.sin(phase),
              py + motion.amplitude[1] * Math.sin(phase + 0.32),
            );
            ctx.rotate(motion.rotation * Math.sin(phase));
            ctx.translate(-px, -py);
          }

          ctx.drawImage(image, x, y, width, height);
          ctx.restore();
        }
      }
      ctx.restore();
    },
    dispose() {
      scratch.width = scratch.height = 0;
    },
  };
}

const bursts = [
  { x: 188, y: 154, crop: [34, 0, 344, 349], period: 7.9, offset: 1.3, color: '#ffd991' },
  { x: 844, y: 203, crop: [720, 69, 266, 264], period: 8.6, offset: 3.2, color: '#fbc2b1' },
  { x: 934, y: 296, crop: [866, 230, 158, 175], period: 11.4, offset: 6.0, color: '#ffdbbf' },
] as const;

function paintFireworks(ctx: CanvasRenderingContext2D, image: HTMLImageElement, time: number) {
  const sx = image.naturalWidth / 1024;
  const sy = image.naturalHeight / 405;
  ctx.save();
  ctx.globalCompositeOperation = 'screen';
  for (const burst of bursts) {
    const phase = (time + burst.offset) % burst.period;
    if (phase < 0.65) {
      const u = phase / 0.65;
      const height = 190 * (1 - u);
      ctx.strokeStyle = burst.color;
      ctx.globalAlpha = u * 0.52;
      ctx.lineWidth = 1.7;
      ctx.beginPath();
      ctx.moveTo(burst.x + 16 * (1 - u), burst.y + height + 29);
      ctx.lineTo(burst.x + 16 * (1 - u), burst.y + height);
      ctx.stroke();
      continue;
    }
    const u = (phase - 0.65) / 2.9;
    if (u >= 1) continue;
    const spread = 0.26 + 0.81 * (1 - Math.exp(-6 * u));
    const [left, top, width, height] = burst.crop;
    ctx.globalAlpha = Math.min(1, u * 12) * (1 - u) ** 1.25;
    ctx.drawImage(
      image,
      left * sx,
      top * sy,
      width * sx,
      height * sy,
      burst.x + (left - burst.x) * spread,
      burst.y + (top - burst.y) * spread + 27 * u ** 2,
      width * spread,
      height * spread,
    );
  }
  ctx.restore();
}
