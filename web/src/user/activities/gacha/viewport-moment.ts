import type { MomentScene } from './moment-frame';

const clamp = (value: number, low: number, high: number) => Math.max(low, Math.min(high, value));

/** Pixels in the original 1024×1536 character frame; expanded layers remain its children. */
export function viewportMomentLayout(scene: MomentScene, width: number, height: number) {
  const imageScale = Math.max(width / scene.canvas[0], height / scene.canvas[1]);
  const imageWidth = scene.canvas[0] * imageScale;
  const imageHeight = scene.canvas[1] * imageScale;
  const background = {
    left: (width - imageWidth) * 0.72,
    top: height - imageHeight,
    width: imageWidth,
    height: imageHeight,
  };
  const placement = scene.viewport?.placement;
  if (!placement) {
    const frame = scene.originalRegion;
    background.top = clamp(72 - frame.y * imageScale, height - imageHeight, 0);
    const target = width < 640 ? width / 2 : width * 0.83;
    background.left = clamp(
      target - (frame.x + frame.width / 2) * imageScale,
      width - imageWidth,
      0,
    );
    return {
      background,
      character: {
        left: background.left + frame.x * imageScale,
        top: background.top + frame.y * imageScale,
        width: frame.width * imageScale,
        height: frame.height * imageScale,
      },
      reserve: frame.width * imageScale,
    };
  }
  const [left, top, right, bottom] = placement.bounds;
  const [ax, ay] = placement.anchor;
  const ground = height * (placement.floor ?? 0.95);
  const availableWidth =
    width < 640 ? width * 0.94 : width < 1280 ? width * 0.57 : Math.max(320, width - 960);
  const scale = Math.min(
    (height - 84) / (bottom - top),
    availableWidth / (right - left),
    (ground - 72) / (ay - top),
    bottom > ay ? (height - ground) / (bottom - ay) : Infinity,
  );
  const margin = width < 640 ? width * 0.03 : 24;
  const target = clamp(
    width < 640 ? width / 2 : width - margin - (right - ax) * scale,
    margin + (ax - left) * scale,
    width - margin - (right - ax) * scale,
  );
  return {
    background,
    character: {
      left: target - ax * scale,
      top: ground - ay * scale,
      width: 1024 * scale,
      height: 1536 * scale,
    },
    reserve: (right - left) * scale + 24,
  };
}
