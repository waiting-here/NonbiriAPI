import { Button } from '@shared/components/ui/Button';
import { useEffect, useId, useState, type ReactNode } from 'react';
import { Link } from 'react-router';
import {
  Affix,
  DataTable,
  Field,
  Fold,
  OutcomeNote,
  Segmented,
  Tabs,
  type OutcomePresentation,
} from '@shared/components/ui';
import { useDetailNavigation } from '@shared/operations/useDetailNavigation';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useSearchState } from '@shared/operations/useSearchState';
import { useTranslation } from 'react-i18next';
import { clearStationSession } from '@shared/charityManagement';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { ReasonText } from '@shared/components/ReasonText';
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
import { useDateTimeFormatter } from '@shared/utils/datetime';
import { accountCopy, accountHistoryCopy } from './accountCopy';
import { DeletedAccountCard } from './DeletedAccountCard';
import {
  banManagedUser,
  mutateManagedUser,
  unbanManagedUser,
  getManagedUserDetail,
  getManagedAccountsPage,
  getDeletedAccountDetail,
  managedUserKeys,
  managementRoot,
  type AdminUser,
  type AccountState,
  type ManagementRole,
} from '@shared/operations/managedUsers';
import {
  type OperationOutcome,
  useRetainedOperation,
} from '@shared/operations/useRetainedOperation';
import '@shared/operations/operations.css';
import './userManagement.css';

interface UserDraft {
  endpointLimit: string;
  rpmLimit: string;
  concurrencyLimit: string;
  level: string;
  lang: '' | 'zh' | 'en';
  discordGatePolicy: AdminUser['discord_gate_policy'];
  economyTarget: 'balance' | 'game_balance' | 'donation_credit';
  economyDirection: 'increase' | 'decrease';
  economyAmount: string;
  economyReason: string;
  banReason: string;
  banDuration: string;
}

interface EconomyIntent {
  revision: string;
  body: {
    mode: 'economy';
    target: UserDraft['economyTarget'];
    direction: UserDraft['economyDirection'];
    amount: string;
    reason: string;
  };
}
const economyTargetLabels = {
  balance: 'management.users.creditsBalance',
  game_balance: 'management.users.gameBalance',
  donation_credit: 'management.users.donationBalance',
} as const;

