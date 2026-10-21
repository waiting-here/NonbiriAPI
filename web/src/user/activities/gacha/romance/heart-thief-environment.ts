function glow(
  context: CanvasRenderingContext2D,
  x: number,
  y: number,
  radius: number,
  color: string,
  alpha: number,
) {
  const light = context.createRadialGradient(x, y, 0, x, y, radius);
  light.addColorStop(0, `rgba(${color},${alpha})`);
  light.addColorStop(0.35, `rgba(${color},${alpha * 0.3})`);
  light.addColorStop(1, `rgba(${color},0)`);
  context.fillStyle = light;
  context.fillRect(x - radius, y - radius, radius * 2, radius * 2);
}

export function heartThiefLight(time: number) {
  return {
    moon: 0.75 + 0.25 * Math.sin((time * Math.PI * 2) / 34.8),
    candle: 0.93 + 0.045 * Math.sin(time * 3.2) + 0.025 * Math.sin(time * 5.7 + 0.6),
  };
}

export function heartThiefEnvironment(
  image: HTMLImageElement,
  background: { draw(time: number): CanvasImageSource },
) {
  // The moving sky stops before the moon, castle spires, flowers and window frames.
  const cloud = document.createElement('canvas');
  const resolution = image.naturalWidth / 1536;
  cloud.width = Math.round(500 * resolution);
  cloud.height = Math.round(148 * resolution);
  const sky = cloud.getContext('2d')!;
  sky.drawImage(
    image,
    655 * resolution,
    95 * resolution,
    500 * resolution,
    148 * resolution,
    0,
    0,
    cloud.width,
    cloud.height,
  );
  sky.globalCompositeOperation = 'destination-in';
  sky.translate(cloud.width / 2, cloud.height / 2);
  sky.scale(cloud.width / 2, cloud.height / 2);
  const mask = sky.createRadialGradient(0, 0, 0.55, 0, 0, 1);
  mask.addColorStop(0, '#fff');
  mask.addColorStop(1, 'rgba(255,255,255,0)');
  sky.fillStyle = mask;
  sky.fillRect(-1, -1, 2, 2);

  return {
    draw(context: CanvasRenderingContext2D, time: number, dark: boolean) {
      context.drawImage(background.draw(12 + time), 0, 0, 1536, 1024);
      context.drawImage(cloud, 655 + 2.5 * Math.sin((time * Math.PI * 2) / 42), 95, 500, 148);
      context.save();
      context.globalCompositeOperation = 'screen';
      const { moon, candle } = heartThiefLight(time);
      glow(context, 1184, 72, 300, '179,208,255', (dark ? 0.035 : 0.065) * moon);
      for (const [x, y, radius, strength] of [
        [25, 218, 145, 1],
        [67, 266, 105, 0.8],
        [48, 873, 140, 0.65],
      ]) {
        glow(context, x, y, radius, '255,188,105', (dark ? 0.14 : 0.065) * strength * candle);
      }
      for (const [index, [x, y]] of [
        [834, 358],
        [1127, 396],
        [1211, 293],
        [1055, 328],
      ].entries()) {
        glow(
          context,
          x,
          y,
          5,
          '255,215,151',
          0.12 * (0.75 + 0.25 * Math.sin(time * 0.3 + index * 1.8)),
        );
      }
      context.beginPath();
      context.moveTo(548, 550);
      context.lineTo(1297, 605);
      context.lineTo(1265, 613);
      context.lineTo(506, 559);
      context.closePath();
      context.fillStyle = `rgba(187,216,255,${(dark ? 0.018 : 0.035) * moon})`;
      context.fill();
      context.save();
      context.translate(1100, 990);
      context.scale(1, 0.19);
      glow(context, 0, 0, 210, '174,206,255', 0.045 * moon);
      context.restore();
      context.restore();
    },
    dispose() {
      cloud.width = 0;
      cloud.height = 0;
    },
  };
}

export function heartThiefDust(
  context: CanvasRenderingContext2D,
  time: number,
  dark: boolean,
  foreground: boolean,
) {
  const points = foreground
    ? [
        [468, 717, 4],
        [1390, 758, 4.5],
      ]
    : [
        [688, 435, 1.4],
        [1160, 430, 1.3],
        [1006, 515, 1.6],
        [593, 674, 1.8],
      ];
  context.save();
  context.globalCompositeOperation = 'screen';
  points.forEach(([x, y, radius], index) => {
    const phase = index * 2.1;
    glow(
      context,
      x + 5 * Math.sin(time * 0.19 + phase),
      y + 7 * Math.cos(time * 0.16 + phase),
      radius * 2.6,
      dark ? '255,216,165' : '193,218,255',
      (foreground ? 0.12 : 0.19) * (0.75 + 0.25 * Math.sin(time * 0.3 + phase)),
    );
  });
  context.restore();
}
