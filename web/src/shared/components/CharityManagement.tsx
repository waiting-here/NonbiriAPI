import { useEffect, useState, type FormEvent } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { CharityPriceTable, type CharityPriceRow } from './CharityPriceTable';
import { Card, EmptyState, ErrorState, LoadingState, PageHeader, StatusBadge } from './States';
import { ConfirmDialog } from './ConfirmDialog';
import { isForbidden } from '@shared/query/http';
import { formatCreditsFromMilli } from '@shared/utils/formatNumber';
import {
  charityManagementKeys,
  type CharityManagementFrame,
  type CharityModelPayload,
  type ManagementCharityModel,
  type ManagementDonation,
  type ManagementDonationKey,
  type ManagementPriceSet,
  useCharityAdminSettings,
  useCreateManagedBinding,
  useCreateManagedModel,
  useDeleteManagedBinding,
  useDeleteManagedDonation,
  useDeleteManagedModel,
  useManagementBindings,
  useManagementDonation,
  useManagementDonations,
  useManagementModels,
  usePatchCharityAdminSetting,
  useReviewDonation,
  useUpdateManagedBinding,
  useUpdateManagedModel,
} from '../charityManagement';

const PRICE_KEYS: Array<keyof ManagementPriceSet> = [
  'request_user_price_milli', 'request_donor_reward_milli',
  'uncached_user_price_milli', 'cache_write_user_price_milli',
  'cache_read_user_price_milli', 'output_user_price_milli',
  'uncached_donor_reward_milli', 'cache_write_donor_reward_milli',
  'cache_read_donor_reward_milli', 'output_donor_reward_milli',
];
const TOKEN_PRICE_KEYS = PRICE_KEYS.slice(2);
const ZERO_PRICES: ManagementPriceSet = {
  request_user_price_milli: '0', request_donor_reward_milli: '0',
  uncached_user_price_milli: '0', cache_write_user_price_milli: '0',
  cache_read_user_price_milli: '0', output_user_price_milli: '0',
  uncached_donor_reward_milli: '0', cache_write_donor_reward_milli: '0',
  cache_read_donor_reward_milli: '0', output_donor_reward_milli: '0',
};

function useText(frame: CharityManagementFrame) {
  const { t } = useTranslation();
  const root = frame === 'admin' ? 'admin.charity' : 'user.steward';
  return (key: string, values?: Record<string, unknown>) => t(`${root}.${key}`, values);
}

function errorText(error: unknown): string {
  return error instanceof Error ? error.message : 'Request failed.';
}

