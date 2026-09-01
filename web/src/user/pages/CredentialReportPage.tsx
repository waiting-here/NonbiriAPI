import { useEffect, useMemo, useRef, useState, type FormEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { Card, ErrorState, LoadingState, PageHeader } from '@shared/components/States';
import { operationKey } from '@shared/operations/api';
import { usePublicConfig } from '@shared/query/publicConfig';
import {
  shouldRetainCredentialReportIntent,
  submitCredentialReport,
  type CredentialReportInput,
} from '../features/operations/data';
import '@shared/operations/operations.css';

const CONNECTOR_LABEL_KEYS: Record<CredentialReportInput['connector_type'], string> = {
  'openai-compatible': 'user.report.connector.openaiCompatible',
  'anthropic-compatible': 'user.report.connector.anthropicCompatible',
};

function validBaseURL(value: string): boolean {
  if (new TextEncoder().encode(value).byteLength > 4_096) return false;
  try {
    const parsed = new URL(value);
    return (parsed.protocol === 'https:' || parsed.protocol === 'http:') && Boolean(parsed.hostname) && !parsed.username && !parsed.password;
  } catch {
    return false;
  }
}

export function CredentialReportPage() {
  const { t } = useTranslation();
  const config = usePublicConfig();
  const [connector, setConnector] = useState<CredentialReportInput['connector_type']>('openai-compatible');
  const [baseURL, setBaseURL] = useState('');
  const [secret, setSecret] = useState('');
  const [note, setNote] = useState('');
  const intentKey = useRef<string | null>(null);
  const [state, setState] = useState<'idle' | 'submitting' | 'accepted'>('idle');
  const [error, setError] = useState<unknown>(null);
  const [validation, setValidation] = useState(false);

  useEffect(() => {
    const clear = () => {
      intentKey.current = null;
      setSecret('');
    };
    window.addEventListener('pagehide', clear);
    return () => window.removeEventListener('pagehide', clear);
  }, []);

  const payload = useMemo<CredentialReportInput>(() => ({
    connector_type: connector,
    base_url: baseURL.trim(),
    secret,
    note,
  }), [baseURL, connector, note, secret]);

  const clearForm = () => {
    intentKey.current = null;
    setBaseURL(''); setSecret(''); setNote(''); setError(null); setValidation(false); setState('idle');
  };

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setValidation(false); setError(null);
    if (config.data?.maintenanceMode) return;
    if (!validBaseURL(payload.base_url) || !secret || new TextEncoder().encode(secret).byteLength > 65_536
      || Array.from(note).length > 2_048) {
      setValidation(true);
      return;
    }
    const currentIntent = intentKey.current ?? operationKey();
    intentKey.current = currentIntent;
    setState('submitting');
    try {
      await submitCredentialReport(payload, currentIntent);
      intentKey.current = null;
      setSecret(''); setNote(''); setBaseURL(''); setState('accepted');
    } catch (submitError) {
      setError(submitError);
      setState('idle');
      if (!shouldRetainCredentialReportIntent(submitError)) {
        // Deterministic rejection or 409 must never be retried automatically.
        intentKey.current = null;
      }
    }
  };

  return (
    <div className="page ops-stack">
      <PageHeader
        eyebrow={t('user.report.eyebrow')}
        title={t('user.report.title')}
        description={t('user.report.description')}
      />
      {config.isPending ? <LoadingState /> : config.error ? <ErrorState error={config.error} onRetry={() => void config.refetch()} /> : (
        <Card className="ops-stack">
          {config.data.maintenanceMode ? <p className="inline-notice" role="status">{t('user.report.maintenanceUnavailable')}</p> : null}
          {state === 'accepted' ? (
            <div role="status" className="ops-stack"><h2>{t('user.report.acceptedTitle')}</h2>
              <p>{t('user.report.acceptedBody')}</p>
              <button type="button" className="btn btn-secondary" onClick={() => setState('idle')}>{t('user.report.submitAnother')}</button>
            </div>
          ) : (
            <form className="ops-stack" onSubmit={submit}>
              <label className="ops-form-field">{t('user.report.connectorLabel')}
                <select value={connector} onChange={(event) => { intentKey.current = null; setConnector(event.target.value as CredentialReportInput['connector_type']); }}>
                  <option value="openai-compatible">{t(CONNECTOR_LABEL_KEYS['openai-compatible'])}</option>
                  <option value="anthropic-compatible">{t(CONNECTOR_LABEL_KEYS['anthropic-compatible'])}</option>
                </select>
              </label>
              <label className="ops-form-field">{t('user.report.baseUrlLabel')}
                <input type="url" required maxLength={4096} value={baseURL} onChange={(event) => { intentKey.current = null; setBaseURL(event.target.value); }} autoComplete="url" />
              </label>
              <label className="ops-form-field">{t('user.report.secretLabel')}
                <textarea className="ops-secret" required rows={4} value={secret} onChange={(event) => { intentKey.current = null; setSecret(event.target.value); }} autoComplete="off" spellCheck={false} />
              </label>
              <label className="ops-form-field">{t('user.report.noteLabel')}
                <textarea rows={5} maxLength={2048} value={note} onChange={(event) => { intentKey.current = null; setNote(event.target.value); }} />
              </label>
              <p className="inline-notice">{t('user.report.noteWarning')}</p>
              {validation ? <p className="field-error" role="alert">{t('user.report.validationError')}</p> : null}
              {error ? <ErrorState error={error} /> : null}
              <div className="ops-actions">
                <button className="btn btn-primary" type="submit" disabled={state === 'submitting' || config.data.maintenanceMode}>{state === 'submitting' ? t('user.report.submitting') : t('user.report.submit')}</button>
                <button className="btn btn-secondary" type="button" onClick={clearForm}>{t('user.report.cancelAndClear')}</button>
              </div>
            </form>
          )}
        </Card>
      )}
    </div>
  );
}
