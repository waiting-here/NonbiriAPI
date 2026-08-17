import { useId, useState, type FormEvent } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import {
  Card,
  EmptyState,
  ErrorState,
  LoadingState,
  PageHeader,
  StatusBadge,
} from '@shared/components/States';
import { apiFetch } from '@shared/query/http';
import { hasControlCharacters } from '@shared/query/normalize';
import {
  type ModelBinding,
  type PlatformModel,
  useEndpointKeys,
  useEndpoints,
  useKeyModels,
  useModelBindings,
  usePlatformModels,
  userKeys,
} from '../data';
import { UserPageGate } from '../components/UserPageGate';

function validNamePart(value: string): boolean {
  return Boolean(value) && value === value.trim() && value.length <= 64 && !hasControlCharacters(value);
}

function ModelForm({ onSaved }: { onSaved: () => void }) {
  const { t } = useTranslation();
  const providerId = useId();
  const modelId = useId();
  const [provider, setProvider] = useState('');
  const [model, setModel] = useState('');
  const [strategy, setStrategy] = useState<'ordered' | 'random'>('ordered');
  const [silentRetry, setSilentRetry] = useState(false);
  const [validationError, setValidationError] = useState('');
  const [requestError, setRequestError] = useState<unknown>(null);
  const [busy, setBusy] = useState(false);

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setValidationError('');
    setRequestError(null);
    if (!validNamePart(provider) || !validNamePart(model)) {
      setValidationError(t('user.models.invalidParts'));
      return;
    }
    setBusy(true);
    try {
      await apiFetch<unknown>('/api/models', {
        method: 'POST',
        json: { provider, model, route_strategy: strategy, silent_retry: silentRetry },
      });
      setProvider('');
      setModel('');
      setSilentRetry(false);
      onSaved();
    } catch (error) {
      setRequestError(error);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Card>
      <h2>{t('user.models.addTitle')}</h2>
      <form onSubmit={submit} noValidate>
        <div className="form-grid">
          <label>
            <span>{t('user.models.provider')}</span>
            <input
              id={providerId}
              value={provider}
              onChange={(event) => setProvider(event.target.value)}
              placeholder={t('user.models.providerPlaceholder')}
              maxLength={64}
              required
              aria-invalid={Boolean(validationError)}
            />
          </label>
          <label>
            <span>{t('user.models.model')}</span>
            <input
              id={modelId}
              value={model}
              onChange={(event) => setModel(event.target.value)}
              placeholder={t('user.models.modelPlaceholder')}
              maxLength={64}
              required
              aria-invalid={Boolean(validationError)}
            />
          </label>
          <label>
            <span>{t('user.models.strategy')}</span>
            <select value={strategy} onChange={(event) => setStrategy(event.target.value as 'ordered' | 'random')}>
              <option value="ordered">{t('user.models.ordered')}</option>
              <option value="random">{t('user.models.random')}</option>
            </select>
          </label>
          <label className="checkbox-label">
            <input
              type="checkbox"
              checked={silentRetry}
              onChange={(event) => setSilentRetry(event.target.checked)}
            />
            <span>{t('user.models.silentRetry')}</span>
          </label>
        </div>
        {validationError ? <p className="field-error" role="alert">{validationError}</p> : null}
        {requestError ? <ErrorState error={requestError} /> : null}
        <div className="form-actions">
          <button type="submit" className="btn btn-primary" disabled={busy}>
            {busy ? t('common.working') : t('user.models.create')}
          </button>
        </div>
      </form>
    </Card>
  );
}

