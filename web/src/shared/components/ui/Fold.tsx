import { useState, type ReactNode } from 'react';

export function Fold({
  title,
  summary,
  meta,
  children,
  defaultOpen = false,
  plain = false,
  persistKey,
}: {
  title: ReactNode;
  summary?: ReactNode;
  meta?: ReactNode;
  children: ReactNode;
  defaultOpen?: boolean;
  plain?: boolean;
  persistKey?: string;
}) {
  const storageKey = persistKey ? `nb.fold.${persistKey}` : null;
  const [open, setOpen] = useState(() => {
    if (!storageKey) return defaultOpen;
    try {
      const saved = sessionStorage.getItem(storageKey);
      return saved === '1' ? true : saved === '0' ? false : defaultOpen;
    } catch {
      return defaultOpen;
    }
  });
  return (
    <details
      className={`nb-fold${plain ? ' nb-fold--plain' : ''}`}
      open={open}
      onToggle={(event) => {
        const next = event.currentTarget.open;
        setOpen(next);
        if (storageKey) {
          try {
            sessionStorage.setItem(storageKey, next ? '1' : '0');
          } catch {
            // Disclosure remains usable when browser storage is unavailable.
          }
        }
      }}
    >
      <summary>
        <span className="nb-fold__title">
          <strong>{title}</strong>
          {summary ? <span>{summary}</span> : null}
        </span>
        {meta ? <span className="nb-fold__meta">{meta}</span> : null}
      </summary>

      <div className="nb-fold__body">{children}</div>
    </details>
  );
}
