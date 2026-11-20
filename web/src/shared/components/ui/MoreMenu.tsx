import { useRef, useState, type CSSProperties, type ReactNode } from 'react';
import { Button, Menu, MenuItem, MenuSection, MenuTrigger, Separator } from 'react-aria-components';
import { AnchoredSurface } from '../OverlaySurface';

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
  menuClassName,
}: {
  label: string;
  items: readonly (MoreMenuItem | 'separator')[];
  trigger?: ReactNode;
  triggerClassName?: string;
  /** A local surface (such as a game) can carry its colours into the portal. */
  menuClassName?: string;
}) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLButtonElement>(null);
  const [surface, setSurface] = useState<CSSProperties>();
  const openMenu = (next: boolean) => {
    if (next && menuClassName && ref.current) {
      const source = getComputedStyle(ref.current);
      const roles = [
        'text',
        'surface-raised',
        'surface-muted',
        'accent',
        'accent-quiet',
        'border',
        'danger',
        'focus',
      ];
      setSurface(
        Object.fromEntries(
          roles.map((role) => {
            const name = '--nb-color-' + role;
            return [name, source.getPropertyValue(name)];
          }),
        ),
      );
    }
    setOpen(next);
  };
  return (
    <span className="nb-more">
      <MenuTrigger isOpen={open} onOpenChange={openMenu}>
        <Button
          ref={ref}
          className={['nb-btn nb-btn--secondary', triggerClassName ?? 'nb-btn--sm'].join(' ')}
          aria-label={label}
        >
          {trigger ?? <span aria-hidden="true">⋯</span>}
        </Button>
        <AnchoredSurface
          open={open}
          onClose={() => setOpen(false)}
          triggerRef={ref}
          className="nb-more__popover"
        >
          <Menu
            autoFocus="first"
            className={['nb-more__menu', menuClassName].filter(Boolean).join(' ')}
            style={surface}
            aria-label={label}
            shouldFocusWrap
          >
            {items.map((item, index) =>
              item === 'separator' ? (
                <Separator key={'sep-' + index} />
              ) : (
                <MenuSection
                  key={index}
                  selectionMode={item.checked === undefined ? 'none' : 'multiple'}
                  selectedKeys={item.checked ? [String(index)] : []}
                >
                  <MenuItem
                    id={String(index)}
                    isDisabled={item.disabled}
                    shouldCloseOnSelect
                    textValue={
                      item.ariaLabel ?? (typeof item.label === 'string' ? item.label : undefined)
                    }
                    aria-label={item.ariaLabel}
                    className={item.danger ? 'is-danger' : ''}
                    href={item.to ?? item.href}
                    onAction={item.onSelect}
                  >
                    <span title={item.title}>{item.label}</span>
                  </MenuItem>
                </MenuSection>
              ),
            )}
          </Menu>
        </AnchoredSurface>
      </MenuTrigger>
    </span>
  );
}
