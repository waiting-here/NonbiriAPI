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
  const { i18n } = useTranslation();
  const zh = i18n.resolvedLanguage?.toLowerCase().startsWith('zh') ?? false;
  const config = usePublicConfig();
  const [connector, setConnector] = useState<CredentialReportInput['connector_type']>('openai-compatible');
  const [baseURL, setBaseURL] = useState('');
  const [secret, setSecret] = useState('');
  const [note, setNote] = useState('');
  const intentKey = useRef<string | null>(null);
  const [state, setState] = useState<'idle' | 'submitting' | 'accepted'>('idle');
  const [error, setError] = useState<unknown>(null);
  const [validation, setValidation] = useState('');

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
    setBaseURL(''); setSecret(''); setNote(''); setError(null); setValidation(''); setState('idle');
  };

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setValidation(''); setError(null);
    if (config.data?.maintenanceMode) return;
    if (!validBaseURL(payload.base_url) || !secret || new TextEncoder().encode(secret).byteLength > 65_536
      || Array.from(note).length > 2_048) {
      setValidation(zh ? '请检查端点 URL、凭据和补充说明的长度。' : 'Check the endpoint URL, credential, and note length.');
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
      <PageHeader eyebrow="Security" title={zh ? '举报疑似泄露凭据' : 'Report a suspected leaked credential'}
        description={zh ? '匿名与登录用户使用相同入口；结果不会透露是否命中本站数据。' : 'Anonymous and signed-in reporters use the same endpoint; the result never reveals whether a match exists.'} />
      {config.isPending ? <LoadingState /> : config.error ? <ErrorState error={config.error} onRetry={() => void config.refetch()} /> : (
        <Card className="ops-stack">
          {config.data.maintenanceMode ? <p className="inline-notice" role="status">{zh ? '维护期间暂不接受新举报。' : 'New reports are unavailable during maintenance.'}</p> : null}
          {state === 'accepted' ? (
            <div role="status" className="ops-stack"><h2>{zh ? '举报已受理' : 'Report accepted'}</h2>
              <p>{zh ? '若存在匹配凭据，系统会临时保护并由管理员复核。' : 'If matching credentials exist, temporary protection will be applied and an administrator will review the report.'}</p>
              <button type="button" className="btn btn-secondary" onClick={() => setState('idle')}>{zh ? '提交另一份举报' : 'Submit another report'}</button>
            </div>
          ) : (
            <form className="ops-stack" onSubmit={submit}>
              <label className="ops-form-field">{zh ? 'Connector 类型' : 'Connector type'}
                <select value={connector} onChange={(event) => { intentKey.current = null; setConnector(event.target.value as CredentialReportInput['connector_type']); }}>
                  <option value="openai-compatible">OpenAI-compatible</option>
                  <option value="anthropic-compatible">Anthropic-compatible</option>
                </select>
              </label>
              <label className="ops-form-field">{zh ? '规范端点 URL' : 'Canonical endpoint URL'}
                <input type="url" required maxLength={4096} value={baseURL} onChange={(event) => { intentKey.current = null; setBaseURL(event.target.value); }} autoComplete="url" />
              </label>
              <label className="ops-form-field">{zh ? '疑似泄露的凭据' : 'Suspected leaked credential'}
                <textarea className="ops-secret" required rows={4} value={secret} onChange={(event) => { intentKey.current = null; setSecret(event.target.value); }} autoComplete="off" spellCheck={false} />
              </label>
              <label className="ops-form-field">{zh ? '补充说明（可选）' : 'Additional note (optional)'}
                <textarea rows={5} maxLength={2048} value={note} onChange={(event) => { intentKey.current = null; setNote(event.target.value); }} />
              </label>
              <p className="inline-notice">{zh ? '请勿在补充说明中填写任何凭据。' : 'Do not put credentials in the additional note.'}</p>
              {validation ? <p className="field-error" role="alert">{validation}</p> : null}
              {error ? <ErrorState error={error} /> : null}
              <div className="ops-actions">
                <button className="btn btn-primary" type="submit" disabled={state === 'submitting' || config.data.maintenanceMode}>{state === 'submitting' ? (zh ? '提交中…' : 'Submitting…') : (zh ? '提交举报' : 'Submit report')}</button>
                <button className="btn btn-secondary" type="button" onClick={clearForm}>{zh ? '取消并清除' : 'Cancel and clear'}</button>
              </div>
            </form>
          )}
        </Card>
      )}
    </div>
  );
}
