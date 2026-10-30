export type MomentFrame = {
  canvas: [number, number];
  originalRegion: { x: number; y: number; width: number; height: number };
};
export type MomentScene = MomentFrame & {
  path: string;
  highResolutionWidth?: number;
};
export type MomentRegistration = {
  expansion?: MomentExpansion;
  scene?: MomentScene;
};
export type MomentExpansion = MomentFrame & {
  mode: 'native-camera' | 'expanded-layer';
  hero?: string;
  closed?: string;
  half?: string;
  highResolutionWidth?: number;
};

/** Keep the old viewport scale and reveal only the newly registered margins. */
export function expandMomentView(view: readonly number[], frame?: MomentFrame): number[] {
  if (!frame) return [...view];
  const { x, y, width, height } = frame.originalRegion;
  return [
    view[0]! - (x / width) * view[2]!,
    view[1]! - (y / height) * view[3]!,
    view[2]! * (frame.canvas[0] / width),
    view[3]! * (frame.canvas[1] / height),
  ];
}

export function momentReferenceSize(width: number, height: number, frame?: MomentFrame) {
  return frame
    ? {
        width: (width * frame.originalRegion.width) / frame.canvas[0],
        height: (height * frame.originalRegion.height) / frame.canvas[1],
      }
    : { width, height };
}

export function momentFrameStyle(frame: MomentFrame) {
  const { x, y, width, height } = frame.originalRegion;
  return {
    left: (-100 * x) / width + '%',
    top: (-100 * y) / height + '%',
    width: (100 * frame.canvas[0]) / width + '%',
    height: (100 * frame.canvas[1]) / height + '%',
  };
}

/** Upper bound for the original frame's narrow-screen and viewport-height layouts. */
export function momentImageSizes(frame: MomentFrame) {
  const ratio = frame.canvas[0] / frame.originalRegion.width;
  return '(width < 40rem) ' + 100 * ratio + 'vw, ' + (200 / 3) * ratio + 'vh';
}

/** Horizontal room outside the original frame, in units of its unchanged width. */
export function momentHorizontalMargins(frame: MomentFrame) {
  const { x, width } = frame.originalRegion;
  const left = x / width;
  const right = (frame.canvas[0] - x - width) / width;
  return { left, right, total: left + right };
}
