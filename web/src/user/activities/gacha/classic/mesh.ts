import type { Layer, Point, Rect } from './types';

const unit = (amount: number) => Math.max(0, Math.min(1, amount));
const ease = (amount: number) => {
  const value = unit(amount);
  return value * value * (3 - 2 * value);
};

function edgeWeight(x: number, y: number, [left, top, width, height]: Rect) {
  const distanceX = Math.max(left - x, x - left - width, 0);
  const distanceY = Math.max(top - y, y - top - height, 0);
  return ease((Math.hypot(distanceX, distanceY) - 4) / 23);
}

function seamWeight(x: number, y: number, [x0, y0, x1, y1]: Rect) {
  const dx = x1 - x0;
  const dy = y1 - y0;
  const fraction = unit(((x - x0) * dx + (y - y0) * dy) / Math.max(1, dx * dx + dy * dy));
  return ease((Math.hypot(x - x0 - dx * fraction, y - y0 - dy * fraction) - 5) / 27);
}

function pathPosition(x: number, y: number, path: readonly Point[]) {
  let best = Infinity;
  let along = 0;
  let length = 0;
  for (let i = 1; i < path.length; i++) {
    const a = path[i - 1]!;
    const b = path[i]!;
    const dx = b[0] - a[0];
    const dy = b[1] - a[1];
    const segment = Math.hypot(dx, dy);
    const u = unit(((x - a[0]) * dx + (y - a[1]) * dy) / (segment * segment));
    const distance = Math.hypot(x - a[0] - u * dx, y - a[1] - u * dy);
    if (distance < best) {
      best = distance;
      along = length + u * segment;
    }
    length += segment;
  }
  return along / length;
}
/** The raster belongs to one material. Each mesh weight is authored in the original frame. */
export function createMesh(layer: Layer) {
  const [left, top, width, height] = layer.rect;
  const motion = layer.motion;
  const step = layer.swim || motion?.kind === 'mesh' || motion?.kind === 'breath' ? 14 : Infinity;
  const cols = Math.max(1, Math.ceil(width / step));
  const rows = Math.max(1, Math.ceil(height / step));
  const vertices = new Float32Array((cols + 1) * (rows + 1) * 4);
  const indices = new Uint16Array(cols * rows * 6);
  let vertex = 0;
  let triangle = 0;

  for (let row = 0; row <= rows; row++) {
    for (let col = 0; col <= cols; col++) {
      const x = left + (col * width) / cols;
      const y = top + (row * height) / rows;
      vertices[vertex++] = x;
      vertices[vertex++] = y;
      let weight = 0;
      let spread = 0;
      if (motion?.kind === 'mesh') {
        const dx = motion.tip[0] - motion.root[0];
        const dy = motion.tip[1] - motion.root[1];
        const progress =
          ((x - motion.root[0]) * dx + (y - motion.root[1]) * dy) / (dx * dx + dy * dy);
        weight = ease(progress);
        spread = unit(progress);
        if (motion.ribbon) {
          spread = pathPosition(x, y, motion.ribbon.path);
          const [start, end] = motion.ribbon.range;
          weight = ease(Math.min(spread - start, end - spread) / 0.06);
        }
        for (const seam of motion.seams ?? [])
          weight = Math.min(weight, ease(progress) * seamWeight(x, y, seam));
      } else if (motion?.kind === 'breath') {
        const [cx, cy, w, h] = motion.chest;
        const horizontal = ease(1 - Math.abs(x - cx) / (w / 2));
        const distance = (y - cy) / (h / 2);
        weight = horizontal * ease(1 - Math.abs(distance));
        spread = (x - cx) / Math.max(w / 2, 1);
      }
      if (motion?.kind === 'mesh' || motion?.kind === 'breath') {
        for (const contact of motion.protect ?? []) weight *= edgeWeight(x, y, contact);
      }
      vertices[vertex++] = weight;
      vertices[vertex++] = spread;
      if (row === rows || col === cols) continue;
      const first = row * (cols + 1) + col;
      const below = first + cols + 1;
      indices.set([first, first + 1, below, below, first + 1, below + 1], triangle);
      triangle += 6;
    }
  }
  return { vertices, indices };
}
