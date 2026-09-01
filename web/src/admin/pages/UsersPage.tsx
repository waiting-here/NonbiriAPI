import { useEffect, useRef, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { Card, EmptyState, ErrorState, LoadingState, PageHeader, StatusBadge } from '@shared/components/States';
import { CursorPagination } from '@shared/operations/CursorPagination';
import { elevateAdmin } from '@shared/operations/api';
import { useCursorPager } from '@shared/operations/useCursorPager';
import { ApiError, isForbidden, isNotFoundError, isUnauthorized } from '@shared/query/http';
import { formatDateTime } from '@shared/utils/datetime';
import {
  adminCoreKeys,
  banAdminUser,
  deleteAdminUser,
  getAdminUser,
  getAdminUsers,
  mutateAdminUser,
  unbanAdminUser,
  type AdminUser,
} from '../features/operations/core';
import { useRetainedOperation } from '../features/operations/useRetainedOperation';
import '@shared/operations/operations.css';

interface UserDraft {
  endpointLimit: string;
  rpmLimit: string;
  concurrencyLimit: string;
  level: string;
  economyTarget: 'balance' | 'donation_credit';
  economyDirection: 'increase' | 'decrease';
  economyAmount: string;
  economyReason: string;
  banReason: string;
  banDuration: string;
}

const draftFor = (user: AdminUser): UserDraft => ({
  endpointLimit: user.endpoint_limit ?? '',
  rpmLimit: user.rpm_limit ?? '',
  concurrencyLimit: user.concurrency_limit ?? '',
  level: user.level.manual === null ? '' : String(user.level.manual),
  economyTarget: 'balance',
  economyDirection: 'increase',
  economyAmount: '',
  economyReason: '',
  banReason: '',
  banDuration: '',
});

function nullableLimit(value: string): number | null | undefined {
  if (!value.trim()) return null;
  if (!/^(0|[1-9][0-9]*)$/.test(value)) return undefined;
  const parsed = Number(value);
  return Number.isSafeInteger(parsed) ? parsed : undefined;
}

function UserAuthority({ user, refresh }: { user: AdminUser; refresh: () => Promise<unknown> }) {
  const { t } = useTranslation();
  const [draft, setDraft] = useState<UserDraft>(() => draftFor(user));
  const [confirm, setConfirm] = useState<'ban' | 'unban' | 'delete' | null>(null);
  const [password, setPassword] = useState('');
  const [deleteConfirmation, setDeleteConfirmation] = useState('');
  const [elevating, setElevating] = useState(false);
  const [elevationError, setElevationError] = useState<unknown>(null);
  const elevationToken = useRef<string | null>(null);
  const authorityRevision = useRef(user.revision);
  const reconcile = async () => { await refresh(); };
  const patch = useRetainedOperation(
    (input: { revision: string; body: Record<string, unknown> }, key) => mutateAdminUser(user.id, { ...input.body, expected_revision: input.revision }, key),
    reconcile,
  );
  const ban = useRetainedOperation(
    (input: { revision: string; reason: string; duration_seconds: number | null }, key) => banAdminUser(user.id, { reason: input.reason, duration_seconds: input.duration_seconds, expected_revision: input.revision }, key),
    reconcile,
  );
  const unban = useRetainedOperation((input: { revision: string }, key) => unbanAdminUser(user.id, input.revision, key), reconcile);
  const deletion = useRetainedOperation(
    async (input: { revision: string }, key) => {
      const token = elevationToken.current;
      elevationToken.current = null;
      if (!token) throw new ApiError('elevated_required', 'Fresh administrator elevation is required.', 403);
      await deleteAdminUser(user.id, input.revision, key, token);
    },
    reconcile,
  );

  useEffect(() => {
    authorityRevision.current = user.revision;
    elevationToken.current = null;
    // Authority changes preserve safe drafts but always erase secrets and dangerous confirmation.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setConfirm(null);
    setPassword('');
    setDeleteConfirmation('');
    setElevationError(null);
  }, [user.revision]);
  useEffect(() => () => {
    authorityRevision.current = '';
    elevationToken.current = null;
  }, []);

  const saveLimits = () => {
    const endpoint = nullableLimit(draft.endpointLimit);
    const rpm = nullableLimit(draft.rpmLimit);
    const concurrency = nullableLimit(draft.concurrencyLimit);
    if (endpoint === undefined || rpm === undefined || concurrency === undefined) return;
    patch.mutate({ revision: user.revision, body: { mode: 'profile', endpoint_limit: endpoint, rpm_limit: rpm, concurrency_limit: concurrency } });
  };
  const submitDeletion = async () => {
    if (!password || deleteConfirmation !== 'DELETE') return;
    const revision = user.revision;
    const submittedPassword = password;
    setPassword('');
    setDeleteConfirmation('');
    setElevationError(null);
    setElevating(true);
    try {
      const elevation = await elevateAdmin(submittedPassword);
      if (authorityRevision.current !== revision) return;
      elevationToken.current = elevation.token;
      setConfirm(null);
      deletion.mutate({ revision });
    } catch (error) {
      if (authorityRevision.current === revision) setElevationError(error);
    } finally {
      if (authorityRevision.current === revision) setElevating(false);
    }
  };
  const banSeconds = draft.banDuration.trim() ? Number(draft.banDuration) * 86_400 : undefined;
  const mutationError = patch.error ?? ban.error ?? unban.error ?? deletion.error ?? elevationError;

  return (
    <div className="ops-stack">
      <Card>
        <h2>{user.username}</h2>
        <dl className="ops-kv">
          <dt>ID / revision</dt><dd>{user.id} / {user.revision}</dd>
          <dt>State</dt><dd><StatusBadge active={!user.is_banned} danger={user.is_banned} label={user.is_banned ? 'banned' : 'active'} /></dd>
          <dt>Level</dt><dd>{user.level.display_name} (automatic {user.level.automatic}, effective {user.level.effective})</dd>
          <dt>Balances</dt><dd>{user.balance} credits · {user.donation_credit} donation credits</dd>
          <dt>Created / updated</dt><dd>{formatDateTime(user.created_at)} / {formatDateTime(user.updated_at)}</dd>
          {user.is_banned ? <><dt>Ban</dt><dd>{user.banned_until === null ? 'permanent' : `until ${formatDateTime(user.banned_until)}`} · {user.banned_reason || 'no reason supplied'}</dd></> : null}
        </dl>
      </Card>
      <Card>
        <h2>Limits and level</h2>
        <p>{t('admin.users.limitHint')} All writes compare revision {user.revision}.</p>
        <div className="ops-field-grid">
          <label><span>Endpoint limit</span><input inputMode="numeric" value={draft.endpointLimit} onChange={(event) => setDraft({ ...draft, endpointLimit: event.target.value })} /></label>
          <label><span>RPM limit</span><input inputMode="numeric" value={draft.rpmLimit} onChange={(event) => setDraft({ ...draft, rpmLimit: event.target.value })} /></label>
          <label><span>Concurrency limit</span><input inputMode="numeric" value={draft.concurrencyLimit} onChange={(event) => setDraft({ ...draft, concurrencyLimit: event.target.value })} /></label>
          <label><span>Manual level</span><select value={draft.level} onChange={(event) => setDraft({ ...draft, level: event.target.value })}><option value="">Automatic</option>{[1, 2, 3, 4, 5].map((level) => <option key={level} value={level}>{level}</option>)}</select></label>
        </div>
        <div className="ops-actions"><button className="btn btn-primary" type="button" disabled={patch.isPending} onClick={saveLimits}>Save limits</button><button className="btn btn-secondary" type="button" disabled={patch.isPending} onClick={() => patch.mutate({ revision: user.revision, body: { mode: 'profile', level: draft.level ? Number(draft.level) : null } })}>Apply level</button></div>
      </Card>
      <Card>
        <h2>Account balances</h2>
        <p>Signed canonical credit amounts are applied by the server ledger. A reason is mandatory.</p>
        <div className="ops-field-grid">
          <label><span>Target</span><select value={draft.economyTarget} onChange={(event) => setDraft({ ...draft, economyTarget: event.target.value as UserDraft['economyTarget'] })}><option value="balance">Balance</option><option value="donation_credit">Donation credit</option></select></label>
          <label><span>Direction</span><select value={draft.economyDirection} onChange={(event) => setDraft({ ...draft, economyDirection: event.target.value as UserDraft['economyDirection'] })}><option value="increase">Increase</option><option value="decrease">Decrease</option></select></label>
          <label><span>Positive amount</span><input value={draft.economyAmount} placeholder="1.5" onChange={(event) => setDraft({ ...draft, economyAmount: event.target.value })} /></label>
          <label><span>Reason</span><input maxLength={1024} value={draft.economyReason} onChange={(event) => setDraft({ ...draft, economyReason: event.target.value })} /></label>
        </div>
        <button className="btn btn-primary" type="button" disabled={patch.isPending || !draft.economyAmount || draft.economyAmount.startsWith('-') || !draft.economyReason.trim()} onClick={() => patch.mutate({ revision: user.revision, body: { mode: 'economy', target: draft.economyTarget, direction: draft.economyDirection, amount: draft.economyAmount, reason: draft.economyReason.trim() } }, { onSuccess: () => setDraft({ ...draft, economyAmount: '', economyReason: '' }) })}>Apply ledger adjustment</button>
      </Card>
      <Card className="ops-danger">
        <h2>Access and deletion</h2>
        <div className="ops-field-grid">
          <label><span>Ban reason</span><input maxLength={1024} value={draft.banReason} onChange={(event) => setDraft({ ...draft, banReason: event.target.value })} /></label>
          <label><span>Ban duration (days; blank is permanent)</span><input type="number" min="1" max="3660" value={draft.banDuration} onChange={(event) => setDraft({ ...draft, banDuration: event.target.value })} /></label>
        </div>
        <div className="ops-actions">{user.is_banned ? <button className="btn btn-danger" type="button" onClick={() => setConfirm('unban')}>Unban</button> : <button className="btn btn-danger" type="button" disabled={!draft.banReason.trim() || (draft.banDuration !== '' && (!Number.isSafeInteger(banSeconds) || (banSeconds ?? 0) < 1))} onClick={() => setConfirm('ban')}>Ban account</button>}<button className="btn btn-danger" type="button" onClick={() => setConfirm('delete')}>Delete account</button></div>
        {confirm === 'delete' ? <><label className="ops-form-field"><span>Administrator password (fresh elevation)</span><input type="password" autoComplete="current-password" value={password} onChange={(event) => setPassword(event.target.value)} /></label><label className="ops-form-field"><span>Type DELETE to confirm immediate account deletion</span><input autoComplete="off" value={deleteConfirmation} onChange={(event) => setDeleteConfirmation(event.target.value)} /></label></> : null}
        {mutationError ? <ErrorState error={mutationError} /> : null}
      </Card>
      {confirm ? <ConfirmDialog open title={confirm === 'delete' ? 'Delete this account?' : confirm === 'ban' ? 'Ban this account?' : 'Unban this account?'} description={confirm === 'delete' ? 'Fresh elevation is consumed. A conflict or unknown response will only refresh authority; it will not repeat deletion automatically.' : 'This changes access immediately and compares the current user revision.'} confirmLabel={confirm === 'delete' ? 'DELETE account' : confirm === 'ban' ? 'Ban' : 'Unban'} confirmDisabled={confirm === 'delete' && (!password || deleteConfirmation !== 'DELETE')} danger busy={ban.isPending || unban.isPending || deletion.isPending || elevating} onCancel={() => { setConfirm(null); setPassword(''); setDeleteConfirmation(''); setElevationError(null); }} onConfirm={() => { if (confirm === 'ban') ban.mutate({ revision: user.revision, reason: draft.banReason.trim(), duration_seconds: banSeconds ?? null }, { onSuccess: () => setConfirm(null) }); else if (confirm === 'unban') unban.mutate({ revision: user.revision }, { onSuccess: () => setConfirm(null) }); else void submitDeletion(); }} /> : null}
    </div>
  );
}

export function UsersPage() {
  const pager = useCursorPager();
  const [queryDraft, setQueryDraft] = useState('');
  const [query, setQuery] = useState('');
  const [banned, setBanned] = useState<'' | 'true' | 'false'>('');
  const [selected, setSelected] = useState('');
  const users = useQuery({ queryKey: adminCoreKeys.users(banned, query, pager.cursor), queryFn: () => getAdminUsers(banned, query, pager.cursor), retry: false });
  const detail = useQuery({ queryKey: adminCoreKeys.user(selected), queryFn: () => getAdminUser(selected), retry: false, enabled: Boolean(selected) });
  const detailUnavailable = isUnauthorized(detail.error) || isForbidden(detail.error) || isNotFoundError(detail.error);
  return (
    <div className="page ops-page">
      <PageHeader title="Users" description="Filter accounts, inspect authoritative state, and keep routine controls separate from irreversible deletion." />
      <Card><form className="ops-toolbar" onSubmit={(event) => { event.preventDefault(); pager.reset(); setQuery(queryDraft.trim()); }}><label><span>Search</span><input value={queryDraft} onChange={(event) => setQueryDraft(event.target.value)} /></label><label><span>Ban state</span><select value={banned} onChange={(event) => { pager.reset(); setBanned(event.target.value as typeof banned); }}><option value="">All</option><option value="false">Active</option><option value="true">Banned</option></select></label><button className="btn btn-secondary" type="submit">Apply</button></form></Card>
      <Card><h2>Accounts</h2>{users.isPending ? <LoadingState /> : users.error ? <ErrorState error={users.error} onRetry={() => void users.refetch()} /> : users.data.data.length === 0 ? <EmptyState title="No users" body="No account matches this authority filter." /> : <><div className="ops-table-scroll"><table className="ops-table"><thead><tr><th>User</th><th>State</th><th>Level</th><th>Balance</th><th>Open</th></tr></thead><tbody>{users.data.data.map((user) => <tr key={user.id}><td>{user.username}<small>{user.id}</small></td><td><StatusBadge active={!user.is_banned} danger={user.is_banned} label={user.is_banned ? 'banned' : 'active'} /></td><td>{user.level.effective}</td><td>{user.balance}</td><td><button className="btn btn-secondary" type="button" onClick={() => setSelected(user.id)}>Manage</button></td></tr>)}</tbody></table></div><CursorPagination page={pager.page} nextCursor={users.data.next_cursor} onPrevious={pager.previous} onNext={pager.next} /></>}</Card>
      {selected && !detailUnavailable ? detail.isPending ? <LoadingState /> : detail.error ? <ErrorState error={detail.error} onRetry={() => void detail.refetch()} /> : <UserAuthority key={detail.data.id} user={detail.data} refresh={async () => { await Promise.all([detail.refetch(), users.refetch()]); }} /> : null}
      {selected && detailUnavailable ? <ErrorState error={detail.error} onRetry={() => { setSelected(''); }} /> : null}
    </div>
  );
}
