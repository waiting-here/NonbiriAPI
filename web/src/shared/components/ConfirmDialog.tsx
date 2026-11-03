import { ModalSurface } from './OverlaySurface';
import { Button } from '@shared/components/ui/Button';
import { useId, useRef, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';

interface ConfirmDialogProps {
  open: boolean;
  title: string;
  description: ReactNode;
  confirmLabel: string;
  cancelLabel?: string;
  busy?: boolean;
  confirmDisabled?: boolean;
  danger?: boolean;
  showClose?: boolean;
  onConfirm: () => void;
  onCancel: () => void;
  children?: ReactNode;
}

export function ConfirmDialog({
  open,
  title,
  description,
  confirmLabel,
  cancelLabel,
  busy = false,
  confirmDisabled = false,
  danger = false,
  showClose = false,
  onConfirm,
  onCancel,
  children,
}: ConfirmDialogProps) {
  const { t } = useTranslation();
  const titleId = useId();
  const descriptionId = useId();
  const cancelRef = useRef<HTMLButtonElement>(null);

  if (!open) return null;

  return (
    <ModalSurface
      open={open}
      onClose={onCancel}
      busy={busy}
      initialFocusRef={cancelRef}
      className="dialog nb-dialog"
      role="alertdialog"
      aria-labelledby={titleId}
      aria-describedby={descriptionId}
    >
      <div className="dialog-title-row">
        <h2 id={titleId}>{title}</h2>
        {showClose ? (
          <Button
            type="button"
            variant="ghost"
            className="dialog-close"
            aria-label={t('common.close')}
            disabled={busy}
            onClick={onCancel}
          >
            ×
          </Button>
        ) : null}
      </div>
      <div id={descriptionId} className="dialog-description">
        {description}
      </div>
      {children ? <div className="dialog-body">{children}</div> : null}
      <div className="dialog-actions nb-dialog-actions">
        <Button
          ref={cancelRef}
          type="button"

          onClick={onCancel}
          disabled={busy}
        >
          {cancelLabel ?? t('common.cancel')}
        </Button>
        <Button
          type="button"
          variant={danger ? 'danger' : 'primary'}
          onClick={onConfirm}
          disabled={busy || confirmDisabled}
        >
          {busy ? t('common.working') : confirmLabel}
        </Button>
      </div>
    </ModalSurface>
  );
}
