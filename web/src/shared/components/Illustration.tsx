import './illustration.css';

export type IllustrationName = 'empty' | 'search' | 'done' | 'error' | 'maintenance' | 'welcome';

/*
 * A resting cloud in a few small variations. Colours come from CSS roles, so the drawing
 * follows dark mode and account appearances. Decorative only: callers keep the text.
 */
const CLOUD =
  'M30 72h60a14 14 0 0 0 2.8-27.72A20 20 0 0 0 60 29a20 20 0 0 0-32.8 15.28A14 14 0 0 0 30 72Z';

function Face({ awake }: { awake: boolean }) {
  return awake ? (
    <>
      <circle className="nb-illustration__ink-fill" cx="52" cy="53" r="2.4" />
      <circle className="nb-illustration__ink-fill" cx="68" cy="53" r="2.4" />
      <path className="nb-illustration__ink" d="M56 60q4 3 8 0" />
    </>
  ) : (
    <>
      <path className="nb-illustration__ink" d="M48 53q4 3.5 8 0M64 53q4 3.5 8 0" />
      <path className="nb-illustration__ink" d="M57 60.5q3 2 6 0" />
    </>
  );
}

export function Illustration({
  name,
  className = '',
}: {
  name: IllustrationName;
  className?: string;
}) {
  const awake = name === 'search' || name === 'done' || name === 'welcome';
  return (
    <svg
      className={`nb-illustration nb-illustration--${name}${className ? ` ${className}` : ''}`}
      viewBox="0 0 120 96"
      aria-hidden="true"
      focusable="false"
    >
      <ellipse className="nb-illustration__shadow" cx="60" cy="84" rx="32" ry="4.5" />
      {name === 'welcome' ? (
        <g className="nb-illustration__sun">
          <circle cx="30" cy="30" r="11" />
          <path d="M30 12v-4M30 52v-4M12 30H8M52 30h-4M17.3 17.3l-2.8-2.8M42.7 17.3l2.8-2.8" />
        </g>
      ) : null}
      <path className="nb-illustration__cloud" d={CLOUD} />
      <circle className="nb-illustration__blush" cx="45" cy="60" r="3.2" />
      <circle className="nb-illustration__blush" cx="75" cy="60" r="3.2" />
      <Face awake={awake} />
      {name === 'empty' ? (
        <path className="nb-illustration__soft-ink" d="M92 22h7l-7 8h7M102 12h5l-5 6h5" />
      ) : null}
      {name === 'search' ? (
        <g className="nb-illustration__tool">
          <circle cx="94" cy="66" r="8" />
          <path d="m100 72 7 7" />
        </g>
      ) : null}
      {name === 'done' ? (
        <g>
          <circle className="nb-illustration__ok-fill" cx="94" cy="30" r="10" />
          <path className="nb-illustration__ok" d="m89.5 30 3.2 3.2 6-6.4" />
        </g>
      ) : null}
      {name === 'error' ? (
        <g>
          <circle className="nb-illustration__warn-fill" cx="94" cy="30" r="10" />
          <path className="nb-illustration__warn" d="M94 24.5v6.5M94 35.5h.01" />
        </g>
      ) : null}
      {name === 'maintenance' ? (
        <g className="nb-illustration__glyph" transform="translate(84 16)">
          <path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.106-3.105c.32-.322.863-.22.983.218a6 6 0 0 1-8.259 7.057l-7.91 7.91a1 1 0 0 1-2.999-3l7.91-7.91a6 6 0 0 1 7.057-8.259c.438.12.54.662.219.984z" />
        </g>
      ) : null}
    </svg>
  );
}