function unixDate(value: number | undefined): string {
  if (!value) return '';
  const date = new Date(value * 1000);
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

function dateUnix(value: string): number | null {
  if (!value) return null;
  const ms = Date.parse(value);
  return Number.isFinite(ms) && ms > 0 ? Math.floor(ms / 1000) : null;
}

function rowsForModel(model: ManagementCharityModel, tr: (key: string) => string): CharityPriceRow[] {
  const p = model.prices;
  if (model.pricing_mode === 'per_request') {
    return [{ label: tr('priceRequest'), userMilli: p.request_user_price_milli, rewardMilli: p.request_donor_reward_milli }];
  }
  return [
    ['priceUncached', 'uncached_user_price_milli', 'uncached_donor_reward_milli'],
    ['priceCacheWrite', 'cache_write_user_price_milli', 'cache_write_donor_reward_milli'],
    ['priceCacheRead', 'cache_read_user_price_milli', 'cache_read_donor_reward_milli'],
    ['priceOutput', 'output_user_price_milli', 'output_donor_reward_milli'],
  ].map(([label, user, reward]) => ({ label: tr(label), userMilli: p[user as keyof ManagementPriceSet], rewardMilli: p[reward as keyof ManagementPriceSet] }));
}

function ModelEditor({ frame, initial, onClose }: { frame: CharityManagementFrame; initial?: ManagementCharityModel; onClose: () => void }) {
  const tr = useText(frame);
  const create = useCreateManagedModel(frame);
  const update = useUpdateManagedModel(frame);
  const [provider, setProvider] = useState(initial?.provider ?? '');
  const [model, setModel] = useState(initial?.model ?? '');
  const [mode, setMode] = useState(initial?.pricing_mode ?? 'per_request');
  const [enabled, setEnabled] = useState(initial?.enabled ?? true);
  const [prices, setPrices] = useState<ManagementPriceSet>(initial?.prices ?? ZERO_PRICES);
  const [percent, setPercent] = useState(String(initial?.discount.percent ?? 100));
  const [discountEnabled, setDiscountEnabled] = useState(initial?.discount.enabled ?? false);
  const [start, setStart] = useState(unixDate(initial?.discount.start_at));
  const [end, setEnd] = useState(unixDate(initial?.discount.end_at));
  const [validation, setValidation] = useState('');
  const mutation = initial ? update : create;
  const visibleKeys = mode === 'per_request' ? PRICE_KEYS.slice(0, 2) : TOKEN_PRICE_KEYS;

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setValidation('');
    const trimmedProvider = provider.trim();
    const trimmedModel = model.trim();
    const percentNumber = Number(percent);
    if (!trimmedProvider || !trimmedModel || trimmedProvider.startsWith('[公益]')) {
      setValidation(tr('providerInvalid'));
      return;
    }
    if (!/^(0|[1-9]\d*)$/.test(percent) || !Number.isInteger(percentNumber) || percentNumber < 0 || percentNumber > 100) {
      setValidation(tr('discountInvalid'));
      return;
    }
    const nextPrices = { ...ZERO_PRICES, ...prices };
    for (const key of visibleKeys) {
      if (!/^(0|[1-9]\d*)$/.test(nextPrices[key])) {
        setValidation(tr('amountInvalid'));
        return;
      }
    }
    const startAt = dateUnix(start);
    const endAt = dateUnix(end);
    if (start && startAt === null || end && endAt === null || startAt !== null && endAt !== null && endAt <= startAt) {
      setValidation(tr('discountIntervalInvalid'));
      return;
    }
    const payload: CharityModelPayload = {
      provider: trimmedProvider,
      model: trimmedModel,
      pricing_mode: mode,
      enabled,
      prices: nextPrices,
      discount: { percent: percentNumber, enabled: discountEnabled, start_at: startAt, end_at: endAt },
    };
    try {
      if (initial) await update.mutateAsync({ id: initial.id, ...payload });
      else await create.mutateAsync(payload);
      onClose();
    } catch {
      // The server's bounded conflict/validation message is rendered below.
    }
  };

  return (
    <form className="card charity-editor" onSubmit={submit} noValidate>
      <div className="card-title-row"><h3>{initial ? tr('editModel') : tr('newModel')}</h3><button type="button" className="btn btn-secondary" onClick={onClose}>{tr('cancel')}</button></div>
      <div className="form-grid">
        <label><span>{tr('provider')}</span><input value={provider} onChange={(event) => setProvider(event.target.value)} maxLength={128} required /></label>
        <label><span>{tr('model')}</span><input value={model} onChange={(event) => setModel(event.target.value)} maxLength={256} required /></label>
        <label><span>{tr('pricingMode')}</span><select value={mode} onChange={(event) => setMode(event.target.value as 'per_request' | 'per_token')}><option value="per_request">{tr('perRequest')}</option><option value="per_token">{tr('perToken')}</option></select></label>
        <label className="checkbox-label"><input type="checkbox" checked={enabled} onChange={(event) => setEnabled(event.target.checked)} /><span>{tr('enabled')}</span></label>
      </div>
      <fieldset><legend>{tr('prices')}</legend><div className="form-grid">
        {visibleKeys.map((key) => <label key={key}><span>{tr(key)}</span><input value={prices[key]} inputMode="numeric" onChange={(event) => setPrices((current) => ({ ...current, [key]: event.target.value }))} aria-label={tr(key)} /></label>)}
      </div><p className="muted">{tr('amountHint')}</p></fieldset>
      <fieldset><legend>{tr('discount')}</legend><div className="form-grid">
        <label><span>{tr('discountPercent')}</span><input value={percent} inputMode="numeric" onChange={(event) => setPercent(event.target.value)} min="0" max="100" /></label>
        <label className="checkbox-label"><input type="checkbox" checked={discountEnabled} onChange={(event) => setDiscountEnabled(event.target.checked)} /><span>{tr('discountEnabled')}</span></label>
        <label><span>{tr('discountStart')}</span><input type="datetime-local" value={start} onChange={(event) => setStart(event.target.value)} /></label>
        <label><span>{tr('discountEnd')}</span><input type="datetime-local" value={end} onChange={(event) => setEnd(event.target.value)} /></label>
      </div><p className="muted">{tr('discountHint')}</p></fieldset>
      {validation ? <p className="field-error" role="alert">{validation}</p> : null}
      {mutation.error ? <p className="field-error" role="alert">{errorText(mutation.error)}</p> : null}
      <div className="form-actions"><button className="btn btn-primary" type="submit" disabled={mutation.isPending}>{mutation.isPending ? tr('working') : tr('save')}</button></div>
    </form>
  );
}

