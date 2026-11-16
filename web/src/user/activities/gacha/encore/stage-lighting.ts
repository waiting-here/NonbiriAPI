import type { EncoreCard } from './layers';
import { paintPartyRibbons, partyRibbonsAt } from './party-ribbons';

const tau = Math.PI * 2;
const wave = (time: number, period: number, phase = 0) => Math.sin((tau * time) / period + phase);
const mix = (a: number, b: number, t: number) => a + (b - a) * t;
type RGB = readonly [number, number, number];
const amber: RGB = [255, 199, 115];
const blue: RGB = [133, 181, 255];
const rose: RGB = [249, 164, 180];
const colorMix = (a: RGB, b: RGB, t: number): RGB => [
  mix(a[0], b[0], t),
  mix(a[1], b[1], t),
  mix(a[2], b[2], t),
];
const rgba = (color: RGB, alpha: number) => `rgba(${color.join(',')},${alpha})`;

/** Independent fixture clocks share elapsed time, never a scene-wide modulo/reset. */
export function stageLightingAt(time: number, id: EncoreCard) {
  const t = time + { 'u-gemini': 0, 'u-glm': 5.4, 'u-deepseek': 11.7 }[id];
  const cue = (1 + wave(t, 73.7, -1.2)) / 2;
  const warm = colorMix(amber, rose, cue * 0.6);
  const cool = colorMix(blue, amber, (1 + wave(t, 97.3, 1.1)) * 0.24);
  const tint = colorMix(warm, cool, 0.4);
  return {
    ribbons: partyRibbonsAt(time, id),
    warm,
    cool,
    wash: 0.065 + cue * 0.08,
    lamps: [0.13 + wave(t, 6.7) * 0.025, 0.13 + wave(t, 8.9, 1.4) * 0.025],
    beams: [
      {
        origin: [253, 70] as const,
        target: [420 + 260 * wave(t, 19.3, 0.42 * wave(t, 53.1)), 1310] as const,
        width: 160 + 24 * wave(t, 31.7),
        alpha: 0.12 + 0.065 * (1 + wave(t, 13.9, 0.6)),
        color: warm,
      },
      {
        origin: [771, 70] as const,
        target: [607 + 245 * wave(t, 26.9, 1.9 + 0.3 * wave(t, 67.3)), 1385] as const,
        width: 146 + 23 * wave(t, 43.1, 1.7),
        alpha: 0.1 + 0.055 * (1 + wave(t, 17.3, 2.3)),
        color: cool,
      },
    ],
    floor: {
      x: 512 + 210 * wave(t, 41.3, 0.25 * wave(t, 109.7)),
      y: 1215 + 65 * wave(t, 59.9, 0.8),
      angle: t * 0.028 + wave(t, 83.9) * 0.4,
      alpha: 0.055 + 0.045 * (1 + wave(t, 37.1, 1.1)),
    },
    glint: 0.1 + 0.16 * Math.pow((1 + wave(t, 11.3, 0.3 * wave(t, 47.9))) / 2, 5),
    tint: [tint[0] / 255, tint[1] / 255, tint[2] / 255] as RGB,
    tintStrength: 0.018 + cue * 0.018,
  };
}

function glow(
  context: CanvasRenderingContext2D,
  x: number,
  y: number,
  rx: number,
  ry: number,
  color: RGB,
  alpha: number,
) {
  context.save();
  context.translate(x, y);
  context.scale(rx, ry);
  const gradient = context.createRadialGradient(0, 0, 0, 0, 0, 1);
  gradient.addColorStop(0, rgba(color, alpha));
  gradient.addColorStop(0.42, rgba(color, alpha * 0.6));
  gradient.addColorStop(1, rgba(color, 0));
  context.fillStyle = gradient;
  context.fillRect(-1, -1, 2, 2);
  context.restore();
}

export function paintStageLighting(
  context: CanvasRenderingContext2D,
  state: ReturnType<typeof stageLightingAt>,
) {
  context.save();
  context.globalCompositeOperation = 'screen';
  // Wash stays on the back curtain, lamps on their painted fixtures.
  glow(context, 290, 610, 440, 470, state.warm, state.wash);
  glow(context, 755, 620, 410, 460, state.cool, state.wash * 0.8);
  glow(context, 91, 438, 100, 130, amber, state.lamps[0]);
  glow(context, 936, 438, 100, 130, amber, state.lamps[1]);
  for (const beam of state.beams) {
    const [ox, oy] = beam.origin;
    const [tx, ty] = beam.target;
    const length = Math.hypot(tx - ox, ty - oy);
    context.save();
    context.translate(ox, oy);
    context.rotate(-Math.atan2(tx - ox, ty - oy));
    const light = context.createLinearGradient(0, 0, 0, length);
    light.addColorStop(0, rgba(beam.color, beam.alpha * 0.17));
    light.addColorStop(0.55, rgba(beam.color, beam.alpha * 0.1));
    light.addColorStop(1, rgba(beam.color, 0));
    context.fillStyle = light;
    // Nested soft apertures avoid the hard triangular edges of a flat cone.
    for (let band = 0; band < 7; band++) {
      const width = beam.width * (1 - band * 0.1);
      context.beginPath();
      context.moveTo(-8, 0);
      context.lineTo(8, 0);
      context.lineTo(width, length);
      context.lineTo(-width, length);
      context.fill();
    }
    context.restore();
    glow(context, tx, ty, beam.width * 1.22, 44, beam.color, beam.alpha * 0.8);
  }
  // A slowly rotating, defocused stage gobo projects onto the floor plane.
  context.save();
  context.beginPath();
  context.rect(0, 950, 1024, 586);
  context.clip();
  context.translate(state.floor.x, state.floor.y);
  context.scale(1, 0.24);
  context.rotate(state.floor.angle);
  for (let i = 0; i < 5; i++) {
    const angle = (i * tau) / 5;
    glow(
      context,
      Math.cos(angle) * 112,
      Math.sin(angle) * 112,
      105,
      70,
      state.warm,
      state.floor.alpha,
    );
  }
  context.restore();
  // Small reflections belong to actual hanging stars, not free-floating particles.
  for (const [x, y] of [
    [170, 245],
    [215, 324],
    [808, 323],
    [512, 55],
  ])
    glow(context, x, y, 19, 23, amber, state.glint);
  context.restore();
  paintPartyRibbons(context, state.ribbons);
}