function BindingForm({ modelId, onSaved }: { modelId: string; onSaved: () => void }) {
  const { t } = useTranslation();
  const endpoints = useEndpoints(true);
  const [endpointId, setEndpointId] = useState('');
  const [keyId, setKeyId] = useState('');
  const [upstreamModelId, setUpstreamModelId] = useState('');
  const [ord, setOrd] = useState('');
  const [requestError, setRequestError] = useState<unknown>(null);
  const [validationError, setValidationError] = useState('');
  const [busy, setBusy] = useState(false);
  const keys = useEndpointKeys(endpointId || undefined, Boolean(endpointId));
  const keyModels = useKeyModels(endpointId || undefined, keyId || undefined, Boolean(keyId));
  const enabledEndpoints = endpoints.data?.filter((endpoint) => endpoint.enabled) ?? [];
  const enabledKeys = keys.data?.filter((key) => key.enabled) ?? [];

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setRequestError(null);
    setValidationError('');
    if (!endpointId || !keyId || !upstreamModelId) {
      setValidationError(t('common.formInvalid'));
      return;
    }
    const payload: { endpoint_key_id: string; upstream_model_id: string; ord?: number } = {
      endpoint_key_id: keyId,
      upstream_model_id: upstreamModelId,
    };
    if (ord.trim()) {
      if (!/^\d+$/.test(ord.trim())) {
        setValidationError(t('user.models.invalidOrder'));
        return;
      }
      payload.ord = Number(ord.trim());
      if (!Number.isSafeInteger(payload.ord) || payload.ord < 0) {
        setValidationError(t('user.models.invalidOrder'));
        return;
      }
    }
    setBusy(true);
    try {
      await apiFetch<unknown>(`/api/models/${encodeURIComponent(modelId)}/bindings`, {
        method: 'POST',
        json: payload,
      });
      setUpstreamModelId('');
      setOrd('');
      onSaved();
    } catch (error) {
      setRequestError(error);
    } finally {
      setBusy(false);
    }
  };

  if (endpoints.isPending) return <LoadingState />;
  if (endpoints.error) return <ErrorState error={endpoints.error} onRetry={() => void endpoints.refetch()} />;
  if (endpoints.data.length === 0 || enabledEndpoints.length === 0) {
    return <EmptyState title={t('user.models.noEndpointKeys')} body={t('user.models.noEndpointsForBinding')} />;
  }

  return (
    <form className="binding-form" onSubmit={submit} noValidate>
      <h3>{t('user.models.bindingFormTitle')}</h3>
      <div className="form-grid">
        <label>
          <span>{t('user.models.endpoint')}</span>
          <select
            value={endpointId}
            onChange={(event) => {
              setEndpointId(event.target.value);
              setKeyId('');
              setUpstreamModelId('');
            }}
            required
          >
            <option value="">{t('user.models.chooseEndpoint')}</option>
            {enabledEndpoints.map((endpoint) => (
              <option key={endpoint.id} value={endpoint.id}>
                {endpoint.base_url}
              </option>
            ))}
          </select>
        </label>
        <div className="field-group">
          <label>
            <span>{t('user.models.key')}</span>
            <select
              value={keyId}
              onChange={(event) => {
                setKeyId(event.target.value);
                setUpstreamModelId('');
              }}
              disabled={!endpointId || keys.isPending}
              required
            >
              <option value="">{keys.isPending ? t('common.loading') : t('user.models.chooseKey')}</option>
              {enabledKeys.map((key) => (
                <option key={key.id} value={key.id}>
                  {key.display ?? t('user.endpoints.keyHidden')}
                </option>
              ))}
            </select>
          </label>
          {keys.error ? <ErrorState error={keys.error} onRetry={() => void keys.refetch()} /> : null}
          {endpointId && !keys.isPending && !keys.error && enabledKeys.length === 0 ? (
            <small className="muted">{t('user.models.noEndpointKeys')}</small>
          ) : null}
        </div>
        <div className="field-group">
          <label>
            <span>{t('user.models.upstreamModel')}</span>
            <select
              value={upstreamModelId}
              onChange={(event) => setUpstreamModelId(event.target.value)}
              disabled={!keyId || keyModels.isPending || Boolean(keyModels.error)}
              required
            >
              <option value="">{keyModels.isPending ? t('common.loading') : t('user.models.chooseModel')}</option>
              {keyModels.data?.map((model) => (
                <option key={model.upstream_model_id} value={model.upstream_model_id}>
                  {model.upstream_model_id}
                </option>
              ))}
            </select>
          </label>
          {keyId && keyModels.error ? <ErrorState error={keyModels.error} onRetry={() => void keyModels.refetch()} /> : null}
          {keyId && !keyModels.isPending && !keyModels.error && keyModels.data?.length === 0 ? (
            <small className="muted">{t('user.models.noFetchedModels')}</small>
          ) : null}
        </div>
        <label>
          <span>{t('user.models.order')} <em>{t('common.optional')}</em></span>
          <input type="number" min="0" step="1" value={ord} onChange={(event) => setOrd(event.target.value)} />
        </label>
      </div>
      {validationError ? <p className="field-error" role="alert">{validationError}</p> : null}
      {requestError ? <ErrorState error={requestError} /> : null}
      <div className="form-actions">
        <button type="submit" className="btn btn-primary" disabled={busy || !endpointId || !keyId || !upstreamModelId}>
          {busy ? t('common.working') : t('user.models.addBinding')}
        </button>
      </div>
    </form>
  );
}

