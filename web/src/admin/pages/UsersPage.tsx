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
      if (!token)
        throw new ApiError(
          'elevated_required',
          t('admin.users.deletePasswordRequired'),
          403,
        );
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
  const banDurationLabel = draft.banDuration.trim()
    ? t('admin.users.banPresetLabel', { days: draft.banDuration.trim() })
    : t('admin.users.banPermanent');
  const mutationError = patch.error ?? ban.error ?? unban.error ?? deletion.error ?? elevationError;

  return (
    <div className="ops-stack">
      <Card>
        <h2>{user.username}</h2>
        <dl className="ops-kv">
          <dt>{t('admin.users.detailIdRevision')}</dt>
          <dd>
            {user.id} / {user.revision}
          </dd>
          <dt>{t('admin.users.status')}</dt>
          <dd>
            <StatusBadge
              active={!user.is_banned}
              danger={user.is_banned}
              label={t(user.is_banned ? 'admin.users.banned' : 'admin.users.active')}
            />
          </dd>
          <dt>{t('admin.users.level')}</dt>
          <dd>
            {user.level.display_name} · {t('admin.users.levelAutoTag')} {user.level.automatic} ·{' '}
            {t('admin.users.levelEffectiveTag')} {user.level.effective}
          </dd>
          <dt>{t('admin.users.balances')}</dt>
          <dd>
            {user.balance} {t('admin.users.creditsBalance')} · {user.donation_credit}{' '}
            {t('admin.users.donationBalance')}
          </dd>
          <dt>
            {t('admin.users.created')} / {t('admin.users.updated')}
          </dt>
          <dd>
            {formatDateTime(user.created_at)} / {formatDateTime(user.updated_at)}
          </dd>
          {user.is_banned ? (
            <>
              <dt>{t('admin.users.banSectionTitle')}</dt>
              <dd>
                {user.banned_until === null
                  ? t('admin.users.banPermanent')
                  : t('admin.users.banUntilShort', {
                      time: formatDateTime(user.banned_until),
                    })}{' '}
                · {user.banned_reason || t('admin.users.banReasonMissing')}
              </dd>
            </>
          ) : null}
        </dl>
      </Card>
      <Card>
        <h2>
          {t('admin.users.manageLimitsTitle')} / {t('admin.users.levelSectionTitle')}
        </h2>
        <p>
          {t('admin.users.limitHint')} {t('admin.users.revisionGuard', { revision: user.revision })}
        </p>
        <div className="ops-field-grid">
          <label>
            <span>{t('admin.users.endpointLimit')}</span>
            <input
              inputMode="numeric"
              value={draft.endpointLimit}
              onChange={(event) => setDraft({ ...draft, endpointLimit: event.target.value })}
            />
          </label>
          <label>
            <span>{t('admin.users.rpmLimit')}</span>
            <input
              inputMode="numeric"
              value={draft.rpmLimit}
              onChange={(event) => setDraft({ ...draft, rpmLimit: event.target.value })}
            />
          </label>
          <label>
            <span>{t('admin.users.concurrencyLimit')}</span>
            <input
              inputMode="numeric"
              value={draft.concurrencyLimit}
              onChange={(event) => setDraft({ ...draft, concurrencyLimit: event.target.value })}
            />
          </label>
          <label>
            <span>{t('admin.users.levelControl')}</span>
            <select
              value={draft.level}
              onChange={(event) => setDraft({ ...draft, level: event.target.value })}
            >
              <option value="">{t('admin.users.levelManualNone')}</option>
              {[1, 2, 3, 4, 5].map((level) => (
                <option key={level} value={level}>
                  {level}
                </option>
              ))}
            </select>
          </label>
        </div>
        <div className="ops-actions">
          <button
            className="btn btn-primary"
            type="button"
            disabled={patch.isPending}
            onClick={saveLimits}
          >
            {t('admin.users.saveLimits')}
          </button>
          <button
            className="btn btn-secondary"
            type="button"
            disabled={patch.isPending}
            onClick={() =>
              patch.mutate({
                revision: user.revision,
                body: { mode: 'profile', level: draft.level ? Number(draft.level) : null },
              })
            }
          >
            {t('admin.users.levelApply')}
          </button>
        </div>
      </Card>
      <Card>
        <h2>{t('admin.users.economyTitle')}</h2>
        <p>{t('admin.users.economyPositiveHint')}</p>
        <div className="ops-field-grid">
          <label>
            <span>{t('admin.users.economyTarget')}</span>
            <select
              value={draft.economyTarget}
              onChange={(event) =>
                setDraft({
                  ...draft,
                  economyTarget: event.target.value as UserDraft['economyTarget'],
                })
              }
            >
              <option value="balance">{t('admin.users.creditsBalance')}</option>
              <option value="donation_credit">{t('admin.users.donationBalance')}</option>
            </select>
          </label>
          <label>
            <span>{t('admin.users.economyDirection')}</span>
            <select
              value={draft.economyDirection}
              onChange={(event) =>
                setDraft({
                  ...draft,
                  economyDirection: event.target.value as UserDraft['economyDirection'],
                })
              }
            >
              <option value="increase">{t('admin.users.economyIncrease')}</option>
              <option value="decrease">{t('admin.users.economyDecrease')}</option>
            </select>
          </label>
          <label>
            <span>{t('admin.users.economyPositiveAmount')}</span>
            <input
              value={draft.economyAmount}
              placeholder="1.5"
              onChange={(event) => setDraft({ ...draft, economyAmount: event.target.value })}
            />
          </label>
          <label>
            <span>{t('admin.users.economyReason')}</span>
            <input
              maxLength={1024}
              value={draft.economyReason}
              onChange={(event) => setDraft({ ...draft, economyReason: event.target.value })}
            />
          </label>
        </div>
        <button
          className="btn btn-primary"
          type="button"
          disabled={
            patch.isPending ||
            !draft.economyAmount ||
            draft.economyAmount.startsWith('-') ||
            !draft.economyReason.trim()
          }
          onClick={() =>
            patch.mutate(
              {
                revision: user.revision,
                body: {
                  mode: 'economy',
                  target: draft.economyTarget,
                  direction: draft.economyDirection,
                  amount: draft.economyAmount,
                  reason: draft.economyReason.trim(),
                },
              },
              {
                onSuccess: () =>
                  setDraft({ ...draft, economyAmount: '', economyReason: '' }),
              },
            )
          }
        >
          {t('admin.users.economySubmit')}
        </button>
      </Card>
      <Card className="ops-danger">
        <h2>
          {t('admin.users.banSectionTitle')} / {t('admin.users.delete')}
        </h2>
        <div className="ops-field-grid">
          <label>
            <span>{t('admin.users.banReason')}</span>
            <input
              maxLength={1024}
              value={draft.banReason}
              onChange={(event) => setDraft({ ...draft, banReason: event.target.value })}
            />
          </label>
          <label>
            <span>{t('admin.users.banDurationDays')}</span>
            <input
              type="number"
              min="1"
              max="3660"
              value={draft.banDuration}
              onChange={(event) => setDraft({ ...draft, banDuration: event.target.value })}
            />
          </label>
        </div>
        <div className="ops-actions">
          {user.is_banned ? (
            <button
              className="btn btn-danger"
              type="button"
              onClick={() => setConfirm('unban')}
            >
              {t('admin.users.unban')}
            </button>
          ) : (
            <button
              className="btn btn-danger"
              type="button"
              disabled={
                !draft.banReason.trim() ||
                (draft.banDuration !== '' &&
                  (!Number.isSafeInteger(banSeconds) || (banSeconds ?? 0) < 1))
              }
              onClick={() => setConfirm('ban')}
            >
              {t('admin.users.ban')}
            </button>
          )}
          <button
            className="btn btn-danger"
            type="button"
            onClick={() => setConfirm('delete')}
          >
            {t('admin.users.delete')}
          </button>
        </div>
        {confirm === 'delete' ? (
          <>
            <label className="ops-form-field">
              <span>{t('admin.users.elevatedPassword')}</span>
              <input
                type="password"
                autoComplete="current-password"
                value={password}
                onChange={(event) => setPassword(event.target.value)}
              />
              <small>{t('admin.users.elevatedPasswordHint')}</small>
            </label>
            <label className="ops-form-field">
              <span>{t('admin.users.deleteTypeConfirmation', { token: 'DELETE' })}</span>
              <input
                autoComplete="off"
                value={deleteConfirmation}
                onChange={(event) => setDeleteConfirmation(event.target.value)}
              />
            </label>
          </>
        ) : null}
        {mutationError ? <ErrorState error={mutationError} /> : null}
      </Card>
      {confirm ? (
        <ConfirmDialog
          open
          title={t(
            confirm === 'delete'
              ? 'admin.users.deleteTitle'
              : confirm === 'ban'
                ? 'admin.users.banTitle'
                : 'admin.users.unbanTitle',
          )}
          description={
            confirm === 'delete'
              ? t('admin.users.deleteRefreshOnlyBody')
              : confirm === 'ban'
                ? t('admin.users.banSummary', { duration: banDurationLabel })
                : t('admin.users.unbanBody')
          }
          confirmLabel={t(
            confirm === 'delete'
              ? 'admin.users.deleteConfirm'
              : confirm === 'ban'
                ? 'admin.users.banConfirm'
                : 'admin.users.unbanConfirm',
          )}
          confirmDisabled={confirm === 'delete' && (!password || deleteConfirmation !== 'DELETE')}
          danger
          busy={ban.isPending || unban.isPending || deletion.isPending || elevating}
          onCancel={() => {
            setConfirm(null);
            setPassword('');
            setDeleteConfirmation('');
            setElevationError(null);
          }}
          onConfirm={() => {
            if (confirm === 'ban')
              ban.mutate(
                {
                  revision: user.revision,
                  reason: draft.banReason.trim(),
                  duration_seconds: banSeconds ?? null,
                },
                { onSuccess: () => setConfirm(null) },
              );
            else if (confirm === 'unban')
              unban.mutate(
                { revision: user.revision },
                { onSuccess: () => setConfirm(null) },
              );
            else void submitDeletion();
          }}
        />
      ) : null}
    </div>
  );
}

