import { useId, useRef, useState, type ReactNode } from 'react';
import { ModalSurface } from '../OverlaySurface';
import { Icon } from '../Icon';
import { Button } from './Button';
import './sheet.css';

export function Sheet({
  open,
  title,
  description,
  closeLabel,
  onClose,
  busy = false,
  keepMounted = false,
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
  /** Retain a visited task while dismissed so drafts and write recovery survive reopening. */
  keepMounted?: boolean;
  size?: 'md' | 'lg';
  footer?: ReactNode;
  children: ReactNode;
}) {
  const titleID = useId();
  const descriptionID = useId();
  const titleRef = useRef<HTMLHeadingElement>(null);
  const [visited, setVisited] = useState(open);
  if (open && !visited) setVisited(true);
  if (!open && (!keepMounted || !visited)) return null;
  return (
    <ModalSurface
      open={open}
      onClose={onClose}
      busy={busy}
      initialFocusRef={titleRef}
      backdropClassName="nb-sheet-backdrop"
      className={'nb-sheet nb-sheet--' + size}
      aria-labelledby={titleID}
      aria-describedby={description ? descriptionID : undefined}
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
    </ModalSurface>
  );
}
