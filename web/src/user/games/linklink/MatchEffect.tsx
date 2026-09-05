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
      const tiles = board.querySelectorAll<HTMLButtonElement>('.linklink-tile');
      const origin = tiles[0]?.getBoundingClientRect();
      const right = tiles[1]?.getBoundingClientRect();
      const below = tiles[animation.before.board.cols]?.getBoundingClientRect();
      if (!origin || !right || !below) return;
      const bounds = board.getBoundingClientRect();
      svg.setAttribute('viewBox', `0 0 ${bounds.width} ${bounds.height}`);
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
    const observer = new ResizeObserver(position);
    observer.observe(board);
    return () => observer.disconnect();
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
              <path
                key={spark}
                d="M0-18v-7"
                transform={`rotate(${spark * 45})`}
                strokeLinecap="round"
              />
            ))}
          </g>
        </g>
      ))}
    </svg>
  );
}
