import { useState, type ReactNode } from 'react';

export function Fold({
  title,
  summary,
  meta,
  children,
  defaultOpen = false,
  plain = false,
  persistKey,
  open: controlledOpen,
  onOpenChange,
  className = '',
  lazy = false,
}: {
  title: ReactNode;
  summary?: ReactNode;
  meta?: ReactNode;
  children: ReactNode;
  defaultOpen?: boolean;
  plain?: boolean;
  persistKey?: string;
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
  className?: string;
  lazy?: boolean;
}) {
  const storageKey = persistKey ? `nb.fold.${persistKey}` : null;
  const [localOpen, setOpen] = useState(() => {
    if (!storageKey) return defaultOpen;
    try {
      const saved = sessionStorage.getItem(storageKey);
      return saved === '1' ? true : saved === '0' ? false : defaultOpen;
    } catch {
      return defaultOpen;
    }
  });
  const open = controlledOpen ?? localOpen;
  const [visited, setVisited] = useState(open);
  if (open && !visited) setVisited(true);
  function update(next: boolean) {
    if (next === open) return;
    setOpen(next);
    onOpenChange?.(next);
    if (storageKey) {
      try {
        sessionStorage.setItem(storageKey, next ? '1' : '0');
      } catch {
        // Disclosure remains usable when browser storage is unavailable.
      }
    }
  }
  return (
    <details
      className={`nb-fold${plain ? ' nb-fold--plain' : ''} ${className}`.trim()}
      open={open}
      onToggle={(event) => update(event.currentTarget.open)}
    >
      <summary
        onClick={(event) => {
          event.preventDefault();
          update(!open);
        }}
      >
        <span className="nb-fold__title">
          <strong>{title}</strong>
          {summary ? <span>{summary}</span> : null}
        </span>
        {meta ? <span className="nb-fold__meta">{meta}</span> : null}
      </summary>

      {!lazy || visited ? <div className="nb-fold__body">{children}</div> : null}
    </details>
  );
}
