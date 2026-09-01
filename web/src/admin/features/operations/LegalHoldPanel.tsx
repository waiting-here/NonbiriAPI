import { useEffect, useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { clearStationSession } from '@shared/charityManagement';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { Card, EmptyState, ErrorState, LoadingState, StatusBadge } from '@shared/components/States';
import { CursorPagination } from '@shared/operations/CursorPagination';
import { elevateAdmin } from '@shared/operations/api';
import { useCursorPager } from '@shared/operations/useCursorPager';
import { ApiError, isForbidden, isNotFoundError, isUnauthorized } from '@shared/query/http';
import { formatDateTime } from '@shared/utils/datetime';
import {
  adminCoreKeys,
  createLegalHold,
  getLegalHold,
  getLegalHolds,
  releaseLegalHold,
  type HeldObjectKind,
} from './core';
import { useRetainedOperation } from './useRetainedOperation';

const KINDS: HeldObjectKind[] = ['maintenance_event', 'report_case', 'announcement_audit', 'donation', 'request_log'];

function isFinalAuthorityLoss(error: unknown): boolean {
  return isUnauthorized(error)
    || (isForbidden(error) && !(error instanceof ApiError && error.code === 'elevated_required'));
}

export function LegalHoldPanel() {
  const client = useQueryClient();
  const authorityEpoch = useRef(0);
  const createElevationToken = useRef<string | null>(null);
  const releaseElevationToken = useRef<string | null>(null);
  const pager = useCursorPager();
  const [state, setState] = useState('');
  const [kind, setKind] = useState('');
  const [selected, setSelected] = useState('');
  const [createDraft, setCreateDraft] = useState({ object_kind: 'report_case' as HeldObjectKind, object_ref: '', basis: '', days: '30', password: '', confirmed: false });
  const [createExpiry, setCreateExpiry] = useState<number | null>(null);
  const [releaseDraft, setReleaseDraft] = useState({ reason: '', password: '', confirmed: false });
  const [confirmation, setConfirmation] = useState<'create' | 'release' | null>(null);
  const [elevating, setElevating] = useState(false);
  const [elevationError, setElevationError] = useState<unknown>(null);
  const list = useQuery({ queryKey: adminCoreKeys.holds(state, kind, pager.cursor), queryFn: ({ signal }) => getLegalHolds(state, kind, pager.cursor, signal), retry: false });
  const detail = useQuery({ queryKey: adminCoreKeys.hold(selected), queryFn: ({ signal }) => getLegalHold(selected, signal), retry: false, enabled: Boolean(selected) });
  const reconcile = async () => { await Promise.all([list.refetch(), selected ? detail.refetch() : Promise.resolve()]); };
  const create = useRetainedOperation(async (input: { object_kind: HeldObjectKind; object_ref: string; basis: string; expires_at: number }, key) => {
    const epoch = authorityEpoch.current;
    const token = createElevationToken.current;
    createElevationToken.current = null;
    if (!token) throw new ApiError('elevated_required', 'Fresh administrator elevation is required.', 403);
    const hold = await createLegalHold({ object_kind: input.object_kind, object_ref: input.object_ref, basis: input.basis, expires_at: input.expires_at, confirmation: true }, key, token);
    if (epoch !== authorityEpoch.current) throw new ApiError('authority_changed', 'Administrator authority changed.', 401);
    return hold;
  }, reconcile);
  const release = useRetainedOperation(async (input: { id: string; revision: string; reason: string }, key) => {
    const epoch = authorityEpoch.current;
    const token = releaseElevationToken.current;
    releaseElevationToken.current = null;
    if (!token) throw new ApiError('elevated_required', 'Fresh administrator elevation is required.', 403);
    const hold = await releaseLegalHold(input.id, { expected_revision: input.revision, reason: input.reason, confirmation: true }, key, token);
    if (epoch !== authorityEpoch.current) throw new ApiError('authority_changed', 'Administrator authority changed.', 401);
    return hold;
  }, reconcile);
  const createError = create.error;
  const resetCreate = create.reset;
  const releaseError = release.error;
  const resetRelease = release.reset;

  /* eslint-disable react-hooks/set-state-in-effect -- External authority failures must purge fresh-elevation secrets and irreversible confirmations. */
  useEffect(() => {
    const error = list.error ?? detail.error ?? createError ?? releaseError;
    // Fresh-elevation passwords and irreversible confirmations must be erased on authority loss.
    if (isFinalAuthorityLoss(error)) {
      authorityEpoch.current += 1;
      createElevationToken.current = null;
      releaseElevationToken.current = null;
      clearStationSession(client, 'admin');
      setSelected('');
      setCreateDraft({ object_kind: 'report_case', object_ref: '', basis: '', days: '30', password: '', confirmed: false });
      setCreateExpiry(null);
      setReleaseDraft({ reason: '', password: '', confirmed: false });
      setConfirmation(null);
      setElevationError(null);
      resetCreate();
      resetRelease();
    }
  }, [client, createError, detail.error, list.error, releaseError, resetCreate, resetRelease]);

  useEffect(() => {
    if (!selected || !isNotFoundError(detail.error)) return;
    setSelected('');
    setReleaseDraft({ reason: '', password: '', confirmed: false });
    setConfirmation(null);
  }, [detail.error, selected]);
  /* eslint-enable react-hooks/set-state-in-effect */

  useEffect(() => () => {
    authorityEpoch.current += 1;
    createElevationToken.current = null;
    releaseElevationToken.current = null;
  }, []);

  const submitCreate = async () => {
    if (createExpiry === null || !createDraft.password) return;
    const epoch = authorityEpoch.current;
    const password = createDraft.password;
    const input = {
      object_kind: createDraft.object_kind,
      object_ref: createDraft.object_ref.trim(),
      basis: createDraft.basis.trim(),
      expires_at: createExpiry,
    };
    setConfirmation(null);
    setCreateDraft((current) => ({ ...current, password: '', confirmed: false }));
    setElevationError(null);
    setElevating(true);
    try {
      const elevation = await elevateAdmin(password);
      if (epoch !== authorityEpoch.current) return;
      createElevationToken.current = elevation.token;
      create.mutate(input, { onSuccess: (hold) => {
        setSelected(hold.id);
        setCreateExpiry(null);
        setCreateDraft({ object_kind: 'report_case', object_ref: '', basis: '', days: '30', password: '', confirmed: false });
      } });
    } catch (error) {
      if (epoch === authorityEpoch.current) setElevationError(error);
    } finally {
      if (epoch === authorityEpoch.current) setElevating(false);
    }
  };

  const submitRelease = async () => {
    if (!detail.data || !releaseDraft.password) return;
    const epoch = authorityEpoch.current;
    const password = releaseDraft.password;
    const input = { id: detail.data.id, revision: detail.data.revision, reason: releaseDraft.reason.trim() };
    setConfirmation(null);
    setReleaseDraft((current) => ({ ...current, password: '', confirmed: false }));
    setElevationError(null);
    setElevating(true);
    try {
      const elevation = await elevateAdmin(password);
      if (epoch !== authorityEpoch.current) return;
      releaseElevationToken.current = elevation.token;
      release.mutate(input, { onSuccess: () => setReleaseDraft({ reason: '', password: '', confirmed: false }) });
    } catch (error) {
      if (epoch === authorityEpoch.current) setElevationError(error);
    } finally {
      if (epoch === authorityEpoch.current) setElevating(false);
    }
  };

  const days = Number(createDraft.days);
  const validDays = Number.isSafeInteger(days) && days >= 1 && days <= 365;
  return (
    <div className="ops-stack">
      <Card className="ops-danger">
        <h2>Legal holds</h2>
        <p>Metadata only. A hold may preserve one eligible aggregate for at most 365 days, once. It never pauses account revocation or deletion, business deadlines, settlement, or adjudication.</p>
        <div className="ops-toolbar"><label><span>State</span><select value={state} onChange={(event) => { pager.reset(); setState(event.target.value); }}><option value="">All</option><option value="active">Active</option><option value="released">Released</option><option value="expired">Expired</option></select></label><label><span>Object kind</span><select value={kind} onChange={(event) => { pager.reset(); setKind(event.target.value); }}><option value="">All</option>{KINDS.map((value) => <option key={value}>{value}</option>)}</select></label></div>
        {list.isPending ? <LoadingState /> : list.error ? <ErrorState error={list.error} onRetry={() => void list.refetch()} /> : list.data.data.length === 0 ? <EmptyState title="No legal-hold metadata" body="No hold matches this filter." /> : <><div className="ops-table-scroll"><table className="ops-table"><thead><tr><th>Object</th><th>State</th><th>Window</th><th>Detail</th></tr></thead><tbody>{list.data.data.map((hold) => <tr key={hold.id}><td>{hold.object_kind}<small>{hold.object_ref}</small></td><td><StatusBadge active={hold.state === 'active'} label={hold.state} /></td><td>{formatDateTime(hold.created_at)} — {formatDateTime(hold.expires_at)}</td><td><button className="btn btn-secondary" type="button" onClick={() => setSelected(hold.id)}>Metadata</button></td></tr>)}</tbody></table></div><CursorPagination page={pager.page} nextCursor={list.data.next_cursor} onPrevious={pager.previous} onNext={pager.next} /></>}
      </Card>
      {selected ? <Card>{detail.isPending ? <LoadingState /> : detail.error ? <ErrorState error={detail.error} onRetry={() => void detail.refetch()} /> : <><h3>Hold metadata</h3><dl className="ops-kv"><dt>ID / revision</dt><dd>{detail.data.id} / {detail.data.revision}</dd><dt>Object</dt><dd>{detail.data.object_kind} · {detail.data.object_ref}</dd><dt>State</dt><dd>{detail.data.state}</dd><dt>Basis</dt><dd>{detail.data.basis}</dd><dt>Expires</dt><dd>{formatDateTime(detail.data.expires_at)}</dd>{detail.data.end_reason ? <><dt>End reason</dt><dd>{detail.data.end_reason}</dd></> : null}</dl>{detail.data.state === 'active' ? <><label className="ops-form-field"><span>Release reason</span><input maxLength={1024} value={releaseDraft.reason} onChange={(event) => setReleaseDraft({ ...releaseDraft, reason: event.target.value })} /></label><label className="ops-form-field"><span>Administrator password (fresh elevation)</span><input type="password" autoComplete="current-password" value={releaseDraft.password} onChange={(event) => setReleaseDraft({ ...releaseDraft, password: event.target.value })} /></label><label><input type="checkbox" checked={releaseDraft.confirmed} onChange={(event) => setReleaseDraft({ ...releaseDraft, confirmed: event.target.checked })} /> Release is final; this object cannot receive another hold.</label><button className="btn btn-danger" type="button" disabled={!releaseDraft.reason.trim() || !releaseDraft.password || !releaseDraft.confirmed || release.isPending} onClick={() => setConfirmation('release')}>Release hold</button></> : null}</>}</Card> : null}
      <Card className="ops-danger">
        <h3>Create one-time hold</h3>
        <div className="ops-field-grid"><label><span>Object kind</span><select value={createDraft.object_kind} onChange={(event) => { setCreateExpiry(null); setCreateDraft({ ...createDraft, object_kind: event.target.value as HeldObjectKind }); }}>{KINDS.map((value) => <option key={value}>{value}</option>)}</select></label><label><span>Exact object reference</span><input value={createDraft.object_ref} onChange={(event) => { setCreateExpiry(null); setCreateDraft({ ...createDraft, object_ref: event.target.value }); }} /></label><label><span>Days (1–365)</span><input type="number" min="1" max="365" value={createDraft.days} onChange={(event) => { setCreateExpiry(null); setCreateDraft({ ...createDraft, days: event.target.value }); }} /></label><label><span>Administrator password</span><input type="password" autoComplete="current-password" value={createDraft.password} onChange={(event) => setCreateDraft({ ...createDraft, password: event.target.value })} /></label></div><label className="ops-form-field"><span>Basis</span><textarea maxLength={1024} value={createDraft.basis} onChange={(event) => { setCreateExpiry(null); setCreateDraft({ ...createDraft, basis: event.target.value }); }} /></label><label><input type="checkbox" checked={createDraft.confirmed} onChange={(event) => setCreateDraft({ ...createDraft, confirmed: event.target.checked })} /> I confirm this exact aggregate, expiry, and irreversible one-hold limit.</label>{create.error ? <ErrorState error={create.error} /> : release.error ? <ErrorState error={release.error} /> : elevationError ? <ErrorState error={elevationError} /> : null}<button className="btn btn-danger" type="button" disabled={!createDraft.object_ref.trim() || !createDraft.basis.trim() || !createDraft.password || !createDraft.confirmed || !validDays || create.isPending || elevating} onClick={() => { setCreateExpiry((value) => value ?? Math.floor(Date.now() / 1_000) + days * 86_400); setConfirmation('create'); }}>Create legal hold</button>
      </Card>
      {confirmation ? <ConfirmDialog open title={confirmation === 'create' ? 'Create this legal hold?' : 'Release this legal hold?'} description={confirmation === 'create' ? `The hold expires after ${days} day(s), cannot be extended, copied, reopened, or recreated.` : 'Release is final and ordinary cleanup may proceed immediately when its original deadline has passed.'} confirmLabel={confirmation === 'create' ? 'Create hold' : 'Release hold'} danger busy={create.isPending || release.isPending || elevating} onCancel={() => { setConfirmation(null); setCreateDraft((current) => ({ ...current, password: '', confirmed: false })); setReleaseDraft((current) => ({ ...current, password: '', confirmed: false })); setElevationError(null); }} onConfirm={() => { if (confirmation === 'create') void submitCreate(); else void submitRelease(); }} /> : null}
    </div>
  );
}