const draftFor = (user: AdminUser): UserDraft => ({
  endpointLimit: user.endpoint_limit ?? '',
  rpmLimit: user.rpm_limit ?? '',
  concurrencyLimit: user.concurrency_limit ?? '',
  level: user.level.manual === null ? '' : String(user.level.manual),
  lang: user.lang,
  discordGatePolicy: user.discord_gate_policy,
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

function profileOutcome(outcome: OperationOutcome, recheck: () => void): OutcomePresentation {
  if (outcome === 'confirmed') return { kind: 'saved' };
  if (outcome === 'refresh-failed') return { kind: 'savedRefreshFailed', recheck };
  if (outcome === 'unknown') return { kind: 'unknown', recheck };
  if (outcome === 'conflict') return { kind: 'conflict', reload: recheck };
  return { kind: 'idle' };
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
  const formatDateTime = useDateTimeFormatter();
  const { t } = useTranslation();
  const [draft, setDraft] = useState<UserDraft>(() => draftFor(user));
  const tabID = useId();
  const [tab, setTab] = useState<'overview' | 'limits' | 'economy' | 'penalties'>('overview');
  const [penaltiesVisited, setPenaltiesVisited] = useState(false);
  const [coordinating, setCoordinating] = useState(false);
  const [confirmedUser, setConfirmedUser] = useState<AdminUser | null>(null);
  const [confirm, setConfirm] = useState<'ban' | 'unban' | null>(null);
  const [economyConfirm, setEconomyConfirm] = useState<EconomyIntent | null>(null);
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
  const profile = useRetainedOperation(
    (input: { revision: string; body: Record<string, unknown> }, key) =>
      mutateManagedUser(role, user.id, { ...input.body, expected_revision: input.revision }, key),
    reconcile,
    root,
  );
  const manualLevel = useRetainedOperation(
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
    setEconomyConfirm(null);
  }, [user.revision]);
  const endpoint = nullableLimit(draft.endpointLimit, 0, 10_000);
  const rpm = nullableLimit(draft.rpmLimit, 1, 4_096);
  const concurrency = nullableLimit(draft.concurrencyLimit, 1, 100_000);
  const invalidLimits = endpoint === undefined || rpm === undefined || concurrency === undefined;

  const baseline =
    confirmedUser && BigInt(confirmedUser.revision) > BigInt(user.revision) ? confirmedUser : user;
  const profileChanged =
    endpoint !== baseline.endpoint_limit ||
    rpm !== baseline.rpm_limit ||
    concurrency !== baseline.concurrency_limit ||
    draft.lang !== baseline.lang ||
    (role === 'admin' && draft.discordGatePolicy !== baseline.discord_gate_policy);
  const levelChanged = (draft.level ? Number(draft.level) : null) !== baseline.level.manual;
  const profileBusy = coordinating || profile.isPending || manualLevel.isPending;
  const saveLimits = async () => {
    if (!editable || invalidLimits || profileBusy) return;
    const profileBody = {
      mode: 'profile',
      endpoint_limit: endpoint,
      rpm_limit: rpm,
      concurrency_limit: concurrency,
      ...(draft.lang ? { lang: draft.lang } : {}),
      ...(role === 'admin' && draft.discordGatePolicy !== baseline.discord_gate_policy
        ? { discord_gate_policy: draft.discordGatePolicy }
        : {}),
    };
    const levelBody = { mode: 'profile', level: draft.level ? Number(draft.level) : null };
    let revision = baseline.revision;
    setCoordinating(true);
    try {
      if (profileChanged) {
        const saved = await profile.mutateAsync({ revision, body: profileBody });
        setConfirmedUser(saved);
        revision = saved.revision;
      }
      if (levelChanged) {
        const saved = await manualLevel.mutateAsync({ revision, body: levelBody });
        setConfirmedUser(saved);
      }
    } catch {
      // Each operation retains its own outcome and retry identity.
    } finally {
      setCoordinating(false);
    }
  };
  const banSeconds = draft.banDuration.trim() ? Number(draft.banDuration) * 86_400 : undefined;
  const banDurationLabel = draft.banDuration.trim()
    ? t('management.users.banPresetLabel', { days: draft.banDuration.trim() })
    : t('management.users.banPermanent');
  const mutationError =
    patch.error ?? profile.error ?? manualLevel.error ?? ban.error ?? unban.error;
  useEffect(() => {
    if (isUnauthorized(mutationError) || isForbidden(mutationError)) onAuthorityLoss?.();
  }, [mutationError, onAuthorityLoss]);

  return (
    <div className="ops-stack user-authority">
      <Card>
        <div className="ops-actions">
          <h2>{user.username}</h2>
          <Button variant="ghost" type="button" onClick={onClose}>
            {t('management.users.backToList')}
          </Button>
        </div>
        <dl className="nb-facts nb-facts--inline user-identity">
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
        </dl>
      </Card>
      <Tabs
        label={t('management.users.detailTabs')}
        value={tab}
        tabs={[
          { value: 'overview', label: t('management.users.tabOverview') },
          { value: 'limits', label: t('management.users.tabLimits') },
          { value: 'economy', label: t('management.users.tabEconomy') },
          { value: 'penalties', label: t('management.users.tabPenalties') },
        ].map(({ value, label }) => ({
          value: value as typeof tab,
          label,
          id: `${tabID}-${value}`,
          panelId: `${tabID}-${value}-panel`,
        }))}
        onChange={(next) => {
          setTab(next);
          if (next === 'penalties') setPenaltiesVisited(true);
        }}
      />
      <section
        role="tabpanel"
        id={`${tabID}-overview-panel`}
        aria-labelledby={`${tabID}-overview`}
        hidden={tab !== 'overview'}
      >
        <Card>
          <dl className="nb-facts nb-facts--inline">
            <dt>{t('management.users.level')}</dt>
            <dd>
              {user.level.display_name} · {t('management.users.levelAutoTag')}{' '}
              {user.level.automatic} · {t('management.users.levelEffectiveTag')}{' '}
              {user.level.effective}
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
                  <ReasonText reason={user.banned_reason} automatic={user.automatic_reason} />
                </dd>
              </>
            ) : null}
            <dt>{t('management.users.levelControl')}</dt>
            <dd>{user.level.manual ?? t('management.users.levelManualNone')}</dd>
          </dl>
          <div className="nb-actions">
            <Link
              className="nb-btn nb-btn--secondary"
              to={
                role === 'admin'
                  ? `/logs?user_id=${user.id}`
                  : `/steward?tab=logs&user_id=${user.id}`
              }
            >
              {t('management.users.viewLogs')}
            </Link>
            <LoanHistory role={role} account={account} userID={user.id} />
          </div>
        </Card>
      </section>
      {editable ? (
        <>
          <section
            role="tabpanel"
            id={`${tabID}-limits-panel`}
            aria-labelledby={`${tabID}-limits`}
            hidden={tab !== 'limits'}
          >
            <Card>
              <h2>
                {t('management.users.manageLimitsTitle')} /{' '}
                {t('management.users.levelSectionTitle')}
              </h2>
              <p>{t('management.users.limitHint')}</p>
              <Fold title={t('common.operations.management.details')}>
                <p>{t('management.users.revisionGuard', { revision: user.revision })}</p>
              </Fold>
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
                    onChange={(event) =>
                      setDraft({ ...draft, concurrencyLimit: event.target.value })
                    }
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
              {role === 'admin' ? (
                <Field
                  label={t('management.users.discordGatePolicy')}
                  help={t('management.users.discordGateHelp')}
                >
                  {(field) => (
                    <select
                      {...field}
                      value={draft.discordGatePolicy}
                      onChange={(event) =>
                        setDraft({
                          ...draft,
                          discordGatePolicy: event.target.value as UserDraft['discordGatePolicy'],
                        })
                      }
                    >
                      <option value="inherit">{t('management.users.discordGateInherit')}</option>
                      <option value="require">{t('management.users.discordGateRequire')}</option>
                      <option value="exempt">{t('management.users.discordGateExempt')}</option>
                    </select>
                  )}
                </Field>
              ) : null}
              {invalidLimits ? (
                <p className="field-error" role="alert">
                  {t('management.users.limitInvalid')}
                </p>
              ) : null}
              <div className="nb-actions user-save-actions">
                <Button
                  variant="primary"
                  type="button"
                  disabled={profileBusy || invalidLimits || (!profileChanged && !levelChanged)}
                  onClick={() => void saveLimits()}
                >
                  {t('management.users.saveProfile')}
                </Button>
              </div>
              {[
                { label: 'management.users.profileResult', operation: profile },
                { label: 'management.users.levelResult', operation: manualLevel },
              ].map(({ label, operation }) =>
                operation.outcome !== 'idle' && operation.outcome !== 'pending' ? (
                  <section className="user-save-result" aria-label={t(label)} key={label}>
                    <h3>{t(label)}</h3>
                    <OutcomeNote
                      outcome={profileOutcome(
                        operation.outcome,
                        () => void (operation.isSuccess ? operation.refresh() : operation.check()),
                      )}
                    />
                    {operation.error ? <ErrorState error={operation.error} /> : null}
                    {operation.refreshError ? (
                      <ErrorState
                        error={operation.refreshError}
                        onRetry={() =>
                          void (operation.isSuccess ? operation.refresh() : operation.check())
                        }
                      />
                    ) : null}
                  </section>
                ) : null,
              )}
            </Card>
          </section>
          <section
            role="tabpanel"
            id={`${tabID}-economy-panel`}
            aria-labelledby={`${tabID}-economy`}
            hidden={tab !== 'economy'}
          >
            <Card>
              <h2>{t('management.users.economyTitle')}</h2>
              <p>{t('management.users.economyPositiveHint')}</p>
              <div className="ops-field-grid">
                <Segmented
                  label={t('management.users.economyTarget')}
                  value={draft.economyTarget}
                  options={(role === 'admin'
                    ? (['balance', 'game_balance', 'donation_credit'] as const)
                    : (['balance', 'game_balance'] as const)
                  ).map((value) => ({ value, label: t(economyTargetLabels[value]) }))}
                  onChange={(economyTarget) => setDraft({ ...draft, economyTarget })}
                />
                <Segmented
                  label={t('management.users.economyDirection')}
                  value={draft.economyDirection}
                  options={[
                    { value: 'increase', label: t('management.users.economyIncrease') },
                    { value: 'decrease', label: t('management.users.economyDecrease') },
                  ]}
                  onChange={(economyDirection) => setDraft({ ...draft, economyDirection })}
                />
                <label>
                  <span>{t('management.users.economyPositiveAmount')}</span>
                  <Affix
                    aria-label={t('management.users.economyPositiveAmount')}
                    unit={t('management.users.creditsBalance')}
                    inputMode="decimal"
                    value={draft.economyAmount}
                    placeholder="1.5"
                    onChange={(event) => setDraft({ ...draft, economyAmount: event.target.value })}
                  />
                </label>
                <label>
                  <span>{t('management.users.economyReason')}</span>
                  <input
                    aria-label={t('management.users.economyReason')}
                    required
                    maxLength={1024}
                    value={draft.economyReason}
                    onChange={(event) => setDraft({ ...draft, economyReason: event.target.value })}
                  />
                  <small className="user-field-hint">{t('management.users.reasonRequired')}</small>
                </label>
              </div>
              <Button
                variant="primary"
                type="button"
                disabled={
                  patch.isPending ||
                  !draft.economyAmount ||
                  draft.economyAmount.startsWith('-') ||
                  !draft.economyReason.trim()
                }
                onClick={() =>
                  setEconomyConfirm({
                    revision: user.revision,
                    body: {
                      mode: 'economy',
                      target: draft.economyTarget,
                      direction: draft.economyDirection,
                      amount: draft.economyAmount,
                      reason: draft.economyReason.trim(),
                    },
                  })
                }
              >
                {t('management.users.economySubmit')}
              </Button>
            </Card>
          </section>
          <section
            role="tabpanel"
            id={`${tabID}-penalties-panel`}
            aria-labelledby={`${tabID}-penalties`}
            hidden={tab !== 'penalties'}
          >
            {penaltiesVisited ? (
              <Card>
                <PenaltyHistory inline role={role} account={account} userID={user.id} />
              </Card>
            ) : null}
            <Card>
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
                  <Button
                    variant="danger-outline"
                    type="button"
                    onClick={() => setConfirm('unban')}
                  >
                    {t('management.users.unban')}
                  </Button>
                ) : (
                  <Button
                    variant="danger-outline"
                    type="button"
                    disabled={
                      !draft.banReason.trim() ||
                      (draft.banDuration !== '' &&
                        (!Number.isSafeInteger(banSeconds) || (banSeconds ?? 0) < 1))
                    }
                    onClick={() => setConfirm('ban')}
                  >
                    {t('management.users.ban')}
                  </Button>
                )}
              </div>
              {mutationError && !economyConfirm ? <ErrorState error={mutationError} /> : null}
            </Card>
          </section>
          <Card className="user-danger">
            <h2>{t('management.users.dangerTitle')}</h2>
            <div className="user-danger-row">
              <p>{t('management.users.banConsequence')}</p>
              <Button
                variant="danger-outline"
                type="button"
                onClick={() => {
                  if (user.is_banned) setConfirm('unban');
                  else {
                    setTab('penalties');
                    setPenaltiesVisited(true);
                  }
                }}
              >
                {t(user.is_banned ? 'management.users.unbanAction' : 'management.users.banAction')}
              </Button>
            </div>
            {renderDeletion ? (
              <div className="user-danger-row">
                <p>{t('management.users.deleteConsequence')}</p>
                {renderDeletion(user, refresh)}
              </div>
            ) : null}
          </Card>
        </>
      ) : (
        <>
          <section
            role="tabpanel"
            id={`${tabID}-limits-panel`}
            aria-labelledby={`${tabID}-limits`}
            hidden={tab !== 'limits'}
          >
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
          </section>
          <section
            role="tabpanel"
            id={`${tabID}-economy-panel`}
            aria-labelledby={`${tabID}-economy`}
            hidden={tab !== 'economy'}
          >
            <Card>
              <p>{t('management.users.readOnly')}</p>
            </Card>
          </section>
          <section
            role="tabpanel"
            id={`${tabID}-penalties-panel`}
            aria-labelledby={`${tabID}-penalties`}
            hidden={tab !== 'penalties'}
          >
            {penaltiesVisited ? (
              <Card>
                <PenaltyHistory inline role={role} account={account} userID={user.id} />
              </Card>
            ) : null}
          </section>
        </>
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
      {economyConfirm ? (
        <ConfirmDialog
          open
          danger={economyConfirm.body.direction === 'decrease'}
          title={t('management.users.economyConfirmTitle')}
          description={t('management.users.economyConfirmBody', { user: user.username })}
          confirmLabel={t('management.users.economySubmit')}
          busy={patch.isPending}
          onCancel={() => setEconomyConfirm(null)}
          onConfirm={() =>
            patch.mutate(economyConfirm, {
              onSuccess: () => {
                setEconomyConfirm(null);
                setDraft((value) => ({ ...value, economyAmount: '', economyReason: '' }));
              },
            })
          }
        >
          <dl className="ops-kv">
            <dt>{t('management.users.economyTarget')}</dt>
            <dd>{t(economyTargetLabels[economyConfirm.body.target])}</dd>
            <dt>{t('management.users.economyDirection')}</dt>
            <dd>
              {t(
                economyConfirm.body.direction === 'increase'
                  ? 'management.users.economyIncrease'
                  : 'management.users.economyDecrease',
              )}
            </dd>
            <dt>{t('management.users.economyPositiveAmount')}</dt>
            <dd>{economyConfirm.body.amount}</dd>
            <dt>{t('management.users.economyReason')}</dt>
            <dd>{economyConfirm.body.reason}</dd>
          </dl>
          {patch.error ? <ErrorState error={patch.error} /> : null}
        </ConfirmDialog>
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
  const accountLabels = accountCopy(t);
  const client = useQueryClient();
  const [searchParams, setSearchParams] = useSearchState();
  const rawBanned = searchParams.get('is_banned');
  const banned: '' | 'true' | 'false' =
    rawBanned === 'true' || rawBanned === 'false' ? rawBanned : '';
  const rawAccountState = searchParams.get('account_state');
  const accountState: AccountState =
    rawAccountState === 'active' || rawAccountState === 'deleted' ? rawAccountState : 'all';
  const query = searchParams.get('q') ?? '';
  const rawLevel = searchParams.get('level') ?? '';
  const level = ['1', '2', '3', '4', '5', '6'].includes(rawLevel) ? rawLevel : '';
  const rawUserID = searchParams.get('user_id') ?? '';
  const userID = rawUserID;
  const invalidCommittedUserID = userID !== '' && !isPageNumber(userID, 9_223_372_036_854_775_807n);
  const discordID = searchParams.get('discord_id') ?? '';
  const invalidCommittedDiscordID =
    discordID !== '' && !isPageNumber(discordID, 18_446_744_073_709_551_615n);
  const selectedValue = searchParams.get('user');
  const selected = isPageNumber(selectedValue, 9_223_372_036_854_775_807n) ? selectedValue : '';
  const selectedDeletedValue = searchParams.get('deleted');
  const selectedDeleted = isPageNumber(selectedDeletedValue, 9_223_372_036_854_775_807n)
    ? selectedDeletedValue
    : '';
  const [queryDraft, setQueryDraft] = useState(query);
  const [userIDDraft, setUserIDDraft] = useState(userID);
  const [discordIDDraft, setDiscordIDDraft] = useState(discordID);
  const invalidUserIDDraft =
    userIDDraft !== '' && !isPageNumber(userIDDraft, 9_223_372_036_854_775_807n);
  const invalidDiscordIDDraft =
    discordIDDraft !== '' && !isPageNumber(discordIDDraft, 18_446_744_073_709_551_615n);
  const pager = useUrlPagePager({
    station: role === 'admin' ? 'admin' : 'user',
    listType: `${role}.users`,
    scopeKey: account,
    scopeReady,
    resetKey: `${accountState}|${banned}|${query}|${level}|${userID}|${discordID}`,
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
      accountState,
      discordID,
    ),
    queryFn: ({ signal }) =>
      getManagedAccountsPage(
        role,
        accountState,
        banned,
        query,
        level,
        pager.page,
        pager.pageSize,
        signal,
        userID,
        discordID,
      ),
    retry: false,
    enabled: scopeReady && !invalidCommittedUserID && !invalidCommittedDiscordID,
    placeholderData: (previous, previousQuery) =>
      !invalidCommittedUserID &&
      !invalidCommittedDiscordID &&
      previousQuery?.queryKey[3] === account &&
      previousQuery.queryKey[4] === banned &&
      previousQuery.queryKey[5] === query &&
      previousQuery.queryKey[6] === level &&
      previousQuery.queryKey[7] === userID &&
      previousQuery.queryKey[10] === accountState &&
      previousQuery.queryKey[11] === discordID
        ? previous
        : undefined,
  });
  const detail = useQuery({
    queryKey: managedUserKeys.detail(role, account, selected),
    queryFn: ({ signal }) => getManagedUserDetail(role, selected, signal),
    retry: false,
    enabled:
      Boolean(selected) && scopeReady && !invalidCommittedUserID && !invalidCommittedDiscordID,
  });
  const deletedDetail = useQuery({
    queryKey: [...managementRoot(role), 'deleted-account', account, selectedDeleted],
    queryFn: ({ signal }) => getDeletedAccountDetail(role, selectedDeleted, signal),
    retry: false,
    enabled:
      Boolean(selectedDeleted) &&
      scopeReady &&
      !invalidCommittedUserID &&
      !invalidCommittedDiscordID,
  });
  const detailUnavailable =
    isUnauthorized(users.error) ||
    isForbidden(users.error) ||
    isUnauthorized(detail.error) ||
    isForbidden(detail.error) ||
    isNotFoundError(detail.error) ||
    isUnauthorized(deletedDetail.error) ||
    isForbidden(deletedDetail.error) ||
    isNotFoundError(deletedDetail.error);
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
    // Keep the exact identity filter aligned with browser navigation.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setDiscordIDDraft(discordID);
  }, [discordID]);
  useEffect(() => {
    if (
      isUnauthorized(users.error) ||
      isForbidden(users.error) ||
      isUnauthorized(detail.error) ||
      isForbidden(detail.error) ||
      isUnauthorized(deletedDetail.error) ||
      isForbidden(deletedDetail.error)
    ) {
      if (onAuthorityLoss) onAuthorityLoss();
      else clearStationSession(client, role);
    }
  }, [client, detail.error, deletedDetail.error, users.error, role, onAuthorityLoss]);
  const commitListState = (
    nextQuery: string,
    nextBanned: typeof banned,
    nextLevel = level,
    nextUserID = userID,
    nextAccountState: AccountState = accountState,
    nextDiscordID = discordID,
  ) => {
    if (
      (nextUserID !== '' && !isPageNumber(nextUserID, 9_223_372_036_854_775_807n)) ||
      (nextDiscordID !== '' && !isPageNumber(nextDiscordID, 18_446_744_073_709_551_615n))
    )
      return;
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
      if (nextDiscordID) next.set('discord_id', nextDiscordID);
      else next.delete('discord_id');
      next.set('account_state', nextAccountState);
      next.delete('page');
      next.set('page', '1');
      next.delete('page_size');
      next.set('page_size', String(pager.pageSize));
      next.delete('user');
      next.delete('deleted');
      return next;
    });
  };
  const selection = selected
    ? `user:${selected}`
    : selectedDeleted
      ? `deleted:${selectedDeleted}`
      : '';
  const navigationReady =
    !users.isPending &&
    !users.isFetching &&
    (!selection ||
      (selected
        ? !detail.isPending && !detail.isFetching
        : !deletedDetail.isPending && !deletedDetail.isFetching));
  const { listRef, detailRef, remember } = useDetailNavigation(selection, navigationReady);
  const selectUser = (id: string) => {
    remember();
    setSearchParams((previous) => {
      const next = new URLSearchParams(previous);
      next.set('user', id);
      next.delete('deleted');
      if (users.data) next.set('page', users.data.pagination.page);
      return next;
    });
  };
  const selectDeleted = (id: string) => {
    remember();
    setSearchParams((previous) => {
      const next = new URLSearchParams(previous);
      next.set('deleted', id);
      next.delete('user');
      if (users.data) next.set('page', users.data.pagination.page);
      return next;
    });
  };
  const closeUser = () => {
    setSearchParams((previous) => {
      const next = new URLSearchParams(previous);
      next.delete('user');
      next.delete('deleted');
      if (users.data) next.set('page', users.data.pagination.page);
      return next;
    });
  };
  return (
    <div className="page ops-page user-management">
      <PageHeader
        title={role === 'admin' ? t('admin.navigation.users') : t('management.users.title')}
        description={t('management.users.description')}
      />
      <div className={`nb-md${selection ? ' has-selection' : ''}`}>
        <div className="nb-md__list ops-stack" ref={listRef} tabIndex={-1}>
          <Card>
            <form
              className="ops-toolbar"
              onSubmit={(event) => {
                event.preventDefault();
                commitListState(
                  queryDraft.trim(),
                  banned,
                  level,
                  userIDDraft,
                  accountState,
                  discordIDDraft,
                );
              }}
            >
              <label>
                <span>{t('common.search')}</span>
                <input
                  aria-label={t('management.users.searchAria')}
                  placeholder={t('management.users.searchPlaceholder')}
                  value={queryDraft}
                  onChange={(event) => setQueryDraft(event.target.value)}
                />
              </label>
              <Fold title={t('management.users.exactFilters')}>
                <div className="ops-field-grid">
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
                    <span>{t('management.users.discordId')}</span>
                    <input
                      aria-label={t('management.users.discordId')}
                      aria-invalid={invalidDiscordIDDraft}
                      inputMode="numeric"
                      maxLength={20}
                      value={discordIDDraft}
                      onChange={(event) => setDiscordIDDraft(event.target.value)}
                    />
                  </label>
                  <label>
                    <span>{accountLabels.allAccounts}</span>
                    <select
                      value={accountState}
                      onChange={(event) =>
                        commitListState(
                          query,
                          banned,
                          level,
                          userIDDraft,
                          event.target.value as AccountState,
                        )
                      }
                    >
                      <option value="all">{accountLabels.allAccounts}</option>
                      <option value="active">{accountLabels.activeAccounts}</option>
                      <option value="deleted">{accountLabels.deletedAccounts}</option>
                    </select>
                  </label>
                </div>
              </Fold>
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
                  onChange={(event) =>
                    commitListState(query, banned, event.target.value, userIDDraft)
                  }
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
              {invalidDiscordIDDraft ? (
                <p className="field-error" role="alert">
                  {accountLabels.discordInvalid}
                </p>
              ) : null}
              <Button

                type="submit"
                disabled={invalidUserIDDraft || invalidDiscordIDDraft}
              >
                {t('common.applyFilter')}
              </Button>
            </form>
          </Card>
          <Card>
            <h2>{t('management.users.listTitle')}</h2>
            {accountState !== 'active' ? <p>{accountHistoryCopy(t).coverage}</p> : null}
            {invalidCommittedUserID || invalidCommittedDiscordID ? (
              <p role="status">
                {invalidCommittedUserID
                  ? t('management.users.userIdInvalid')
                  : accountLabels.discordInvalid}
              </p>
            ) : sessionError ? (
              <ErrorState error={sessionError} />
            ) : users.isPending ? (
              <LoadingState />
            ) : users.error ? (
              <ErrorState error={users.error} onRetry={() => void users.refetch()} />
            ) : users.data.data.length === 0 ? (
              <EmptyState
                title={t('management.users.empty')}
                body={t('management.users.emptyBody')}
              />
            ) : (
              <div aria-busy={users.isFetching}>
                {users.isFetching ? <LoadingState /> : null}
                <DataTable
                  dense
                  caption={t('management.users.listTitle')}
                  rows={users.data.data}
                  rowKey={(entry) =>
                    entry.account_state === 'deleted'
                      ? `deleted:${entry.deleted.record_id}`
                      : `user:${entry.user.id}`
                  }
                  selectedKey={selection}
                  columns={[
                    {
                      key: 'user',
                      header: t('management.users.username'),
                      cell: 'title',
                      render: (entry) => {
                        const deleted = entry.account_state === 'deleted';
                        const id = deleted ? entry.deleted.former_user_id : entry.user.id;
                        const discord = deleted ? entry.deleted.discord_id : entry.user.discord_id;
                        return (
                          <div
                            className={
                              deleted ? 'user-list-identity is-deleted' : 'user-list-identity'
                            }
                          >
                            <button
                              className="user-list-name"
                              type="button"
                              disabled={!scopeReady || users.isFetching}
                              aria-label={deleted ? t('management.users.view') : undefined}
                              onClick={() =>
                                deleted
                                  ? selectDeleted(entry.deleted.record_id)
                                  : selectUser(entry.user.id)
                              }
                            >
                              {deleted ? accountLabels.deleted : entry.user.username}
                            </button>
                            <span>
                              #{id ?? accountLabels.unknown} ·{' '}
                              {discord
                                ? `${discord.slice(0, 4)}…${discord.slice(-4)}`
                                : t('management.users.discordUnlinked')}
                            </span>
                          </div>
                        );
                      },
                    },
                    {
                      key: 'level',
                      header: t('management.users.level'),
                      cell: 'meta',
                      mobileLabel: t('management.users.level'),
                      render: (entry) =>
                        entry.account_state === 'deleted'
                          ? (entry.deleted.effective_level ?? accountLabels.unknown)
                          : entry.user.level.effective,
                    },
                    {
                      key: 'status',
                      header: t('management.users.status'),
                      cell: 'status',
                      render: (entry) =>
                        entry.account_state === 'deleted' ? (
                          <StatusBadge active={false} label={accountLabels.deleted} />
                        ) : (
                          <StatusBadge
                            active={!entry.user.is_banned}
                            danger={entry.user.is_banned}
                            label={t(
                              entry.user.is_banned
                                ? 'management.users.banned'
                                : 'management.users.active',
                            )}
                          />
                        ),
                    },
                    {
                      key: 'balance',
                      header: t('management.users.creditsBalance'),
                      cell: 'meta',
                      align: 'num',
                      mobileLabel: t('management.users.creditsBalance'),
                      render: (entry) =>
                        entry.account_state === 'deleted'
                          ? (entry.deleted.general_balance ?? accountLabels.unknown)
                          : entry.user.balance,
                    },
                  ]}
                />
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
        </div>
        <div className="nb-md__detail" ref={detailRef} tabIndex={-1}>
          {!selection ? (
            <EmptyState
              title={t('management.users.selectUser')}
              body={t('management.users.selectUserBody')}
            />
          ) : !selected || detailUnavailable || detail.isPending || detail.error ? (
            <Button
              variant="ghost" className="user-detail-back"
              type="button"
              onClick={closeUser}
            >
              {t('management.users.backToList')}
            </Button>
          ) : null}
          {scopeReady &&
          !sessionError &&
          !invalidCommittedUserID &&
          !invalidCommittedDiscordID &&
          selected &&
          !detailUnavailable ? (
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
                  const results = await Promise.all([detail.refetch(), users.refetch()]);
                  for (const result of results) if (result.error) throw result.error;
                }}
              />
            )
          ) : null}
          {selected && detailUnavailable ? (
            <ErrorState error={detail.error ?? users.error} onRetry={closeUser} />
          ) : null}
          {scopeReady &&
          !sessionError &&
          !invalidCommittedUserID &&
          !invalidCommittedDiscordID &&
          selectedDeleted &&
          !detailUnavailable ? (
            deletedDetail.isPending ? (
              <LoadingState />
            ) : deletedDetail.error ? (
              <ErrorState
                error={deletedDetail.error}
                onRetry={() => void deletedDetail.refetch()}
              />
            ) : (
              <DeletedAccountCard
                key={deletedDetail.data.record_id}
                account={deletedDetail.data}
                role={role}
                onClose={closeUser}
              />
            )
          ) : null}
          {selectedDeleted && detailUnavailable ? (
            <ErrorState error={deletedDetail.error ?? users.error} onRetry={closeUser} />
          ) : null}
        </div>
      </div>
    </div>
  );
}
import { LoanHistory } from './LoanDetails';
import { PenaltyHistory } from './PenaltyHistory';
