import { useEffect, useState, type ReactNode } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useSearchState } from '@shared/operations/useSearchState';
import { useTranslation } from 'react-i18next';
import { clearStationSession } from '@shared/charityManagement';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { CopyValue } from '@shared/components/CopyValue';
import {
  Card,
  EmptyState,
  ErrorState,
  LoadingState,
  PageHeader,
  StatusBadge,
} from '@shared/components/States';
import { PagePagination } from '@shared/operations/PagePagination';
import { isPageNumber } from '@shared/operations/pageNumbers';
import { useUrlPagePager } from '@shared/operations/useUrlPagePager';
import { isForbidden, isNotFoundError, isUnauthorized } from '@shared/query/http';
import { formatDateTime } from '@shared/utils/datetime';
import {
  banManagedUser,
  mutateManagedUser,
  unbanManagedUser,
  getManagedUserDetail,
  getManagedUsersPage,
  managedUserKeys,
  managementRoot,
  type AdminUser,
  type ManagementRole,
} from '@shared/operations/managedUsers';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import '@shared/operations/operations.css';

interface UserDraft {
  endpointLimit: string;
  rpmLimit: string;
  concurrencyLimit: string;
  level: string;
  lang: '' | 'zh' | 'en';
  economyTarget: 'balance' | 'game_balance' | 'donation_credit';
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
  lang: user.lang,
  economyTarget: 'balance',
  economyDirection: 'increase',
  economyAmount: '',
  economyReason: '',
  banReason: '',
  banDuration: '',
});

function nullableLimit(value: string, min: number, max: number): string | null | undefined {
  if (!value.trim()) return null;
  if (!/^(0|[1-9][0-9]*)$/.test(value)) return undefined;
  const parsed = Number(value);
  return Number.isSafeInteger(parsed) && parsed >= min && parsed <= max ? value : undefined;
}

