import { useEffect, useRef, type ReactNode } from 'react';
import { Button } from '@shared/components/ui/Button';
import { Icon } from '@shared/components/Icon';

export function GachaDialog({
  title,
  children,
  onClose,
  className = '',
  closeLabel = '关闭',
}: {
  title: string;
  children: ReactNode;
  onClose: () => void;
  className?: string;
  closeLabel?: string;
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
    const previous = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    return () => {
      node.close();
      document.body.style.overflow = previous;
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
        handler.current();
      }}
      onClick={(event) => {
        if (event.target === event.currentTarget) handler.current();
      }}
    >
      <div className="gacha-dialog__inner">
        <header>
          <h2>{title}</h2>
          <Button variant="ghost" onClick={onClose} aria-label={closeLabel} title={closeLabel}>
            <Icon name="close" />
          </Button>
        </header>
        {children}
      </div>
    </dialog>
  );
}
