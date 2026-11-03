import { NativeDialog } from '@shared/components/NativeDialog';
import './duel.css';
import { useId, useRef, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
export function DuelDialog({
  title,
  onClose,
  children,
  className = '',
  closeLabel,
  closeOnBackdrop = false,
}: {
  readonly title: string;
  readonly onClose: () => void;
  readonly children: ReactNode;
  readonly className?: string;
  readonly closeLabel?: string;
  readonly closeOnBackdrop?: boolean;
}) {
  const closeButton = useRef<HTMLButtonElement>(null);
  const titleID = useId();
  const { t } = useTranslation();
  return (
    <NativeDialog
      onDismiss={onClose}
      className={`duel-dialog ${className}`}
      aria-labelledby={titleID}
      onClick={(event) => {
        if (!closeOnBackdrop || event.target !== event.currentTarget) return;
        const bounds = event.currentTarget.getBoundingClientRect();
        if (
          event.clientX < bounds.left ||
          event.clientX > bounds.right ||
          event.clientY < bounds.top ||
          event.clientY > bounds.bottom
        )
          onClose();
      }}
    >
      <header>
        <h2 id={titleID}>{title}</h2>
        <button
          ref={closeButton}
          type="button"
          className="nb-btn nb-btn--secondary"
          onClick={onClose}
        >
          {closeLabel ?? t('common.close')}
        </button>
      </header>
      <div className="duel-dialog__body">{children}</div>
    </NativeDialog>
  );
}
