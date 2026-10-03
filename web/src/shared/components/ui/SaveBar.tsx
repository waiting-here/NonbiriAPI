import { type ReactNode } from 'react';

export function SaveBar({
  dirtyCount,
  scope,
  busy,
  onSave,
  onDiscard,
  saveLabel,
  discardLabel,
  dirtyLabel,
}: {
  dirtyCount: number;
  scope?: ReactNode;
  busy?: boolean;
  onSave: () => void;
  onDiscard: () => void;
  saveLabel: string;
  discardLabel: string;
  dirtyLabel: (count: number) => string;
}) {
  if (dirtyCount === 0) return null;
  return (
    <div className="nb-savebar" role="region" aria-label={dirtyLabel(dirtyCount)}>
      <span className="nb-savebar__state" aria-live="polite">
        <strong>{dirtyLabel(dirtyCount)}</strong>
        {scope ? <> · {scope}</> : null}
      </span>
      <span className="nb-savebar__actions">
        <button
          type="button"
          className="nb-btn nb-btn--secondary"
          disabled={busy}
          onClick={onDiscard}
        >
          {discardLabel}
        </button>
        <button type="button" className="nb-btn nb-btn--primary" disabled={busy} onClick={onSave}>
          {saveLabel}
        </button>
      </span>
    </div>
  );
}
