import type { Point, Rect } from './layers';
export type CelestialPart = {
  name: string;
  label: string;
  rect: Rect;
  pivot: Point;
  kind: 'cloud' | 'chain' | 'crescent' | 'glow';
  phase: number;
  amplitude: number;
  buddy?: boolean;
};
const cloud = (name: string, rect: Rect, phase: number, amplitude: number): CelestialPart => ({
  name,
  label: '云层 · ' + name,
  rect,
  pivot: [0, 0],
  kind: 'cloud',
  phase,
  amplitude,
});
const chain = (name: string, rect: Rect, pivot: Point, buddy = false): CelestialPart => ({
  name,
  label: '星链 · ' + name,
  rect,
  pivot,
  kind: 'chain',
  phase: pivot[0] * 0.019,
  amplitude: 0.006,
  buddy,
});
export const celestialParts: CelestialPart[] = [
  {
    name: 'sun-glow',
    label: '日晕',
    rect: [785, 250, 230, 170],
    pivot: [900, 335],
    kind: 'glow',
    phase: 0,
    amplitude: 0,
  },
  cloud('cloud-left', [0, 400, 410, 270], 0.2, 5),
  cloud('cloud-right', [646, 395, 378, 275], 1.1, 4),
  cloud('cloud-middle', [292, 420, 450, 250], 2.1, 6),
  chain('left-star-chain', [125, 0, 72, 395], [158, 1]),
  chain('top-star-chain', [605, 0, 44, 98], [626, 1]),
  chain('top-gold-chain', [730, 0, 40, 99], [748, 1]),
  chain('top-diamond-chain', [809, 0, 46, 153], [832, 1]),
  chain('right-diamond-chain', [865, 0, 46, 161], [886, 1]),
  chain('companion-chain', [751, 246, 45, 126], [774, 254], true),
  {
    name: 'small-crescent',
    label: '悬浮月饰',
    rect: [508, 240, 77, 87],
    pivot: [545, 285],
    kind: 'crescent',
    phase: 0.7,
    amplitude: 2.5,
  },
];
export function paintCelestialPart(
  ctx: CanvasRenderingContext2D,
  image: HTMLImageElement,
  part: CelestialPart,
  time: number,
  active: boolean,
) {
  const [x, y, w, h] = part.rect;
  ctx.save();
  if (part.kind === 'glow') ctx.globalCompositeOperation = 'screen';
  if (active && part.kind !== 'glow') {
    if (part.kind === 'cloud') {
      const phase = part.phase,
        speed = 0.024 + phase * 0.004;
      ctx.translate(
        (Math.sin(time * speed + phase) - Math.sin(phase)) * part.amplitude,
        (Math.cos(time * speed * 0.7 + phase) - Math.cos(phase)) * 0.8,
      );
    } else {
      const [px, py] = part.pivot;
      const bx = part.buddy ? 4 * Math.sin(time * 0.57) : 0,
        by = part.buddy ? 5 * Math.sin(time * 0.74) : 0;
      ctx.translate(px + bx, py + by);
      ctx.rotate(
        (Math.sin(time * (part.kind === 'chain' ? 0.68 : 0.13) + part.phase) -
          Math.sin(part.phase)) *
          (part.kind === 'chain' ? part.amplitude : 0.008),
      );
      if (part.kind === 'crescent')
        ctx.translate(Math.sin(time * 0.19) * part.amplitude, Math.sin(time * 0.17) * 1.3);
      ctx.translate(-px, -py);
    }
  }
  ctx.drawImage(image, x, y, w, h);
  ctx.restore();
}
