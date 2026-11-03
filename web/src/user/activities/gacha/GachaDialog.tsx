import { NativeDialog } from '@shared/components/NativeDialog';
import { type ReactNode } from 'react';
import { Button } from '@shared/components/ui/Button';
import { Icon } from '@shared/components/Icon';

export function GachaDialog({
  title,
  children,
  onClose,
  onSurfaceClick,
  className = '',
  closeLabel = '关闭',
  closeDisabled = false,
}: {
  title: string;
  children: ReactNode;
  onClose: () => void;
  onSurfaceClick?: () => void;
  className?: string;
  closeLabel?: string;
  closeDisabled?: boolean;
}) {
  return (
    <NativeDialog
      onDismiss={onClose}
      busy={closeDisabled}
      className={'gacha-dialog ' + className}
      aria-label={title}
      onClick={(event) => {
        if (onSurfaceClick) {
          if (
            event.target instanceof Element &&
            !event.target.closest('button, a, input, select, textarea, [role="button"]')
          ) {
            onSurfaceClick();
          }
        } else if (event.target === event.currentTarget) onClose();
      }}
    >
      <div className="gacha-dialog__inner">
        <header>
          <h2>{title}</h2>
          <Button
            variant="ghost"
            onClick={onClose}
            disabled={closeDisabled}
            aria-label={closeLabel}
            title={closeLabel}
          >
            <Icon name="close" />
          </Button>
        </header>
        {children}
      </div>
    </NativeDialog>
  );
}
