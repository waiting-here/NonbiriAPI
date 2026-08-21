import { useId, useMemo, useState, type FormEvent } from 'react';
import { formatDateTime } from '@shared/utils/datetime';
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
import { apiFetch, isApiError } from '@shared/query/http';
import { hasControlCharacters } from '@shared/query/normalize';
import {
  type ModelBinding,
  type PlatformModel,
  normalizeBindingList,
  useBindingUpstreamModels,
  useEndpointKeys,
  useEndpoints,
  useKeyModels,
  useModelBindings,
  usePlatformModels,
  useUpdateBinding,
  useUpdateModel,
  userKeys,
} from '../data';
import { UserPageGate } from '../components/UserPageGate';

function validNamePart(value: string): boolean {
  return Boolean(value) && value === value.trim() && value.length <= 64 && !hasControlCharacters(value);
}

/** The charity namespace prefix reserved server-side for charity models. */
const CHARITY_PREFIX = '[公益]';

/** Max characters of a note shown inside a native select option. */
const OPTION_NOTE_MAX = 80;

function truncateOptionText(value: string, max = OPTION_NOTE_MAX): string {
  const characters = Array.from(value);
  return characters.length > max ? `${characters.slice(0, max).join('')}…` : value;
}

/** Compose a select-option label with a bounded note; the full note stays in title. */
function optionWithNote(main: string, note: string): { label: string; title?: string } {
  if (!note) return { label: main };
  return { label: `${main} · ${truncateOptionText(note)}`, title: note };
}

/** Server rejections that quote the reserved charity prefix get a readable line. */
function isCharityPrefixError(error: unknown): boolean {
  return isApiError(error) && error.message.includes(CHARITY_PREFIX);
}

interface ModelFormProps {
  /** When present the form patches this model instead of creating one. */
  initial?: PlatformModel;
  onCancel?: () => void;
  onSaved: () => void;
}

function ModelForm({ initial, onCancel, onSaved }: ModelFormProps) {
  const { t } = useTranslation();
  const updateModel = useUpdateModel();
  const providerId = useId();
  const modelId = useId();
  const [provider, setProvider] = useState(initial?.provider ?? '');
  const [model, setModel] = useState(initial?.model === '—' ? '' : (initial?.model ?? ''));
  const [strategy, setStrategy] = useState<'ordered' | 'random'>(initial?.route_strategy ?? 'ordered');
  const [silentRetry, setSilentRetry] = useState(initial?.silent_retry ?? false);
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
    if (provider.startsWith(CHARITY_PREFIX)) {
      setValidationError(t('user.models.charityPrefixForbidden'));
      return;
    }
    setBusy(true);
    try {
      if (initial) {
        await updateModel.mutateAsync({
          modelId: initial.id,
          provider,
          model,
          route_strategy: strategy,
          silent_retry: silentRetry,
        });
      } else {
        await apiFetch<unknown>('/api/models', {
          method: 'POST',
          json: { provider, model, route_strategy: strategy, silent_retry: silentRetry },
        });
        setProvider('');
        setModel('');
        setSilentRetry(false);
      }
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
        <h2>{initial ? t('user.models.editTitle') : t('user.models.addTitle')}</h2>
        {initial && onCancel ? (
          <button type="button" className="btn btn-quiet" onClick={onCancel} disabled={busy}>
            {t('common.cancel')}
          </button>
        ) : null}
      </div>
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
            <small className="muted">{t('user.models.providerHint')}</small>
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
            <small className="muted">{t('user.models.modelHint')}</small>
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
        {requestError ? (
          isCharityPrefixError(requestError) ? (
            <p className="field-error" role="alert">{t('user.models.charityPrefixRejected')}</p>
          ) : isApiError(requestError) && requestError.code === 'conflict' ? (
            <p className="field-error" role="alert">{t('user.models.nameConflict')}</p>
          ) : (
            <ErrorState error={requestError} />
          )
        ) : null}
        <div className="form-actions">
          <button type="submit" className="btn btn-primary" disabled={busy}>
            {busy ? t('common.working') : initial ? t('common.save') : t('user.models.create')}
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
    const endpointKeyId = Number(keyId);
    if (!Number.isSafeInteger(endpointKeyId) || endpointKeyId <= 0) {
      setValidationError(t('common.formInvalid'));
      return;
    }
    const payload = { endpoint_key_id: endpointKeyId, upstream_model_id: upstreamModelId };
    setBusy(true);
    try {
      await apiFetch<unknown>(`/api/models/${encodeURIComponent(modelId)}/bindings`, {
        method: 'POST',
        json: payload,
      });
      setUpstreamModelId('');
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
            {enabledEndpoints.map((endpoint) => {
              const { label, title } = optionWithNote(endpoint.base_url, endpoint.note);
              return (
                <option key={endpoint.id} value={endpoint.id} title={title}>
                  {label}
                </option>
              );
            })}
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
              {enabledKeys.map((key) => {
                const main = key.display ?? t('user.endpoints.keyHidden');
                const { label, title } = optionWithNote(main, key.note);
                return (
                  <option key={key.id} value={key.id} title={title}>
                    {label}
                  </option>
                );
              })}
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
                <option key={model.upstream_model_id} value={model.upstream_model_id} title={model.provider}>
                  {truncateOptionText(model.upstream_model_id, 120)} · {model.provider}
                </option>
              ))}
            </select>
          </label>
          {keyId && keyModels.error ? <ErrorState error={keyModels.error} onRetry={() => void keyModels.refetch()} /> : null}
          {keyId && !keyModels.isPending && !keyModels.error && keyModels.data?.length === 0 ? (
            <small className="muted">{t('user.models.noFetchedModels')}</small>
          ) : null}
        </div>
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

