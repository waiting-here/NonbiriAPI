import { useEffect, useRef, useState, type CSSProperties } from 'react';
import { tomorrowAssets, refinedTomorrowAssets, walkingTomorrowAssets } from './assets';
import { tomorrowLayout, type Box, type TomorrowVariant } from './layout';
import { renderTomorrow } from './renderer';
import './artwork.css';

type Props = {
  walking?: boolean;
  refined?: boolean;
  variant: TomorrowVariant;
  dark?: boolean;
  animated?: boolean;
  paused?: boolean;
  className?: string;
  highResolution?: boolean;
};
const boxStyle = (box: Box): CSSProperties => ({
  left: `${box[0] * 100}%`,
  top: `${box[1] * 100}%`,
  width: `${box[2] * 100}%`,
  height: `${box[3] * 100}%`,
});

export function TomorrowArtwork({
  walking = false,
  refined = false,
  variant,
  dark = false,
  animated = true,
  paused = false,
  className = '',
  highResolution = false,
}: Props) {
  const portraitStudy = !walking && refined && variant === 'card';
  const assets = walking
    ? walkingTomorrowAssets
    : portraitStudy
      ? refinedTomorrowAssets
      : tomorrowAssets;
  const sceneDark = portraitStudy ? false : dark;
  const root = useRef<HTMLDivElement>(null),
    canvas = useRef<HTMLCanvasElement>(null),
    pause = useRef(paused);
  const [size, setSize] = useState({
    width: variant === 'card' ? 2 : 16,
    height: variant === 'card' ? 3 : 9,
  });
  const [ready, setReady] = useState(false);
  const effectiveHigh =
    highResolution || Math.max(size.width, size.height) * (window.devicePixelRatio || 1) > 1920;
  const [reduced, setReduced] = useState(
    () => matchMedia('(prefers-reduced-motion: reduce)').matches,
  );
  useEffect(() => {
    pause.current = paused;
  }, [paused]);
  useEffect(() => {
    const query = matchMedia('(prefers-reduced-motion: reduce)');
    const change = () => setReduced(query.matches);
    query.addEventListener('change', change);
    return () => query.removeEventListener('change', change);
  }, []);
  useEffect(() => {
    const el = root.current;
    if (!el) return;
    const observer = new ResizeObserver(() => {
      const rect = el.getBoundingClientRect();
      if (rect.width && rect.height) setSize({ width: rect.width, height: rect.height });
    });
    observer.observe(el);
    return () => observer.disconnect();
  }, []);
  useEffect(() => {
    if (!canvas.current || !animated || reduced) return;
    const controller = new AbortController();
    let stop: (() => void) | undefined;
    renderTomorrow(canvas.current, {
      walking,
      refined: portraitStudy,
      variant,
      dark: sceneDark,
      highResolution: effectiveHigh,
      landscape: variant !== 'card',
      ambient: variant === 'appearance',
      paused: () => pause.current,
      signal: controller.signal,
    })
      .then((dispose) => {
        if (controller.signal.aborted) dispose();
        else {
          stop = dispose;
          setReady(true);
        }
      })
      .catch(() => {
        if (!controller.signal.aborted) setReady(false);
      });
    return () => {
      controller.abort();
      stop?.();
      setReady(false);
    };
  }, [variant, sceneDark, animated, reduced, effectiveHigh, portraitStudy, walking]);
  const layout = tomorrowLayout(size.width, size.height, variant, portraitStudy, walking);
  const background: Box = [
    -layout.view[0] / layout.view[2],
    -layout.view[1] / layout.view[3],
    1 / layout.view[2],
    1 / layout.view[3],
  ];
  const still =
    variant === 'card' ? assets.card : variant === 'cover' ? assets.cover : assets.illustration;
  return (
    <div
      ref={root}
      className={`tomorrow-art tomorrow-art--${variant} ${className}`}
      role="img"
      aria-label="未命名的明天 · ChatGPT / Tomorrow, Still Unwritten · ChatGPT"
    >
      {variant === 'appearance' || (dark && !portraitStudy) ? (
        <div className="tomorrow-art__still">
          <img
            src={dark ? assets.backgroundDark : assets.backgroundLight}
            style={boxStyle(background)}
            alt=""
          />
          {(walking
            ? [
                [0.414, 0.893, 0.306, 0.048],
                [0.651, 0.989, 0.402, 0.084],
              ]
            : [
                [0.528, 0.976, 0.39, 0.042],
                [0.463, 0.887, 0.288, 0.036],
              ]
          ).map(([x, y, w, h], index) => (
            <i
              key={index}
              className="tomorrow-art__shadow"
              style={boxStyle([
                layout.hero[0] + (x - w / 2) * layout.hero[2],
                layout.hero[1] + (y - h / 2) * layout.hero[3],
                w * layout.hero[2],
                h * layout.hero[3],
              ])}
            />
          ))}
          <img
            src={assets.cloud}
            style={{
              ...boxStyle(layout.cloud),
              filter: dark ? 'brightness(.87)' : undefined,
              zIndex: variant === 'card' ? 1 : undefined,
            }}
            alt=""
          />
          <img src={dark ? assets.heroDark : assets.hero} style={boxStyle(layout.hero)} alt="" />
        </div>
      ) : (
        <img
          className="tomorrow-art__flat"
          src={effectiveHigh ? still.replace('/gacha/', '/gacha/hires/') : still}
          alt=""
        />
      )}
      {animated && !reduced && (
        <canvas ref={canvas} className={ready ? 'is-ready' : ''} aria-hidden="true" />
      )}
    </div>
  );
}
