export const armOutline = [
  [589, 301],
  [638, 301],
  [639, 315],
  [644, 327],
  [648, 344],
  [650, 361],
  [651, 375],
  [656, 381],
  [666, 397],
  [674, 414],
  [681, 427],
  [690, 435],
  [705, 440],
  [719, 447],
  [732, 460],
  [747, 474],
  [754, 484],
  [760, 499],
  [764, 509],
  [798, 529],
  [783, 605],
  [625, 543],
  [656, 478],
  [650, 464],
  [635, 458],
  [618, 449],
  [605, 441],
  [600, 429],
  [596, 417],
  [594, 398],
  [592, 381],
  [595, 369],
  [599, 357],
  [596, 342],
  [592, 325],
];
const smooth = (a, b, x) => {
  const q = Math.max(0, Math.min(1, (x - a) / (b - a)));
  return q * q * (3 - 2 * q);
};
export function makeArmLayers(image, repairImage) {
  const w = image.naturalWidth,
    h = image.naturalHeight;
  const original = document.createElement('canvas');
  original.width = w;
  original.height = h;
  const oc = original.getContext('2d', { willReadFrequently: true });
  oc.drawImage(image, 0, 0);
  const mask = document.createElement('canvas');
  mask.width = w;
  mask.height = h;
  const mc = mask.getContext('2d', { willReadFrequently: true });
  mc.fillStyle = 'white';
  mc.beginPath();
  armOutline.forEach(([x, y], i) => (i ? mc.lineTo(x, y) : mc.moveTo(x, y)));
  mc.closePath();
  mc.fill();
  const md = mc.getImageData(0, 0, w, h);
  for (let y = 298; y < 609; y++)
    for (let x = 580; x < 817; x++) {
      const n = (y * w + x) * 4;
      md.data[n + 3] = Math.round(md.data[n + 3] * smooth(309, 333, y));
    }
  mc.putImageData(md, 0, 0);
  const at = (x, y) =>
    md.data[
      (Math.max(0, Math.min(h - 1, Math.round(y))) * w +
        Math.max(0, Math.min(w - 1, Math.round(x)))) *
        4 +
        3
    ] / 255;
  const backing = document.createElement('canvas');
  backing.width = w;
  backing.height = h;
  const bc = backing.getContext('2d', { willReadFrequently: true });
  bc.drawImage(image, 0, 0);
  const hasRepair = repairImage?.naturalWidth === w && repairImage?.naturalHeight === h;
  if (hasRepair) {
    const repaired = document.createElement('canvas');
    repaired.width = w;
    repaired.height = h;
    const rc = repaired.getContext('2d', { willReadFrequently: true });
    rc.drawImage(repairImage, 0, 0);
    const patch = rc.getImageData(0, 0, w, h),
      base = bc.getImageData(0, 0, w, h);
    // Dilate just the backing coverage; the moving foreground keeps its exact edge.
    for (let y = 306; y < 610; y++)
      for (let x = 578; x < 818; x++) {
        let cover = at(x, y);
        for (const [dx, dy] of [
          [1, 0],
          [-1, 0],
          [0, 1],
          [0, -1],
          [2, 0],
          [-2, 0],
          [0, 2],
          [0, -2],
          [4, 0],
          [-4, 0],
          [0, 4],
          [0, -4],
          [2, 2],
          [-2, 2],
          [2, -2],
          [-2, -2],
        ])
          cover = Math.max(
            cover,
            at(x + dx, y + dy) * (Math.abs(dx) + Math.abs(dy) <= 2 ? 1 : 0.18),
          );
        if (!cover) continue;
        const n = (y * w + x) * 4;
        for (let c = 0; c < 4; c++)
          base.data[n + c] = Math.round(base.data[n + c] * (1 - cover) + patch.data[n + c] * cover);
      }
    bc.putImageData(base, 0, 0);
  }
  return { backing, mask, at, original, hasRepair };
}
/* global document */