function Bindings({ frame, model }: { frame: CharityManagementFrame; model: ManagementCharityModel }) {
  const tr = useText(frame);
  const client = useQueryClient();
  const bindings = useManagementBindings(frame, model.id);
  useEffect(() => {
    if (isForbidden(bindings.error)) client.removeQueries({ queryKey: charityManagementKeys.root(frame) });
  }, [bindings.error, client, frame]);
  const create = useCreateManagedBinding(frame);
  const update = useUpdateManagedBinding(frame);
  const remove = useDeleteManagedBinding(frame);
  const [donationKeyID, setDonationKeyID] = useState('');
  const [upstream, setUpstream] = useState('');
  const [ord, setOrd] = useState('0');
  const [error, setError] = useState('');
  const [editing, setEditing] = useState<string | null>(null);

  const add = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault(); setError('');
    if (!/^\d+$/.test(donationKeyID) || !upstream.trim() || !/^\d+$/.test(ord)) { setError(tr('bindingInvalid')); return; }
    try { await create.mutateAsync({ modelId: model.id, donation_key_id: donationKeyID, upstream_model_id: upstream.trim(), ord: Number(ord) }); setDonationKeyID(''); setUpstream(''); setOrd('0'); } catch (requestError) { setError(errorText(requestError)); }
  };
  return <details className="charity-bindings"><summary>{tr('bindings')}</summary>
    {bindings.isPending ? <LoadingState /> : bindings.error ? <ErrorState error={bindings.error} /> : bindings.data?.length ? <div className="table-scroll"><table className="compact-table"><thead><tr><th>{tr('donationKey')}</th><th>{tr('upstreamModel')}</th><th>{tr('order')}</th><th>{tr('actions')}</th></tr></thead><tbody>{bindings.data.map((binding) => <tr key={binding.id}>
      <td><span className="mono">{binding.key_display ?? tr('keyFragmentUnavailable')}</span><small className="muted"> · {binding.donation_key_id}</small></td><td>{editing === binding.id ? <input defaultValue={binding.upstream_model_id} aria-label={tr('upstreamModel')} onBlur={async (event) => { try { await update.mutateAsync({ modelId: model.id, bindingId: binding.id, upstream_model_id: event.target.value }); setEditing(null); } catch (requestError) { setError(errorText(requestError)); } }} /> : binding.upstream_model_id}</td><td>{binding.ord}</td><td><button type="button" className="btn btn-secondary" onClick={() => setEditing(binding.id)}>{tr('edit')}</button> <button type="button" className="btn btn-danger" onClick={() => { if (window.confirm(tr('deleteBindingConfirm'))) void remove.mutateAsync({ modelId: model.id, bindingId: binding.id }); }}>{tr('delete')}</button></td>
    </tr>)}</tbody></table></div> : <p className="muted">{tr('noBindings')}</p>}
    <form className="form-grid binding-form" onSubmit={add}><label><span>{tr('donationKeyId')}</span><input value={donationKeyID} inputMode="numeric" onChange={(event) => setDonationKeyID(event.target.value)} /></label><label><span>{tr('upstreamModel')}</span><input value={upstream} onChange={(event) => setUpstream(event.target.value)} /></label><label><span>{tr('order')}</span><input value={ord} inputMode="numeric" onChange={(event) => setOrd(event.target.value)} /></label><button className="btn btn-secondary" type="submit" disabled={create.isPending}>{tr('addBinding')}</button></form>
    {error ? <p className="field-error" role="alert">{error}</p> : null}
  </details>;
}