function BindingRow({ modelId, binding, onDeleted }: { modelId: string; binding: ModelBinding; onDeleted: () => void }) {
  const { t } = useTranslation();
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [busy, setBusy] = useState(false);

  const remove = async () => {
    setError(null);
    setBusy(true);
    try {
      await apiFetch<void>(
        `/api/models/${encodeURIComponent(modelId)}/bindings/${encodeURIComponent(binding.id)}`,
        { method: 'DELETE' },
      );
      setDeleteOpen(false);
      onDeleted();
    } catch (requestError) {
      setError(requestError);
    } finally {
      setBusy(false);
    }
  };

  return (
    <li className="binding-row">
      <div>
        <strong className="mono">{binding.upstream_model_id}</strong>
        <span className="table-note">{t('user.models.key')}: {binding.endpoint_key_id} · {t('user.models.order')}: {binding.ord}</span>
      </div>
      <div className="table-actions">
        <button type="button" className="btn btn-danger" onClick={() => setDeleteOpen(true)}>
          {t('user.models.deleteBinding')}
        </button>
      </div>
      {error ? <ErrorState error={error} /> : null}
      <ConfirmDialog
        open={deleteOpen}
        title={t('user.models.deleteBindingTitle')}
        description={t('user.models.deleteBindingBody')}
        confirmLabel={t('user.models.deleteConfirm')}
        danger
        busy={busy}
        onCancel={() => setDeleteOpen(false)}
        onConfirm={() => void remove()}
      />
    </li>
  );
}

