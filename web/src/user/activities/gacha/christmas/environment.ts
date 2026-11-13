import type { ChristmasCard } from './motion';

/** Weather and light are painted behind the character, through registered material masks. */
export function createChristmasEnvironment(windows: HTMLImageElement, lamps: HTMLImageElement) {
  const layer = document.createElement('canvas');
  const context = layer.getContext('2d')!;
  return {
    paint(output: CanvasRenderingContext2D, time: number, width: number, height: number) {
      if (layer.width !== width || layer.height !== height) {
        layer.width = width;
        layer.height = height;
      }
      const sx = width / 1024,
        sy = height / 1536;
      context.clearRect(0, 0, width, height);
      context.save();
      context.scale(sx, sy);
      // Several depths: falling snow has a drifting trajectory, not an oscillating backdrop.
      for (let i = 0; i < 68; i++) {
        const depth = 0.35 + (i % 5) * 0.16,
          speed = 18 + depth * 30;
        const y = 145 + ((i * 89.71 + time * speed) % 640);
        const gust = Math.sin(time * 0.31 + i * 0.07) * 13 + Math.sin(time * 0.73 - y * 0.006) * 5;
        const x = ((i * 137.13 + time * (5 + depth * 3) + gust) % 1100) - 30;
        context.globalAlpha = 0.22 + depth * 0.34;
        context.fillStyle = '#e5f2ff';
        context.beginPath();
        context.ellipse(x, y, 1 + depth * 1.2, 1.4 + depth * 1.7, 0.2, 0, Math.PI * 2);
        context.fill();
      }
      // One brief distant streak, with a long quiet interval, remains outside the windows.
      const event = (time + 8) % 37,
        u = (event - 16) / 1.6;
      if (u > 0 && u < 1) {
        const x = 205 - u * 165,
          y = 193 + u * 70;
        const trail = context.createLinearGradient(x, y, x + 35, y - 15);
        trail.addColorStop(0, 'rgba(221,241,255,.65)');
        trail.addColorStop(1, 'rgba(221,241,255,0)');
        context.globalAlpha = Math.sin(Math.PI * u) ** 2;
        context.strokeStyle = trail;
        context.lineWidth = 1.6;
        context.beginPath();
        context.moveTo(x, y);
        context.lineTo(x + 35, y - 15);
        context.stroke();
      }
      context.restore();
      context.globalCompositeOperation = 'destination-in';
      context.drawImage(windows, 0, 0, width, height);
      context.globalCompositeOperation = 'source-over';
      output.drawImage(layer, 0, 0);
      context.clearRect(0, 0, width, height);
      context.save();
      context.scale(sx, sy);
      for (let i = 0; i < 10; i++) {
        const x = 43 + i * 105,
          y = 62 + Math.sin(i * 1.7) * 26,
          r = 19;
        const glow = context.createRadialGradient(x, y, 0, x, y, r);
        glow.addColorStop(0, 'rgba(255,217,127,.6)');
        glow.addColorStop(1, 'rgba(255,184,79,0)');
        const breathe = 0.5 + 0.5 * Math.sin(time * 0.53 - i * 0.9);
        context.globalAlpha = 0.13 + 0.2 * breathe * breathe;
        context.fillStyle = glow;
        context.fillRect(x - r, y - r, 2 * r, 2 * r);
      }
      context.restore();
      context.globalCompositeOperation = 'destination-in';
      context.drawImage(lamps, 0, 0, width, height);
      context.globalCompositeOperation = 'source-over';
      output.drawImage(layer, 0, 0);
    },
    dispose() {
      layer.width = layer.height = 0;
    },
  };
}

/** Small narrative detail stays inside the held glass; its shell, hand and whale remain intact. */
export function paintChristmasDetail(
  output: CanvasRenderingContext2D,
  id: ChristmasCard,
  time: number,
  width: number,
  height: number,
) {
  if (id !== 't-deepseek') return;
  output.save();
  output.scale(width / 1024, height / 1536);
  output.beginPath();
  output.arc(733, 523, 43, 0, Math.PI * 2);
  output.clip();
  for (let i = 0; i < 13; i++) {
    const angle = i * 2.399 + time * 0.14,
      r = 12 + (i % 4) * 8;
    const fall = (i * 13 + time * 5) % 81;
    const x = 733 + Math.sin(angle) * r,
      y = 483 + fall;
    output.globalAlpha = 0.53 * Math.sin((Math.PI * fall) / 81);
    output.fillStyle = '#f3f7ff';
    output.beginPath();
    output.arc(x, y, 0.8 + (i % 3) * 0.35, 0, Math.PI * 2);
    output.fill();
  }
  output.restore();
}
