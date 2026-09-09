import { useEffect, useRef } from 'react';
import type { LinkLinkCoordinate, LinkLinkState } from './types';

export interface MatchAnimation {
  readonly key: string;
  readonly before: LinkLinkState;
  readonly pair: readonly [LinkLinkCoordinate, LinkLinkCoordinate];
  readonly path: readonly LinkLinkCoordinate[] | null;
}

export function MatchEffect({ animation }: { readonly animation: MatchAnimation }) {
  const ref = useRef<SVGSVGElement>(null);
  useEffect(() => {
    const svg = ref.current;
    const board = svg?.parentElement;
    if (!svg || !board) return;
    const position = () => {
      const tileAt = (row: number, col: number) =>
        board.querySelector<HTMLButtonElement>(
          `[aria-rowindex="${row + 1}"][aria-colindex="${col + 1}"]`,
        );
      const origin = tileAt(0, 0)?.getBoundingClientRect();
      const right = tileAt(0, 1)?.getBoundingClientRect();
      const below = tileAt(1, 0)?.getBoundingClientRect();
      if (!origin || !right || !below) return;
      const bounds = board.getBoundingClientRect();
      svg.setAttribute('viewBox', `0 0 ${bounds.width} ${bounds.height}`);
      const sparkStart = -(origin.width * 18) / 68;
      const sparkLength = -(origin.width * 7) / 68;
      svg.querySelectorAll<SVGPathElement>('.linklink-match-sparks path').forEach((spark) => {
        spark.setAttribute('d', `M0 ${sparkStart}v${sparkLength}`);
      });
      const point = (value: LinkLinkCoordinate) => [
        origin.left - bounds.left + origin.width / 2 + value.col * (right.left - origin.left),
        origin.top - bounds.top + origin.height / 2 + value.row * (below.top - origin.top),
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
    <svg ref={ref} className="linklink-match-effect" aria-hidden="true" focusable="false">
      <polyline
        className="linklink-match-beam"
        fill="none"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
      {animation.pair.map((_, index) => (
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