interface BindingEditFormProps {
  modelId: string;
  binding: ModelBinding;
  onSaved: () => void;
  onCancel: () => void;
}

/**
 * Inline editor for one existing binding. Only the two server-mutable fields
 * are offered: the upstream model (chosen from the bound key's fetched cache)
 * and the order index. The save button stays disabled until something
 * actually changed, so an unchanged patch is never sent.
 */
function BindingEditForm({ modelId, binding, onSaved, onCancel }: BindingEditFormProps) {
  const { t } = useTranslation();
  const updateBinding = useUpdateBinding();
  const upstreamSelectId = useId();
  const ordInputId = useId();
  const upstream = useBindingUpstreamModels(binding, true);
  const [upstreamModelId, setUpstreamModelId] = useState(binding.upstream_model_id);
  const [ordText, setOrdText] = useState(String(binding.ord));
  const [validationError, setValidationError] = useState('');

  const parsedOrd = Number(ordText);
  const ordValid =
    ordText.trim() !== '' && Number.isSafeInteger(parsedOrd) && parsedOrd >= 0 && parsedOrd <= 1_000_000;
  const upstreamChanged = upstreamModelId !== binding.upstream_model_id;
  const ordChanged = ordValid && parsedOrd !== binding.ord;
  const dirty = upstreamChanged || ordChanged;
  const busy = updateBinding.isPending;

  // Keep the stored value selectable even when it has since dropped out of
  // the fetched cache, so the select never silently shows a wrong entry.
  const currentKnown =
    upstream.data?.some((model) => model.upstream_model_id === binding.upstream_model_id) ?? false;

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setValidationError('');
    if (!upstreamModelId || !ordValid) {
      setValidationError(t('common.formInvalid'));
      return;
    }
    try {
      await updateBinding.mutateAsync({
        modelId,
        bindingId: binding.id,
        ...(ordChanged ? { ord: parsedOrd } : {}),
        ...(upstreamChanged ? { upstream_model_id: upstreamModelId } : {}),
      });
      onSaved();
    } catch {
      // The mutation error renders through ErrorState below.
    }
  };

  return (
    <form className="binding-edit-form" onSubmit={submit} noValidate>
      <h4>{t('user.models.editBindingTitle')}</h4>
      <div className="form-grid">
        <label>
          <span>{t('user.models.upstreamModel')}</span>
          <select
            id={upstreamSelectId}
            value={upstreamModelId}
            onChange={(event) => setUpstreamModelId(event.target.value)}
            disabled={upstream.isPending}
          >
            {!currentKnown ? (
              <option value={binding.upstream_model_id}>{t('user.models.currentUpstream')}</option>
            ) : null}
            {currentKnown && upstream.isPending ? (
              <option value="">{t('common.loading')}</option>
            ) : null}
            {upstream.data?.map((model) => (
              <option key={model.upstream_model_id} value={model.upstream_model_id} title={model.provider}>
                {truncateOptionText(model.upstream_model_id, 120)} · {model.provider}
              </option>
            ))}
          </select>
          {upstream.isPending ? <small className="muted">{t('common.loading')}</small> : null}
          {upstream.error ? (
            <ErrorState error={upstream.error} onRetry={() => void upstream.refetch()} />
          ) : null}
          {!upstream.isPending && !upstream.error && (upstream.data?.length ?? 0) === 0 && currentKnown ? (
            <small className="muted">{t('user.models.noFetchedModels')}</small>
          ) : null}
        </label>
        <label>
          <span>{t('user.models.ord')}</span>
          <input
            id={ordInputId}
            type="number"
            inputMode="numeric"
            min={0}
            max={1000000}
            step={1}
            value={ordText}
            onChange={(event) => setOrdText(event.target.value)}
            aria-invalid={!ordValid}
          />
          <small className="muted">{t('user.models.ordHint')}</small>
        </label>
      </div>
      {validationError ? <p className="field-error" role="alert">{validationError}</p> : null}
      {updateBinding.error ? <ErrorState error={updateBinding.error} /> : null}
      <div className="form-actions">
        <button type="button" className="btn btn-secondary" onClick={onCancel} disabled={busy}>
          {t('common.cancel')}
        </button>
        <button type="submit" className="btn btn-primary" disabled={busy || !dirty || !ordValid}>
          {busy ? t('common.working') : t('common.save')}
        </button>
      </div>
    </form>
  );
}

