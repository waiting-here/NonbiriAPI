import { useEffect, useId, useRef } from 'react';
import '../games.css';

export interface GameRulesSection {
  readonly title: string;
  readonly paragraphs?: readonly string[];
  readonly items?: readonly string[];
}

export function GameRulesButton({
  label,
  onClick,
}: {
  readonly label: string;
  readonly onClick: () => void;
}) {
  return (
    <button type="button" className="btn btn-secondary" onClick={onClick}>
      {label}
    </button>
  );
}

export function GameRulesDialog({
  open,
  title,
  closeLabel,
  sections,
  onClose,
}: {
  readonly open: boolean;
  readonly title: string;
  readonly closeLabel: string;
  readonly sections: readonly GameRulesSection[];
  readonly onClose: () => void;
}) {
  const titleID = `game-rules-title-${useId().replaceAll(':', '')}`;
  const bodyID = `${titleID}-body`;
  const closeRef = useRef<HTMLButtonElement>(null);
  const returnFocusRef = useRef<HTMLElement | null>(null);

  useEffect(() => {
    if (!open) return undefined;
    returnFocusRef.current =
      document.activeElement instanceof HTMLElement ? document.activeElement : null;
    closeRef.current?.focus();
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return;
      event.preventDefault();
      onClose();
    };
    document.addEventListener('keydown', handleKeyDown);
    return () => {
      document.removeEventListener('keydown', handleKeyDown);
      returnFocusRef.current?.focus();
      returnFocusRef.current = null;
    };
  }, [onClose, open]);

  if (!open) return null;

  return (
    <div
      className="game-rules-modal"
      role="presentation"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget) onClose();
      }}
    >
      <div
        className="card game-rules-modal__panel"
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleID}
        aria-describedby={bodyID}
      >
        <div className="game-rules-modal__heading">
          <h2 id={titleID}>{title}</h2>
          <button
            ref={closeRef}
            type="button"
            className="btn btn-secondary game-rules-modal__close"
            onClick={onClose}
          >
            {closeLabel}
          </button>
        </div>
        <div id={bodyID} className="game-rules-modal__body">
          {sections.map((section) => (
            <section className="game-rules-modal__section" key={section.title}>
              <h3>{section.title}</h3>
              {section.paragraphs?.map((paragraph) => (
                <p key={paragraph}>{paragraph}</p>
              ))}
              {section.items?.length ? (
                <ul>
                  {section.items.map((item) => (
                    <li key={item}>{item}</li>
                  ))}
                </ul>
              ) : null}
            </section>
          ))}
        </div>
      </div>
    </div>
  );
}
