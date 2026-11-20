import { useLayoutEffect, useRef, useState, type ReactNode } from 'react';
import { SizeContext, windowSize } from './stage-size';
import { useBackgroundPlayback } from './playback-context';

export function ArtworkStage({ group, children }: { group: boolean; children: ReactNode }) {
  const node = useRef<HTMLDivElement>(null);
  const [size, setSize] = useState(windowSize);
  const { playback } = useBackgroundPlayback();
  useLayoutEffect(() => {
    const stage = node.current!;
    const header = stage.parentElement?.querySelector(':scope > .nb-user-header');
    const measure = () => {
      if (header)
        stage.style.setProperty(
          '--nb-art-header-height',
          header.getBoundingClientRect().height + 'px',
        );
      const rect = stage.getBoundingClientRect();
      const w = rect.width || innerWidth,
        h = rect.height || innerHeight;
      setSize((old) => (old[0] === w && old[1] === h ? old : [w, h]));
    };
    measure();
    const observer =
      typeof ResizeObserver === 'undefined' ? undefined : new ResizeObserver(measure);
    observer?.observe(stage);
    if (header) observer?.observe(header);
    window.addEventListener('resize', measure);
    return () => {
      observer?.disconnect();
      window.removeEventListener('resize', measure);
    };
  }, []);
  useLayoutEffect(() => {
    const observer =
      typeof IntersectionObserver === 'undefined'
        ? undefined
        : new IntersectionObserver(([entry]) => playback.setOffscreen(!entry.isIntersecting));
    observer?.observe(node.current!);
    return () => {
      observer?.disconnect();
      playback.setOffscreen(false);
    };
  }, [playback]);
  return (
    <div
      className="nb-art-stage"
      data-composition={group ? 'group' : 'portrait'}
      ref={node}
      aria-hidden="true"
    >
      <SizeContext value={size}>{children}</SizeContext>
    </div>
  );
}
