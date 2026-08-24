import { useEffect, useState, type FormEvent } from 'react';
import { Link } from 'react-router';
import { useTranslation } from 'react-i18next';
import { CharityPriceTable } from '@shared/components/CharityPriceTable';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { Card, EmptyState, ErrorState, LoadingState, PageHeader, StatusBadge } from '@shared/components/States';
import { formatDateTime } from '@shared/utils/datetime';
import { formatCreditsFromMilli } from '@shared/utils/formatNumber';
import { isApiError } from '@shared/query/http';
import {
  type CharityModel,
  type Donation,
  type DonationPayload,
  useCharityModels,
  useCreateDonation,
  useDeleteDonation,
  useDonation,
  useDonations,
  useEndpoints,
  useEndpointKeys,
  useUpdateDonation,
  useUserSession,
} from '../data';
import { UserPageGate } from '../components/UserPageGate';

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
    </article>
  );
}

interface NewKeyDraft {
  secret: string;
  note: string;
  maxConcurrency: string;
  rpm: string;
}

const emptyKey = (): NewKeyDraft => ({ secret: '', note: '', maxConcurrency: '0', rpm: '0' });

function DonationForm({ onSaved, initial }: { onSaved: () => void; initial?: Donation }) {
  const { t } = useTranslation();
  const endpoints = useEndpoints(true);
  const create = useCreateDonation();
  const update = useUpdateDonation();
  const [mode, setMode] = useState<'existing' | 'new'>('existing');
  const [endpointId, setEndpointId] = useState(initial?.endpoint_id ?? '');
  const [selectedKeys, setSelectedKeys] = useState<string[]>(initial?.keys.map((key) => key.endpoint_key_id ?? '').filter(Boolean) ?? []);
  const [newEndpoint, setNewEndpoint] = useState({ baseUrl: '', note: '' });
  const [newKeys, setNewKeys] = useState<NewKeyDraft[]>([emptyKey()]);
  const [description, setDescription] = useState(initial?.description ?? '');
  const [expiresAt, setExpiresAt] = useState('');
  const [validation, setValidation] = useState('');
  const [requestError, setRequestError] = useState<unknown>(null);
  const editing = Boolean(initial);
  const keys = useEndpointKeys(endpointId || undefined, Boolean(endpointId));
  const enabledEndpoints = endpoints.data?.filter((endpoint) => endpoint.enabled) ?? [];
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setValidation('');
    setRequestError(null);
    if (!description.trim()) {
      setValidation(t('user.charity.descriptionRequired'));
      return;
    }
    let payload: DonationPayload;
    if (editing) {
      payload = { description: description.trim() };
      if (expiresAt) payload.expires_at = Math.floor(new Date(expiresAt).getTime() / 1000);
    } else if (mode === 'existing') {
      if (!endpointId || selectedKeys.length === 0) {
        setValidation(t('user.charity.chooseKeys'));
        return;
      }
      const endpointNumber = Number(endpointId);
      const keyNumbers = selectedKeys.map(Number);
      if (!Number.isSafeInteger(endpointNumber) || endpointNumber <= 0 || keyNumbers.some((id) => !Number.isSafeInteger(id) || id <= 0)) {
        setValidation(t('common.formInvalid'));
        return;
      }
      payload = { description: description.trim(), existing_endpoint: { endpoint_id: endpointNumber, key_ids: keyNumbers } };
    } else {
      const submittedKeys = newKeys.map((key) => ({ ...key }));
      // Clear the DOM-bound secret state before the request begins. No secret
      // is ever placed in React Query state or retained after an error.
      setNewKeys(submittedKeys.map((key) => ({ ...key, secret: '' })));
      if (!newEndpoint.baseUrl.trim() || submittedKeys.some((key) => !key.secret)) {
        setValidation(t('user.charity.newEndpointRequired'));
        return;
      }
      payload = {
        description: description.trim(),
        new_endpoint: {
          base_url: newEndpoint.baseUrl.trim(),
          note: newEndpoint.note.trim() || undefined,
          keys: submittedKeys.map((key) => ({
            secret: key.secret,
            note: key.note.trim() || undefined,
            max_concurrency: Math.max(0, Number(key.maxConcurrency) || 0),
            rpm_limit: Math.max(0, Number(key.rpm) || 0),
          })),
        },
      };
    }
    try {
      if (initial) await update.mutateAsync({ id: initial.id, ...payload });
      else await create.mutateAsync(payload);
      setDescription('');
      setExpiresAt('');
      setNewKeys([emptyKey()]);
      onSaved();
    } catch (error) {
      setRequestError(error);
    }
  };

  if (endpoints.isPending) return <LoadingState />;
  if (endpoints.error) return <ErrorState error={endpoints.error} onRetry={() => void endpoints.refetch()} />;

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
        {!editing && mode === 'existing' ? (
          <div className="form-grid">
            <label><span>{t('user.charity.endpoint')}</span><select value={endpointId} onChange={(event) => { setEndpointId(event.target.value); setSelectedKeys([]); }} required>
              <option value="">{t('user.charity.chooseEndpoint')}</option>
              {enabledEndpoints.map((endpoint) => <option key={endpoint.id} value={endpoint.id}>{endpoint.base_url}{endpoint.note ? ` · ${endpoint.note}` : ''}</option>)}
            </select></label>
            <fieldset className="key-picker"><legend>{t('user.charity.keys')}</legend>
              {keys.isPending ? <LoadingState /> : keys.error ? <ErrorState error={keys.error} onRetry={() => void keys.refetch()} /> : keys.data?.filter((key) => key.enabled).map((key) => (
                <label className="checkbox-label" key={key.id}><input type="checkbox" checked={selectedKeys.includes(key.id)} onChange={(event) => setSelectedKeys((current) => event.target.checked ? [...current, key.id] : current.filter((id) => id !== key.id))} /><span>{key.display ?? t('user.charity.keyHidden')}{key.note ? ` · ${key.note}` : ''}</span></label>
              ))}
              {endpointId && !keys.isPending && !keys.error && keys.data?.filter((key) => key.enabled).length === 0 ? <p className="muted">{t('user.charity.noKeys')}</p> : null}
            </fieldset>
          </div>
        ) : null}
        {!editing && mode === 'new' ? (
          <div className="nested-panel">
            <div className="form-grid"><label><span>{t('user.charity.baseUrl')}</span><input value={newEndpoint.baseUrl} onChange={(event) => setNewEndpoint({ ...newEndpoint, baseUrl: event.target.value })} maxLength={2048} required /></label><label><span>{t('user.charity.endpointNote')}</span><input value={newEndpoint.note} onChange={(event) => setNewEndpoint({ ...newEndpoint, note: event.target.value })} maxLength={512} /></label></div>
            {newKeys.map((key, index) => <fieldset className="donation-key-draft" key={index}><legend>{t('user.charity.newKey', { number: index + 1 })}</legend><div className="form-grid"><label><span>{t('user.charity.secret')}</span><input type="password" value={key.secret} onChange={(event) => setNewKeys((all) => all.map((item, i) => i === index ? { ...item, secret: event.target.value } : item))} autoComplete="new-password" maxLength={4096} required /></label><label><span>{t('user.charity.keyNote')}</span><input value={key.note} onChange={(event) => setNewKeys((all) => all.map((item, i) => i === index ? { ...item, note: event.target.value } : item))} maxLength={512} /></label><label><span>{t('user.charity.maxConcurrency')}</span><input type="number" min="0" step="1" value={key.maxConcurrency} onChange={(event) => setNewKeys((all) => all.map((item, i) => i === index ? { ...item, maxConcurrency: event.target.value } : item))} /></label><label><span>{t('user.charity.rpmLimit')}</span><input type="number" min="0" step="1" value={key.rpm} onChange={(event) => setNewKeys((all) => all.map((item, i) => i === index ? { ...item, rpm: event.target.value } : item))} /></label></div></fieldset>)}
            <button type="button" className="btn btn-secondary" onClick={() => setNewKeys((all) => [...all, emptyKey()])}>{t('user.charity.addKey')}</button>
          </div>
        ) : null}
        <div className="form-grid"><label className="full-width"><span>{t('user.charity.donationDescription')}</span><textarea value={description} onChange={(event) => setDescription(event.target.value)} maxLength={4096} required /></label><label><span>{t('user.charity.expires')}</span><input type="datetime-local" value={expiresAt} onChange={(event) => setExpiresAt(event.target.value)} disabled={editing && initial?.status !== 'pending'} /><small className="muted">{t('user.charity.expiryHint')}</small></label></div>
        {validation ? <p className="field-error" role="alert">{validation}</p> : null}
        {requestError ? <ErrorState error={requestError} /> : null}
        <div className="form-actions"><button type="submit" className="btn btn-primary" disabled={create.isPending || update.isPending}>{create.isPending || update.isPending ? t('common.working') : editing ? t('common.save') : t('user.charity.submit')}</button></div>
      </form>
    </Card>
  );
}

