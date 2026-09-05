import type { Gesture } from './types';

export function GestureArt({ gesture }: { readonly gesture: Gesture }) {
  return (
    <svg className="rps-gesture-art" viewBox="0 0 96 96" aria-hidden="true" focusable="false">
      <circle cx="48" cy="48" r="43" fill="currentColor" opacity=".1" />
      {gesture === 'rock' ? (
        <>
          <path
            d="M24 53V37q0-10 10-10 7 0 9 5 4-9 12-5 5 1 6 7 9-4 13 4 9 0 10 9v13q-1 12-13 19H36Q23 70 24 53Z"
            fill="currentColor"
          />
          <path
            d="M34 36v14m10-14v12m11-12v13m10-8v11M26 54q3-7 10-4l15 7q4 6-3 9l-9-4"
            fill="none"
            stroke="var(--nb-color-surface)"
            strokeWidth="3"
            strokeLinecap="round"
          />
          <path d="M35 75h34v8H35Z" fill="currentColor" opacity=".55" />
        </>
      ) : null}
      {gesture === 'scissors' ? (
        <>
          <path
            d="M28 65q-7-14 3-18l7 4-7-29q-2-10 7-11 6-1 8 8l5 25 8-28q3-9 10-5 5 3 2 12l-6 25q10-1 11 9 7 11-4 23H38Z"
            fill="currentColor"
          />
          <path
            d="m36 53 13 10q5 7-3 9l-10-6m16-18q-6 11 2 14l7 1m3-12q-5 9 3 13"
            fill="none"
            stroke="var(--nb-color-surface)"
            strokeWidth="3"
            strokeLinecap="round"
          />
        </>
      ) : null}
      {gesture === 'paper' ? (
        <path
          d="M25 70V38c0-7 9-7 9 0V23c0-7 10-7 10 0v13-18c0-7 10-7 10 0v18-14c0-7 10-7 10 0v18-9c0-7 10-7 10 0v27c0 17-11 25-26 25-10 0-18-5-23-13Z"
          fill="currentColor"
        />
      ) : null}
    </svg>
  );
}
