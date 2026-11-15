import type { EncoreCard } from './layers';

const tau = Math.PI * 2;
const random = (seed: number) => {
  const value = Math.sin(seed * 127.1 + 311.7) * 43758.5453;
  return value - Math.floor(value);
};
const palette = ['#e6bf70', '#ede0b5', '#adcaec', '#dca6b5'];

/** Two offstage party poppers: finite launches, ballistic flight, flutter and a quiet interval. */
export function partyRibbonsAt(time: number, id: EncoreCard) {
  const offset = { 'u-gemini': 0, 'u-glm': 5.4, 'u-deepseek': 11.7 }[id];
  const pieces = [];
  for (const side of [0, 1]) {
    const start = (side ? 47.3 : 8.5) + offset;
    const period = side ? 193.1 : 154.7;
    const cycle = Math.floor((time - start) / period);
    if (cycle < 0) continue;
    const elapsed = time - start - cycle * period;
    if (elapsed > 9.5) continue;
    for (let i = 0; i < 26; i++) {
      const seed = i + side * 103 + cycle * 619;
      const age = elapsed - random(seed + 5) * 0.13;
      if (age <= 0) continue;
      const direction = side ? -1 : 1;
      const x =
        (side ? 978 : 46) +
        direction * (65 + random(seed + 11) * 75) * age +
        Math.sin(age * 2.3 + i) * age * 3;
      const y = 1285 - (390 + random(seed + 17) * 170) * age + 67 * age * age;
      const alpha = Math.min(
        1,
        age / 0.1,
        Math.max(0, (9.5 - age) / 1.8),
        Math.max(0, (1330 - y) / 110),
      );
      if (alpha === 0) continue;
      pieces.push({
        x,
        y,
        alpha,
        age,
        angle: direction * age * (0.8 + random(seed + 23) * 2.3) + i,
        flutter: Math.cos(age * (3.4 + random(seed + 31) * 2.6) + i),
        length: 16 + random(seed + 37) * 27,
        color: palette[i % palette.length],
        streamer: i % 5 === 0,
      });
    }
  }
  return pieces;
}

export function paintPartyRibbons(
  context: CanvasRenderingContext2D,
  pieces: ReturnType<typeof partyRibbonsAt>,
) {
  context.save();
  for (const p of pieces) {
    context.save();
    context.globalAlpha = p.alpha * 0.78;
    context.translate(p.x, p.y);
    context.rotate(p.angle);
    context.scale(0.18 + 0.82 * Math.abs(p.flutter), 1);
    context.fillStyle = p.color;
    context.strokeStyle = p.color;
    if (p.streamer) {
      context.lineWidth = 3.4;
      context.beginPath();
      for (let step = 0; step <= 18; step++) {
        const v = step / 18;
        const x = Math.sin(v * tau * 1.6 + p.age * 2) * 8;
        const y = (v - 0.5) * p.length * 2.2;
        if (step === 0) context.moveTo(x, y);
        else context.lineTo(x, y);
      }
      context.stroke();
      context.strokeStyle = 'rgba(255,244,219,.45)';
      context.lineWidth = 0.9;
      context.stroke();
    } else {
      context.beginPath();
      context.moveTo(-4, -p.length / 2);
      context.lineTo(5, -p.length / 2 + 3);
      context.lineTo(4 + p.flutter * 3, p.length / 2);
      context.lineTo(-5, p.length / 2 - 2);
      context.fill();
      context.fillStyle = 'rgba(255,246,220,.4)';
      context.fillRect(-3, -p.length / 2 + 2, 1.2, p.length - 4);
    }
    context.restore();
  }
  context.restore();
}
