import { Button } from '@shared/components/ui/Button';
import { type ReactNode } from 'react';

export function SaveBar({
  dirtyCount,
  scope,
  busy,
  saveDisabled,
  onSave,
  onDiscard,
  saveLabel,
  discardLabel,
  dirtyLabel,
}: {
  dirtyCount: number;
  scope?: ReactNode;
  busy?: boolean;
  saveDisabled?: boolean;
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
        <Button
          type="button"

          disabled={busy}
          onClick={onDiscard}
        >
          {discardLabel}
        </Button>
        <Button
          type="button"
          variant="primary"
          disabled={busy || saveDisabled}
          onClick={onSave}
        >
          {saveLabel}
        </Button>
      </span>
    </div>
  );
}
