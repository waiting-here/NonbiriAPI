import { useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { copyText } from '@shared/utils/clipboard';
import { Drawer } from '@shared/components/ui/Drawer';
import { Fold } from '@shared/components/ui/Fold';
import './logs.css';

export interface LogDetailField {
  label: string;
  value: ReactNode;
  wide?: boolean;
  technical?: boolean;
}
interface LogDetailDrawerProps {
  open: boolean;
  onClose: () => void;
  title: string;
  fields: readonly LogDetailField[];
  diagnostics?: { label: string; text: string };
  result?: ReactNode;
}
export function LogDetailDrawer({
  open,
  onClose,
  title,
  fields,
  diagnostics,
  result,
}: LogDetailDrawerProps) {
  const { t } = useTranslation();
  const [copied, setCopied] = useState(false);
  const feedbackSeed = `${open}:${diagnostics?.text ?? ''}`;
  const [seededFor, setSeededFor] = useState(feedbackSeed);
  if (seededFor !== feedbackSeed) {
    setSeededFor(feedbackSeed);
    setCopied(false);
  }
  if (!open) return null;
  const renderFields = (values: readonly LogDetailField[]) => (
    <dl className="nb-facts nb-facts--inline log-detail-facts">
      {values.map((field) => (
        <div className={field.wide ? 'log-detail-row--wide' : undefined} key={field.label}>
          <dt>{field.label}</dt>
          <dd>{field.value}</dd>
        </div>
      ))}
    </dl>
  );
  const technical = fields.filter((field) => field.technical);
  return (
    <Drawer open={open} onClose={onClose} title={title} closeLabel={t('common.close')}>
      <div className="log-detail-content">
        {result}
        {renderFields(fields.filter((field) => !field.technical))}
        {technical.length || diagnostics ? (
          <Fold plain title={t('common.operations.logs.presentation.technical')}>
            {renderFields(technical)}
            {diagnostics ? (
              <div className="log-drawer-diagnostics">
                <h3>{diagnostics.label}</h3>
                <pre>{diagnostics.text || t('common.notAvailable')}</pre>
                <button
                  type="button"
                  className="btn btn-secondary"
                  onClick={() => void copyText(diagnostics.text).then((ok) => setCopied(ok))}
                >
                  {copied
                    ? t('common.copied')
                    : t('common.operations.logs.presentation.copyForAdmin')}
                </button>
                <span className="visually-hidden" aria-live="polite">
                  {copied ? t('common.copied') : ''}
                </span>
              </div>
            ) : null}
          </Fold>
        ) : null}
      </div>
    </Drawer>
  );
}