function UserAuthority({
  user,
  refresh,
  onClose,
  role,
  account,
  renderDeletion,
  onAuthorityLoss,
}: {
  user: AdminUser;
  refresh: () => Promise<unknown>;
  onClose: () => void;
  role: ManagementRole;
  account: string;
  onAuthorityLoss?: () => void;
  renderDeletion?: (user: AdminUser, refresh: () => Promise<unknown>) => ReactNode;
}) {
  const { t } = useTranslation();
  const [draft, setDraft] = useState<UserDraft>(() => draftFor(user));
  const [confirm, setConfirm] = useState<'ban' | 'unban' | null>(null);
  const editable = role === 'admin' || (user.id !== account && user.level.effective < 6);
  const root = managementRoot(role);
  const reconcile = async () => {
    await refresh();
  };
  const patch = useRetainedOperation(
    (input: { revision: string; body: Record<string, unknown> }, key) =>
      mutateManagedUser(role, user.id, { ...input.body, expected_revision: input.revision }, key),
    reconcile,
    root,
  );
  const ban = useRetainedOperation(
    (input: { revision: string; reason: string; duration_seconds: number | null }, key) =>
      banManagedUser(
        role,
        user.id,
        {
          reason: input.reason,
          duration_seconds: input.duration_seconds,
          expected_revision: input.revision,
        },
        key,
      ),
    reconcile,
    root,
  );
  const unban = useRetainedOperation(
    (input: { revision: string }, key) => unbanManagedUser(role, user.id, input.revision, key),
    reconcile,
    root,
  );
  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setConfirm(null);
  }, [user.revision]);
  const endpoint = nullableLimit(draft.endpointLimit, 0, 10_000);
  const rpm = nullableLimit(draft.rpmLimit, 1, 4_096);
  const concurrency = nullableLimit(draft.concurrencyLimit, 1, 100_000);
  const invalidLimits = endpoint === undefined || rpm === undefined || concurrency === undefined;

  const saveLimits = () => {
    if (!editable || invalidLimits) return;
    patch.mutate({
      revision: user.revision,
      body: {
        mode: 'profile',
        endpoint_limit: endpoint,
        rpm_limit: rpm,
        concurrency_limit: concurrency,
        ...(draft.lang ? { lang: draft.lang } : {}),
      },
    });
  };
  const banSeconds = draft.banDuration.trim() ? Number(draft.banDuration) * 86_400 : undefined;
  const banDurationLabel = draft.banDuration.trim()
    ? t('management.users.banPresetLabel', { days: draft.banDuration.trim() })
    : t('management.users.banPermanent');
  const mutationError = patch.error ?? ban.error ?? unban.error;
  useEffect(() => {
    if (isUnauthorized(mutationError) || isForbidden(mutationError)) onAuthorityLoss?.();
  }, [mutationError, onAuthorityLoss]);

  return (
    <div className="ops-stack">
      <Card>
        <div className="ops-actions">
          <h2>{user.username}</h2>
          <button className="btn btn-quiet" type="button" onClick={onClose}>
            {t('common.close')}
          </button>
        </div>
        <dl className="ops-kv">
          <dt>{t('management.users.userId')}</dt>
          <dd>{user.id}</dd>
          <dt>{t('management.users.discordId')}</dt>
          <dd>
            {user.discord_id ? (
              <CopyValue value={user.discord_id} label={t('management.users.discordId')} />
            ) : (
              t('management.users.discordUnlinked')
            )}
          </dd>
          <dt>{t('management.users.status')}</dt>
          <dd>
            <StatusBadge
              active={!user.is_banned}
              danger={user.is_banned}
              label={t(user.is_banned ? 'management.users.banned' : 'management.users.active')}
            />
          </dd>
          <dt>{t('management.users.level')}</dt>
          <dd>
            {user.level.display_name} · {t('management.users.levelAutoTag')} {user.level.automatic}{' '}
            · {t('management.users.levelEffectiveTag')} {user.level.effective}
          </dd>
          <dt>{t('management.users.balances')}</dt>
          <dd>
            {user.balance} {t('management.users.creditsBalance')} · {user.game_balance}{' '}
            {t('management.users.gameBalance')} · {user.donation_credit}{' '}
            {t('management.users.donationBalance')}
          </dd>
          <dt>
            {t('management.users.created')} / {t('management.users.updated')}
          </dt>
          <dd>
            {formatDateTime(user.created_at)} / {formatDateTime(user.updated_at)}
          </dd>
          {user.is_banned ? (
            <>
              <dt>{t('management.users.banSectionTitle')}</dt>
              <dd>
                {user.banned_until === null
                  ? t('management.users.banPermanent')
                  : t('management.users.banUntilShort', {
                      time: formatDateTime(user.banned_until),
                    })}{' '}
                · {user.banned_reason || t('management.users.banReasonMissing')}
              </dd>
            </>
          ) : null}
        </dl>
      </Card>
      <LoanHistory role={role} account={account} userID={user.id} />
      <PenaltyHistory role={role} account={account} userID={user.id} />
      {editable ? (
        <>
          <Card>
            <h2>
              {t('management.users.manageLimitsTitle')} / {t('management.users.levelSectionTitle')}
            </h2>
            <p>
              {t('management.users.limitHint')}{' '}
              {t('management.users.revisionGuard', { revision: user.revision })}
            </p>
            <div className="ops-field-grid">
              <label>
                <span>{t('management.users.endpointLimit')}</span>
                <input
                  inputMode="numeric"
                  value={draft.endpointLimit}
                  onChange={(event) => setDraft({ ...draft, endpointLimit: event.target.value })}
                />
              </label>
              <label>
                <span>{t('management.users.rpmLimit')}</span>
                <input
                  inputMode="numeric"
                  value={draft.rpmLimit}
                  onChange={(event) => setDraft({ ...draft, rpmLimit: event.target.value })}
                />
              </label>
              <label>
                <span>{t('management.users.concurrencyLimit')}</span>
                <input
                  inputMode="numeric"
                  value={draft.concurrencyLimit}
                  onChange={(event) => setDraft({ ...draft, concurrencyLimit: event.target.value })}
                />
              </label>
              <label>
                <span>{t('management.users.levelControl')}</span>
                <select
                  value={draft.level}
                  onChange={(event) => setDraft({ ...draft, level: event.target.value })}
                >
                  <option value="">{t('management.users.levelManualNone')}</option>
                  {(role === 'admin' ? [1, 2, 3, 4, 5, 6] : [1, 2, 3, 4, 5]).map((level) => (
                    <option key={level} value={level}>
                      {level}
                    </option>
                  ))}
                </select>
              </label>
            </div>
            <label className="ops-form-field">
              <span>{t('management.users.language')}</span>
              <select
                value={draft.lang}
                onChange={(event) =>
                  setDraft({ ...draft, lang: event.target.value as UserDraft['lang'] })
                }
              >
                <option value="" disabled>
                  {t('management.users.languageDefault')}
                </option>
                <option value="zh">中文</option>
                <option value="en">English</option>
              </select>
            </label>
            {invalidLimits ? (
              <p className="field-error" role="alert">
                {t('management.users.limitInvalid')}
              </p>
            ) : null}
            <div className="ops-actions">
              <button
                className="btn btn-primary"
                type="button"
                disabled={patch.isPending || invalidLimits}
                onClick={saveLimits}
              >
                {t('management.users.saveLimits')}
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
                {t('management.users.levelApply')}
              </button>
            </div>
          </Card>
          <Card>
            <h2>{t('management.users.economyTitle')}</h2>
            <p>{t('management.users.economyPositiveHint')}</p>
            <div className="ops-field-grid">
              <label>
                <span>{t('management.users.economyTarget')}</span>
                <select
                  value={draft.economyTarget}
                  onChange={(event) =>
                    setDraft({
                      ...draft,
                      economyTarget: event.target.value as UserDraft['economyTarget'],
                    })
                  }
                >
                  <option value="balance">{t('management.users.creditsBalance')}</option>
                  <option value="game_balance">{t('management.users.gameBalance')}</option>
                  {role === 'admin' ? (
                    <option value="donation_credit">{t('management.users.donationBalance')}</option>
                  ) : null}
                </select>
              </label>
              <label>
                <span>{t('management.users.economyDirection')}</span>
                <select
                  value={draft.economyDirection}
                  onChange={(event) =>
                    setDraft({
                      ...draft,
                      economyDirection: event.target.value as UserDraft['economyDirection'],
                    })
                  }
                >
                  <option value="increase">{t('management.users.economyIncrease')}</option>
                  <option value="decrease">{t('management.users.economyDecrease')}</option>
                </select>
              </label>
              <label>
                <span>{t('management.users.economyPositiveAmount')}</span>
                <input
                  value={draft.economyAmount}
                  placeholder="1.5"
                  onChange={(event) => setDraft({ ...draft, economyAmount: event.target.value })}
                />
              </label>
              <label>
                <span>{t('management.users.economyReason')}</span>
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
                    onSuccess: () => setDraft({ ...draft, economyAmount: '', economyReason: '' }),
                  },
                )
              }
            >
              {t('management.users.economySubmit')}
            </button>
          </Card>
          <Card className="ops-danger">
            <h2>{t('management.users.banSectionTitle')}</h2>
            <div className="ops-field-grid">
              <label>
                <span>{t('management.users.banReason')}</span>
                <input
                  maxLength={1024}
                  value={draft.banReason}
                  onChange={(event) => setDraft({ ...draft, banReason: event.target.value })}
                />
              </label>
              <label>
                <span>{t('management.users.banDurationDays')}</span>
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
                  {t('management.users.unban')}
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
                  {t('management.users.ban')}
                </button>
              )}
              {renderDeletion?.(user, refresh)}
            </div>
            {mutationError ? <ErrorState error={mutationError} /> : null}
          </Card>
        </>
      ) : (
        <Card>
          <p>{t('management.users.readOnly')}</p>
          <dl className="ops-kv">
            <dt>{t('management.users.endpointLimit')}</dt>
            <dd>{user.effective_endpoint_limit}</dd>
            <dt>{t('management.users.rpmLimit')}</dt>
            <dd>{user.effective_rpm_limit}</dd>
            <dt>{t('management.users.concurrencyLimit')}</dt>
            <dd>{user.effective_concurrency_limit}</dd>
            <dt>{t('management.users.language')}</dt>
            <dd>{user.lang || t('management.users.languageDefault')}</dd>
          </dl>
        </Card>
      )}
      {confirm ? (
        <ConfirmDialog
          open
          title={t(confirm === 'ban' ? 'management.users.banTitle' : 'management.users.unbanTitle')}
          description={
            confirm === 'ban'
              ? t('management.users.banSummary', { duration: banDurationLabel })
              : t('management.users.unbanBody')
          }
          confirmLabel={t(
            confirm === 'ban' ? 'management.users.banConfirm' : 'management.users.unbanConfirm',
          )}
          danger
          busy={ban.isPending || unban.isPending}
          onCancel={() => setConfirm(null)}
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
              unban.mutate({ revision: user.revision }, { onSuccess: () => setConfirm(null) });
          }}
        />
      ) : null}
    </div>
  );
}

