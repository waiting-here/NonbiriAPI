import { describe, expect, it } from 'vitest';
import {
  expandMomentView,
  momentFrameStyle,
  momentReferenceSize,
  momentHorizontalMargins,
} from './moment-frame';
import type { MomentFrame } from './moment-frame';

describe('registered moment viewport', () => {
  it.each([0, 160])('preserves every original camera position with %s top padding', (top) => {
    const frame: MomentFrame = {
      canvas: [1536, 1536 + top],
      originalRegion: { x: 256, y: top, width: 1024, height: 1536 },
    };
    const view = [0.13, 0.025, 0.75, 0.75];
    const next = expandMomentView(view, frame);
    for (const [x, y] of [
      [0.2, 0.1],
      [0.5, 0.5],
      [0.8, 0.7],
    ]) {
      const oldX = ((x! - view[0]!) / view[2]!) * 1024;
      const oldY = ((y! - view[1]!) / view[3]!) * 1536;
      expect(((x! - next[0]!) / next[2]!) * 1536 - 256).toBeCloseTo(oldX, 9);
      expect(((y! - next[1]!) / next[3]!) * frame.canvas[1] - top).toBeCloseTo(oldY, 9);
    }
    for (const width of [390, 640]) {
      const outputHeight = (width * frame.canvas[1]) / frame.canvas[0];
      const reference = momentReferenceSize(width, outputHeight, frame);
      expect(reference.height / reference.width).toBeCloseTo(1.5);
      expect(reference.width / 1024).toBeCloseTo(width / 1536);
    }
    expect(momentFrameStyle(frame).width).toBe('150%');
    expect(momentFrameStyle(frame).left).toBe('-25%');
  });
  it('keeps unregistered cards and the normal viewport unchanged', () => {
    expect(expandMomentView([0.13, 0.025, 0.75, 0.75])).toEqual([0.13, 0.025, 0.75, 0.75]);
    expect(momentReferenceSize(400, 600)).toEqual({ width: 400, height: 600 });
  });
});

it.each([
  { viewport: 1440, originalWidth: 416 },
  { viewport: 1920, originalWidth: 674.6666666666666 },
])(
  'fits both expanded edges at $viewport px without shrinking the original frame',
  ({ viewport, originalWidth }) => {
    for (const frame of [
      { canvas: [1536, 1536], originalRegion: { x: 256, y: 0, width: 1024, height: 1536 } },
      { canvas: [1552, 1808], originalRegion: { x: 264, y: 272, width: 1024, height: 1536 } },
      { canvas: [1600, 1664], originalRegion: { x: 128, y: 128, width: 1024, height: 1536 } },
    ] satisfies MomentFrame[]) {
      const margins = momentHorizontalMargins(frame);
      const canonicalLeft = viewport - originalWidth * (1 + margins.right);
      const expandedLeft = canonicalLeft - originalWidth * margins.left;
      const expandedWidth = (originalWidth * frame.canvas[0]) / frame.originalRegion.width;
      expect(expandedLeft + expandedWidth).toBeCloseTo(viewport, 9);
      expect(expandedLeft).toBeCloseTo(viewport - originalWidth * (1 + margins.total), 9);
      const contentRight = viewport - originalWidth * (1 + margins.total) - 32;
      expect(expandedLeft - contentRight).toBeCloseTo(32, 9);
      const contentWidth = contentRight - 32;
      expect(contentWidth).toBeGreaterThan(720);
      const canonicalRight = canonicalLeft + originalWidth;
      expect(canonicalRight - canonicalLeft).toBeCloseTo(originalWidth, 9);
    }
  },
);
