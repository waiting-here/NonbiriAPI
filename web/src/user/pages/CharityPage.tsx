import { useEffect, useState, type FormEvent } from 'react';
import { Link } from 'react-router';
import { useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { CharityPriceTable } from '@shared/components/CharityPriceTable';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { Card, EmptyState, ErrorState, LoadingState, PageHeader, StatusBadge } from '@shared/components/States';
import { formatDateTime } from '@shared/utils/datetime';
import { formatCreditsFromMilli } from '@shared/utils/formatNumber';
import { ApiError, apiFetch, isApiError, isForbidden, isNotFoundError, isUnauthorized, refetchAuthoritativeQueries } from '@shared/query/http';
import { isStationSessionChanged, stationSessionWrite } from '@shared/charityManagement';
import { positiveDecimalIDNumber } from '@shared/query/normalize';
import {
  type CharityModel,
  type Donation,
  type DonationPayload,
  useCharityModels,
  useDeleteDonation,
  useDonation,
  useDonations,
  useEndpoints,
  useEndpointKeys,
  userKeys,
  useUserSession,
  normalizeDonation,
} from '../data';
import { UserPageGate } from '../components/UserPageGate';

function stationClosed(error: unknown): boolean {
  return isStationSessionChanged(error) || isUnauthorized(error) || isForbidden(error);
}

function priceRows(model: CharityModel, translate: (key: string) => string) {
  const p = model.prices;
  if (model.pricing_mode === 'per_request') {
    return [{ label: translate('user.charity.priceRequest'), userMilli: p.request_user_price_milli, rewardMilli: p.request_donor_reward_milli, currentUserMilli: p.current_request_user_price_milli }];
  }
  return [
    { label: translate('user.charity.priceUncached'), userMilli: p.uncached_user_price_milli, rewardMilli: p.uncached_donor_reward_milli, currentUserMilli: p.current_uncached_user_price_milli },
    { label: translate('user.charity.priceCacheWrite'), userMilli: p.cache_write_user_price_milli, rewardMilli: p.cache_write_donor_reward_milli, currentUserMilli: p.current_cache_write_user_price_milli },
    { label: translate('user.charity.priceCacheRead'), userMilli: p.cache_read_user_price_milli, rewardMilli: p.cache_read_donor_reward_milli, currentUserMilli: p.current_cache_read_user_price_milli },
    { label: translate('user.charity.priceOutput'), userMilli: p.output_user_price_milli, rewardMilli: p.output_donor_reward_milli, currentUserMilli: p.current_output_user_price_milli },
  ];
}

function statusText(model: CharityModel, t: (key: string) => string): string {
  if (!model.enabled) return t('user.charity.modelDisabled');
  if (!model.available) {
    switch (model.availability_reason) {
      case 'no_candidate': return t('user.charity.noCandidate');
      case 'keys_exhausted': return t('user.charity.keysExhausted');
      case 'token_reserve_missing': return t('user.charity.tokenReserveMissing');
      default: return model.availability_reason || t('user.charity.unavailable');
    }
  }
  return t('user.charity.available');
}

function SuccessRate({ model }: { model: CharityModel }) {
  const { t } = useTranslation();
  const rate = model.success_samples > 0 ? Math.round((model.success_count * 10000) / model.success_samples) / 100 : 0;
  const dash = 2 * Math.PI * 24;
  const offset = dash * (1 - rate / 100);
  return (
    <div className="charity-rate" aria-label={t('user.charity.successRateLabel', { rate, count: model.success_samples })}>
      <svg viewBox="0 0 56 56" role="img" aria-hidden="true">
        <circle className="charity-rate-track" cx="28" cy="28" r="24" />
        <circle className="charity-rate-value" cx="28" cy="28" r="24" strokeDasharray={dash} strokeDashoffset={offset} />
      </svg>
      <strong>{model.success_samples > 0 ? `${rate}%` : '—'}</strong>
      <span className="muted">{t('user.charity.successRate', { count: model.success_samples })}</span>
    </div>
  );
}

function CharityModelCard({ model }: { model: CharityModel }) {
  const { t } = useTranslation();
  const status = statusText(model, t);
  const discount = {
    percent: model.discount.percent,
    enabled: model.discount.enabled,
    startAt: model.discount.start_at,
    endAt: model.discount.end_at,
  };
  return (
    <article className={`item-card charity-model-card ${model.available ? '' : 'is-warning'}`}>
      <div className="item-header">
        <div>
          <h2 className="mono">{model.full_name}</h2>
          <p className="item-meta">{model.pricing_mode === 'per_token' ? t('user.charity.perToken') : t('user.charity.perRequest')}</p>
        </div>
        <StatusBadge active={model.available} label={status} danger={!model.available} />
      </div>
      <div className="charity-model-summary">
        <SuccessRate model={model} />
        <p className="charity-availability">
          {model.available ? t('user.charity.availableHint') : status}
        </p>
      </div>
      <CharityPriceTable mode={model.pricing_mode} rows={priceRows(model, t)} discount={discount} />
      {model.flatten_tool_calls ? (
        <p className="risk-note" role="note"><strong>{t('user.charity.flattenEnabled')}</strong> {t('user.charity.flattenRisk')}</p>
      ) : null}
    </article>
  );
}

interface NewKeyDraft {
  secret: string;
  note: string;
  maxConcurrency: string;
  rpm: string;
  forceStoreFalse: boolean;
}

const emptyKey = (): NewKeyDraft => ({ secret: '', note: '', maxConcurrency: '0', rpm: '0', forceStoreFalse: false });

interface DonationLimitDraft {
  maxConcurrency: string;
  rpm: string;
}

function donationLimitDraft(maxConcurrency = 0, rpm = 0): DonationLimitDraft {
  return { maxConcurrency: String(maxConcurrency), rpm: String(rpm) };
}

function parseDonationLimit(value: string, maximum: number): number | null {
  if (value.trim() === '') return 0;
  if (!/^(0|[1-9]\d*)$/.test(value)) return null;
  const parsed = Number(value);
  return Number.isSafeInteger(parsed) && parsed >= 0 && parsed <= maximum ? parsed : null;
}

function dateTimeLocal(unixSeconds: number | undefined): string {
  if (!unixSeconds) return '';
  const date = new Date(unixSeconds * 1000);
  const pad = (value: number) => String(value).padStart(2, '0');
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

function parseDateTimeLocal(value: string): number | null {
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/.test(value)) return null;
  const [datePart, timePart] = value.split('T');
  const [year, month, day] = datePart.split('-').map(Number);
  const [hour, minute] = timePart.split(':').map(Number);
  const parsed = new Date(year, month - 1, day, hour, minute, 0, 0);
  if (!Number.isFinite(parsed.getTime()) || parsed.getTime() <= 0
    || parsed.getFullYear() !== year || parsed.getMonth() !== month - 1
    || parsed.getDate() !== day || parsed.getHours() !== hour || parsed.getMinutes() !== minute) return null;
  return Math.floor(parsed.getTime() / 1000);
}

function DonationForm({ onSaved, initial }: { onSaved: () => void; initial?: Donation }) {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const editing = Boolean(initial);
  const editingExistingEndpoint = Boolean(initial?.endpoint_id);
  const metadataOnlyEdit = editing && !editingExistingEndpoint;
  // A pending nested endpoint has no physical endpoint to select yet. Its
  // metadata-only edit must remain available even if the personal endpoint
  // list is unavailable or malformed.
  const endpoints = useEndpoints(!editing || editingExistingEndpoint);
  const [mode, setMode] = useState<'existing' | 'new'>(editing
    ? (editingExistingEndpoint ? 'existing' : 'new')
    : 'existing');
  const [endpointId, setEndpointId] = useState(initial?.endpoint_id ?? '');
  const [selectedKeys, setSelectedKeys] = useState<string[]>(initial?.keys.map((key) => key.endpoint_key_id ?? '').filter(Boolean) ?? []);
  const [selectedLimits, setSelectedLimits] = useState<Record<string, DonationLimitDraft>>(() => Object.fromEntries(
    (initial?.keys ?? []).filter((key) => key.endpoint_key_id).map((key) => [key.endpoint_key_id as string, donationLimitDraft(key.max_concurrency, key.rpm_limit)]),
  ));
  const [newEndpoint, setNewEndpoint] = useState({ baseUrl: '', note: '', connectorType: 'openai-compatible' });
  const [newKeys, setNewKeys] = useState<NewKeyDraft[]>([emptyKey()]);
  const [description, setDescription] = useState(initial?.description ?? '');
  const [expiresAt, setExpiresAt] = useState(dateTimeLocal(initial?.expires_at));
  const [validation, setValidation] = useState('');
  const [requestError, setRequestError] = useState<unknown>(null);
  const [busy, setBusy] = useState(false);
  const endpointConnectorType = endpoints.data?.find((endpoint) => endpoint.id === endpointId)?.connector_type;
  const keys = useEndpointKeys(endpointId || undefined, Boolean(endpointId), endpointConnectorType);
  const enabledEndpoints = endpoints.data?.filter((endpoint) => endpoint.enabled) ?? [];
  const selectableKeys = keys.data?.filter((key) => key.enabled || (editing && selectedKeys.includes(key.id))) ?? [];
  const existingEndpointContextReady = mode !== 'existing' || (
    !endpoints.isPending && !endpoints.error && Boolean(endpointId && endpointConnectorType)
    && !keys.isPending && !keys.error
  );
  const limitsFor = (keyID: string): DonationLimitDraft => selectedLimits[keyID] ?? donationLimitDraft();
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setValidation('');
    setRequestError(null);
    if (!existingEndpointContextReady) {
      setValidation(t('user.charity.endpointContextUnavailable'));
      return;
    }
    if (!description.trim()) {
      setValidation(t('user.charity.descriptionRequired'));
      return;
    }
    const expiryUnix = expiresAt ? parseDateTimeLocal(expiresAt) : null;
    if (expiresAt && expiryUnix === null) {
      setValidation(t('common.formInvalid'));
      return;
    }
    const physicalKeyLimits = (keyIDs: string[]): { endpoint_key_id: number; max_concurrency: number; rpm_limit: number }[] | null => {
      const result: { endpoint_key_id: number; max_concurrency: number; rpm_limit: number }[] = [];
      for (const keyID of keyIDs) {
        const numericID = positiveDecimalIDNumber(keyID) ?? null;
        const draft = limitsFor(keyID);
        const maxConcurrency = parseDonationLimit(draft.maxConcurrency, 100_000);
        const rpm = parseDonationLimit(draft.rpm, 4_096);
        if (numericID === null || maxConcurrency === null || rpm === null) return null;
        result.push({ endpoint_key_id: numericID, max_concurrency: maxConcurrency, rpm_limit: rpm });
      }
      return result;
    };
    let payload: DonationPayload;
    if (editing) {
      if (!editingExistingEndpoint) {
        // A pending nested/new-endpoint donation may not have a physical
        // endpoint id yet. Its owner can edit only the donation metadata;
        // sending an empty key replacement would accidentally discard the
        // server-side pending secret claim.
        payload = { description: description.trim(), expires_at: expiryUnix };
      } else {
        if (!endpointId || selectedKeys.length === 0) {
          setValidation(t('user.charity.chooseKeys'));
          return;
        }
        const limits = physicalKeyLimits(selectedKeys);
        if (!limits || positiveDecimalIDNumber(endpointId) === undefined) {
          setValidation(limits ? t('common.formInvalid') : t('user.charity.limitInvalid'));
          return;
        }
        payload = {
          description: description.trim(),
          expires_at: expiryUnix,
          keys: {
            key_ids: limits.map((key) => key.endpoint_key_id),
            limits,
          },
        };
      }
    } else if (mode === 'existing') {
      if (!endpointId || selectedKeys.length === 0) {
        setValidation(t('user.charity.chooseKeys'));
        return;
      }
      const endpointNumber = positiveDecimalIDNumber(endpointId) ?? null;
      const limits = physicalKeyLimits(selectedKeys);
      if (endpointNumber === null || !limits) {
        setValidation(limits ? t('common.formInvalid') : t('user.charity.limitInvalid'));
        return;
      }
      payload = { description: description.trim(), existing_endpoint: { endpoint_id: endpointNumber, key_ids: limits.map((key) => key.endpoint_key_id), keys: limits } };
    } else {
      const submittedKeys = newKeys.map((key) => ({ ...key }));
      if (!newEndpoint.baseUrl.trim() || submittedKeys.some((key) => !key.secret)) {
        setNewKeys(submittedKeys.map((key) => ({ ...key, secret: '' })));
        setValidation(t('user.charity.newEndpointRequired'));
        return;
      }
      const parsedNewKeys = submittedKeys.map((key) => ({
        ...key,
        maxConcurrency: parseDonationLimit(key.maxConcurrency, 100_000),
        rpm: parseDonationLimit(key.rpm, 4_096),
      }));
      if (parsedNewKeys.some((key) => key.maxConcurrency === null || key.rpm === null)) {
        setNewKeys(submittedKeys.map((key) => ({ ...key, secret: '' })));
        setValidation(t('user.charity.limitInvalid'));
        return;
      }
      // Clear the DOM-bound secret state before the direct request begins. No
      // secret is ever placed in React Query state, browser storage, or cache.
      setNewKeys(submittedKeys.map((key) => ({ ...key, secret: '' })));
      payload = {
        description: description.trim(),
        new_endpoint: {
          connector_type: newEndpoint.connectorType,
          base_url: newEndpoint.baseUrl.trim(),
          note: newEndpoint.note.trim() || undefined,
          keys: parsedNewKeys.map((key) => ({
            secret: key.secret,
            note: key.note.trim() || undefined,
            max_concurrency: key.maxConcurrency as number,
            rpm_limit: key.rpm as number,
            ...(newEndpoint.connectorType === 'openai-compatible' && key.forceStoreFalse ? { force_store_false: true } : {}),
          })),
        },
      };
    }
    if (!editing && expiryUnix !== null) payload.expires_at = expiryUnix;
    setBusy(true);
    let mutationError: unknown = null;
    try {
      await stationSessionWrite(queryClient, 'steward', async () => {
        const response = initial
          ? await apiFetch<unknown>(`/api/donations/${encodeURIComponent(initial.id)}`, { method: 'PATCH', json: payload })
          : await apiFetch<unknown>('/api/donations', { method: 'POST', json: payload });
        // A malformed/empty 2xx body is an unknown result, not a successful
        // mutation. The authoritative list/detail reads below decide state.
        normalizeDonation(response, false);
      });
    } catch (error) {
      mutationError = error;
    }
    if (stationClosed(mutationError)) {
      setBusy(false);
      return;
    }
    const refreshError = await refetchAuthoritativeQueries(queryClient, [
        { queryKey: userKeys.donations, exact: true },
        ...(initial ? [{ queryKey: userKeys.donation(initial.id), exact: false }] : []),
      ]);
      if (mutationError) {
        setRequestError(mutationError);
      } else if (refreshError) {
        setRequestError(refreshError);
      } else {
        setDescription('');
        setExpiresAt('');
        setNewKeys([emptyKey()]);
        onSaved();
      }
    setBusy(false);
  };

  if (!metadataOnlyEdit && endpoints.isPending) return <LoadingState />;
  if (!metadataOnlyEdit && endpoints.error) return <ErrorState error={endpoints.error} onRetry={() => void endpoints.refetch()} />;

  return (
    <Card>
      <div className="card-title-row"><h2>{editing ? t('user.charity.editDonation') : t('user.charity.submitDonation')}</h2></div>
      {!editing ? (
        <div className="segmented-control" role="group" aria-label={t('user.charity.sourceType')}>
          <button type="button" className={mode === 'existing' ? 'is-selected' : ''} onClick={() => setMode('existing')}>{t('user.charity.useExisting')}</button>
          <button type="button" className={mode === 'new' ? 'is-selected' : ''} onClick={() => setMode('new')}>{t('user.charity.useNew')}</button>
        </div>
      ) : <p className="muted">{t('user.charity.pendingEditOnly')}</p>}
      <form onSubmit={submit} noValidate>
        {(!editing || editingExistingEndpoint) && mode === 'existing' ? (
          <div className="form-grid">
            <label><span>{t('user.charity.endpoint')}</span><select value={endpointId} disabled={editing} onChange={(event) => { setEndpointId(event.target.value); setSelectedKeys([]); }} required>
              <option value="">{t('user.charity.chooseEndpoint')}</option>
              {enabledEndpoints.map((endpoint) => <option key={endpoint.id} value={endpoint.id}>{endpoint.base_url}{endpoint.note ? ` · ${endpoint.note}` : ''}</option>)}
            </select></label>
            <fieldset className="key-picker"><legend>{t('user.charity.keys')}</legend>
              {!endpointId ? <p className="muted" role="status">{t('user.charity.chooseEndpointFirst')}</p> : keys.isPending ? <LoadingState /> : keys.error ? <ErrorState error={keys.error} onRetry={() => void keys.refetch()} /> : selectableKeys.map((key) => (
                <fieldset className="donation-key-selection" key={key.id}>
                  <label className="checkbox-label"><input type="checkbox" checked={selectedKeys.includes(key.id)} onChange={(event) => { setSelectedKeys((current) => event.target.checked ? [...current, key.id] : current.filter((id) => id !== key.id)); setSelectedLimits((current) => ({ ...current, [key.id]: current[key.id] ?? donationLimitDraft() })); }} /><span>{key.display ?? t('user.charity.keyHidden')}{key.note ? ` · ${key.note}` : ''}</span></label>
                  {selectedKeys.includes(key.id) ? <div className="form-grid compact-grid"><label><span>{t('user.charity.maxConcurrency')}</span><input type="number" min="0" max="100000" step="1" value={limitsFor(key.id).maxConcurrency} onChange={(event) => setSelectedLimits((current) => ({ ...current, [key.id]: { ...(current[key.id] ?? donationLimitDraft()), maxConcurrency: event.target.value } }))} /></label><label><span>{t('user.charity.rpmLimit')}</span><input type="number" min="0" max="4096" step="1" value={limitsFor(key.id).rpm} onChange={(event) => setSelectedLimits((current) => ({ ...current, [key.id]: { ...(current[key.id] ?? donationLimitDraft()), rpm: event.target.value } }))} /></label></div> : null}
                </fieldset>
              ))}
              {endpointId && !keys.isPending && !keys.error && selectableKeys.length === 0 ? <p className="muted">{t('user.charity.noKeys')}</p> : null}
            </fieldset>
          </div>
        ) : null}
        {editing && !editingExistingEndpoint ? <p className="inline-notice" role="status">{t('user.charity.newEndpointPendingEditOnly')}</p> : null}
        {!editing && mode === 'new' ? (
          <div className="nested-panel">
            <div className="form-grid"><label><span>{t('user.charity.connectorType')}</span><select value={newEndpoint.connectorType} onChange={(event) => setNewEndpoint({ ...newEndpoint, connectorType: event.target.value })}><option value="openai-compatible">{t('user.charity.connectorOpenAI')}</option><option value="anthropic-compatible">{t('user.charity.connectorAnthropic')}</option></select></label><label><span>{t('user.charity.baseUrl')}</span><input placeholder={t('user.charity.baseUrl')} value={newEndpoint.baseUrl} onChange={(event) => setNewEndpoint({ ...newEndpoint, baseUrl: event.target.value })} maxLength={2048} required /></label><label><span>{t('user.charity.endpointNote')}</span><input value={newEndpoint.note} onChange={(event) => setNewEndpoint({ ...newEndpoint, note: event.target.value })} maxLength={512} /></label></div>
            {newKeys.map((key, index) => <fieldset className="donation-key-draft" key={index}><legend>{t('user.charity.newKey', { number: index + 1 })}</legend><div className="form-grid"><label><span>{t('user.charity.secret')}</span><input type="password" placeholder={t('user.charity.secret')} value={key.secret} onChange={(event) => setNewKeys((all) => all.map((item, i) => i === index ? { ...item, secret: event.target.value } : item))} autoComplete="new-password" maxLength={4096} required /></label><label><span>{t('user.charity.keyNote')}</span><input value={key.note} onChange={(event) => setNewKeys((all) => all.map((item, i) => i === index ? { ...item, note: event.target.value } : item))} maxLength={512} /></label><label><span>{t('user.charity.maxConcurrency')}</span><input type="number" min="0" step="1" value={key.maxConcurrency} onChange={(event) => setNewKeys((all) => all.map((item, i) => i === index ? { ...item, maxConcurrency: event.target.value } : item))} /></label><label><span>{t('user.charity.rpmLimit')}</span><input type="number" min="0" step="1" value={key.rpm} onChange={(event) => setNewKeys((all) => all.map((item, i) => i === index ? { ...item, rpm: event.target.value } : item))} /></label></div>{newEndpoint.connectorType === 'openai-compatible' ? <fieldset className="policy-fieldset"><legend>{t('user.charity.storePolicy')}</legend><label className="checkbox-label"><input type="checkbox" checked={key.forceStoreFalse} onChange={(event) => setNewKeys((all) => all.map((item, i) => i === index ? { ...item, forceStoreFalse: event.target.checked } : item))} /><span>{t('user.charity.storeExperimental')}</span></label><p className="risk-note">{t('user.charity.storePolicyRisk')}</p></fieldset> : null}</fieldset>)}
            <button type="button" className="btn btn-secondary" onClick={() => setNewKeys((all) => [...all, emptyKey()])}>{t('user.charity.addKey')}</button>
          </div>
        ) : null}
        <div className="form-grid"><label className="full-width"><span>{t('user.charity.donationDescription')}</span><textarea value={description} onChange={(event) => setDescription(event.target.value)} maxLength={4096} required /></label><label><span>{t('user.charity.expires')}</span><input type="datetime-local" value={expiresAt} onChange={(event) => setExpiresAt(event.target.value)} disabled={editing && initial?.status !== 'pending'} /><small className="muted">{t('user.charity.expiryHint')}</small></label></div>
        {validation ? <p className="field-error" role="alert">{validation}</p> : null}
        {requestError ? <ErrorState error={requestError} /> : null}
        <div className="form-actions"><button type="submit" className="btn btn-primary" disabled={busy || !existingEndpointContextReady}>{busy ? t('common.working') : editing ? t('common.save') : t('user.charity.submit')}</button></div>
      </form>
    </Card>
  );
}

