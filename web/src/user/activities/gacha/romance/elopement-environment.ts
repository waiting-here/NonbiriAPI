const tau = Math.PI * 2;

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
  light.addColorStop(0.4, `rgba(${color},${alpha * 0.3})`);
  light.addColorStop(1, `rgba(${color},0)`);
  context.fillStyle = light;
  context.fillRect(x - radius, y - radius, radius * 2, radius * 2);
}

export function elopementEnvironment(image: HTMLImageElement) {
  // These sky regions stop before the overhead wires and the city silhouette.
  const clouds = [
    { x: 162, y: 111, rx: 154, ry: 91, period: 38, amplitude: 2.8 },
    { x: 240, y: 263, rx: 222, ry: 31, period: 51, amplitude: 1.8 },
  ].map((region) => {
    const layer = document.createElement('canvas');
    const resolution = image.naturalWidth / 1536;
    layer.width = Math.round(region.rx * 2 * resolution);
    layer.height = Math.round(region.ry * 2 * resolution);
    const context = layer.getContext('2d')!;
    context.drawImage(
      image,
      (region.x - region.rx) * resolution,
      (region.y - region.ry) * resolution,
      region.rx * 2 * resolution,
      region.ry * 2 * resolution,
      0,
      0,
      layer.width,
      layer.height,
    );
    context.globalCompositeOperation = 'destination-in';
    context.translate(layer.width / 2, layer.height / 2);
    context.scale(layer.width / 2, layer.height / 2);
    const mask = context.createRadialGradient(0, 0, 0.65, 0, 0, 1);
    mask.addColorStop(0, '#fff');
    mask.addColorStop(1, 'rgba(255,255,255,0)');
    context.fillStyle = mask;
    context.fillRect(-1, -1, 2, 2);
    return { ...region, layer };
  });

  return {
    draw(context: CanvasRenderingContext2D, time: number, dark: boolean) {
      context.drawImage(image, 0, 0, 1536, 1024);
      for (const cloud of clouds) {
        context.drawImage(
          cloud.layer,
          cloud.x - cloud.rx + Math.sin((time * tau) / cloud.period) * cloud.amplitude,
          cloud.y - cloud.ry,
          cloud.rx * 2,
          cloud.ry * 2,
        );
      }
      context.save();
      context.globalCompositeOperation = 'screen';
      const dawn = 0.5 + 0.5 * Math.sin((time * tau) / 35.2);
      glow(
        context,
        115,
        350,
        430,
        dark ? '148,163,225' : '255,191,112',
        (dark ? 0.025 : 0.045) + dawn * 0.025,
      );
      for (const [x, y, radius, phase] of [
        [818, 44, 112, 0],
        [876, 168, 84, 1.2],
        [907, 244, 64, 2.1],
        [1318, 201, 123, 0.8],
      ]) {
        const pulse =
          0.94 + 0.035 * Math.sin(time * 1.3 + phase) + 0.025 * Math.sin(time * 2.1 + phase);
        glow(context, x, y, radius, '255,206,137', (dark ? 0.1 : 0.055) * pulse);
      }
      for (const [i, [x, y]] of [
        [33, 367],
        [221, 374],
        [399, 353],
        [517, 339],
        [641, 359],
        [778, 405],
      ].entries()) {
        glow(
          context,
          x,
          y,
          5,
          '255,220,156',
          (dark ? 0.25 : 0.12) * (0.6 + 0.4 * Math.sin(time * 0.3 + i * 1.7)),
        );
      }
      // Existing rail and paving reflections respond to the same slow dawn phase.
      context.beginPath();
      context.moveTo(0, 746);
      context.lineTo(861, 451);
      context.lineTo(872, 452);
      context.lineTo(0, 760);
      context.closePath();
      context.fillStyle = `rgba(255,207,148,${(dark ? 0.018 : 0.026) + dawn * 0.018})`;
      context.fill();
      context.restore();
    },
    dispose() {
      clouds.forEach(({ layer }) => {
        layer.width = 0;
        layer.height = 0;
      });
    },
  };
}

export function elopementDust(
  context: CanvasRenderingContext2D,
  time: number,
  dark: boolean,
  foreground: boolean,
) {
  const points = foreground
    ? [
        [86, 711, 4],
        [1326, 700, 5],
        [628, 787, 3.6],
        [1465, 548, 4.2],
      ]
    : [
        [67, 424, 1.1],
        [247, 518, 1.5],
        [557, 396, 1],
        [713, 586, 1.4],
        [1224, 378, 1.2],
        [1392, 615, 1.6],
        [409, 714, 1.9],
        [822, 735, 2.2],
      ];
  context.save();
  context.globalCompositeOperation = 'screen';
  points.forEach(([x, y, radius], index) => {
    const phase = index * 2.3;
    const drift = foreground ? 10 : 5;
    glow(
      context,
      x + drift * Math.sin(time * 0.19 + phase),
      y + drift * 1.2 * Math.cos(time * 0.16 + phase),
      radius * 2.7,
      dark ? '191,211,244' : '255,223,174',
      (foreground ? 0.13 : 0.19) * (0.75 + 0.25 * Math.sin(time * 0.31 + phase)),
    );
  });
  context.restore();
}
