import { useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { Card, EmptyState, ErrorState, LoadingState, PageHeader } from '@shared/components/States';
import { isNotFoundError, apiFetch } from '@shared/query/http';
import { normalizeSecret, useCallerKey, userKeys } from '../data';
import { UserPageGate } from '../components/UserPageGate';

function CallerKeyContent() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const metadata = useCallerKey(true);
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [oneTimeSecret, setOneTimeSecret] = useState('');
  const [requestError, setRequestError] = useState<unknown>(null);
  const [busy, setBusy] = useState(false);
  const [copied, setCopied] = useState(false);
  const hasMetadata = Boolean(metadata.data);

  const closeSecret = () => {
    setOneTimeSecret('');
    setCopied(false);
  };

  const generate = async () => {
    setRequestError(null);
    setCopied(false);
    setBusy(true);
    try {
      const response = await apiFetch<unknown>('/api/caller-key/regenerate', { method: 'POST' });
      const secret = normalizeSecret(response).secret;
      // The plaintext exists only in this component until the user closes it.
      setOneTimeSecret(secret);
      await queryClient.invalidateQueries({ queryKey: userKeys.callerKey });
      setConfirmOpen(false);
    } catch (error) {
      setRequestError(error);
    } finally {
      setBusy(false);
    }
  };

  const copy = async () => {
    if (!oneTimeSecret || !navigator.clipboard) {
      setRequestError(new Error(t('common.copyFailed')));
      return;
    }
    try {
      await navigator.clipboard.writeText(oneTimeSecret);
      setCopied(true);
      setRequestError(null);
    } catch (error) {
      setRequestError(error);
    }
  };

  return (
    <div className="page">
      <PageHeader
        eyebrow={t('app.name')}
        title={t('user.keys.title')}
        description={t('user.keys.description')}
      />
      {requestError ? <ErrorState error={requestError} /> : null}
      {oneTimeSecret ? (
        <section className="card secret-panel" aria-live="polite">
          <h2>{t('user.keys.oneTimeTitle')}</h2>
          <p>{t('user.keys.oneTimeBody')}</p>
          <span className="secret-label">{t('user.keys.secretLabel')}</span>
          <code className="secret-value">{oneTimeSecret}</code>
          <p className="muted">{t('user.keys.copyWarning')}</p>
          <div className="form-actions">
            <button type="button" className="btn btn-secondary" onClick={() => void copy()}>
              {copied ? t('user.keys.copyDone') : t('user.keys.copyKey')}
            </button>
            <button type="button" className="btn btn-danger" onClick={closeSecret}>
              {t('user.keys.closeNotice')}
            </button>
          </div>
        </section>
      ) : null}
      <Card>
        <div className="card-title-row">
          <h2>{t('user.keys.metadataTitle')}</h2>
          <button type="button" className="btn btn-primary" onClick={() => setConfirmOpen(true)}>
            {hasMetadata ? t('user.keys.regenerate') : t('user.keys.generate')}
          </button>
        </div>
        {metadata.isPending ? (
          <LoadingState />
        ) : metadata.error && !isNotFoundError(metadata.error) ? (
          <ErrorState error={metadata.error} onRetry={() => void metadata.refetch()} />
        ) : metadata.data ? (
          <dl className="detail-grid">
            <div className="detail-row">
              <dt>{t('user.keys.display')}</dt>
              <dd><span className="mono">{metadata.data.display}</span></dd>
            </div>
            <div className="detail-row">
              <dt>{t('user.keys.created')}</dt>
              <dd>{metadata.data.created_at}</dd>
            </div>
            <div className="detail-row">
              <dt>{t('user.keys.updated')}</dt>
              <dd>{metadata.data.updated_at}</dd>
            </div>
          </dl>
        ) : (
          <EmptyState title={t('user.keys.noKey')} body={t('user.keys.noKeyBody')} />
        )}
      </Card>
      <ConfirmDialog
        open={confirmOpen}
        title={t('user.keys.regenerateTitle')}
        description={t('user.keys.regenerateBody')}
        confirmLabel={hasMetadata ? t('user.keys.regenerate') : t('user.keys.generate')}
        danger={hasMetadata}
        busy={busy}
        onCancel={() => {
          if (!busy) {
            setConfirmOpen(false);
            setRequestError(null);
          }
        }}
        onConfirm={() => void generate()}
      />
    </div>
  );
}

export function KeysPage() {
  return (
    <UserPageGate>
      <CallerKeyContent />
    </UserPageGate>
  );
}