function DonationCard({ donation, onEdit }: { donation: Donation; onEdit: (item: Donation) => void }) {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  // A donation detail omits force_store_false for Anthropic keys.  Resolve the
  // owner's endpoint projection first so the normalizer never treats an
  // omitted OpenAI field as not_applicable.  Pending nested-endpoint rows have
  // no keys yet and remain readable without this context.
  const endpoints = useEndpoints(true);
  const hasEndpoint = Boolean(donation.endpoint_id);
  const endpointConnectorType = donation.endpoint_id
    ? endpoints.data?.find((endpoint) => endpoint.id === donation.endpoint_id)?.connector_type
    : undefined;
  const endpointMissing = hasEndpoint && !endpoints.isPending && !endpoints.error && !endpointConnectorType;
  const detail = useDonation(
    donation.id,
    !hasEndpoint || Boolean(endpointConnectorType),
    endpointConnectorType,
  );
  const remove = useDeleteDonation();
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [deleteError, setDeleteError] = useState<unknown>(null);
  const item = detail.data ?? donation;
  const statusLabel = t(`user.charity.status.${item.status}`, { defaultValue: item.status });
  const endpointContextError = endpointMissing
    ? new ApiError('endpoint_context_unavailable', t('user.charity.endpointContextUnavailable'), 200)
    : null;
  const detailReady = !detail.isPending && !detail.error && Boolean(detail.data)
    && (!hasEndpoint || (!endpoints.isPending && !endpoints.error && Boolean(endpointConnectorType)));
  const canEdit = item.status === 'pending' && detailReady;
  const removeDonation = async () => {
    setDeleteError(null);
    try {
      await remove.mutateAsync(item.id);
    } catch (requestError) {
      if (stationClosed(requestError)) return;
      const refreshError = await refetchAuthoritativeQueries(queryClient, [
        { queryKey: userKeys.donations, exact: true },
        {
          queryKey: userKeys.donation(item.id),
          exact: false,
          ignoreError: isNotFoundError,
          removeOnIgnoredError: true,
        },
      ]);
      setDeleteError(requestError ?? refreshError);
      return;
    }
    const refreshError = await refetchAuthoritativeQueries(queryClient, [
      { queryKey: userKeys.donations, exact: true },
      {
        queryKey: userKeys.donation(item.id),
        exact: false,
        ignoreError: isNotFoundError,
        removeOnIgnoredError: true,
      },
    ]);
    if (refreshError) {
      setDeleteError(refreshError);
      return;
    }
    setDeleteOpen(false);
  };
  return (
    <article className={`item-card donation-card ${item.status === 'deleted' ? 'is-warning' : ''}`}>
      <div className="item-header"><div><h3>{t('user.charity.donationNumber', { id: item.id })}</h3><p className="item-meta">{item.endpoint_base_url} · {formatDateTime(item.created_at)}</p></div><StatusBadge active={item.enabled && item.status === 'approved'} label={statusLabel} danger={item.status === 'rejected' || item.status === 'deleted'} /></div>
      <p className="item-note">{item.description}</p>
      <dl className="detail-grid"><div className="detail-row"><dt>{t('user.charity.expires')}</dt><dd>{item.expires_at ? formatDateTime(item.expires_at) : t('user.charity.never')}</dd></div><div className="detail-row"><dt>{t('user.charity.keys')}</dt><dd>{item.keys.length || '—'}</dd></div></dl>
      {item.review_note ? <p className="inline-notice"><strong>{t('user.charity.reviewNote')}:</strong> {item.review_note}</p> : null}
      {hasEndpoint && endpoints.isPending ? <LoadingState label={t('user.charity.loadingEndpointContext')} /> : null}
      {hasEndpoint && endpoints.error ? <ErrorState error={endpoints.error} onRetry={() => void endpoints.refetch()} /> : null}
      {endpointContextError ? <ErrorState error={endpointContextError} onRetry={() => void endpoints.refetch()} /> : null}
      {!endpointContextError && !endpoints.error && !endpoints.isPending && (!hasEndpoint || endpointConnectorType) && detail.isPending ? <p className="muted">{t('user.charity.loadingDetails')}</p> : null}
      {!endpointContextError && !endpoints.error && !endpoints.isPending && (!hasEndpoint || endpointConnectorType) && detail.error ? <ErrorState error={detail.error} onRetry={() => void detail.refetch()} /> : null}
      {item.keys.length ? <ul className="plain-list donation-key-list">{item.keys.map((key) => <li key={key.id}><span className="mono">{key.display ?? t('user.charity.keyHidden')}</span><span className="muted">{t('user.charity.keyLimits', { concurrency: key.max_concurrency || t('user.charity.unlimited'), rpm: key.rpm_limit || t('user.charity.unlimited') })} · {key.enabled ? t('common.enabled') : t('common.disabled')} · {formatCreditsFromMilli(key.credits_used_milli).display}{key.force_store_false === 'not_applicable' ? '' : ` · ${t('user.charity.storeReadOnly')}: ${key.force_store_false ? t('user.charity.storeNoStore') : t('user.charity.storeDefault')}`}</span></li>)}</ul> : null}
      {item.reviews.length ? <details className="review-history"><summary>{t('user.charity.reviewHistory')}</summary><ul className="plain-list">{item.reviews.map((review) => <li key={review.id}><strong>{review.action}</strong> · {formatDateTime(review.created_at)}{review.note ? ` · ${review.note}` : ''}</li>)}</ul></details> : null}
      {remove.error ? <ErrorState error={remove.error} /> : null}
      {deleteError ? <ErrorState error={deleteError} /> : null}
      <div className="form-actions">{item.status === 'pending' ? <button type="button" className="btn btn-secondary" onClick={() => { if (detail.data) onEdit(detail.data); }} disabled={!canEdit}>{t('common.edit')}</button> : null}{item.status !== 'deleted' ? <button type="button" className="btn btn-danger" onClick={() => { setDeleteError(null); setDeleteOpen(true); }} disabled={!detailReady || remove.isPending}>{t('user.charity.softDelete')}</button> : null}</div>
      <ConfirmDialog open={deleteOpen} title={t('user.charity.deleteTitle')} description={t('user.charity.deleteBody')} confirmLabel={t('user.charity.softDelete')} danger busy={remove.isPending} onCancel={() => setDeleteOpen(false)} onConfirm={() => void removeDonation()} />
    </article>
  );
}