export function UsersPage() {
  const { t } = useTranslation();
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
      <PageHeader title={t('admin.users.title')} description={t('admin.users.description')} />
      <Card>
        <form
          className="ops-toolbar"
          onSubmit={(event) => {
            event.preventDefault();
            pager.reset();
            setQuery(queryDraft.trim());
          }}
        >
          <label>
            <span>{t('common.search')}</span>
            <input
              aria-label={t('admin.users.searchAria')}
              placeholder={t('admin.users.searchPlaceholder')}
              value={queryDraft}
              onChange={(event) => setQueryDraft(event.target.value)}
            />
          </label>
          <label>
            <span>{t('admin.users.filterStatus')}</span>
            <select
              value={banned}
              onChange={(event) => {
                pager.reset();
                setBanned(event.target.value as typeof banned);
              }}
            >
              <option value="">{t('common.all')}</option>
              <option value="false">{t('admin.users.active')}</option>
              <option value="true">{t('admin.users.banned')}</option>
            </select>
          </label>
          <button className="btn btn-secondary" type="submit">
            {t('common.applyFilter')}
          </button>
        </form>
      </Card>
      <Card>
        <h2>{t('admin.users.listTitle')}</h2>
        {users.isPending ? (
          <LoadingState />
        ) : users.error ? (
          <ErrorState error={users.error} onRetry={() => void users.refetch()} />
        ) : users.data.data.length === 0 ? (
          <EmptyState title={t('admin.users.empty')} body={t('admin.users.emptyBody')} />
        ) : (
          <>
            <div className="ops-table-scroll">
              <table className="ops-table">
                <thead>
                  <tr>
                    <th>{t('admin.users.username')}</th>
                    <th>{t('admin.users.status')}</th>
                    <th>{t('admin.users.level')}</th>
                    <th>{t('admin.users.balances')}</th>
                    <th>{t('admin.users.actions')}</th>
                  </tr>
                </thead>
                <tbody>
                  {users.data.data.map((user) => (
                    <tr key={user.id}>
                      <td>
                        {user.username}
                        <small>{user.id}</small>
                      </td>
                      <td>
                        <StatusBadge
                          active={!user.is_banned}
                          danger={user.is_banned}
                          label={t(
                            user.is_banned ? 'admin.users.banned' : 'admin.users.active',
                          )}
                        />
                      </td>
                      <td>{user.level.effective}</td>
                      <td>{user.balance}</td>
                      <td>
                        <button
                          className="btn btn-secondary"
                          type="button"
                          onClick={() => setSelected(user.id)}
                        >
                          {t('admin.users.manage')}
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <CursorPagination
              page={pager.page}
              nextCursor={users.data.next_cursor}
              onPrevious={pager.previous}
              onNext={pager.next}
            />
          </>
        )}
      </Card>
      {selected && !detailUnavailable ? detail.isPending ? <LoadingState /> : detail.error ? <ErrorState error={detail.error} onRetry={() => void detail.refetch()} /> : <UserAuthority key={detail.data.id} user={detail.data} refresh={async () => { await Promise.all([detail.refetch(), users.refetch()]); }} /> : null}
      {selected && detailUnavailable ? <ErrorState error={detail.error} onRetry={() => { setSelected(''); }} /> : null}
    </div>
  );
}
