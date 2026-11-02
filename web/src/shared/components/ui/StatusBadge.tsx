import type { ReactNode } from 'react';

export type StatusTone = 'ok' | 'warn' | 'bad' | 'info' | 'neutral' | 'accent';

/**
 * Dot-and-text status label. The text carries the meaning; tone only adds colour. Use the
 * same tone for the same state everywhere (for example pending is always "warn").
 */
export function StatusBadge({
  tone = 'neutral',
  children,
  title,
}: {
  tone?: StatusTone;
  children: ReactNode;
  title?: string;
}) {
  const toneClass = tone === 'neutral' ? '' : ` nb-badge--${tone}`;
  return (
    <span className={`nb-badge${toneClass}`} title={title}>
      {children}
    </span>
  );
}
