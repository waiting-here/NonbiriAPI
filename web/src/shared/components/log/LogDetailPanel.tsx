import { Button } from '@shared/components/ui/Button';
import { useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { copyText } from '@shared/utils/clipboard';
import { Fold } from '@shared/components/ui/Fold';
import './logs.css';

export interface LogDetailField {
  label: string;
  value: ReactNode;
  wide?: boolean;
  technical?: boolean;
}
interface LogDetailPanelProps {
  fields: readonly LogDetailField[];
  diagnostics?: { label: string; text: string };
  result?: ReactNode;
  headers?: ReactNode;
}
export function LogDetailPanel({ fields, diagnostics, result, headers }: LogDetailPanelProps) {
  const { t } = useTranslation();
  const [copied, setCopied] = useState(false);
  const feedbackSeed = diagnostics?.text ?? '';
  const [seededFor, setSeededFor] = useState(feedbackSeed);
  if (seededFor !== feedbackSeed) {
    setSeededFor(feedbackSeed);
    setCopied(false);
  }
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
    <>
      <div className="log-detail-content">
        {result}
        {diagnostics && (
          <Button
            type="button"
            variant="secondary"
            size="sm"
            onClick={() => void copyText(diagnostics.text).then((ok) => setCopied(ok))}
          >
            {copied ? t('common.copied') : t('logs.copyVisibleSummary')}
          </Button>
        )}
        {headers}
        {renderFields(fields.filter((field) => !field.technical))}
        {technical.length || diagnostics ? (
          <Fold plain title={t('common.operations.logs.presentation.technical')}>
            {renderFields(technical)}
            {diagnostics ? (
              <div className="log-detail-diagnostics">
                <h3>{diagnostics.label}</h3>
                <pre>{diagnostics.text || t('common.notAvailable')}</pre>
              </div>
            ) : null}
          </Fold>
        ) : null}
      </div>
    </>
  );
}
