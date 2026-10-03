import { type ReactNode } from 'react';

export function FilterBar({
  search,
  secondary,
  secondaryLabel,
  activeCount,
  chips,
  onClearAll,
  clearAllLabel,
  onSubmit,
}: {
  search: ReactNode;
  secondary?: ReactNode;
  secondaryLabel: string;
  activeCount: number;
  chips?: readonly { key: string; label: ReactNode; onRemove: () => void; removeLabel: string }[];
  onClearAll?: () => void;
  clearAllLabel?: string;
  onSubmit: () => void;
}) {
  return (
    <form
      className="nb-filter"
      onSubmit={(event) => {
        event.preventDefault();
        onSubmit();
      }}
    >
      <div className="nb-filter__row">
        <div className="nb-filter__search">{search}</div>
        {secondary ? (
          <details className="nb-filter__more">
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
            <button type="button" className="nb-btn nb-btn--ghost nb-btn--sm" onClick={onClearAll}>
              {clearAllLabel}
            </button>
          ) : null}
        </div>
      ) : null}
    </form>
  );
}