function ModelCard({ frame, model, onEdit, onDelete }: { frame: CharityManagementFrame; model: ManagementCharityModel; onEdit: () => void; onDelete: () => void }) {
  const tr = useText(frame);
  const rate = model.success_samples ? Math.round(model.success_count * 100 / model.success_samples) : 0;
  return <article className="item-card charity-model-card"><div className="item-header"><div><h3>{model.full_name}</h3><p className="item-meta">{model.pricing_mode === 'per_token' ? tr('perToken') : tr('perRequest')} · {tr('successRate', { rate, count: model.success_samples })}</p></div><StatusBadge active={model.enabled} label={model.enabled ? tr('enabled') : tr('disabled')} danger={!model.enabled} /></div>
    <CharityPriceTable mode={model.pricing_mode} rows={rowsForModel(model, tr)} discount={{ percent: model.discount.percent, enabled: model.discount.enabled, startAt: model.discount.start_at, endAt: model.discount.end_at }} />
    <div className="form-actions"><button type="button" className="btn btn-secondary" onClick={onEdit}>{tr('edit')}</button><button type="button" className="btn btn-danger" onClick={onDelete}>{tr('delete')}</button></div>
    <Bindings frame={frame} model={model} />
  </article>;
}

function DonationKeyEditor({ frame, keyRow, onChange }: { frame: CharityManagementFrame; keyRow: ManagementDonationKey; onChange: (next: ManagementDonationKey) => void }) {
  const tr = useText(frame);
  return <fieldset className="donation-review-key"><legend>{keyRow.display ?? tr('keyHidden')}</legend><div className="form-grid">
    <label><span>{tr('maxConcurrency')}</span><input type="number" min="0" max="100000" value={keyRow.max_concurrency} onChange={(event) => onChange({ ...keyRow, max_concurrency: Number(event.target.value) })} /></label>
    <label><span>{tr('rpmLimit')}</span><input type="number" min="0" max="4096" value={keyRow.rpm_limit} onChange={(event) => onChange({ ...keyRow, rpm_limit: Number(event.target.value) })} /></label>
    <label><span>{tr('usageCap')}</span><input value={keyRow.credits_usage_cap_milli} inputMode="numeric" onChange={(event) => onChange({ ...keyRow, credits_usage_cap_milli: event.target.value })} /></label>
    <label className="checkbox-label"><input type="checkbox" checked={keyRow.enabled} onChange={(event) => onChange({ ...keyRow, enabled: event.target.checked })} /><span>{tr('enabled')}</span></label>
  </div><p className="muted">{tr('usageUsed', { value: formatCreditsFromMilli(keyRow.credits_used_milli).exact })}</p></fieldset>;
}

