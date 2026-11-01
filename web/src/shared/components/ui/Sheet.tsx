import { useEffect, useId, useRef, type ReactNode } from 'react';
import { OverlayPortal } from '../OverlayPortal';
import { Icon } from '../Icon';
import { Button } from './Button';
import './sheet.css';

const FOCUSABLE =
  'button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), a[href], summary, [tabindex]:not([tabindex="-1"])';

/** True when the event belongs to another modal layer opened on top of this one. */
function fromOtherModal(event: KeyboardEvent, own: HTMLElement | null): boolean {
  const target = event.target instanceof Element ? event.target : null;
  const modal = target?.closest('[aria-modal="true"]');
  return Boolean(modal && modal !== own);
}

/**
 * Side sheet for a task that needs a form but should keep the list behind it. Full screen on
 * phones. Focus moves to the title, Tab stays inside, Escape and the close button call
 * `onClose`, and focus returns to the trigger. Dialogs opened from inside keep working.
 */
export function Sheet({
  open,
  title,
  description,
  closeLabel,
  onClose,
  busy = false,
  size = 'md',
  footer,
  children,
}: {
  open: boolean;
  title: string;
  description?: ReactNode;
  closeLabel: string;
  onClose: () => void;
  busy?: boolean;
  size?: 'md' | 'lg';
  footer?: ReactNode;
  children: ReactNode;
}) {
  const titleID = useId();
  const descriptionID = useId();
  const sheetRef = useRef<HTMLElement>(null);
  const titleRef = useRef<HTMLHeadingElement>(null);
  const closeRef = useRef(onClose);
  const busyRef = useRef(busy);
  useEffect(() => {
    closeRef.current = onClose;
    busyRef.current = busy;
  });

  useEffect(() => {
    if (!open) return;
    const previous = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const overflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    titleRef.current?.focus({ preventScroll: true });
    const onKeyDown = (event: KeyboardEvent) => {
      const sheet = sheetRef.current;
      if (!sheet || fromOtherModal(event, sheet)) return;
      if (event.key === 'Escape') {
        event.preventDefault();
        if (!busyRef.current) closeRef.current();
        return;
      }
      if (event.key !== 'Tab') return;
      const focusable = Array.from(sheet.querySelectorAll<HTMLElement>(FOCUSABLE)).filter(
        (element) => !element.closest('[hidden]'),
      );
      if (focusable.length === 0) {
        event.preventDefault();
        titleRef.current?.focus();
        return;
      }
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      const active = document.activeElement;
      if (event.shiftKey && (active === first || active === titleRef.current)) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && active === last) {
        event.preventDefault();
        first.focus();
      } else if (!(active instanceof Node) || !sheet.contains(active)) {
        event.preventDefault();
        first.focus();
      }
    };
    document.addEventListener('keydown', onKeyDown);
    return () => {
      document.removeEventListener('keydown', onKeyDown);
      document.body.style.overflow = overflow;
      if (previous?.isConnected) previous.focus({ preventScroll: true });
    };
  }, [open]);

  if (!open) return null;
  return (
    <OverlayPortal>
      <div
        className="nb-sheet-backdrop"
        role="presentation"
        onMouseDown={(event) => {
          if (event.target === event.currentTarget && !busy) onClose();
        }}
      >
        <section
          ref={sheetRef}
          className={`nb-sheet nb-sheet--${size}`}
          role="dialog"
          aria-modal="true"
          aria-labelledby={titleID}
          aria-describedby={description ? descriptionID : undefined}
          aria-busy={busy}
        >
          <header className="nb-sheet__head">
            <div className="nb-sheet__title">
              <h2 id={titleID} ref={titleRef} tabIndex={-1}>
                {title}
              </h2>
              {description ? (
                <p id={descriptionID} className="nb-sheet__description">
                  {description}
                </p>
              ) : null}
            </div>
            <Button
              type="button"
              variant="ghost"
              className="nb-sheet__close"
              aria-label={closeLabel}
              title={closeLabel}
              disabled={busy}
              onClick={onClose}
            >
              <Icon name="close" />
            </Button>
          </header>
          <div className="nb-sheet__body">{children}</div>
          {footer ? <footer className="nb-sheet__foot">{footer}</footer> : null}
        </section>
      </div>
    </OverlayPortal>
  );
}