function CharityContent() {
  const { t } = useTranslation();
  const session = useUserSession();
  const models = useCharityModels(true);
  const donations = useDonations(true);
  const [editing, setEditing] = useState<Donation | undefined>();
  const [now, setNow] = useState(() => Math.floor(Date.now() / 1000));
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Math.floor(Date.now() / 1000)), 30_000);
    return () => window.clearInterval(timer);
  }, []);
  const activeDonations = donations.data ?? [];
  const charitySuspended = Boolean(session.data?.user?.charity_suspended_until && session.data.user.charity_suspended_until > now);
  const negative = session.data?.user.credits.startsWith('-') ?? false;
  const noCandidates = models.data?.length === 0;
  const canSubmit = !charitySuspended;
  return (
    <div className="page">
      <PageHeader eyebrow={t('app.name')} title={t('user.charity.title')} description={t('user.charity.description')} actions={<Link className="btn btn-secondary" to="/models">{t('user.charity.personalModels')}</Link>} />
      {charitySuspended ? <p className="field-error" role="alert">{t('user.charity.suspended')}</p> : null}
      {negative ? <p className="inline-notice" role="status">{t('user.charity.negativeFree')}</p> : null}
      {session.data?.user.is_banned ? <p className="field-error" role="alert">{t('user.charity.banned')}</p> : null}
      <p className="inline-notice secret-panel" role="note">
        <strong>{t('user.charity.upstreamPrivacyTitle')}</strong>{' '}
        {t('user.charity.upstreamPrivacyWarning')}
      </p>
      <section aria-labelledby="charity-models-title"><div className="card-title-row"><h2 id="charity-models-title">{t('user.charity.pricesTitle')}</h2></div>{models.isPending ? <LoadingState /> : models.error ? <ErrorState error={models.error} onRetry={() => void models.refetch()} /> : noCandidates ? <EmptyState title={t('user.charity.noModels')} body={t('user.charity.noModelsBody')} /> : <div className="item-list">{models.data.map((model) => <CharityModelCard key={model.id} model={model} />)}</div>}</section>
      <section aria-labelledby="donations-title"><div className="card-title-row"><h2 id="donations-title">{t('user.charity.donationsTitle')}</h2></div>{donations.isPending ? <LoadingState /> : donations.error ? <ErrorState error={donations.error} onRetry={() => void donations.refetch()} /> : activeDonations.length === 0 ? <EmptyState title={t('user.charity.noDonations')} body={t('user.charity.noDonationsBody')} /> : <div className="item-list">{activeDonations.map((donation) => <DonationCard key={donation.id} donation={donation} onEdit={(item) => setEditing(item)} />)}</div>}</section>
      {canSubmit ? <DonationForm key={editing?.id ?? 'new'} initial={editing} onSaved={() => setEditing(undefined)} /> : null}
      {editing ? <button type="button" className="btn btn-quiet" onClick={() => setEditing(undefined)}>{t('common.cancel')}</button> : null}
      {isApiError(models.error) && models.error.code === 'feature_disabled' ? <p className="muted">{t('user.charity.featureDisabled')}</p> : null}
    </div>
  );
}

export function CharityPage() {
  return <UserPageGate><CharityContent /></UserPageGate>;
}
