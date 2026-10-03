import { useEffect, useId, useRef, useState, type KeyboardEvent, type ReactNode } from 'react';

export function MoreMenu({
  label,
  items,
}: {
  label: string;
  items: readonly (
    { label: ReactNode; onSelect: () => void; danger?: boolean; disabled?: boolean } | 'separator'
  )[];
}) {
  const ref = useRef<HTMLDetailsElement>(null);
  const menuId = useId();
  const [open, setOpen] = useState(false);
  const close = () => {
    if (!ref.current?.open) return;
    ref.current.open = false;
    setOpen(false);
    ref.current.querySelector('summary')?.focus();
  };
  useEffect(() => {
    const onDown = (event: PointerEvent) => {
      if (ref.current?.open && !ref.current.contains(event.target as Node)) {
        ref.current.open = false;
        setOpen(false);
        ref.current.querySelector('summary')?.focus();
      }
    };
    document.addEventListener('pointerdown', onDown);
    return () => document.removeEventListener('pointerdown', onDown);
  }, []);
  const enabledItems = () =>
    Array.from(
      ref.current?.querySelectorAll<HTMLButtonElement>('[role="menuitem"]:not(:disabled)') ?? [],
    );
  const onKeyDown = (event: KeyboardEvent) => {
    if (event.key === 'Escape' && ref.current?.open) {
      event.preventDefault();
      close();
    } else if (['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) {
      event.preventDefault();
      if (!ref.current?.open) {
        if (ref.current) ref.current.open = true;
        return;
      }
      const buttons = enabledItems();
      if (!buttons.length) return;
      const current = buttons.findIndex((button) => button === document.activeElement);
      const next =
        event.key === 'Home'
          ? 0
          : event.key === 'End'
            ? buttons.length - 1
            : (current + (event.key === 'ArrowDown' ? 1 : -1) + buttons.length) % buttons.length;
      buttons[next]?.focus();
    } else if (event.key === 'Tab') {
      close();
    }
  };
  return (
    <details
      ref={ref}
      className="nb-more"
      onKeyDown={onKeyDown}
      onToggle={(event) => {
        const next = event.currentTarget.open;
        setOpen(next);
        if (next) enabledItems()[0]?.focus();
        else if (event.currentTarget.contains(document.activeElement))
          event.currentTarget.querySelector('summary')?.focus();
      }}
    >
      <summary
        className="nb-btn nb-btn--secondary nb-btn--sm"
        role="button"
        aria-label={label}
        aria-haspopup="menu"
        aria-expanded={open}
        aria-controls={menuId}
      >
        <span aria-hidden="true">⋯</span>
      </summary>
      <div id={menuId} className="nb-more__menu" role="menu" aria-label={label}>
        {items.map((item, index) =>
          item === 'separator' ? (
            <hr key={`sep-${index}`} role="separator" />
          ) : (
            <button
              key={index}
              type="button"
              role="menuitem"
              disabled={item.disabled}
              className={item.danger ? 'is-danger' : undefined}
              onClick={() => {
                close();
                item.onSelect();
              }}
            >
              {item.label}
            </button>
          ),
        )}
      </div>
    </details>
  );
}
