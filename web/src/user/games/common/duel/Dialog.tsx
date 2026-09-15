import { useEffect, useId, useRef, type ReactNode } from 'react';
import { useDuelText } from './copy';

export function DuelDialog({
  title,
  onClose,
  children,
  className = '',
}: {
  readonly title: string;
  readonly onClose: () => void;
  readonly children: ReactNode;
  readonly className?: string;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const titleID = useId();
  const text = useDuelText();
  useEffect(() => {
    const previous = document.activeElement;
    const node = dialog.current;
    node?.showModal();
    return () => {
      node?.close();
      if (previous instanceof HTMLElement && previous.isConnected) previous.focus();
    };
  }, []);
  return (
    <dialog
      ref={dialog}
      className={`duel-dialog ${className}`}
      aria-labelledby={titleID}
      onKeyDown={(event) => {
        if (event.key !== 'Tab') return;
        const items = Array.from(
          event.currentTarget.querySelectorAll<HTMLElement>(
            'button:not(:disabled), a[href], input:not(:disabled), select:not(:disabled), textarea:not(:disabled), summary, [tabindex="0"]',
          ),
        );
        const target = event.shiftKey ? items.at(-1) : items[0];
        if (!target) {
          event.preventDefault();
          return;
        }
        if (document.activeElement === (event.shiftKey ? items[0] : items.at(-1))) {
          event.preventDefault();
          target.focus();
        }
      }}
      onCancel={(event) => {
        event.preventDefault();
        onClose();
      }}
    >
      <header>
        <h2 id={titleID}>{title}</h2>
        <button type="button" className="btn btn-secondary" onClick={onClose} autoFocus>
          {text('关闭', 'Close')}
        </button>
      </header>
      <div className="duel-dialog__body">{children}</div>
    </dialog>
  );
}
