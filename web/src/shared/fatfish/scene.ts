import { type EngineState, type Level, type Point, type Polygon } from './engine/types';
import { fishFootprint } from './engine/trig_helpers';
import { toolIcon, type FatFishArt } from './art';
import { toolPosition } from './workspace';

const unit = 64;
const width = 480;
const height = 560;

export function drawableDraft(level: Level): Level {
  const point = (value: Point) => Number.isFinite(value.x) && Number.isFinite(value.y);
  const shape = (value: { polygon: Polygon }) => value.polygon.outer.length >= 3 &&
    value.polygon.outer.every(point) && value.polygon.holes.every((ring) => ring.length >= 3 && ring.every(point));
  return { ...level,
    fish: level.fish.filter((fish) => point(fish) && Number.isFinite(fish.heading)),
    tools: level.tools.filter((tool) => point(tool) && shape(tool)).sort((a, b) => a.id - b.id),
    solids: level.solids.filter(shape), hazards: level.hazards.filter(shape), bowls: level.bowls.filter(shape),
    gates: level.gates.filter(shape), switches: level.switches.filter(shape), directions: level.directions.filter(shape),
  };
}

function polygonPath(ctx: CanvasRenderingContext2D, polygon: Polygon, offsetX = 0, offsetY = 0): void {
  const ring = (points: Point[]) => {
    points.forEach((point, index) => {
      const x = (point.x + offsetX) / unit, y = (point.y + offsetY) / unit;
      if (index === 0) ctx.moveTo(x, y);
      else ctx.lineTo(x, y);
    });
    ctx.closePath();
  };
  ctx.beginPath();
  ring(polygon.outer);
  polygon.holes.forEach(ring);
}
function shapeBounds(polygon: Polygon, offsetX = 0, offsetY = 0) {
  const xs = polygon.outer.map((point) => (point.x + offsetX) / unit);
  const ys = polygon.outer.map((point) => (point.y + offsetY) / unit);
  return { x: Math.min(...xs), y: Math.min(...ys), width: Math.max(...xs) - Math.min(...xs),
    height: Math.max(...ys) - Math.min(...ys) };
}
function drawShape(ctx: CanvasRenderingContext2D, polygon: Polygon, fill: string,
  stroke: string, icon?: HTMLImageElement, offsetX = 0, offsetY = 0,
  minimumWidth = 52, minimumHeight = 52, rotation = 0): void {
  const box = shapeBounds(polygon, offsetX, offsetY);
  ctx.save();
  polygonPath(ctx, polygon, offsetX, offsetY);
  ctx.fillStyle = fill; ctx.fill('evenodd');
  if (icon && icon.naturalWidth > 0) {
    ctx.clip('evenodd');
    const scale = Math.max(box.width / icon.naturalWidth, box.height / icon.naturalHeight,
      Math.min(minimumWidth, minimumHeight) / Math.max(icon.naturalWidth, icon.naturalHeight));
    ctx.translate(box.x + box.width / 2, box.y + box.height / 2); ctx.rotate(rotation);
    ctx.drawImage(icon, -icon.naturalWidth * scale / 2, -icon.naturalHeight * scale / 2,
      icon.naturalWidth * scale, icon.naturalHeight * scale);
  }
  ctx.restore();
  polygonPath(ctx, polygon, offsetX, offsetY); ctx.strokeStyle = stroke; ctx.lineWidth = 1.3; ctx.stroke();
}

