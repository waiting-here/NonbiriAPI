import { Button } from '@shared/components/ui/Button';
import { useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { FilterFieldLayoutContext } from './FieldLayoutContext';

export function FilterBar({
  search,
  as: Tag = 'form',
  persistKey,
  ariaLabel,
  secondary,
  secondaryLabel,
  secondaryAlwaysVisible = false,
  activeCount,
  chips,
  onClearAll,
  clearAllLabel,
  onSubmit,
  onReset,
  submitLabel,
  submitDisabled,
}: {
  search: ReactNode;
  as?: 'form' | 'div';
  persistKey?: string;
  ariaLabel?: string;
  secondary?: ReactNode;
  /** Defaults to “More filters”. */
  secondaryLabel?: string;
  secondaryAlwaysVisible?: boolean;
  activeCount: number;
  chips?: readonly { key: string; label: ReactNode; onRemove: () => void; removeLabel: string }[];
  onClearAll?: () => void;
  clearAllLabel?: string;
  onSubmit: () => void;
  /**
   * When set, the bar renders its own Search and Reset buttons after the search slot, so
   * pages share one wording. Selects and toggles should still apply on change.
   */
  onReset?: () => void;
  /** Label of the built-in Search button. */
  submitLabel?: string;
  /** Disables the built-in Search and Reset buttons while a request or input check runs. */
  submitDisabled?: boolean;
}) {
  const { t } = useTranslation();
  const storageKey = persistKey ? `nb.fold.${persistKey}` : null;
  const [open, setOpen] = useState(() => {
    try {
      return storageKey ? sessionStorage.getItem(storageKey) === '1' : false;
    } catch {
      return false;
    }
  });
  return (
    <FilterFieldLayoutContext value>
      <Tag
        role={Tag === 'div' ? 'search' : undefined}
        onKeyDown={(event) => {
          if (
            Tag === 'div' &&
            event.key === 'Enter' &&
            (event.target as HTMLElement).tagName === 'INPUT'
          ) {
            event.preventDefault();
            event.stopPropagation();
            onSubmit();
          }
        }}
        className="nb-filter"
        aria-label={ariaLabel}
        onSubmit={(event) => {
          event.preventDefault();
          onSubmit();
        }}
      >
        <div className="nb-filter__row">
          <div className="nb-filter__search">
            {search}
            {onReset ? (
              <>
                <Button
                  type={Tag === 'form' ? 'submit' : 'button'}
                  disabled={submitDisabled}
                  onClick={Tag === 'div' ? onSubmit : undefined}
                >
                  {submitLabel ?? t('common.search')}
                </Button>
                <Button
                  type="button"
                  variant="secondary"
                  disabled={submitDisabled}
                  onClick={onReset}
                >
                  {t('common.resetFilter')}
                </Button>
              </>
            ) : null}
          </div>
          {secondary && !secondaryAlwaysVisible ? (
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
                {secondaryLabel ?? t('common.moreFilters')}
                {activeCount ? ` · ${activeCount}` : ''}
              </summary>
              <div className="nb-filter__fields">{secondary}</div>
            </details>
          ) : null}
        </div>
        {secondary && secondaryAlwaysVisible ? (
          <div className="nb-filter__fields">{secondary}</div>
        ) : null}
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
      </Tag>
    </FilterFieldLayoutContext>
  );
}
