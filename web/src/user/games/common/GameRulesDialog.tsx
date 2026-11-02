import { OverlayPortal } from '@shared/components/OverlayPortal';
import { DuelDialog } from './duel/Dialog';
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
    <button type="button" className="nb-btn nb-btn--secondary" onClick={onClick}>
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
  if (!open) return null;

  return (
    <OverlayPortal>
      <DuelDialog
        title={title}
        closeLabel={closeLabel}
        onClose={onClose}
        closeOnBackdrop
        className="game-rules-dialog"
      >
        <div className="game-rules-modal__body">
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
      </DuelDialog>
    </OverlayPortal>
  );
}
