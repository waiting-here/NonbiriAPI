import { useEffect, useRef } from 'react';
import type { LinkLinkCoordinate, LinkLinkState } from './types';

export interface MatchAnimation {
  readonly key: string;
  readonly before: LinkLinkState;
  readonly pair: readonly [LinkLinkCoordinate, LinkLinkCoordinate];
  readonly path: readonly LinkLinkCoordinate[] | null;
}

export function MatchEffect({
  animation,
  hint = false,
}: {
  readonly animation: MatchAnimation;
  readonly hint?: boolean;
}) {
  const ref = useRef<SVGSVGElement>(null);
  useEffect(() => {
    const svg = ref.current;
    const board = svg?.parentElement;
    if (!svg || !board) return;
    const position = () => {
      const tile = board.querySelector<HTMLButtonElement>('[aria-rowindex="1"][aria-colindex="1"]');
      if (!tile) return;
      // Vanishing tiles scale and rotate; anchor effects to the unchanged grid layout.
      const tileStyle = getComputedStyle(tile);
      const boardStyle = getComputedStyle(board);
      const width = parseFloat(tileStyle.width);
      const height = parseFloat(tileStyle.height);
      const originX = parseFloat(boardStyle.paddingLeft) + width / 2;
      const originY = parseFloat(boardStyle.paddingTop) + height / 2;
      const pitchX = width + parseFloat(boardStyle.columnGap);
      const pitchY = height + parseFloat(boardStyle.rowGap);
      const bounds = board.getBoundingClientRect();
      svg.setAttribute('viewBox', `0 0 ${bounds.width} ${bounds.height}`);
      const sparkStart = -(width * 18) / 68;
      const sparkLength = -(width * 7) / 68;
      svg.querySelectorAll<SVGPathElement>('.linklink-match-sparks path').forEach((spark) => {
        spark.setAttribute('d', `M0 ${sparkStart}v${sparkLength}`);
      });
      const point = (value: LinkLinkCoordinate) => [
        originX + value.col * pitchX,
        originY + value.row * pitchY,
      ];
      svg
        .querySelector('polyline')
        ?.setAttribute(
          'points',
          animation.path?.map((value) => point(value).join(',')).join(' ') ?? '',
        );
      svg.querySelectorAll('.linklink-match-burst').forEach((burst, index) => {
        const [x, y] = point(animation.pair[index]);
        burst.setAttribute('transform', `translate(${x} ${y})`);
      });
    };
    position();
    let frame = 0;
    const schedule = () => {
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(() => {
        position();
        frame = requestAnimationFrame(position);
      });
    };
    const observer = new ResizeObserver(schedule);
    observer.observe(board);
    if (board.parentElement) observer.observe(board.parentElement);
    window.addEventListener('resize', schedule);
    return () => {
      cancelAnimationFrame(frame);
      observer.disconnect();
      window.removeEventListener('resize', schedule);
    };
  }, [animation]);
  return (
    <svg
      ref={ref}
      className={`linklink-match-effect${hint ? ' is-hint' : ''}`}
      aria-hidden="true"
      focusable="false"
    >
      <polyline
        className="linklink-match-beam"
        fill="none"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
      {!hint &&
        animation.pair.map((_, index) => (
          <g className="linklink-match-burst" key={index}>
            <circle className="linklink-match-ring" r="12" fill="none" />
            <g className="linklink-match-sparks">
              {Array.from({ length: 8 }, (_, spark) => (
                <path key={spark} transform={`rotate(${spark * 45})`} strokeLinecap="round" />
              ))}
            </g>
          </g>
        ))}
    </svg>
  );
}