type OrientedBox = { x: number; y: number; width: number; height: number; angle: number };
const boxCache = new WeakMap<Polygon, Map<boolean, OrientedBox>>();
function orientedBox(polygon: Polygon, vertical: boolean): OrientedBox {
  const cached = boxCache.get(polygon)?.get(vertical);
  if (cached) return cached;
  let best = { angle: 0, x: 0, y: 0, width: Infinity, height: Infinity };
  for (let index = 0; index < polygon.outer.length; index++) {
    const a = polygon.outer[index], b = polygon.outer[(index + 1) % polygon.outer.length];
    const angle = Math.atan2(b.y - a.y, b.x - a.x);
    const c = Math.cos(angle), s = Math.sin(angle);
    const xs = polygon.outer.map((p) => (p.x * c + p.y * s) / unit);
    const ys = polygon.outer.map((p) => (-p.x * s + p.y * c) / unit);
    const x = Math.min(...xs), y = Math.min(...ys);
    const w = Math.max(...xs) - x, h = Math.max(...ys) - y;
    if (w * h < best.width * best.height - .001) best = { angle, x, y, width: w, height: h };
  }
  const { x, y } = best;
  let { angle, width: w, height: h } = best;
  const cx = (x + w / 2) * Math.cos(angle) - (y + h / 2) * Math.sin(angle);
  const cy = (x + w / 2) * Math.sin(angle) + (y + h / 2) * Math.cos(angle);
  if (vertical ? w > h : h > w) { angle += Math.PI / 2; [w, h] = [h, w]; }
  while (angle > Math.PI / 2) angle -= Math.PI;
  while (angle <= -Math.PI / 2) angle += Math.PI;
  const value = { x: cx, y: cy, width: w, height: h, angle };
  const entries = boxCache.get(polygon) ?? new Map<boolean, OrientedBox>();
  entries.set(vertical, value); boxCache.set(polygon, entries);
  return value;
}