interface UserManagementProps {
  account: string;
  scopeReady: boolean;
  sessionError: unknown;
  role: ManagementRole;
  onAuthorityLoss?: () => void;
  renderDeletion?: (user: AdminUser, refresh: () => Promise<unknown>) => ReactNode;
}

export function UserManagement({
  account,
  scopeReady,
  sessionError,
  role,
  onAuthorityLoss,
  renderDeletion,
}: UserManagementProps) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const [searchParams, setSearchParams] = useSearchState();
  const rawBanned = searchParams.get('is_banned');
  const banned: '' | 'true' | 'false' =
    rawBanned === 'true' || rawBanned === 'false' ? rawBanned : '';
  const query = searchParams.get('q') ?? '';
  const rawLevel = searchParams.get('level') ?? '';
  const level = ['1', '2', '3', '4', '5', '6'].includes(rawLevel) ? rawLevel : '';
  const rawUserID = searchParams.get('user_id') ?? '';
  const userID = rawUserID;
  const invalidCommittedUserID = userID !== '' && !isPageNumber(userID, 9_223_372_036_854_775_807n);
  const selectedValue = searchParams.get('user');
  const selected = isPageNumber(selectedValue, 9_223_372_036_854_775_807n) ? selectedValue : '';
  const [queryDraft, setQueryDraft] = useState(query);
  const [userIDDraft, setUserIDDraft] = useState(userID);
  const invalidUserIDDraft =
    userIDDraft !== '' && !isPageNumber(userIDDraft, 9_223_372_036_854_775_807n);
  const pager = useUrlPagePager({
    station: role === 'admin' ? 'admin' : 'user',
    listType: `${role}.users`,
    scopeKey: account,
    scopeReady,
    resetKey: `${banned}|${query}|${level}|${userID}`,
  });
  const users = useQuery({
    queryKey: managedUserKeys.list(
      role,
      account,
      banned,
      query,
      level,
      userID,
      pager.page,
      pager.pageSize,
    ),
    queryFn: ({ signal }) =>
      getManagedUsersPage(role, banned, query, level, pager.page, pager.pageSize, signal, userID),
    retry: false,
    enabled: scopeReady && !invalidCommittedUserID,
    placeholderData: (previous, previousQuery) =>
      !invalidCommittedUserID &&
      previousQuery?.queryKey[3] === account &&
      previousQuery.queryKey[4] === banned &&
      previousQuery.queryKey[5] === query &&
      previousQuery.queryKey[6] === level &&
      previousQuery.queryKey[7] === userID
        ? previous
        : undefined,
  });
  const detail = useQuery({
    queryKey: managedUserKeys.detail(role, account, selected),
    queryFn: ({ signal }) => getManagedUserDetail(role, selected, signal),
    retry: false,
    enabled: Boolean(selected) && scopeReady && !invalidCommittedUserID,
  });
  const detailUnavailable =
    isUnauthorized(users.error) ||
    isForbidden(users.error) ||
    isUnauthorized(detail.error) ||
    isForbidden(detail.error) ||
    isNotFoundError(detail.error);
  useEffect(() => {
    // Browser back/forward can change the committed filter while this draft
    // remains mounted. Keep the input aligned with that URL state.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setQueryDraft(query);
  }, [query]);
  useEffect(() => {
    // Keep the ID field aligned when browser history changes the URL.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setUserIDDraft(userID);
  }, [userID]);
  useEffect(() => {
    if (
      isUnauthorized(users.error) ||
      isForbidden(users.error) ||
      isUnauthorized(detail.error) ||
      isForbidden(detail.error)
    ) {
      if (onAuthorityLoss) onAuthorityLoss();
      else clearStationSession(client, role);
    }
  }, [client, detail.error, users.error, role, onAuthorityLoss]);
  const commitListState = (
    nextQuery: string,
    nextBanned: typeof banned,
    nextLevel = level,
    nextUserID = userID,
  ) => {
    if (nextUserID !== '' && !isPageNumber(nextUserID, 9_223_372_036_854_775_807n)) return;
    setSearchParams((previous) => {
      const next = new URLSearchParams(previous);
      if (nextQuery) next.set('q', nextQuery);
      else next.delete('q');
      if (nextBanned) next.set('is_banned', nextBanned);
      else next.delete('is_banned');
      if (nextLevel) next.set('level', nextLevel);
      else next.delete('level');
      if (nextUserID) next.set('user_id', nextUserID);
      else next.delete('user_id');
      next.delete('page');
      next.set('page', '1');
      next.delete('page_size');
      next.set('page_size', String(pager.pageSize));
      next.delete('user');
      return next;
    });
  };
  const selectUser = (id: string) => {
    setSearchParams((previous) => {
      const next = new URLSearchParams(previous);
      next.set('user', id);
      if (users.data) next.set('page', users.data.pagination.page);
      return next;
    });
  };
  const closeUser = () => {
    setSearchParams((previous) => {
      const next = new URLSearchParams(previous);
      next.delete('user');
      if (users.data) next.set('page', users.data.pagination.page);
      return next;
    });
  };
  return (
    <div className="page ops-page">
      <PageHeader
        title={t('management.users.title')}
        description={t('management.users.description')}
      />
      <Card>
        <form
          className="ops-toolbar"
          onSubmit={(event) => {
            event.preventDefault();
            commitListState(queryDraft.trim(), banned, level, userIDDraft);
          }}
        >
          <label>
            <span>{t('management.users.userId')}</span>
            <input
              aria-label={t('management.users.userId')}
              aria-invalid={invalidUserIDDraft}
              inputMode="numeric"
              maxLength={64}
              value={userIDDraft}
              onChange={(event) => setUserIDDraft(event.target.value)}
            />
          </label>
          <label>
            <span>{t('common.search')}</span>
            <input
              aria-label={t('management.users.searchAria')}
              placeholder={t('management.users.searchPlaceholder')}
              value={queryDraft}
              onChange={(event) => setQueryDraft(event.target.value)}
            />
          </label>
          <label>
            <span>{t('management.users.filterStatus')}</span>
            <select
              value={banned}
              onChange={(event) =>
                commitListState(query, event.target.value as typeof banned, level, userIDDraft)
              }
            >
              <option value="">{t('common.all')}</option>
              <option value="false">{t('management.users.active')}</option>
              <option value="true">{t('management.users.banned')}</option>
            </select>
          </label>
          <label>
            <span>{t('management.users.level')}</span>
            <select
              value={level}
              onChange={(event) => commitListState(query, banned, event.target.value, userIDDraft)}
            >
              <option value="">{t('common.all')}</option>
              {[1, 2, 3, 4, 5, 6].map((value) => (
                <option key={value} value={value}>
                  {value}
                </option>
              ))}
            </select>
          </label>
          {invalidUserIDDraft ? (
            <p className="field-error" role="alert">
              {t('management.users.userIdInvalid')}
            </p>
          ) : null}
          <button className="btn btn-secondary" type="submit" disabled={invalidUserIDDraft}>
            {t('common.applyFilter')}
          </button>
        </form>
      </Card>
      <Card>
        <h2>{t('management.users.listTitle')}</h2>
        {invalidCommittedUserID ? (
          <p role="status">{t('management.users.userIdInvalid')}</p>
        ) : sessionError ? (
          <ErrorState error={sessionError} />
        ) : users.isPending ? (
          <LoadingState />
        ) : users.error ? (
          <ErrorState error={users.error} onRetry={() => void users.refetch()} />
        ) : users.data.data.length === 0 ? (
          <EmptyState title={t('management.users.empty')} body={t('management.users.emptyBody')} />
        ) : (
          <div aria-busy={users.isFetching}>
            {users.isFetching ? <LoadingState /> : null}
            <div className="ops-table-scroll">
              <table className="ops-table ops-users-table">
                <thead>
                  <tr>
                    <th>{t('management.users.userId')}</th>
                    <th>{t('management.users.username')}</th>
                    <th>{t('management.users.discordId')}</th>
                    <th>{t('management.users.status')}</th>
                    <th>{t('management.users.level')}</th>
                    <th>{t('management.users.balances')}</th>
                    <th>{t('management.users.actions')}</th>
                  </tr>
                </thead>
                <tbody>
                  {users.data.data.map((user) => (
                    <tr key={user.id}>
                      <td data-label={t('management.users.userId')}>{user.id}</td>
                      <td data-label={t('management.users.username')}>{user.username}</td>
                      <td
                        data-label={t('management.users.discordId')}
                        className="ops-users-discord"
                      >
                        {user.discord_id ? (
                          <CopyValue
                            value={user.discord_id}
                            label={t('management.users.discordId')}
                          />
                        ) : (
                          t('management.users.discordUnlinked')
                        )}
                      </td>
                      <td data-label={t('management.users.status')}>
                        <StatusBadge
                          active={!user.is_banned}
                          danger={user.is_banned}
                          label={t(
                            user.is_banned ? 'management.users.banned' : 'management.users.active',
                          )}
                        />
                      </td>
                      <td data-label={t('management.users.level')}>{user.level.effective}</td>
                      <td data-label={t('management.users.balances')}>
                        {user.balance} {t('management.users.creditsBalance')} · {user.game_balance}{' '}
                        {t('management.users.gameBalance')}
                      </td>
                      <td data-label={t('management.users.actions')}>
                        <button
                          className="btn btn-secondary"
                          type="button"
                          disabled={!scopeReady || users.isFetching}
                          onClick={() => selectUser(user.id)}
                        >
                          {t(
                            role === 'steward' &&
                              (user.id === account || user.level.effective === 6)
                              ? 'management.users.view'
                              : 'management.users.manage',
                          )}
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>
        )}
        {scopeReady && !sessionError && !users.error && users.data ? (
          <PagePagination
            metadata={users.data.pagination}
            requestedPage={pager.page}
            busy={users.isFetching}
            onPageChange={pager.setPage}
            onPageSizeChange={pager.setPageSize}
          />
        ) : null}
      </Card>
      {scopeReady && !sessionError && !invalidCommittedUserID && selected && !detailUnavailable ? (
        detail.isPending ? (
          <LoadingState />
        ) : detail.error ? (
          <ErrorState error={detail.error} onRetry={() => void detail.refetch()} />
        ) : (
          <UserAuthority
            key={detail.data.id}
            user={detail.data}
            role={role}
            account={account}
            renderDeletion={renderDeletion}
            onAuthorityLoss={onAuthorityLoss}
            onClose={closeUser}
            refresh={async () => {
              await Promise.all([detail.refetch(), users.refetch()]);
            }}
          />
        )
      ) : null}
      {selected && detailUnavailable ? (
        <ErrorState error={detail.error ?? users.error} onRetry={closeUser} />
      ) : null}
    </div>
  );
}
import { LoanHistory } from './LoanDetails';
import { PenaltyHistory } from './PenaltyHistory';