function DonationCard({ frame, donation, onDeleted }: { frame: CharityManagementFrame; donation: ManagementDonation; onDeleted: () => void }) {
  const tr = useText(frame);
  const detail = useManagementDonation(frame, donation.id);
  const client = useQueryClient();
  useEffect(() => {
    if (isForbidden(detail.error)) client.removeQueries({ queryKey: charityManagementKeys.root(frame) });
  }, [client, detail.error, frame]);
  const review = useReviewDonation(frame);
  const remove = useDeleteManagedDonation(frame);
  const [note, setNote] = useState('');
  const [actionError, setActionError] = useState('');
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [expiry, setExpiry] = useState(unixDate(donation.expires_at));
  const [draftKeys, setDraftKeys] = useState<ManagementDonationKey[]>([]);
  const full = detail.data;
  useEffect(() => {
    if (!full?.keys) return;
    // The detail query is the authoritative replacement for this edit draft.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setDraftKeys(full.keys);
  }, [full?.keys]);
  const run = async (action: string) => {
    setActionError('');
    try { await review.mutateAsync({ id: donation.id, action, expires_at: dateUnix(expiry), ...(note.trim() ? { note: note.trim() } : {}), ...(draftKeys.length ? { keys: draftKeys.map((key) => ({ id: Number(key.id), max_concurrency: key.max_concurrency, rpm_limit: key.rpm_limit, credits_usage_cap_milli: key.credits_usage_cap_milli, enabled: key.enabled })) } : {}) }); setNote(''); } catch (requestError) { setActionError(errorText(requestError)); }
  };
  const softDelete = async () => { setActionError(''); try { await remove.mutateAsync(donation.id); setConfirmDelete(false); onDeleted(); } catch (requestError) { setActionError(errorText(requestError)); } };
  const statusLabel = tr(`status.${donation.status}`);
  return <article className="item-card donation-review-card"><div className="item-header"><div><h3>{tr('donationNumber', { id: donation.id })}</h3><p className="item-meta">{donation.endpoint_base_url} · {donation.user_id ? `${tr('submitter')} ${donation.user_id}` : ''}</p></div><StatusBadge active={donation.enabled && donation.status === 'approved'} label={statusLabel} danger={donation.status === 'rejected' || donation.status === 'deleted'} /></div>
    <p className="donation-description">{donation.description}</p>
    {donation.expires_at ? <p className="muted">{tr('expiresAt', { value: new Date(donation.expires_at * 1000).toLocaleString() })}</p> : null}
    {detail.isPending ? <LoadingState label={tr('loadingDetails')} /> : detail.error ? <ErrorState error={detail.error} /> : full?.keys.length ? <div className="donation-key-editors">{draftKeys.map((keyRow) => <DonationKeyEditor key={keyRow.id} frame={frame} keyRow={keyRow} onChange={(next) => setDraftKeys((current) => current.map((item) => item.id === next.id ? next : item))} />)}</div> : null}
    <div className="form-grid"><label><span>{tr('expires')}</span><input type="datetime-local" value={expiry} onChange={(event) => setExpiry(event.target.value)} /><small className="muted">{tr('expiryHint')}</small></label><label className="full-width"><span>{tr('reviewNote')}</span><textarea value={note} onChange={(event) => setNote(event.target.value)} maxLength={4096} rows={2} /></label></div>
    {actionError ? <p className="field-error" role="alert">{actionError}</p> : null}
    <div className="form-actions"><button type="button" className="btn btn-primary" onClick={() => void run('approve')} disabled={review.isPending || donation.status !== 'pending'}>{tr('approve')}</button><button type="button" className="btn btn-secondary" onClick={() => void run('reject')} disabled={review.isPending || donation.status !== 'pending'}>{tr('reject')}</button><button type="button" className="btn btn-secondary" onClick={() => void run(donation.enabled ? 'disable' : 'enable')} disabled={review.isPending || donation.status !== 'approved'}>{donation.enabled ? tr('disable') : tr('enable')}</button><button type="button" className="btn btn-secondary" onClick={() => void run('update')} disabled={review.isPending}>{tr('saveKeyLimits')}</button><button type="button" className="btn btn-danger" onClick={() => setConfirmDelete(true)} disabled={remove.isPending || donation.status === 'deleted'}>{tr('delete')}</button></div>
    {full?.reviews.length ? <details className="review-history"><summary>{tr('reviewHistory')}</summary><ul className="plain-list">{full.reviews.map((item) => <li key={item.id}><strong>{item.action}</strong>{item.note ? ` · ${item.note}` : ''}</li>)}</ul></details> : null}
    <ConfirmDialog open={confirmDelete} title={tr('deleteDonationTitle')} description={tr('deleteDonationBody')} confirmLabel={tr('delete')} danger busy={remove.isPending} onCancel={() => setConfirmDelete(false)} onConfirm={() => void softDelete()} />
  </article>;
}

