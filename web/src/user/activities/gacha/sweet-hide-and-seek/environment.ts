import type { SweetCard } from './motion';

export function createSweetEnvironment(background: HTMLImageElement, id: SweetCard) {
  const make = () => Object.assign(document.createElement('canvas'), { width: 512, height: 768 });
  const source = make(),
    sx = source.getContext('2d', { willReadFrequently: true })!;
  sx.drawImage(background, 0, 0, 512, 768);
  const pixels = sx.getImageData(0, 0, 512, 768);
  const lamps = make(),
    glass = make(),
    sweep = make();
  const lampPixels = new ImageData(512, 768),
    glassPixels = new ImageData(512, 768);
  for (let y = 0; y < 768; y++)
    for (let x = 0; x < 512; x++) {
      const p = (y * 512 + x) * 4,
        [r, g, b] = pixels.data.slice(p, p + 3),
        px = x * 2,
        py = y * 2;
      const lamp =
        id === 's-chatgpt'
          ? px > 309 && px < 379 && py > 242 && py < 355
          : (px < 240 && py < 95) ||
            (px > 820 && px < 1013 && py > 268 && py < 312) ||
            (px > 824 && px < 867 && py > 391 && py < 455) ||
            (px > 919 && px < 951 && py > 481 && py < 511);
      if (lamp && r > 145 && g > 96 && r > b * 1.35)
        lampPixels.data.set([255, 181, 90, Math.round(Math.min(1, (r - 120) / 110) * 190)], p);
      const window =
        id === 's-chatgpt'
          ? (px < 285 && py < 930) || (px > 660 && py < 1000)
          : px < 250 && py < 1010;
      if (window && r > 185 && g > 165 && b > 100)
        glassPixels.data.set(
          [236, 226, 196, Math.round(Math.min(1, (Math.min(r, g) - 160) / 80) * 90)],
          p,
        );
    }
  lamps.getContext('2d')!.putImageData(lampPixels, 0, 0);
  glass.getContext('2d')!.putImageData(glassPixels, 0, 0);
  source.width = source.height = 0;
  const sc = sweep.getContext('2d')!;
  return {
    paint(ctx: CanvasRenderingContext2D, time: number, width: number, height: number) {
      ctx.save();
      ctx.globalCompositeOperation = 'screen';
      ctx.globalAlpha = 0.085 + 0.035 * Math.sin(time * 0.73) + 0.018 * Math.sin(time * 1.41 + 0.8);
      ctx.drawImage(lamps, 0, 0, width, height);
      const cycle = (time % 22) / 22;
      sc.clearRect(0, 0, 512, 768);
      sc.globalCompositeOperation = 'source-over';
      const x = -250 + cycle * 1100;
      const sheen = sc.createLinearGradient(x, 0, x + 100, 0);
      sheen.addColorStop(0, 'transparent');
      sheen.addColorStop(0.5, 'rgba(255,239,206,.38)');
      sheen.addColorStop(1, 'transparent');
      sc.fillStyle = sheen;
      sc.fillRect(0, 0, 512, 768);
      sc.globalCompositeOperation = 'destination-in';
      sc.drawImage(glass, 0, 0);
      ctx.globalAlpha = 0.34;
      ctx.drawImage(sweep, 0, 0, width, height);
      ctx.restore();
      if (id === 's-claude') {
        ctx.save();
        ctx.scale(width / 1024, height / 1536);
        // Leaves travel through the open alley, behind the door, figure and roof line.
        ctx.beginPath();
        ctx.rect(791, 330, 233, 910);
        ctx.clip();
        for (let i = 0; i < 3; i++) {
          const t = ((time + i * 8) % 34) - 22;
          if (t < 0 || t > 7) continue;
          const progress = t / 7;
          const x = 851 + i * 43 + Math.sin(t * 0.9 + i) * 27,
            y = 340 + progress * 850;
          const alpha = Math.min(1, progress * 7, (1 - progress) * 7) * 0.68;
          ctx.save();
          ctx.translate(x, y);
          ctx.rotate(t * 0.8 + i);
          ctx.scale(0.65 + i * 0.15, 0.55 + Math.abs(Math.cos(t * 1.15)) * 0.4);
          ctx.globalAlpha = alpha;
          ctx.fillStyle = i === 1 ? '#af6c38' : '#d29a57';
          ctx.beginPath();
          ctx.moveTo(0, -8);
          ctx.lineTo(3, -3);
          ctx.lineTo(8, -5);
          ctx.lineTo(6, 0);
          ctx.lineTo(9, 3);
          ctx.lineTo(2, 5);
          ctx.lineTo(0, 9);
          ctx.lineTo(-2, 4);
          ctx.lineTo(-8, 2);
          ctx.lineTo(-5, -1);
          ctx.lineTo(-7, -6);
          ctx.lineTo(-2, -4);
          ctx.closePath();
          ctx.fill();
          ctx.restore();
        }
        ctx.restore();
      }
    },
    dispose() {
      for (const canvas of [lamps, glass, sweep]) canvas.width = canvas.height = 0;
    },
  };
}
