import { useState, type FormEvent } from 'react';
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
import { asRecord, hasControlCharacters } from '@shared/query/normalize';
import { useUserMe, useUserSession, useUserUsage, useUpdateUserProfile, userKeys } from '../data';
import { UserPageGate } from '../components/UserPageGate';

function number(value: number): string {
  return value.toLocaleString();
}

function elevatedTokenFromCookie(): string | undefined {
  const cookie = document.cookie
    .split(';')
    .map((part) => part.trim())
    .find((part) => part.startsWith('nb_elevated='));
  if (!cookie) return undefined;
  const raw = cookie.slice('nb_elevated='.length);
  let token: string;
  try {
    token = decodeURIComponent(raw);
  } catch {
    return undefined;
  }
  return /^[A-Za-z0-9._-]{8,512}$/.test(token) ? token : undefined;
}

function elevatedHeaders(): Record<string, string> | undefined {
  const token = elevatedTokenFromCookie();
  return token ? { 'X-Elevated-Token': token } : undefined;
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

function PreferencesForm({ initialLang, initialRpm }: { initialLang: 'zh' | 'en'; initialRpm?: number }) {
  const { t } = useTranslation();
  const updateProfile = useUpdateUserProfile();
  const [lang, setLang] = useState<'zh' | 'en'>(initialLang);
  const [rpm, setRpm] = useState(initialRpm === undefined ? '' : String(initialRpm));
  const [validationError, setValidationError] = useState('');

  const save = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setValidationError('');
    let rpmLimit: number | null = null;
    if (rpm.trim()) {
      if (!/^\d+$/.test(rpm.trim())) {
        setValidationError(t('user.account.preferencesRpmInvalid'));
        return;
      }
      const parsed = Number(rpm.trim());
      if (!Number.isSafeInteger(parsed) || parsed < 1) {
        setValidationError(t('user.account.preferencesRpmInvalid'));
        return;
      }
      rpmLimit = parsed;
    }
    updateProfile.mutate({ lang, rpm_limit: rpmLimit });
  };

  return (
    <form className="limit-form" onSubmit={save} noValidate>
      <div className="limit-fields">
        <label>
          <span>{t('user.account.preferencesLang')}</span>
          <select value={lang} onChange={(event) => setLang(event.target.value === 'en' ? 'en' : 'zh')} aria-label={t('user.account.preferencesLang')}>
            <option value="zh">中文</option>
            <option value="en">EN</option>
          </select>
        </label>
        <label>
          <span>{t('user.account.preferencesRpm')}</span>
          <input
            type="number"
            min="1"
            step="1"
            value={rpm}
            onChange={(event) => setRpm(event.target.value)}
            placeholder={t('user.account.preferencesRpmPlaceholder')}
            aria-label={t('user.account.preferencesRpm')}
          />
        </label>
      </div>
      <small className="muted">{t('user.account.preferencesRpmHint')}</small>
      {validationError ? <p className="field-error" role="alert">{validationError}</p> : null}
      {updateProfile.error ? <ErrorState error={updateProfile.error} /> : null}
      {updateProfile.isSuccess ? <p className="inline-success" role="status">{t('user.account.preferencesSaved')}</p> : null}
      <div className="table-actions">
        <button type="submit" className="btn btn-quiet" disabled={updateProfile.isPending}>
          {updateProfile.isPending ? t('common.working') : t('user.account.preferencesSave')}
        </button>
        <button
          type="button"
          className="btn btn-link"
          onClick={() => setRpm('')}
        >
          {t('user.account.preferencesRestore')}
        </button>
      </div>
    </form>
  );
}

function PreferencesCard() {
  const { t } = useTranslation();
  const me = useUserMe(true);
  return (
    <Card>
      <h2>{t('user.account.preferencesTitle')}</h2>
      <p className="muted">{t('user.account.preferencesBody')}</p>
      {me.isPending ? (
        <LoadingState />
      ) : me.error ? (
        <ErrorState error={me.error} onRetry={() => void me.refetch()} />
      ) : (
        // Remount with fresh server values after every save (the profile
        // query is invalidated by the mutation), so the form always shows
        // the server-authoritative, clamped state.
        <PreferencesForm
          key={`${me.data.id}-${me.data.lang}-${me.data.rpm_limit ?? 'default'}`}
          initialLang={me.data.lang}
          initialRpm={me.data.rpm_limit}
        />
      )}
    </Card>
  );
}

