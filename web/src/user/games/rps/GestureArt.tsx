import type { Gesture } from './types';

export function GestureArt({ gesture }: { readonly gesture: Gesture }) {
  return (
    <svg className="rps-gesture-art" viewBox="0 0 96 96" aria-hidden="true" focusable="false">
      <circle cx="48" cy="48" r="43" fill="currentColor" opacity=".1" />
      {gesture === 'rock' ? (
        <path d="M24 55 29 37l10-8 12 2 8 8 10 3 4 14-9 16-28 2Z" fill="currentColor" />
      ) : null}
      {gesture === 'scissors' ? (
        <>
          <path
            d="m23 66 28-20 21-27c4-5 11 1 7 6L61 51l15 8c7 4 2 12-4 9L53 58 34 76Z"
            fill="currentColor"
          />
          <circle
            cx="32"
            cy="51"
            r="9"
            fill="none"
            stroke="var(--nb-color-surface)"
            strokeWidth="5"
          />
          <circle
            cx="42"
            cy="68"
            r="9"
            fill="none"
            stroke="var(--nb-color-surface)"
            strokeWidth="5"
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
