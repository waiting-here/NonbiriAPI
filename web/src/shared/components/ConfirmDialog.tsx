import { useCallback, useEffect, useId, useRef, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';

interface ConfirmDialogProps {
  open: boolean;
  title: string;
  description: string;
  confirmLabel: string;
  cancelLabel?: string;
  busy?: boolean;
  danger?: boolean;
  onConfirm: () => void;
  onCancel: () => void;
  children?: ReactNode;
}

const FOCUSABLE_SELECTOR =
  'button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), a[href], [tabindex]:not([tabindex="-1"]):not([aria-disabled="true"])';

export function ConfirmDialog({
  open,
  title,
  description,
  confirmLabel,
  cancelLabel,
  busy = false,
  danger = false,
  onConfirm,
  onCancel,
  children,
}: ConfirmDialogProps) {
  const { t } = useTranslation();
  const titleId = useId();
  const descriptionId = useId();
  const dialogRef = useRef<HTMLElement>(null);
  const cancelRef = useRef<HTMLButtonElement>(null);
  const previousActiveRef = useRef<HTMLElement | null>(null);
  const previousBusyRef = useRef(false);

  const restoreFocus = useCallback(() => {
    const previous = previousActiveRef.current;
    previousActiveRef.current = null;
    if (previous?.isConnected) previous.focus();
  }, []);

  useEffect(() => {
    if (open) {
      if (!previousActiveRef.current) {
        previousActiveRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
      }
      const dialog = dialogRef.current;
      if (dialog) {
        const first = cancelRef.current?.disabled
          ? dialog.querySelector<HTMLElement>(FOCUSABLE_SELECTOR)
          : cancelRef.current;
        (first ?? dialog).focus();
      }
      return;
    }
    restoreFocus();
  }, [open, restoreFocus]);

  // Unmounting an open dialog (for example when a route changes) must restore
  // the trigger as well; this cleanup is deliberately independent of `busy`.
  useEffect(() => () => restoreFocus(), [restoreFocus]);

  useEffect(() => {
    if (!open) {
      previousBusyRef.current = busy;
      return;
    }
    if (busy && !previousBusyRef.current) {
      const dialog = dialogRef.current;
      const active = document.activeElement;
      const activeInside = Boolean(dialog && active instanceof Node && dialog.contains(active));
      const activeDisabled = active instanceof HTMLElement && active.hasAttribute('disabled');
      if (!activeInside || activeDisabled) dialog?.focus();
    }
    previousBusyRef.current = busy;
  }, [busy, open]);

  // Keyboard handling is a separate lifecycle from open/close focus capture:
  // changing `busy` only refreshes this listener and can never restore focus to
  // the background or try to focus a now-disabled action.
  useEffect(() => {
    if (!open) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault();
        if (!busy) onCancel();
        return;
      }
      if (event.key !== 'Tab') return;
      const dialog = dialogRef.current;
      if (!dialog) return;
      const focusable = Array.from(dialog.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR));
      if (focusable.length === 0) {
        event.preventDefault();
        dialog.focus();
        return;
      }
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last?.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first?.focus();
      }
    };
    document.addEventListener('keydown', onKeyDown);
    return () => document.removeEventListener('keydown', onKeyDown);
  }, [busy, onCancel, open]);

  if (!open) return null;

  return (
    <div
      className="dialog-backdrop nb-dialog-backdrop"
      role="presentation"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget && !busy) onCancel();
      }}
    >
      <section
        ref={dialogRef}
        className="dialog nb-dialog"
        role="alertdialog"
        aria-modal="true"
        aria-busy={busy}
        tabIndex={-1}
        aria-labelledby={titleId}
        aria-describedby={descriptionId}
      >
        <h2 id={titleId}>{title}</h2>
        <p id={descriptionId}>{description}</p>
        {children ? <div className="dialog-body">{children}</div> : null}
        <div className="dialog-actions nb-dialog-actions">
          <button
            ref={cancelRef}
            type="button"
            className="btn btn-secondary"
            onClick={onCancel}
            disabled={busy}
          >
            {cancelLabel ?? t('common.cancel')}
          </button>
          <button
            type="button"
            className={`btn ${danger ? 'btn-danger' : 'btn-primary'}`}
            onClick={onConfirm}
            disabled={busy}
          >
            {busy ? t('common.working') : confirmLabel}
          </button>
        </div>
      </section>
    </div>
  );
}
