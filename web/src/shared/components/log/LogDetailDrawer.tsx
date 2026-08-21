import { useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { copyText } from '@shared/utils/clipboard';

// Right-side detail drawer for one log row. Accessibility contract: dialog
// role, focus trapped inside while open, Escape closes, and focus returns to
// the triggering element on close. Field values arrive as ready-made React
// nodes and are rendered as text only — this component never interprets HTML.

export interface LogDetailField {
  label: string;
  value: React.ReactNode;
}

interface LogDetailDrawerProps {
  open: boolean;
  onClose: () => void;
  title: string;
  fields: readonly LogDetailField[];
  /** Bounded diagnostic text offered through a copy button. */
  diagnostics?: { label: string; text: string };
}

const FOCUSABLE_SELECTOR =
  'button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), a[href], [tabindex]:not([tabindex="-1"])';

export function LogDetailDrawer({ open, onClose, title, fields, diagnostics }: LogDetailDrawerProps) {
  const { t } = useTranslation();
  const panelRef = useRef<HTMLDivElement>(null);
  const closeRef = useRef<HTMLButtonElement>(null);
  const previousActiveRef = useRef<HTMLElement | null>(null);
  const [copied, setCopied] = useState(false);

  // Clear the copied feedback whenever the drawer opens again or shows a
  // different row's diagnostics (render-time state adjustment).
  const feedbackSeed = `${open}:${diagnostics?.text ?? ''}`;
  const [seededFor, setSeededFor] = useState(feedbackSeed);
  if (seededFor !== feedbackSeed) {
    setSeededFor(feedbackSeed);
    setCopied(false);
  }

  useEffect(() => {
    if (!open) return;
    previousActiveRef.current =
      document.activeElement instanceof HTMLElement ? document.activeElement : null;
    closeRef.current?.focus();

    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault();
        onClose();
        return;
      }
      if (event.key !== 'Tab') return;
      const panel = panelRef.current;
      if (!panel) return;
      const focusable = Array.from(panel.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR));
      if (focusable.length === 0) return;
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last?.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first?.focus();
      }
    };
    document.addEventListener('keydown', onKeyDown);
    return () => {
      document.removeEventListener('keydown', onKeyDown);
      previousActiveRef.current?.focus();
      previousActiveRef.current = null;
    };
  }, [open, onClose]);

  const onCopy = useCallback(() => {
    if (!diagnostics) return;
    void copyText(diagnostics.text).then((ok) => {
      setCopied(ok);
      // Reset the feedback label after a short pause.
      window.setTimeout(() => setCopied(false), 2000);
    });
  }, [diagnostics]);

  if (!open) return null;

  return (
    <div className="log-drawer-backdrop" role="presentation" onMouseDown={onClose}>
      <div
        ref={panelRef}
        className="log-drawer"
        role="dialog"
        aria-modal="true"
        aria-label={title}
        onMouseDown={(event) => event.stopPropagation()}
      >
        <div className="log-drawer-header">
          <h2>{title}</h2>
          <button
            ref={closeRef}
            type="button"
            className="btn btn-secondary"
            onClick={onClose}
            aria-label={t('common.close')}
          >
            ×
          </button>
        </div>
        <dl className="log-drawer-fields">
          {fields.map((field) => (
            <div className="log-detail-row" key={field.label}>
              <dt>{field.label}</dt>
              <dd>{field.value}</dd>
            </div>
          ))}
        </dl>
        {diagnostics ? (
          <div className="log-drawer-diagnostics">
            <h3>{diagnostics.label}</h3>
            <p className="mono">{diagnostics.text || t('common.notAvailable')}</p>
            <button type="button" className="btn btn-secondary" onClick={onCopy}>
              {copied ? t('common.copied') : t('logs.copyDiagnostics')}
            </button>
            <span className="visually-hidden" aria-live="polite">
              {copied ? t('common.copied') : ''}
            </span>
          </div>
        ) : null}
      </div>
    </div>
  );
}