interface BindingRowProps {
  modelId: string;
  binding: ModelBinding;
  index: number;
  /** True only under the ordered strategy, where row order steers routing. */
  canReorder: boolean;
  /** Temporarily disables drag and keyboard moves while a reorder request runs. */
  reorderDisabled: boolean;
  dragging: boolean;
  dropIndicator: 'before' | 'after' | null;
  onDragStart: (index: number) => void;
  onDragTarget: (index: number, after: boolean) => void;
  onDragEnd: () => void;
  onDropAt: () => void;
  onKeyboardMove: (index: number, delta: number) => void;
  /** Called after a successful inline edit so owners can refresh queries. */
  onUpdated: () => void;
  onDeleted: () => void;
}

function BindingRow({
  modelId,
  binding,
  index,
  canReorder,
  reorderDisabled,
  dragging,
  dropIndicator,
  onDragStart,
  onDragTarget,
  onDragEnd,
  onDropAt,
  onKeyboardMove,
  onUpdated,
  onDeleted,
}: BindingRowProps) {
  const { t } = useTranslation();
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [editOpen, setEditOpen] = useState(false);
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

  const rowClass = ['binding-row'];
  if (dragging) rowClass.push('is-dragging');
  if (dropIndicator === 'before') rowClass.push('drop-before');
  if (dropIndicator === 'after') rowClass.push('drop-after');

  return (
    <li
      className={rowClass.join(' ')}
      onDragEnter={(event) => {
        if (canReorder && !reorderDisabled) event.preventDefault();
      }}
      onDragOver={(event) => {
        if (!canReorder || reorderDisabled) return;
        event.preventDefault();
        event.dataTransfer.dropEffect = 'move';
        const rect = event.currentTarget.getBoundingClientRect();
        onDragTarget(index, event.clientY > rect.top + rect.height / 2);
      }}
      onDrop={(event) => {
        if (!canReorder || reorderDisabled) return;
        event.preventDefault();
        onDropAt();
      }}
    >
      {canReorder ? (
        <span
          className={reorderDisabled ? 'drag-handle is-disabled' : 'drag-handle'}
          role="button"
          tabIndex={0}
          aria-label={t('user.models.dragToReorder')}
          aria-disabled={reorderDisabled}
          draggable={!reorderDisabled}
          onDragStart={(event) => {
            if (reorderDisabled) {
              event.preventDefault();
              return;
            }
            event.dataTransfer.effectAllowed = 'move';
            event.dataTransfer.setData('text/plain', binding.id);
            onDragStart(index);
          }}
          onDragEnd={onDragEnd}
          onKeyDown={(event) => {
            if (reorderDisabled) return;
            if (event.key === 'ArrowUp') {
              event.preventDefault();
              onKeyboardMove(index, -1);
            } else if (event.key === 'ArrowDown') {
              event.preventDefault();
              onKeyboardMove(index, 1);
            }
          }}
        >
          ⠿
        </span>
      ) : null}
      <div className="binding-row-info">
        <strong className="mono">{binding.upstream_model_id}</strong>
        <span className="table-note">
          {t('user.models.endpoint')}: <span className="mono">{binding.endpoint_base_url}</span>
          {binding.endpoint_note ? <> · {binding.endpoint_note}</> : null}
        </span>
        {(binding.endpoint_key_display || binding.endpoint_key_note) ? (
          <span className="table-note">
            {t('user.models.key')}: <span className="mono">{binding.endpoint_key_display || '—'}</span>
            {binding.endpoint_key_note ? <> · {binding.endpoint_key_note}</> : null}
          </span>
        ) : null}
      </div>
      <div className="table-actions">
        <button type="button" className="btn btn-secondary" onClick={() => setEditOpen((value) => !value)}>
          {editOpen ? t('common.close') : t('user.models.editBinding')}
        </button>
        <button type="button" className="btn btn-danger" onClick={() => setDeleteOpen(true)}>
          {t('user.models.deleteBinding')}
        </button>
      </div>
      {editOpen ? (
        <BindingEditForm
          modelId={modelId}
          binding={binding}
          onSaved={() => {
            setEditOpen(false);
            onUpdated();
          }}
          onCancel={() => setEditOpen(false)}
        />
      ) : null}
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

function ModelCard({ model, onEdit, onChanged }: { model: PlatformModel; onEdit: () => void; onChanged: () => void }) {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const [addBindingOpen, setAddBindingOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [deleting, setDeleting] = useState(false);
  const bindings = useModelBindings(model.id, open);
  // Drag indexes address gaps between rows: dropIndex n means insert before
  // the row currently at index n (n = length appends at the end).
  const [dragIndex, setDragIndex] = useState<number | null>(null);
  const [dropIndex, setDropIndex] = useState<number | null>(null);
  const [reorderBusy, setReorderBusy] = useState(false);
  const [reorderError, setReorderError] = useState<unknown>(null);
  const canReorder = model.route_strategy === 'ordered';

  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: userKeys.models });
    void queryClient.invalidateQueries({ queryKey: userKeys.bindings(model.id) });
    void queryClient.invalidateQueries({ queryKey: userKeys.bindingsRoot });
  };

  const clearDragState = () => {
    setDragIndex(null);
    setDropIndex(null);
  };

  const applyReorder = async (next: ModelBinding[]) => {
    if (reorderBusy) return;
    const queryKey = userKeys.bindings(model.id);
    const previous = queryClient.getQueryData<ModelBinding[]>(queryKey);
    setReorderError(null);
    setReorderBusy(true);
    queryClient.setQueryData(queryKey, next);
    try {
      const payload = await apiFetch<unknown>(
        `/api/models/${encodeURIComponent(model.id)}/bindings/order`,
        { method: 'PUT', json: { order: next.map((binding) => Number(binding.id)) } },
      );
      queryClient.setQueryData(queryKey, normalizeBindingList(payload));
    } catch (requestError) {
      if (previous === undefined) {
        queryClient.removeQueries({ queryKey });
      } else {
        queryClient.setQueryData(queryKey, previous);
      }
      void queryClient.invalidateQueries({ queryKey });
      setReorderError(requestError);
    } finally {
      setReorderBusy(false);
    }
  };

  const handleDrop = () => {
    const from = dragIndex;
    const to = dropIndex;
    clearDragState();
    const list = bindings.data;
    if (from === null || to === null || !list) return;
    if (from === to || from + 1 === to) return;
    const next = [...list];
    const [moved] = next.splice(from, 1);
    next.splice(to > from ? to - 1 : to, 0, moved);
    void applyReorder(next);
  };

  const moveBinding = (from: number, delta: number) => {
    const list = bindings.data;
    if (!list || reorderBusy) return;
    const to = from + delta;
    if (to < 0 || to >= list.length) return;
    const next = [...list];
    const [moved] = next.splice(from, 1);
    next.splice(to, 0, moved);
    void applyReorder(next);
  };

  const dropIndicatorFor = (index: number): 'before' | 'after' | null => {
    if (dragIndex === null || dropIndex === null || !bindings.data) return null;
    if (dropIndex === dragIndex || dropIndex === dragIndex + 1) return null;
    if (dropIndex === index) return 'before';
    if (dropIndex === index + 1 && index === bindings.data.length - 1) return 'after';
    return null;
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
            {model.route_strategy === 'ordered' ? t('user.models.ordered') : t('user.models.random')} · {formatDateTime(model.updated_at)}
          </p>
        </div>
        <div className="badge-list">
          {model.silent_retry ? <span className="tag">{t('user.models.silentRetry')}</span> : null}
          {model.binding_count === 0 ? <span className="tag">{t('user.models.draft')}</span> : null}
          <StatusBadge active={model.binding_count > 0} label={t('user.models.bindingCount', { count: model.binding_count })} />
          <button type="button" className="btn btn-secondary" onClick={() => setOpen((value) => !value)}>
            {open ? t('user.models.hideBindings') : t('user.models.showBindings')}
          </button>
          <button type="button" className="btn btn-secondary" onClick={onEdit}>
            {t('common.edit')}
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
            <>
              {reorderError ? <ErrorState error={reorderError} /> : null}
              {model.route_strategy === 'random' ? (
                <p className="table-note">{t('user.models.randomOrderNote')}</p>
              ) : null}
              <ul className="binding-list">
                {bindings.data.map((binding, index) => (
                  <BindingRow
                    key={binding.id}
                    modelId={model.id}
                    binding={binding}
                    index={index}
                    canReorder={canReorder}
                    reorderDisabled={reorderBusy}
                    dragging={dragIndex === index}
                    dropIndicator={dropIndicatorFor(index)}
                    onDragStart={(i) => {
                      setDragIndex(i);
                      setDropIndex(null);
                      setReorderError(null);
                    }}
                    onDragTarget={(i, after) => setDropIndex(after ? i + 1 : i)}
                    onDragEnd={clearDragState}
                    onDropAt={handleDrop}
                    onKeyboardMove={moveBinding}
                    onUpdated={() => {
                      invalidate();
                      onChanged();
                    }}
                    onDeleted={() => {
                      invalidate();
                      onChanged();
                    }}
                  />
                ))}
              </ul>
            </>
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
  const [editing, setEditing] = useState<PlatformModel | undefined>();
  const [query, setQuery] = useState('');

  const filtered = useMemo(() => {
    const data = models.data ?? [];
    const needle = query.trim().toLowerCase();
    if (!needle) return data;
    return data.filter((model) =>
      [model.full_name, model.provider].some((value) => value.toLowerCase().includes(needle)),
    );
  }, [models.data, query]);

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
      {showCreate && !editing ? (
        <ModelForm
          onSaved={() => {
            setShowCreate(false);
            invalidateModels();
          }}
        />
      ) : null}
      {editing ? (
        <ModelForm
          key={editing.id}
          initial={editing}
          onCancel={() => setEditing(undefined)}
          onSaved={() => {
            setEditing(undefined);
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
            <span className="muted">{filtered.length}</span>
          </div>
          <div className="filter-bar" role="search">
            <label>
              <span>{t('common.search')}</span>
              <input
                type="search"
                value={query}
                maxLength={256}
                onChange={(event) => setQuery(event.target.value)}
                placeholder={t('user.models.searchPlaceholder')}
                aria-label={t('user.models.searchAria')}
              />
            </label>
          </div>
          {filtered.length === 0 ? (
            <EmptyState title={t('common.noResults')} body={t('common.noResultsBody')} />
          ) : (
            <div className="item-list">
              {filtered.map((model) => (
                <ModelCard
                  key={model.id}
                  model={model}
                  onEdit={() => {
                    setShowCreate(false);
                    setEditing(model);
                  }}
                  onChanged={invalidateModels}
                />
              ))}
            </div>
          )}
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
