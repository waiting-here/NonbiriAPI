import { Field, Fields, FormActions } from '@shared/components/ui';
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
  'ai-sdk-gateway-v3': 'common.gatewayName',
  'ai-sdk-gateway-v4': 'common.gatewayV4Name',
  'google-gemini': 'common.connectorGemini',
  'openai-responses': 'common.connectorResponses',
  'openai-decisions': 'common.connectorDecisions',
  'cohere-rerank': 'common.connectorRerank.cohere',
  'jina-rerank': 'common.connectorRerank.jina',
  'siliconflow-rerank': 'common.connectorRerank.siliconflow',
  'voyage-rerank': 'common.connectorRerank.voyage',
  'typesafe-systemone': 'common.connectorSystemOne',
  'openai-chat-completions': 'common.connectorChatCompletions',
};

function validBaseURL(value: string): boolean {
  if (new TextEncoder().encode(value).byteLength > 4_096) return false;
  try {
    const parsed = new URL(value);
    return (
      (parsed.protocol === 'https:' || parsed.protocol === 'http:') &&
      Boolean(parsed.hostname) &&
      !parsed.username &&
      !parsed.password &&
      !/[?#]/.test(value)
    );
  } catch {
    return false;
  }
}

export function CredentialReportPage() {
  const { t } = useTranslation();
  const config = usePublicConfig();
  const [connector, setConnector] =
    useState<CredentialReportInput['connector_type']>('openai-compatible');
  const [baseURL, setBaseURL] = useState('');
  const [secret, setSecret] = useState('');
  const [note, setNote] = useState('');
  const intentKey = useRef<string | null>(null);
  const [state, setState] = useState<'idle' | 'submitting' | 'accepted'>('idle');
  const [error, setError] = useState<unknown>(null);
  const [validation, setValidation] = useState(false);
  const submitting = state === 'submitting';

  useEffect(() => {
    const clear = () => {
      intentKey.current = null;
      setSecret('');
    };
    window.addEventListener('pagehide', clear);
    return () => window.removeEventListener('pagehide', clear);
  }, []);

  const payload = useMemo<CredentialReportInput>(
    () => ({
      connector_type: connector,
      base_url: baseURL.trim(),
      secret,
      note,
    }),
    [baseURL, connector, note, secret],
  );
  const fields = {
    baseURL: !validBaseURL(payload.base_url) ? 'user.report.invalidUrl' : undefined,
    secret:
      !secret || new TextEncoder().encode(secret).byteLength > 65_536
        ? 'user.report.invalidSecret'
        : undefined,
    note:
      Array.from(note).length > 2_048 || /\p{Cc}/u.test(note)
        ? 'user.report.invalidNote'
        : undefined,
  };

  const clearForm = () => {
    intentKey.current = null;
    setBaseURL('');
    setSecret('');
    setNote('');
    setError(null);
    setValidation(false);
    setState('idle');
  };

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (submitting || config.data?.maintenanceMode) return;
    setError(null);
    const firstInvalid = Object.entries(fields).find(([, message]) => message);
    if (firstInvalid) {
      setValidation(true);
      const field = event.currentTarget.elements.namedItem(firstInvalid[0]);
      if (field instanceof HTMLElement) field.focus();
      return;
    }
    setValidation(false);
    const currentIntent = intentKey.current ?? operationKey();
    intentKey.current = currentIntent;
    setState('submitting');
    try {
      await submitCredentialReport(payload, currentIntent);
      intentKey.current = null;
      setSecret('');
      setNote('');
      setBaseURL('');
      setState('accepted');
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
    <div className="page ops-stack ops-readable-page">
      <PageHeader
        eyebrow={t('user.report.eyebrow')}
        title={t('user.report.title')}
        description={t('user.report.description')}
      />
      {config.isPending ? (
        <LoadingState />
      ) : config.error ? (
        <ErrorState error={config.error} onRetry={() => void config.refetch()} />
      ) : (
        <Card className="ops-stack">
          {config.data.maintenanceMode ? (
            <p className="inline-notice" role="status">
              {t('user.report.maintenanceUnavailable')}
            </p>
          ) : null}
          {state === 'accepted' ? (
            <div role="status" className="ops-stack">
              <h2>{t('user.report.acceptedTitle')}</h2>
              <p>{t('user.report.acceptedBody')}</p>
              <button
                type="button"
                className="nb-btn nb-btn--secondary"
                onClick={() => setState('idle')}
              >
                {t('user.report.submitAnother')}
              </button>
            </div>
          ) : (
            <form className="ops-stack" noValidate onSubmit={submit} aria-busy={submitting}>
              <Fields>
                <Field label={t('user.report.connectorLabel')}>
                  {(props) => (
                    <select
                      {...props}
                      disabled={submitting}
                      value={connector}
                      onChange={(event) => {
                        intentKey.current = null;
                        setConnector(event.target.value as CredentialReportInput['connector_type']);
                      }}
                    >
                      <option value="openai-compatible">
                        {t(CONNECTOR_LABEL_KEYS['openai-compatible'])}
                      </option>
                      <option value="anthropic-compatible">
                        {t(CONNECTOR_LABEL_KEYS['anthropic-compatible'])}
                      </option>
                      <option value="ai-sdk-gateway-v3">
                        {t(CONNECTOR_LABEL_KEYS['ai-sdk-gateway-v3'])}
                      </option>
                      <option value="ai-sdk-gateway-v4">
                        {t(CONNECTOR_LABEL_KEYS['ai-sdk-gateway-v4'])}
                      </option>
                      <option value="google-gemini">
                        {t(CONNECTOR_LABEL_KEYS['google-gemini'])}
                      </option>
                      <option value="openai-responses">
                        {t(CONNECTOR_LABEL_KEYS['openai-responses'])}
                      </option>
                      <option value="openai-decisions">
                        {t(CONNECTOR_LABEL_KEYS['openai-decisions'])}
                      </option>
                      <option value="cohere-rerank">
                        {t(CONNECTOR_LABEL_KEYS['cohere-rerank'])}
                      </option>
                      <option value="jina-rerank">{t(CONNECTOR_LABEL_KEYS['jina-rerank'])}</option>
                      <option value="siliconflow-rerank">
                        {t(CONNECTOR_LABEL_KEYS['siliconflow-rerank'])}
                      </option>
                      <option value="voyage-rerank">
                        {t(CONNECTOR_LABEL_KEYS['voyage-rerank'])}
                      </option>
                      <option value="typesafe-systemone">
                        {t(CONNECTOR_LABEL_KEYS['typesafe-systemone'])}
                      </option>
                      <option value="openai-chat-completions">
                        {t(CONNECTOR_LABEL_KEYS['openai-chat-completions'])}
                      </option>
                    </select>
                  )}
                </Field>
                <Field
                  label={t('user.report.baseUrlLabel')}
                  error={validation && fields.baseURL && t(fields.baseURL)}
                >
                  {(props) => (
                    <input
                      {...props}
                      name="baseURL"
                      readOnly={submitting}
                      type="url"
                      placeholder="https://api.example.com/v1"
                      required
                      value={baseURL}
                      onChange={(event) => {
                        intentKey.current = null;
                        setBaseURL(event.target.value);
                      }}
                      autoComplete="url"
                    />
                  )}
                </Field>
                <Field
                  label={t('user.report.secretLabel')}
                  span
                  error={validation && fields.secret && t(fields.secret)}
                >
                  {(props) => (
                    <textarea
                      {...props}
                      name="secret"
                      readOnly={submitting}
                      className="ops-secret"
                      required
                      rows={4}
                      value={secret}
                      onChange={(event) => {
                        intentKey.current = null;
                        setSecret(event.target.value);
                      }}
                      autoComplete="off"
                      spellCheck={false}
                    />
                  )}
                </Field>
                <Field
                  label={t('user.report.noteLabel')}
                  span
                  help={t('user.report.noteWarning')}
                  error={validation && fields.note && t(fields.note)}
                >
                  {(props) => (
                    <textarea
                      {...props}
                      name="note"
                      readOnly={submitting}
                      rows={3}
                      value={note}
                      onChange={(event) => {
                        intentKey.current = null;
                        setNote(event.target.value);
                      }}
                    />
                  )}
                </Field>
              </Fields>
              {error ? <ErrorState error={error} /> : null}
              <FormActions>
                <button
                  className="nb-btn nb-btn--primary"
                  type="submit"
                  disabled={state === 'submitting' || config.data.maintenanceMode}
                >
                  {state === 'submitting' ? t('user.report.submitting') : t('user.report.submit')}
                </button>
                <button
                  className="nb-btn nb-btn--secondary"
                  type="button"
                  disabled={submitting}
                  onClick={clearForm}
                >
                  {t('user.report.cancelAndClear')}
                </button>
              </FormActions>
            </form>
          )}
        </Card>
      )}
    </div>
  );
}
