import { useId, useState, type FormEvent } from 'react';
import { formatDateTime } from '@shared/utils/datetime';
import { useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import {
  Card,
  EmptyState,
  ErrorState,
  LoadingState,
  PageHeader,
  StatusBadge,
} from '@shared/components/States';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { apiFetch } from '@shared/query/http';
import {
  type Endpoint,
  type EndpointKey,
  useEndpointKeys,
  useEndpoints,
  useKeyModels,
  userKeys,
} from '../data';
import { UserPageGate } from '../components/UserPageGate';

interface EndpointFormProps {
  initial?: Endpoint;
  onCancel?: () => void;
  onSaved: () => void;
}

function EndpointForm({ initial, onCancel, onSaved }: EndpointFormProps) {
  const { t } = useTranslation();
  const urlHintId = useId();
  const urlErrorId = useId();
  const [baseUrl, setBaseUrl] = useState(initial?.base_url === '—' ? '' : initial?.base_url ?? '');
  const [note, setNote] = useState(initial?.note ?? '');
  const [enabled, setEnabled] = useState(initial?.enabled ?? true);
  const [validationError, setValidationError] = useState('');
  const [requestError, setRequestError] = useState<unknown>(null);
  const [busy, setBusy] = useState(false);

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setValidationError('');
    setRequestError(null);
    const value = baseUrl.trim();
    try {
      const parsed = new URL(value);
      if (
        !parsed.hostname ||
        (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') ||
        parsed.username ||
        parsed.password ||
        parsed.search ||
        parsed.hash
      ) {
        throw new Error('invalid');
      }
    } catch {
      setValidationError(t('user.endpoints.invalidUrl'));
      return;
    }

    setBusy(true);
    try {
      const path = initial
        ? `/api/endpoints/${encodeURIComponent(initial.id)}`
        : '/api/endpoints';
      await apiFetch<unknown>(path, {
        method: initial ? 'PATCH' : 'POST',
        json: { base_url: value, note: note.trim() || undefined, enabled },
      });
      onSaved();
    } catch (error) {
      setRequestError(error);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Card>
      <div className="card-title-row">
        <h2>{initial ? t('user.endpoints.editTitle') : t('user.endpoints.addTitle')}</h2>
        {initial ? (
          <button type="button" className="btn btn-quiet" onClick={onCancel}>
            {t('user.endpoints.cancel')}
          </button>
        ) : null}
      </div>
      <form onSubmit={submit} noValidate>
        <div className="form-grid">
          <label className="full-width">
            <span>
              {t('user.endpoints.baseUrl')} <em>{t('common.required')}</em>
            </span>
            <input
              type="url"
              value={baseUrl}
              onChange={(event) => setBaseUrl(event.target.value)}
              placeholder="https://api.example.com/v1"
              maxLength={2048}
              required
              aria-invalid={Boolean(validationError)}
              aria-describedby={`${urlHintId}${validationError ? ` ${urlErrorId}` : ''}`}
            />
            <small id={urlHintId} className="muted">
              {t('user.endpoints.baseUrlHint')}
            </small>
            {validationError ? (
              <small id={urlErrorId} className="field-error" role="alert">
                {validationError}
              </small>
            ) : null}
          </label>
          <label>
            <span>{t('user.endpoints.note')}</span>
            <textarea
              value={note}
              onChange={(event) => setNote(event.target.value)}
              placeholder={t('user.endpoints.notePlaceholder')}
              maxLength={512}
            />
          </label>
          <label className="check-field">
            <input
              type="checkbox"
              checked={enabled}
              onChange={(event) => setEnabled(event.target.checked)}
            />
            <span>{t('user.endpoints.enabled')}</span>
          </label>
        </div>
        {requestError ? <ErrorState error={requestError} /> : null}
        <div className="form-actions">
          {initial ? (
            <button type="button" className="btn btn-secondary" onClick={onCancel} disabled={busy}>
              {t('common.cancel')}
            </button>
          ) : null}
          <button type="submit" className="btn btn-primary" disabled={busy}>
            {busy ? t('common.working') : initial ? t('user.endpoints.save') : t('user.endpoints.create')}
          </button>
        </div>
      </form>
    </Card>
  );
}

function AddKeyForm({ endpointId, onCancel, onSaved }: { endpointId: string; onCancel: () => void; onSaved: () => void }) {
  const { t } = useTranslation();
  const secretHintId = useId();
  const [secret, setSecret] = useState('');
  const [keyNote, setKeyNote] = useState('');
  const [requestError, setRequestError] = useState<unknown>(null);
  const [busy, setBusy] = useState(false);

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setRequestError(null);
    const submittedSecret = secret;
    setSecret('');
    if (!submittedSecret) {
      setRequestError(new Error(t('common.formInvalid')));
      return;
    }
    setBusy(true);
    try {
      await apiFetch<unknown>(`/api/endpoints/${encodeURIComponent(endpointId)}/keys`, {
        method: 'POST',
        json: { secret: submittedSecret, note: keyNote.trim() || undefined, enabled: true },
      });
      setKeyNote('');
      onSaved();
    } catch (error) {
      setRequestError(error);
    } finally {
      // The submitted secret is not kept in form state or a query/mutation cache.
      setSecret('');
      setBusy(false);
    }
  };

  return (
    <form className="card compact-card" onSubmit={submit} noValidate>
      <h3>{t('user.endpoints.addKey')}</h3>
      <label>
        <span>{t('user.endpoints.secret')}</span>
        <input
          type="password"
          value={secret}
          onChange={(event) => setSecret(event.target.value)}
          placeholder={t('user.endpoints.secretPlaceholder')}
          autoComplete="new-password"
          maxLength={4096}
          required
          aria-describedby={secretHintId}
        />
        <small id={secretHintId} className="muted">
          {t('user.endpoints.secretHint')}
        </small>
      </label>
      <label>
        <span>{t('user.endpoints.keyNote')}</span>
        <input
          value={keyNote}
          onChange={(event) => setKeyNote(event.target.value)}
          placeholder={t('user.endpoints.keyNotePlaceholder')}
          maxLength={512}
        />
      </label>
      {requestError ? <ErrorState error={requestError} /> : null}
      <div className="form-actions">
        <button type="button" className="btn btn-secondary" onClick={onCancel} disabled={busy}>
          {t('common.cancel')}
        </button>
        <button type="submit" className="btn btn-primary" disabled={busy}>
          {busy ? t('common.working') : t('user.endpoints.saveKey')}
        </button>
      </div>
    </form>
  );
}

function EndpointKeyCard({ endpointId, keyData }: { endpointId: string; keyData: EndpointKey }) {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [requestError, setRequestError] = useState<unknown>(null);
  const [busyAction, setBusyAction] = useState<'toggle' | 'refresh' | 'delete' | null>(null);
  const models = useKeyModels(endpointId, keyData.id, open);

  const invalidateKey = () => {
    void queryClient.invalidateQueries({ queryKey: userKeys.endpointKeys(endpointId) });
    void queryClient.invalidateQueries({ queryKey: userKeys.keyModels(endpointId, keyData.id) });
    void queryClient.invalidateQueries({ queryKey: userKeys.models });
    void queryClient.invalidateQueries({ queryKey: userKeys.bindingsRoot });
  };

  const toggle = async () => {
    setRequestError(null);
    setBusyAction('toggle');
    try {
      await apiFetch<unknown>(
        `/api/endpoints/${encodeURIComponent(endpointId)}/keys/${encodeURIComponent(keyData.id)}`,
        { method: 'PATCH', json: { enabled: !keyData.enabled } },
      );
      invalidateKey();
    } catch (error) {
      setRequestError(error);
    } finally {
      setBusyAction(null);
    }
  };

  const refresh = async () => {
    setRequestError(null);
    setBusyAction('refresh');
    try {
      await apiFetch<unknown>(
        `/api/endpoints/${encodeURIComponent(endpointId)}/keys/${encodeURIComponent(keyData.id)}/models/refresh`,
        { method: 'POST' },
      );
      await queryClient.invalidateQueries({ queryKey: userKeys.keyModels(endpointId, keyData.id) });
      await queryClient.invalidateQueries({ queryKey: userKeys.endpoints });
    } catch (error) {
      setRequestError(error);
    } finally {
      setBusyAction(null);
    }
  };

  const remove = async () => {
    setRequestError(null);
    setBusyAction('delete');
    try {
      await apiFetch<void>(
        `/api/endpoints/${encodeURIComponent(endpointId)}/keys/${encodeURIComponent(keyData.id)}`,
        { method: 'DELETE' },
      );
      setDeleteOpen(false);
      invalidateKey();
    } catch (error) {
      setRequestError(error);
    } finally {
      setBusyAction(null);
    }
  };

  return (
    <article className="item-card">
      <div className="item-header">
        <div>
          <h3 className="mono">{keyData.display ?? t('user.endpoints.keyHidden')}</h3>
          <p className="item-meta">
            {keyData.note || t('user.endpoints.keyHidden')} · {formatDateTime(keyData.updated_at)}
          </p>
        </div>
        <div className="badge-list">
          <StatusBadge active={keyData.enabled} />
          <button type="button" className="btn btn-secondary" onClick={() => setOpen((value) => !value)}>
            {open ? t('user.endpoints.hideModels') : t('user.endpoints.showModels')}
          </button>
          <button type="button" className="btn btn-quiet" onClick={() => void refresh()} disabled={busyAction !== null}>
            {busyAction === 'refresh' ? t('common.working') : t('user.endpoints.refreshModels')}
          </button>
          <button type="button" className="btn btn-quiet" onClick={() => void toggle()} disabled={busyAction !== null}>
            {keyData.enabled ? t('user.endpoints.disableKey') : t('user.endpoints.enableKey')}
          </button>
          <button type="button" className="btn btn-danger" onClick={() => setDeleteOpen(true)} disabled={busyAction !== null}>
            {t('user.endpoints.deleteKey')}
          </button>
        </div>
      </div>
      {requestError ? <ErrorState error={requestError} /> : null}
      {open ? (
        <div className="nested-panel">
          {models.isPending ? (
            <LoadingState />
          ) : models.error ? (
            <ErrorState error={models.error} onRetry={() => void models.refetch()} />
          ) : models.data.length === 0 ? (
            <EmptyState title={t('user.endpoints.modelsEmpty')} body={t('user.endpoints.modelsEmptyBody')} />
          ) : (
            <ul className="plain-list">
              {models.data.map((model) => (
                <li key={`${model.provider}:${model.upstream_model_id}`}>
                  <span className="mono">{model.upstream_model_id}</span>
                  <span className="muted">{model.provider} · {model.status}</span>
                </li>
              ))}
            </ul>
          )}
        </div>
      ) : null}
      <ConfirmDialog
        open={deleteOpen}
        title={t('user.endpoints.deleteKeyTitle')}
        description={t('user.endpoints.deleteKeyBody')}
        confirmLabel={t('user.endpoints.deleteKeyConfirm')}
        danger
        busy={busyAction === 'delete'}
        onCancel={() => setDeleteOpen(false)}
        onConfirm={() => void remove()}
      />
    </article>
  );
}

function EndpointCard({ endpoint, onEdit, onDeleted }: { endpoint: Endpoint; onEdit: () => void; onDeleted: () => void }) {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const [addKeyOpen, setAddKeyOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [requestError, setRequestError] = useState<unknown>(null);
  const [deleting, setDeleting] = useState(false);
  const keys = useEndpointKeys(endpoint.id, open);

  const remove = async () => {
    setRequestError(null);
    setDeleting(true);
    try {
      await apiFetch<void>(`/api/endpoints/${encodeURIComponent(endpoint.id)}`, { method: 'DELETE' });
      queryClient.removeQueries({ queryKey: userKeys.endpointKeys(endpoint.id) });
      queryClient.removeQueries({ queryKey: userKeys.keyModelsRoot });
      await queryClient.invalidateQueries({ queryKey: userKeys.endpoints });
      await queryClient.invalidateQueries({ queryKey: userKeys.models });
      await queryClient.invalidateQueries({ queryKey: userKeys.bindingsRoot });
      setDeleteOpen(false);
      onDeleted();
    } catch (error) {
      setRequestError(error);
    } finally {
      setDeleting(false);
    }
  };

  return (
    <article className={`item-card ${endpoint.enabled ? '' : 'is-warning'}`}>
      <div className="item-header">
        <div>
          <h2 className="mono">{endpoint.base_url}</h2>
          <p className="item-meta">{endpoint.connector_type} · {formatDateTime(endpoint.updated_at)}</p>
        </div>
        <div className="badge-list">
          <StatusBadge active={endpoint.enabled} />
          {endpoint.model_fetch_failed ? (
            <StatusBadge danger active={false} label={t('user.endpoints.modelFetchFailed')} />
          ) : null}
          <button type="button" className="btn btn-secondary" onClick={onEdit}>
            {t('user.endpoints.edit')}
          </button>
          <button type="button" className="btn btn-danger" onClick={() => setDeleteOpen(true)}>
            {t('user.endpoints.delete')}
          </button>
        </div>
      </div>
      {endpoint.note ? <p className="item-note">{endpoint.note}</p> : null}
      <dl className="detail-grid">
        <div className="detail-row">
          <dt>{t('user.endpoints.connector')}</dt>
          <dd>{t('user.endpoints.connectorValue')}</dd>
        </div>
        <div className="detail-row">
          <dt>{t('user.endpoints.keyTitle')}</dt>
          <dd>{open && keys.data ? t('user.endpoints.keyCount', { count: keys.data.length }) : '—'}</dd>
        </div>
      </dl>
      <div className="form-actions">
        <button type="button" className="btn btn-secondary" onClick={() => setOpen((value) => !value)}>
          {open ? t('common.close') : t('user.endpoints.keyTitle')}
        </button>
        {open ? (
          <button type="button" className="btn btn-primary" onClick={() => setAddKeyOpen((value) => !value)}>
            {addKeyOpen ? t('common.close') : t('user.endpoints.addKey')}
          </button>
        ) : null}
      </div>
      {requestError ? <ErrorState error={requestError} /> : null}
      {open ? (
        <div className="nested-panel">
          {addKeyOpen ? (
            <AddKeyForm
              endpointId={endpoint.id}
              onCancel={() => setAddKeyOpen(false)}
              onSaved={() => {
                setAddKeyOpen(false);
                void queryClient.invalidateQueries({ queryKey: userKeys.endpointKeys(endpoint.id) });
              }}
            />
          ) : null}
          {keys.isPending ? (
            <LoadingState />
          ) : keys.error ? (
            <ErrorState error={keys.error} onRetry={() => void keys.refetch()} />
          ) : keys.data.length === 0 ? (
            <EmptyState title={t('user.endpoints.noKeys')} body={t('user.endpoints.noKeysBody')} />
          ) : (
            <div className="item-list">
              {keys.data.map((key) => (
                <EndpointKeyCard key={key.id} endpointId={endpoint.id} keyData={key} />
              ))}
            </div>
          )}
        </div>
      ) : null}
      <ConfirmDialog
        open={deleteOpen}
        title={t('user.endpoints.deleteTitle')}
        description={t('user.endpoints.deleteBody')}
        confirmLabel={t('user.endpoints.deleteConfirm')}
        danger
        busy={deleting}
        onCancel={() => setDeleteOpen(false)}
        onConfirm={() => void remove()}
      />
    </article>
  );
}

function EndpointsContent() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const endpoints = useEndpoints(true);
  const [editing, setEditing] = useState<Endpoint | undefined>();
  const [showCreate, setShowCreate] = useState(false);

  const invalidateAfterEndpointChange = () => {
    void queryClient.invalidateQueries({ queryKey: userKeys.endpoints });
    void queryClient.invalidateQueries({ queryKey: userKeys.endpointKeysRoot });
    void queryClient.invalidateQueries({ queryKey: userKeys.keyModelsRoot });
    void queryClient.invalidateQueries({ queryKey: userKeys.models });
    void queryClient.invalidateQueries({ queryKey: userKeys.bindingsRoot });
  };

  if (endpoints.isPending) return <LoadingState />;
  if (endpoints.error) return <ErrorState error={endpoints.error} onRetry={() => void endpoints.refetch()} />;

  return (
    <div className="page">
      <PageHeader
        eyebrow={t('app.name')}
        title={t('user.endpoints.title')}
        description={t('user.endpoints.description')}
        actions={
          <button
            type="button"
            className="btn btn-primary"
            onClick={() => {
              setEditing(undefined);
              setShowCreate((value) => !value);
            }}
          >
            {showCreate ? t('common.close') : t('user.endpoints.create')}
          </button>
        }
      />
      <p className="inline-notice">{t('user.endpoints.endpointLimit')}</p>
      {showCreate && !editing ? (
        <EndpointForm
          onSaved={() => {
            setShowCreate(false);
            invalidateAfterEndpointChange();
          }}
        />
      ) : null}
      {editing ? (
        <EndpointForm
          key={editing.id}
          initial={editing}
          onCancel={() => setEditing(undefined)}
          onSaved={() => {
            setEditing(undefined);
            invalidateAfterEndpointChange();
          }}
        />
      ) : null}
      {endpoints.data.length === 0 ? (
        <EmptyState title={t('user.endpoints.noEndpoints')} body={t('user.endpoints.noEndpointsBody')} />
      ) : (
        <Card>
          <div className="card-title-row">
            <h2>{t('user.endpoints.listTitle')}</h2>
            <span className="muted">{endpoints.data.length}</span>
          </div>
          <div className="item-list">
            {endpoints.data.map((endpoint) => (
              <EndpointCard
                key={endpoint.id}
                endpoint={endpoint}
                onEdit={() => {
                  setShowCreate(false);
                  setEditing(endpoint);
                }}
                onDeleted={invalidateAfterEndpointChange}
              />
            ))}
          </div>
        </Card>
      )}
    </div>
  );
}

export function EndpointsPage() {
  return (
    <UserPageGate>
      <EndpointsContent />
    </UserPageGate>
  );
}