function DonationCard({ donation, onEdit }: { donation: Donation; onEdit: () => void }) {
  const { t } = useTranslation();
  const detail = useDonation(donation.id, true);
  const remove = useDeleteDonation();
  const [deleteOpen, setDeleteOpen] = useState(false);
  const item = detail.data ?? donation;
  const statusLabel = t(`user.charity.status.${item.status}`, { defaultValue: item.status });
  const canEdit = item.status === 'pending';
  const removeDonation = async () => {
    try { await remove.mutateAsync(item.id); setDeleteOpen(false); } catch { /* ErrorState below remains visible. */ }
  };
  return (
    <article className={`item-card donation-card ${item.status === 'deleted' ? 'is-warning' : ''}`}>
      <div className="item-header"><div><h3>{t('user.charity.donationNumber', { id: item.id })}</h3><p className="item-meta">{item.endpoint_base_url} · {formatDateTime(item.created_at)}</p></div><StatusBadge active={item.enabled && item.status === 'approved'} label={statusLabel} danger={item.status === 'rejected' || item.status === 'deleted'} /></div>
      <p className="item-note">{item.description}</p>
      <dl className="detail-grid"><div className="detail-row"><dt>{t('user.charity.expires')}</dt><dd>{item.expires_at ? formatDateTime(item.expires_at) : t('user.charity.never')}</dd></div><div className="detail-row"><dt>{t('user.charity.keys')}</dt><dd>{item.keys.length || '—'}</dd></div></dl>
      {item.review_note ? <p className="inline-notice"><strong>{t('user.charity.reviewNote')}:</strong> {item.review_note}</p> : null}
      {detail.isPending ? <p className="muted">{t('user.charity.loadingDetails')}</p> : null}
      {item.keys.length ? <ul className="plain-list donation-key-list">{item.keys.map((key) => <li key={key.id}><span className="mono">{key.display ?? t('user.charity.keyHidden')}</span><span className="muted">{t('user.charity.keyLimits', { concurrency: key.max_concurrency || t('user.charity.unlimited'), rpm: key.rpm_limit || t('user.charity.unlimited') })} · {key.enabled ? t('common.enabled') : t('common.disabled')} · {formatCreditsFromMilli(key.credits_used_milli).display}</span></li>)}</ul> : null}
      {item.reviews.length ? <details className="review-history"><summary>{t('user.charity.reviewHistory')}</summary><ul className="plain-list">{item.reviews.map((review) => <li key={review.id}><strong>{review.action}</strong> · {formatDateTime(review.created_at)}{review.note ? ` · ${review.note}` : ''}</li>)}</ul></details> : null}
      {remove.error ? <ErrorState error={remove.error} /> : null}
      <div className="form-actions">{canEdit ? <button type="button" className="btn btn-secondary" onClick={onEdit}>{t('common.edit')}</button> : null}{item.status !== 'deleted' ? <button type="button" className="btn btn-danger" onClick={() => setDeleteOpen(true)} disabled={remove.isPending}>{t('user.charity.softDelete')}</button> : null}</div>
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
      <section aria-labelledby="donations-title"><div className="card-title-row"><h2 id="donations-title">{t('user.charity.donationsTitle')}</h2></div>{donations.isPending ? <LoadingState /> : donations.error ? <ErrorState error={donations.error} onRetry={() => void donations.refetch()} /> : activeDonations.length === 0 ? <EmptyState title={t('user.charity.noDonations')} body={t('user.charity.noDonationsBody')} /> : <div className="item-list">{activeDonations.map((donation) => <DonationCard key={donation.id} donation={donation} onEdit={() => setEditing(donation)} />)}</div>}</section>
      {canSubmit ? <DonationForm key={editing?.id ?? 'new'} initial={editing} onSaved={() => setEditing(undefined)} /> : null}
      {editing ? <button type="button" className="btn btn-quiet" onClick={() => setEditing(undefined)}>{t('common.cancel')}</button> : null}
      {isApiError(models.error) && models.error.code === 'feature_disabled' ? <p className="muted">{t('user.charity.featureDisabled')}</p> : null}
    </div>
  );
}

export function CharityPage() {
  return <UserPageGate><CharityContent /></UserPageGate>;
}
