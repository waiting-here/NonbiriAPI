import { useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { useNavigate } from 'react-router';
import { useTranslation } from 'react-i18next';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import {
  Card,
  ErrorState,
  LoadingState,
  PageHeader,
} from '@shared/components/States';
import { apiFetch } from '@shared/query/http';
import { asRecord } from '@shared/query/normalize';
import { useUserMe, useUserSession, useUserUsage, userKeys } from '../data';
import { UserPageGate } from '../components/UserPageGate';

function number(value: number): string {
  return value.toLocaleString();
}

function removeSensitiveFields(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(removeSensitiveFields);
  const record = asRecord(value);
  if (!record) return value;
  const output: Record<string, unknown> = {};
  for (const [key, nested] of Object.entries(record)) {
    const normalizedKey = key.toLowerCase().replace(/-/g, '_');
    const sensitiveKey =
      normalizedKey === 'secret' ||
      normalizedKey === 'password' ||
      normalizedKey === 'authorization' ||
      normalizedKey === 'token' ||
      normalizedKey === 'access_token' ||
      normalizedKey === 'refresh_token' ||
      normalizedKey === 'session_token' ||
      normalizedKey === 'api_key' ||
      normalizedKey === 'private_key' ||
      normalizedKey.endsWith('_secret') ||
      normalizedKey.endsWith('_password');
    if (sensitiveKey) continue;
    output[key] = removeSensitiveFields(nested);
  }
  return output;
}

function downloadExport(payload: unknown): void {
  const safePayload = removeSensitiveFields(payload);
  const blob = new Blob([JSON.stringify(safePayload, null, 2)], { type: 'application/json' });
  const url = URL.createObjectURL(blob);
  const link = document.createElement('a');
  link.href = url;
  link.download = 'nonbiriapi-account-export.json';
  link.rel = 'noopener';
  document.body.appendChild(link);
  link.click();
  link.remove();
  URL.revokeObjectURL(url);
}

function AccountContent() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const session = useUserSession();
  const me = useUserMe(true);
  const usage = useUserUsage(true);
  const [elevationStarted, setElevationStarted] = useState(false);
  const [exportBusy, setExportBusy] = useState(false);
  const [exportError, setExportError] = useState<unknown>(null);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [deleteWord, setDeleteWord] = useState('');
  const [deleteBusy, setDeleteBusy] = useState(false);
  const [deleteError, setDeleteError] = useState<unknown>(null);

  const exportAccount = async () => {
    setExportError(null);
    setExportBusy(true);
    try {
      const payload = await apiFetch<unknown>('/api/account/export', { method: 'POST' });
      if (payload === undefined) throw new Error(t('common.invalidResponse'));
      downloadExport(payload);
    } catch (error) {
      setExportError(error);
    } finally {
      setExportBusy(false);
    }
  };

  const deleteAccount = async () => {
    setDeleteError(null);
    if (deleteWord !== 'DELETE') {
      setDeleteError(new Error(t('user.account.deleteWordError')));
      return;
    }
    setDeleteBusy(true);
    try {
      await apiFetch<void>('/api/account/delete', {
        method: 'POST',
        json: { confirm: 'DELETE' },
      });
      queryClient.removeQueries({ queryKey: userKeys.all });
      setDeleteOpen(false);
      setDeleteWord('');
      navigate('/');
    } catch (error) {
      setDeleteError(error);
    } finally {
      setDeleteBusy(false);
    }
  };

  const user = me.data ?? session.data?.user;

  return (
    <div className="page">
      <PageHeader
        eyebrow={t('app.name')}
        title={t('user.account.title')}
        description={t('user.account.description')}
      />
      <div className="split-grid">
        <Card>
          <h2>{t('user.account.profileTitle')}</h2>
          {me.isPending ? (
            <LoadingState />
          ) : me.error ? (
            <ErrorState error={me.error} onRetry={() => void me.refetch()} />
          ) : user ? (
            <dl className="detail-grid">
              <div className="detail-row">
                <dt>{t('user.account.id')}</dt>
                <dd><span className="mono">{user.id}</span></dd>
              </div>
              <div className="detail-row">
                <dt>{t('user.account.username')}</dt>
                <dd>{user.username}</dd>
              </div>
              <div className="detail-row">
                <dt>{t('user.account.created')}</dt>
                <dd>{user.created_at}</dd>
              </div>
            </dl>
          ) : null}
        </Card>
        <Card>
          <h2>{t('user.account.usageTitle')}</h2>
          {usage.isPending ? (
            <LoadingState />
          ) : usage.error ? (
            <ErrorState error={usage.error} onRetry={() => void usage.refetch()} />
          ) : (
            <dl className="detail-grid">
              <div className="detail-row"><dt>{t('user.account.requests')}</dt><dd>{number(usage.data.total_requests)}</dd></div>
              <div className="detail-row"><dt>{t('user.account.promptTokens')}</dt><dd>{number(usage.data.total_prompt_tokens)}</dd></div>
              <div className="detail-row"><dt>{t('user.account.completionTokens')}</dt><dd>{number(usage.data.total_completion_tokens)}</dd></div>
              <div className="detail-row"><dt>{t('user.account.unknownUsage')}</dt><dd>{number(usage.data.total_unknown_usage_requests)}</dd></div>
            </dl>
          )}
        </Card>
      </div>

      <Card className="danger-zone">
        <h2>{t('user.account.securityTitle')}</h2>
        <p>{t('user.account.elevateBody')}</p>
        <form
          method="post"
          action="/api/auth/elevate"
          onSubmit={() => setElevationStarted(true)}
        >
          <button type="submit" className="btn btn-secondary">
            {t('user.account.startElevation')}
          </button>
        </form>
        {elevationStarted ? <p className="inline-success" role="status">{t('user.account.elevationStarted')}</p> : null}
        <div className="security-actions">
          <div>
            <h3>{t('user.account.export')}</h3>
            <p className="muted">{t('user.account.exportBody')}</p>
            {exportError ? <ErrorState error={exportError} /> : null}
            <button type="button" className="btn btn-primary" onClick={() => void exportAccount()} disabled={exportBusy}>
              {exportBusy ? t('common.working') : t('user.account.export')}
            </button>
          </div>
          <div>
            <h3>{t('user.account.delete')}</h3>
            <p className="muted">{t('user.account.deleteBody')}</p>
            <button
              type="button"
              className="btn btn-danger"
              onClick={() => {
                setDeleteWord('');
                setDeleteError(null);
                setDeleteOpen(true);
              }}
            >
              {t('user.account.delete')}
            </button>
          </div>
        </div>
      </Card>

      <ConfirmDialog
        open={deleteOpen}
        title={t('user.account.deleteTitle')}
        description={t('user.account.deleteBody')}
        confirmLabel={t('user.account.deleteConfirm')}
        danger
        busy={deleteBusy}
        onCancel={() => {
          if (!deleteBusy) {
            setDeleteOpen(false);
            setDeleteWord('');
            setDeleteError(null);
          }
        }}
        onConfirm={() => void deleteAccount()}
      >
        <label>
          <span>{t('user.account.deleteWord')}</span>
          <input
            value={deleteWord}
            onChange={(event) => setDeleteWord(event.target.value)}
            autoComplete="off"
            spellCheck={false}
            maxLength={6}
            aria-invalid={deleteWord.length > 0 && deleteWord !== 'DELETE'}
          />
        </label>
        {deleteError ? <ErrorState error={deleteError} /> : null}
      </ConfirmDialog>
    </div>
  );
}

export function AccountPage() {
  return (
    <UserPageGate>
      <AccountContent />
    </UserPageGate>
  );
}
