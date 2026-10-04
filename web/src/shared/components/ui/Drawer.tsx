import { useEffect, useId, useRef, type ReactNode } from 'react';
import './drawer.css';

export function Drawer({
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
  const ref = useRef<HTMLDialogElement>(null);
  const titleID = useId();
  useEffect(() => {
    const dialog = ref.current;
    if (!dialog || !open) return;
    const previous = document.activeElement;
    const overflow = document.body.style.overflow;
    dialog.showModal();
    document.body.style.overflow = 'hidden';
    return () => {
      dialog.close();
      document.body.style.overflow = overflow;
      if (previous instanceof HTMLElement && previous.isConnected) previous.focus();
    };
  }, [open]);
  return (
    <dialog
      ref={ref}
      className="nb-drawer nb-form-drawer"
      aria-labelledby={titleID}
      aria-busy={busy}
      onCancel={(event) => {
        event.preventDefault();
        if (!busy) onClose();
      }}
      onClick={(event) => {
        if (busy || event.target !== event.currentTarget) return;
        const box = event.currentTarget.getBoundingClientRect();
        if (
          event.clientX < box.left ||
          event.clientX > box.right ||
          event.clientY < box.top ||
          event.clientY > box.bottom
        )
          onClose();
      }}
    >
      <div className="nb-drawer__head">
        <h2 id={titleID}>{title}</h2>
        <button
          type="button"
          className="nb-btn nb-btn--secondary"
          aria-label={closeLabel}
          disabled={busy}
          onClick={onClose}
        >
          ×
        </button>
      </div>
      <div className="nb-drawer__body">{children}</div>
      {footer ? <div className="nb-drawer__foot">{footer}</div> : null}
    </dialog>
  );
}
