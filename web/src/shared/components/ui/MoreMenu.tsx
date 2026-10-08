import {
  useEffect,
  useLayoutEffect,
  useId,
  useRef,
  useState,
  type KeyboardEvent,
  type ReactNode,
} from 'react';
import { Link } from 'react-router';

export interface MoreMenuItem {
  label: ReactNode;
  onSelect?: () => void;
  danger?: boolean;
  disabled?: boolean;
  checked?: boolean;
  ariaLabel?: string;
  title?: string;
  to?: string;
  href?: string;
}

export function MoreMenu({
  label,
  items,
  trigger,
  triggerClassName,
}: {
  label: string;
  items: readonly (MoreMenuItem | 'separator')[];
  trigger?: ReactNode;
  triggerClassName?: string;
}) {
  const ref = useRef<HTMLDetailsElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);
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
  useLayoutEffect(() => {
    const menu = menuRef.current;
    const trigger = ref.current?.querySelector('summary');
    if (!open || !menu || !trigger) return;
    menu.showPopover();
    const position = () => {
      const anchor = trigger.getBoundingClientRect();
      const bounds = menu.getBoundingClientRect();
      const width = document.documentElement.clientWidth;
      const height = window.innerHeight;
      const below = anchor.bottom + 4;
      const top = below + bounds.height <= height - 8 ? below : anchor.top - bounds.height - 4;
      menu.style.left = `${Math.max(8, Math.min(anchor.right - bounds.width, width - bounds.width - 8))}px`;
      menu.style.top = `${Math.max(8, Math.min(top, height - bounds.height - 8))}px`;
    };
    position();
    menu
      .querySelector<HTMLElement>('[role^="menuitem"]:not(:disabled):not([aria-disabled="true"])')
      ?.focus();
    window.addEventListener('resize', position);
    window.addEventListener('scroll', position, true);
    return () => {
      window.removeEventListener('resize', position);
      window.removeEventListener('scroll', position, true);
      menu.hidePopover();
    };
  }, [open]);
  const enabledItems = () =>
    Array.from(
      ref.current?.querySelectorAll<HTMLElement>(
        '[role^="menuitem"]:not(:disabled):not([aria-disabled="true"])',
      ) ?? [],
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
        if (event.target !== event.currentTarget) return;
        const next = event.currentTarget.open;
        setOpen(next);
        if (!next && event.currentTarget.contains(document.activeElement))
          event.currentTarget.querySelector('summary')?.focus();
      }}
    >
      <summary
        className={['nb-btn nb-btn--secondary', triggerClassName ?? 'nb-btn--sm'].join(' ')}
        title={label}
        role="button"
        aria-label={label}
        aria-haspopup="menu"
        aria-expanded={open}
        aria-controls={menuId}
      >
        {trigger ?? <span aria-hidden="true">⋯</span>}
      </summary>
      <div
        ref={menuRef}
        id={menuId}
        popover="manual"
        hidden={!open}
        className="nb-more__menu"
        role="menu"
        aria-label={label}
      >
        {items.map((item, index) => {
          if (item === 'separator') return <hr key={`sep-${index}`} role="separator" />;
          const props = {
            role: item.checked === undefined ? 'menuitem' : 'menuitemcheckbox',
            'aria-label': item.ariaLabel,
            'aria-checked': item.checked,
            title: item.title,
            className: item.danger ? 'is-danger' : undefined,
            onClick: (event: React.MouseEvent) => {
              if (item.disabled) {
                event.preventDefault();
                return;
              }
              close();
              item.onSelect?.();
            },
            children: item.label,
          };
          if (item.to || item.href) {
            const linkProps = {
              ...props,
              'aria-disabled': item.disabled || undefined,
              tabIndex: item.disabled ? -1 : undefined,
            };
            return item.to ? (
              <Link key={index} {...linkProps} to={item.to} />
            ) : (
              <a key={index} {...linkProps} href={item.href} />
            );
          }
          return <button key={index} type="button" {...props} disabled={item.disabled} />;
        })}
      </div>
    </details>
  );
}
