import { useEffect, useRef, type ReactNode } from 'react';
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
  const ref = useRef<HTMLDialogElement>(null);
  const handler = useRef(onClose);
  useEffect(() => {
    handler.current = onClose;
  });
  useEffect(() => {
    const node = ref.current;
    if (!node) return;
    const active = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    node.showModal();
    return () => {
      node.close();
      active?.focus();
    };
  }, []);
  return (
    <dialog
      ref={ref}
      className={'gacha-dialog ' + className}
      aria-label={title}
      onCancel={(event) => {
        event.preventDefault();
        event.stopPropagation();
        handler.current();
      }}
      onClick={(event) => {
        if (onSurfaceClick) {
          if (
            event.target instanceof Element &&
            !event.target.closest('button, a, input, select, textarea, [role="button"]')
          ) {
            onSurfaceClick();
          }
        } else if (event.target === event.currentTarget) handler.current();
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
    </dialog>
  );
}