function drawTool(ctx: CanvasRenderingContext2D, polygon: Polygon, position: Point,
  resourceKey: string, art: FatFishArt | null | undefined, selected: boolean) {
  const icon = art?.icons.get(toolIcon(resourceKey) ?? '');
  const axis = shapeBounds(polygon);
  const box = resourceKey === 'cup' ? { x: axis.x + axis.width / 2, y: axis.y + axis.height / 2,
    width: axis.width, height: axis.height, angle: 0 } : orientedBox(polygon, resourceKey === 'memory');
  const meta: Record<string, [number, number, number, number, number, number]> = {
    barrier: [240, 140, 120, 82, 200, 82],
    memory: [160, 320, 77, 154, 106, 282],
    fan: [320, 140, 159, 64, 290, 101],
    light: [320, 140, 160, 64, 288, 101],
    cup: [160, 160, 80, 92, 106, 86],
  };
  ctx.save();
  polygonPath(ctx, polygon, position.x, position.y);
  ctx.fillStyle = '#8db7ba60'; ctx.fill('evenodd');
  if (icon && meta[resourceKey]) {
    // Holes must stay visibly open even when the artwork spans their bounds.
    if (polygon.holes.length) ctx.clip('evenodd');
    const [iw, ih, ax, ay, pw, ph] = meta[resourceKey];
    const sx = box.width / pw, sy = box.height / ph;
    ctx.translate(position.x / unit + box.x, position.y / unit + box.y);
    ctx.rotate(box.angle);
    ctx.shadowColor = selected ? '#a2784145' : '#34556428';
    ctx.shadowBlur = selected ? 8 : 3; ctx.shadowOffsetY = selected ? 5 : 2;
    ctx.drawImage(icon, -ax * sx, -ay * sy, iw * sx, ih * sy);
  }
  ctx.restore();
  polygonPath(ctx, polygon, position.x, position.y);
  ctx.strokeStyle = selected ? '#b98935' : '#527c805c';
  ctx.lineWidth = selected ? 2.3 : .8; ctx.stroke();
}
export function drawFatFishScene(ctx: CanvasRenderingContext2D, level: Level, state?: EngineState | null,
  art?: FatFishArt | null, selectedTool?: number | null): void {
  ctx.clearRect(-128, -128, 736, 816);
  ctx.fillStyle = '#dae7df';
  ctx.fillRect(-128, -128, 736, 816);
  const floor = art?.icons.get('floor-tile');
  if (floor) {
    ctx.save(); ctx.globalAlpha = .4;
    for (let y = -128; y < 688; y += 160) for (let x = -128; x < 608; x += 160) {
      ctx.drawImage(floor, x, y, 160, 160);
    }
    ctx.restore();
  }
  ctx.save();
  const field = art?.icons.get('floor-portrait');
  if (field) {
    // The artwork's inner floor is 386 by 506 at (22, 20). Align that
    // floor with the simulated field while leaving its rim on the bench.
    ctx.drawImage(field, -22 * width / 386, -20 * height / 506, 430 * width / 386, 553 * height / 506);
  } else {
    ctx.shadowColor = '#45697430'; ctx.shadowBlur = 12; ctx.shadowOffsetY = 5;
    ctx.beginPath(); ctx.roundRect(0, 0, width, height, 18);
    ctx.fillStyle = '#f6f4e8'; ctx.fill();
    ctx.shadowColor = 'transparent'; ctx.lineWidth = 2; ctx.strokeStyle = '#71979a'; ctx.stroke();
  }
  ctx.restore();
  ctx.save(); ctx.fillStyle = '#66897f'; ctx.font = '600 11px system-ui'; ctx.textAlign = 'center';
  ctx.fillText('TOOL WORKSPACE', width / 2, -91); ctx.restore();
  level.directions.forEach((item) => drawShape(ctx, item.polygon, '#62b9c535', '#2d7486',
    art?.icons.get('rice-arrow'), 0, 0, 52, 52, item.heading * Math.PI * 2 / 4096));
  level.solids.forEach((item) => {
    const box = shapeBounds(item.polygon);
    const icon = box.width > box.height * 1.4 ? 'server-wall' : box.height > box.width * 1.4 ? 'server-rack' : 'router-wedge';
    drawShape(ctx, item.polygon, '#5e829955', '#345c75', art?.icons.get(icon), 0, 0, 42, 42);
  });
  level.hazards.forEach((item) => drawShape(ctx, item.polygon, '#e8867960', '#ae4e52',
    art?.icons.get('offline-pool'), 0, 0, 58, 48));
  level.bowls.forEach((item) => {
    const count = state?.bowls.find((entry) => entry.id === item.id)?.count ?? 0;
    const full = count >= item.capacity;
    const box = shapeBounds(item.polygon);
    const cx = box.x + box.width / 2, cy = box.y + box.height / 2;
    const icon = art?.icons.get(full ? 'rice-goal-full' : 'rice-goal');
    ctx.save();
    polygonPath(ctx, item.polygon);
    ctx.fillStyle = '#8ad5ab25'; ctx.fill('evenodd');
    if (icon) {
      const sx = box.width / 86 * .72, sy = box.height / 56 * .72;
      ctx.drawImage(icon, cx - 100 * sx, cy - 145 * sy, 200 * sx, 180 * sy);
    } else {
      ctx.fillStyle = '#f0cf96'; ctx.fill('evenodd');
    }
    polygonPath(ctx, item.polygon);
    ctx.strokeStyle = full ? '#8b9d9a' : '#75a991';
    ctx.lineWidth = 1; ctx.setLineDash([4, 4]); ctx.stroke(); ctx.setLineDash([]);
    ctx.fillStyle = '#3e6263'; ctx.font = '600 11px system-ui'; ctx.textAlign = 'center';
    ctx.fillText(`${count}/${item.capacity}`, cx, cy + box.height / 2 + 15);
    ctx.restore();
  });
  level.switches.forEach((item) => {
    const active = state?.switches.find((entry) => entry.id === item.id)?.active;
    drawShape(ctx, item.polygon, active ? '#8bd19f66' : '#f1db9766', '#64866d',
      art?.icons.get(active ? 'switch-on' : 'switch-off'));
  });
  level.gates.forEach((item) => {
    const open = state?.gates.find((entry) => entry.id === item.id)?.open ?? item.initially_open;
    drawShape(ctx, item.polygon, open ? '#8bd19f55' : '#59697855', open ? '#519b70' : '#354c5b',
      art?.icons.get(open ? 'gate-open' : 'gate-closed'), 0, 0, 60, 48);
  });
  const atlas = art?.atlas;
  const elapsed = (state?.tick ?? 0) * 1000 / 60;
  for (const fish of state?.fish ?? level.fish) {
    const status = 'status' in fish ? fish.status : 'walking';
    const x = fish.x / unit, y = fish.y / unit;
    const heading = ((fish.heading + 512) % 4096) >> 10;
    const animationName = status === 'fed' ? 'eat' : status === 'lost' || state?.terminal ? 'idle' :
      ['walk-right', 'walk-down', 'walk-left', 'walk-up'][heading];
    const animation = atlas?.animations[animationName];
    let frameID: string | undefined;
    if (animation?.frames.length && animation.frames.length === animation.durationsMs.length) {
      const duration = animation.durationsMs.reduce((sum, value) => sum + value, 0);
      let cursor = duration > 0 ? elapsed % duration : 0;
      for (let index = 0; index < animation.frames.length; index++) {
        cursor -= animation.durationsMs[index];
        if (cursor < 0) { frameID = animation.frames[index]; break; }
      }
    }
    const frame = frameID ? atlas?.frames[frameID] : undefined;
    const sprite = frame ? art?.sources[frame.source] : undefined;
    ctx.save(); ctx.translate(x, y);
    ctx.fillStyle = '#254d5660';
    ctx.beginPath(); ctx.ellipse(0, 1, 12, 5, 0, 0, Math.PI * 2); ctx.fill();
    if (status === 'lost') ctx.globalAlpha = .5;
    if (frame && sprite && frame.referenceHeight > 0) {
      const scale = (atlas?.defaultDisplayHeight ?? 48) / frame.referenceHeight;
      const [sx, sy, sw, sh] = frame.rect;
      ctx.drawImage(sprite, sx, sy, sw, sh, -frame.pivot[0] * scale,
        -frame.pivot[1] * scale, sw * scale, sh * scale);
    } else {
      ctx.rotate(fish.heading * Math.PI * 2 / 4096);
      ctx.fillStyle = '#426f9b';
      ctx.beginPath(); ctx.ellipse(0, 0, 8, 5.5, 0, 0, Math.PI * 2); ctx.fill();
      ctx.beginPath(); ctx.moveTo(-6, 0); ctx.lineTo(-13, -5); ctx.lineTo(-13, 5); ctx.closePath(); ctx.fill();
      ctx.fillStyle = '#f8faf6'; ctx.beginPath(); ctx.arc(3, -2, 1.2, 0, Math.PI * 2); ctx.fill();
    }
    ctx.restore();
    polygonPath(ctx, { outer: fishFootprint(fish.x, fish.y), holes: [] });
    ctx.lineWidth = 1; ctx.strokeStyle = status === 'lost' ? '#aa4444' : status === 'fed' ? '#3d925d' : '#184f71b0'; ctx.stroke();
    if (status === 'lost') {
      const bubble = art?.icons.get('offline-bubble');
      if (bubble) ctx.drawImage(bubble, x - 10, y - 18, 20, 20);
    } else if (status === 'walking' && 'turn_dir' in fish && fish.turn_dir !== 0) {
      ctx.beginPath(); ctx.arc(x, y, 11, -.6, .6);
      ctx.lineWidth = 1.3; ctx.strokeStyle = '#277b91'; ctx.stroke();
    }
  }
  // Pieces share the field coordinate system and remain at their actual
  // bench positions. Paint the selected piece last, matching hit-test order.
  [...level.tools.entries()].sort((a, b) => Number(a[1].id === selectedTool) - Number(b[1].id === selectedTool))
    .forEach(([index, item]) => {
      const current = state?.tools.find((entry) => entry.id === item.id);
      const position = toolPosition(item, index, current);
      drawTool(ctx, item.polygon, position, item.resource_key, art, item.id === selectedTool);
    });

}