function AccountContent() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const session = useUserSession();
  const me = useUserMe(true);
  const usage = useUserUsage(true);
  const [elevationStarted, setElevationStarted] = useState(false);
  const [elevationBusy, setElevationBusy] = useState(false);
  const [elevationError, setElevationError] = useState<unknown>(null);
  const [exportOpen, setExportOpen] = useState(false);
  const [exportBusy, setExportBusy] = useState(false);
  const [exportError, setExportError] = useState<unknown>(null);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [deleteWord, setDeleteWord] = useState('');
  const [deleteBusy, setDeleteBusy] = useState(false);
  const [deleteError, setDeleteError] = useState<unknown>(null);

  const startElevation = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setElevationError(null);
    setElevationBusy(true);
    try {
      const payload = await apiFetch<unknown>('/api/auth/elevate', { method: 'POST' });
      const record = asRecord(payload);
      const location = record?.authorization_url;
      if (
        typeof location !== 'string' ||
        location.length > 2048 ||
        hasControlCharacters(location)
      ) {
        throw new Error(t('user.account.elevationUnavailable'));
      }
      const authorizationUrl = new URL(location);
      if (authorizationUrl.protocol !== 'https:') {
        throw new Error(t('user.account.elevationUnavailable'));
      }
      setElevationStarted(true);
      window.location.assign(authorizationUrl.toString());
    } catch (error) {
      setElevationError(error);
    } finally {
      setElevationBusy(false);
    }
  };

  const exportAccount = async () => {
    setExportError(null);
    const headers = elevatedHeaders();
    if (!headers) {
      setExportError(new Error(t('user.account.elevationRequired')));
      return;
    }
    setExportBusy(true);
    try {
      const payload = await apiFetch<unknown>('/api/account/export', {
        method: 'POST',
        headers,
      });
      if (payload === undefined) throw new Error(t('common.invalidResponse'));
      downloadExport(payload);
      setExportOpen(false);
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
    const headers = elevatedHeaders();
    if (!headers) {
      setDeleteError(new Error(t('user.account.elevationRequired')));
      return;
    }
    setDeleteBusy(true);
    try {
      await apiFetch<void>('/api/account/delete', {
        method: 'POST',
        headers,
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

      <PreferencesCard />

      <Card className="danger-zone">
        <h2>{t('user.account.securityTitle')}</h2>
        <p>{t('user.account.elevateBody')}</p>
        <form onSubmit={(event) => void startElevation(event)} noValidate>
          <button type="submit" className="btn btn-secondary" disabled={elevationBusy}>
            {elevationBusy ? t('common.working') : t('user.account.startElevation')}
          </button>
        </form>
        {elevationError ? <ErrorState error={elevationError} /> : null}
        {elevationStarted ? <p className="inline-success" role="status">{t('user.account.elevationStarted')}</p> : null}
        <div className="security-actions">
          <div>
            <h3>{t('user.account.export')}</h3>
            <p className="muted">{t('user.account.exportBody')}</p>
            {exportError ? <ErrorState error={exportError} /> : null}
            <button
              type="button"
              className="btn btn-primary"
              onClick={() => {
                setExportError(null);
                setExportOpen(true);
              }}
              disabled={exportBusy}
            >
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
        open={exportOpen}
        title={t('user.account.exportTitle')}
        description={t('user.account.exportBody')}
        confirmLabel={t('user.account.exportConfirm')}
        busy={exportBusy}
        onCancel={() => {
          if (!exportBusy) {
            setExportOpen(false);
            setExportError(null);
          }
        }}
        onConfirm={() => void exportAccount()}
      >
        {exportError ? <ErrorState error={exportError} /> : null}
      </ConfirmDialog>

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
