import { Button } from '@shared/components/ui/Button';
import { useState, type ReactNode } from 'react';

export function FilterBar({
  search,
  persistKey,
  ariaLabel,
  secondary,
  secondaryLabel,
  activeCount,
  chips,
  onClearAll,
  clearAllLabel,
  onSubmit,
}: {
  search: ReactNode;
  persistKey?: string;
  ariaLabel?: string;
  secondary?: ReactNode;
  secondaryLabel: string;
  activeCount: number;
  chips?: readonly { key: string; label: ReactNode; onRemove: () => void; removeLabel: string }[];
  onClearAll?: () => void;
  clearAllLabel?: string;
  onSubmit: () => void;
}) {
  const storageKey = persistKey ? `nb.fold.${persistKey}` : null;
  const [open, setOpen] = useState(() => {
    try {
      return storageKey ? sessionStorage.getItem(storageKey) === '1' : false;
    } catch {
      return false;
    }
  });
  return (
    <form
      className="nb-filter"
      aria-label={ariaLabel}
      onSubmit={(event) => {
        event.preventDefault();
        onSubmit();
      }}
    >
      <div className="nb-filter__row">
        <div className="nb-filter__search">{search}</div>
        {secondary ? (
          <details
            className="nb-filter__more"
            open={open}
            onToggle={(event) => {
              const next = event.currentTarget.open;
              setOpen(next);
              if (storageKey) {
                try {
                  sessionStorage.setItem(storageKey, next ? '1' : '0');
                } catch {
                  /* Disclosure remains usable without browser storage. */
                }
              }
            }}
          >
            <summary className="nb-btn nb-btn--secondary">
              {secondaryLabel}
              {activeCount ? ` · ${activeCount}` : ''}
            </summary>
            <div className="nb-filter__fields">{secondary}</div>
          </details>
        ) : null}
      </div>
      {chips && chips.length ? (
        <div className="nb-chips">
          {chips.map((chip) => (
            <span key={chip.key} className="nb-chip">
              {chip.label}
              <button type="button" aria-label={chip.removeLabel} onClick={chip.onRemove}>
                ×
              </button>
            </span>
          ))}
          {onClearAll ? (
            <Button type="button" variant="ghost" size="sm" onClick={onClearAll}>
              {clearAllLabel}
            </Button>
          ) : null}
        </div>
      ) : null}
    </form>
  );
}