function DonationQueue({ frame }: { frame: CharityManagementFrame }) {
  const tr = useText(frame);
  const [status, setStatus] = useState('pending');
  const [page, setPage] = useState(1);
  const donations = useManagementDonations(frame, page, status);
  return <Card><div className="card-title-row"><h2>{tr('donationsTitle')}</h2><label><span className="sr-only">{tr('statusFilter')}</span><select value={status} onChange={(event) => { setStatus(event.target.value); setPage(1); }}><option value="pending">{tr('status.pending')}</option><option value="approved">{tr('status.approved')}</option><option value="rejected">{tr('status.rejected')}</option><option value="deleted">{tr('status.deleted')}</option><option value="">{tr('allStatuses')}</option></select></label></div>
    {donations.isPending ? <LoadingState /> : donations.error ? <ErrorState error={donations.error} onRetry={() => void donations.refetch()} /> : donations.data.items.length === 0 ? <EmptyState title={tr('noDonations')} body={tr('noDonationsBody')} /> : <div className="item-list">{donations.data.items.map((donation) => <DonationCard key={donation.id} frame={frame} donation={donation} onDeleted={() => void donations.refetch()} />)}</div>}
    {donations.data?.total ? <nav className="pagination" aria-label={tr('pagination')}><button type="button" className="btn btn-secondary" disabled={page <= 1} onClick={() => setPage((value) => Math.max(1, value - 1))}>{tr('previous')}</button><span>{page}</span><button type="button" className="btn btn-secondary" disabled={!donations.data.hasMore} onClick={() => setPage((value) => value + 1)}>{tr('next')}</button></nav> : null}
  </Card>;
}

function ModelsSection({ frame }: { frame: CharityManagementFrame }) {
  const tr = useText(frame);
  const models = useManagementModels(frame);
  const [editing, setEditing] = useState<ManagementCharityModel | null | undefined>(undefined);
  const [deleting, setDeleting] = useState<ManagementCharityModel | null>(null);
  const remove = useDeleteManagedModel(frame);
  return <Card><div className="card-title-row"><h2>{tr('modelsTitle')}</h2><button type="button" className="btn btn-primary" onClick={() => setEditing(null)}>{tr('newModel')}</button></div>
    {editing !== undefined ? <ModelEditor frame={frame} initial={editing ?? undefined} onClose={() => setEditing(undefined)} /> : null}
    {models.isPending ? <LoadingState /> : models.error ? <ErrorState error={models.error} onRetry={() => void models.refetch()} /> : models.data.length === 0 ? <EmptyState title={tr('noModels')} body={tr('noModelsBody')} /> : <div className="item-list">{models.data.map((model) => <ModelCard key={model.id} frame={frame} model={model} onEdit={() => setEditing(model)} onDelete={() => setDeleting(model)} />)}</div>}
    <ConfirmDialog open={Boolean(deleting)} title={tr('deleteModelTitle')} description={tr('deleteModelBody')} confirmLabel={tr('delete')} danger busy={remove.isPending} onCancel={() => setDeleting(null)} onConfirm={() => { if (!deleting) return; void remove.mutateAsync(deleting.id).then(() => setDeleting(null)).catch(() => undefined); }} />
  </Card>;
}

const ANTI_ABUSE_KEYS = ['charity_min_chars', 'charity_violation_deduct_milli', 'charity_violation_ban_seconds', 'charity_violation_window_seconds', 'charity_violation_ban_threshold', 'charity_violation_window_ban_seconds', 'charity_suspend_window_seconds', 'charity_suspend_threshold', 'charity_suspend_duration_seconds', 'rpm_ban_threshold', 'rpm_ban_window_seconds', 'rpm_ban_duration_seconds'];