function ModelCard({ model, onChanged }: { model: PlatformModel; onChanged: () => void }) {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const [addBindingOpen, setAddBindingOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [deleting, setDeleting] = useState(false);
  const bindings = useModelBindings(model.id, open);

  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: userKeys.models });
    void queryClient.invalidateQueries({ queryKey: userKeys.bindings(model.id) });
    void queryClient.invalidateQueries({ queryKey: userKeys.bindingsRoot });
  };

  const remove = async () => {
    setError(null);
    setDeleting(true);
    try {
      await apiFetch<void>(`/api/models/${encodeURIComponent(model.id)}`, { method: 'DELETE' });
      invalidate();
      setDeleteOpen(false);
      onChanged();
    } catch (requestError) {
      setError(requestError);
    } finally {
      setDeleting(false);
    }
  };

  return (
    <article className="item-card">
      <div className="item-header">
        <div>
          <h2 className="mono">{model.full_name}</h2>
          <p className="item-meta">
            {model.route_strategy === 'ordered' ? t('user.models.ordered') : t('user.models.random')} · {model.updated_at}
          </p>
        </div>
        <div className="badge-list">
          {model.silent_retry ? <span className="tag">{t('user.models.silentRetry')}</span> : null}
          {model.binding_count === 0 ? <span className="tag">{t('user.models.draft')}</span> : null}
          <StatusBadge active={model.binding_count > 0} label={t('user.models.bindingCount', { count: model.binding_count })} />
          <button type="button" className="btn btn-secondary" onClick={() => setOpen((value) => !value)}>
            {open ? t('user.models.hideBindings') : t('user.models.showBindings')}
          </button>
          <button type="button" className="btn btn-danger" onClick={() => setDeleteOpen(true)}>
            {t('user.models.deleteModel')}
          </button>
        </div>
      </div>
      {error ? <ErrorState error={error} /> : null}
      {open ? (
        <div className="nested-panel">
          <div className="form-actions">
            <button type="button" className="btn btn-primary" onClick={() => setAddBindingOpen((value) => !value)}>
              {addBindingOpen ? t('common.close') : t('user.models.addBinding')}
            </button>
          </div>
          {addBindingOpen ? (
            <BindingForm
              modelId={model.id}
              onSaved={() => {
                setAddBindingOpen(false);
                invalidate();
                onChanged();
              }}
            />
          ) : null}
          {bindings.isPending ? (
            <LoadingState />
          ) : bindings.error ? (
            <ErrorState error={bindings.error} onRetry={() => void bindings.refetch()} />
          ) : bindings.data.length === 0 ? (
            <EmptyState title={t('user.models.noBindings')} body={t('user.models.noBindingsBody')} />
          ) : (
            <ul className="binding-list">
              {bindings.data.map((binding) => (
                <BindingRow
                  key={binding.id}
                  modelId={model.id}
                  binding={binding}
                  onDeleted={() => {
                    invalidate();
                    onChanged();
                  }}
                />
              ))}
            </ul>
          )}
        </div>
      ) : null}
      <ConfirmDialog
        open={deleteOpen}
        title={t('user.models.deleteModelTitle')}
        description={t('user.models.deleteModelBody')}
        confirmLabel={t('user.models.deleteConfirm')}
        danger
        busy={deleting}
        onCancel={() => setDeleteOpen(false)}
        onConfirm={() => void remove()}
      />
    </article>
  );
}

function ModelsContent() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const models = usePlatformModels(true);
  const [showCreate, setShowCreate] = useState(false);

  if (models.isPending) return <LoadingState />;
  if (models.error) return <ErrorState error={models.error} onRetry={() => void models.refetch()} />;

  const invalidateModels = () => {
    void queryClient.invalidateQueries({ queryKey: userKeys.models });
    void queryClient.invalidateQueries({ queryKey: userKeys.bindingsRoot });
  };

  return (
    <div className="page">
      <PageHeader
        eyebrow={t('app.name')}
        title={t('user.models.title')}
        description={t('user.models.description')}
        actions={
          <button type="button" className="btn btn-primary" onClick={() => setShowCreate((value) => !value)}>
            {showCreate ? t('common.close') : t('user.models.create')}
          </button>
        }
      />
      {showCreate ? (
        <ModelForm
          onSaved={() => {
            setShowCreate(false);
            invalidateModels();
          }}
        />
      ) : null}
      {models.data.length === 0 ? (
        <EmptyState title={t('user.models.noModels')} body={t('user.models.noModelsBody')} />
      ) : (
        <Card>
          <div className="card-title-row">
            <h2>{t('user.models.listTitle')}</h2>
            <span className="muted">{models.data.length}</span>
          </div>
          <div className="item-list">
            {models.data.map((model) => <ModelCard key={model.id} model={model} onChanged={invalidateModels} />)}
          </div>
        </Card>
      )}
    </div>
  );
}

export function ModelsPage() {
  return (
    <UserPageGate>
      <ModelsContent />
    </UserPageGate>
  );
}
