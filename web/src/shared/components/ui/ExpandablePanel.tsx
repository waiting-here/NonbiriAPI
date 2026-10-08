import { Button } from '@shared/components/ui/Button';
import { useEffect, useId, useRef, type ReactNode } from 'react';
import './expandable-panel.css';

export function ExpandablePanel({
  open,
  onClose,
  title,
  closeLabel,
  busy = false,
  children,
  footer,
}: {
  open: boolean;
  onClose: () => void;
  title: string;
  closeLabel: string;
  busy?: boolean;
  children: ReactNode;
  footer?: ReactNode;
}) {
  const ref = useRef<HTMLElement>(null);
  const titleID = useId();
  useEffect(() => {
    if (!open || !ref.current) return;
    const previous = document.activeElement;
    ref.current.scrollIntoView?.({ block: 'start' });
    ref.current.querySelector<HTMLElement>('h2')?.focus({ preventScroll: true });
    return () => {
      if (previous instanceof HTMLElement && previous.isConnected) previous.focus();
    };
  }, [open]);
  return (
    <section
      ref={ref}
      hidden={!open}
      className="nb-expandable-panel"
      aria-labelledby={titleID}
      aria-busy={busy}
    >
      <div className="nb-expandable-panel__head">
        <h2 id={titleID} tabIndex={-1}>
          {title}
        </h2>
        <Button
          type="button"

          disabled={busy}
          onClick={onClose}
        >
          {closeLabel}
        </Button>
      </div>
      <div className="nb-expandable-panel__body">{children}</div>
      {footer ? <div className="nb-expandable-panel__foot">{footer}</div> : null}
    </section>
  );
}