function CharitySettings({ frame }: { frame: CharityManagementFrame }) {
  const tr = useText(frame);
  const settings = useCharityAdminSettings(frame === 'admin');
  const patch = usePatchCharityAdminSetting();
  const [confirm, setConfirm] = useState<{ key: string; value: boolean } | null>(null);
  const [reserve, setReserve] = useState('');
  const [values, setValues] = useState<Record<string, string>>({});
  useEffect(() => {
    if (!settings.data) return;
    const next: Record<string, string> = {};
    for (const key of ANTI_ABUSE_KEYS) next[key] = String(settings.data[key] ?? '');
    // Settings are replaced by the server after every mutation, so reset the
    // form draft to that authoritative snapshot.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setValues(next);
    setReserve(typeof settings.data.charity_token_reserve_milli === 'string' ? settings.data.charity_token_reserve_milli : '');
  }, [settings.data]);
  if (frame !== 'admin') return null;
  if (settings.isPending) return <Card><LoadingState /></Card>;
  if (settings.error) return <Card><ErrorState error={settings.error} onRetry={() => void settings.refetch()} /></Card>;
  const config = settings.data ?? {};
  const toggle = (key: string) => { const value = config[key] !== true; setConfirm({ key, value }); };
  const save = async (key: string, value: unknown) => { try { await patch.mutateAsync({ key, value }); } catch { /* mutation feedback is shown below */ } };
  return <Card><div className="card-title-row"><h2>{tr('settingsTitle')}</h2></div><p className="inline-notice">{tr('settingsDescription')}</p>
    <div className="config-list charity-settings"><div className="config-row"><div className="config-key-info"><strong>{tr('charityEnabled')}</strong><span className="table-note">charity_enabled</span></div><div className="config-control"><button type="button" className={`btn ${config.charity_enabled === true ? 'btn-primary' : 'btn-secondary'}`} onClick={() => toggle('charity_enabled')}>{config.charity_enabled === true ? tr('on') : tr('off')}</button></div></div>
      <div className="config-row"><div className="config-key-info"><strong>{tr('donationAcceptEnabled')}</strong><span className="table-note">donation_accept_enabled</span></div><div className="config-control"><button type="button" className={`btn ${config.donation_accept_enabled === true ? 'btn-primary' : 'btn-secondary'}`} onClick={() => toggle('donation_accept_enabled')}>{config.donation_accept_enabled === true ? tr('on') : tr('off')}</button></div></div>
      <form className="config-row" onSubmit={(event) => { event.preventDefault(); if (/^(0|[1-9]\d*)$/.test(reserve)) void save('charity_token_reserve_milli', reserve); }}><div className="config-key-info"><strong>{tr('tokenReserve')}</strong><span className="table-note">charity_token_reserve_milli</span><span className="inline-notice">{tr('tokenReserveNull')}</span></div><div className="config-control"><input value={reserve} inputMode="numeric" onChange={(event) => setReserve(event.target.value)} placeholder={config.charity_token_reserve_milli === null ? tr('notConfigured') : ''} aria-label={tr('tokenReserve')} /><button className="btn btn-secondary" type="submit" disabled={patch.isPending}>{tr('save')}</button></div></form>
      {ANTI_ABUSE_KEYS.map((key) => <form className="config-row" key={key} onSubmit={(event) => { event.preventDefault(); const value = values[key]; if (/^(0|[1-9]\d*)$/.test(value)) void save(key, key.endsWith('_milli') ? value : Number(value)); }}><div className="config-key-info"><strong>{tr(`antiAbuse.${key}`)}</strong><span className="mono table-note">{key}</span></div><div className="config-control"><input value={values[key] ?? ''} inputMode="numeric" onChange={(event) => setValues((current) => ({ ...current, [key]: event.target.value }))} aria-label={tr(`antiAbuse.${key}`)} /><button className="btn btn-secondary" type="submit" disabled={patch.isPending}>{tr('save')}</button></div></form>)}
    </div>{patch.error ? <p className="field-error" role="alert">{errorText(patch.error)}</p> : null}
    <ConfirmDialog open={Boolean(confirm)} title={tr('dangerConfirmTitle')} description={tr('dangerConfirmBody')} confirmLabel={tr('confirm')} danger busy={patch.isPending} onCancel={() => setConfirm(null)} onConfirm={() => { if (!confirm) return; void save(confirm.key, confirm.value).then(() => setConfirm(null)); }} />
  </Card>;
}

export function CharityManagement({ frame }: { frame: CharityManagementFrame }) {
  const tr = useText(frame);
  const client = useQueryClient();
  const donations = useManagementDonations(frame, 1, 'pending');
  const models = useManagementModels(frame);
  const forbidden = isForbidden(donations.error) || isForbidden(models.error);
  useEffect(() => {
    if (!forbidden) return;
    client.removeQueries({ queryKey: charityManagementKeys.root(frame) });
  }, [client, forbidden, frame]);
  return <div className="page charity-management"><PageHeader eyebrow={tr('eyebrow')} title={tr('title')} description={tr('description')} />
    {forbidden ? <Card><p className="field-error" role="alert">{tr('accessRevoked')}</p></Card> : null}
    {!forbidden ? <><CharitySettings frame={frame} /><DonationQueue frame={frame} /><ModelsSection frame={frame} /></> : null}
  </div>;
}
