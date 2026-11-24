import { TableCheckbox } from './ui/TableCheckbox';
import { CreditNumber } from '@shared/components/CreditNumber';
import { FilterBar } from '@shared/components/ui/FilterBar';
import { Fields, FormActions } from '@shared/components/ui/Fields';
import { InferenceLimitFields, InferenceLimitSummary } from './userInferenceLimits';
import { decimalAmount, scaledAmount } from '@shared/inactivity/amounts';
import { DraftLeaveGuard } from './charity/DraftLeaveScope';
import { UserLimitBatch } from './UserLimitBatch';
import { UserResources } from './UserResources';
import { StewardPermissionEditor } from './StewardPermissionEditor';
import { StewardPermissionNotice } from './StewardPermissionNotice';
import { UserDonationHistory } from './UserDonationHistory';
import {
  defaultStewardPermissions,
  type StewardPermissions,
  type StewardPermission,
} from '@shared/operations/stewardPermissions';
import {
  INFERENCE_LIMITS,
  canEditInferenceLimits,
  inferenceLimitDraft,
  inferenceCeiling,
  parseInferenceLimit,
  type InferenceLimitDraft,
} from '@shared/operations/userInferenceLimits';
import { Button } from '@shared/components/ui/Button';
import { Icon } from '@shared/components/Icon';
import { useEffect, useId, useState, type ReactNode } from 'react';
import { Link } from 'react-router';
import { routePath } from '@shared/routing/routeRegistry';
import {
  Affix,
  DataTable,
  Field,
  Fold,
  Note,
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
  SectionHead,
  StatusBadge,
} from '@shared/components/States';
import { PagePagination } from '@shared/operations/PagePagination';
import { isPageNumber } from '@shared/operations/pageNumbers';
import { useUrlPagePager } from '@shared/operations/useUrlPagePager';
import { isManagementForbidden, isNotFoundError, isUnauthorized } from '@shared/query/http';
import { useDateTimeFormatter } from '@shared/utils/datetime';
import { accountCopy, accountHistoryCopy } from './accountCopy';
import { formatCredits } from '../../user/games/common/strict';
import { DeletedAccountCard } from './DeletedAccountCard';
import {
  banManagedUser,
  getManagedCallerKey,
  revokeManagedCallerKey,
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
  inferenceLimits: InferenceLimitDraft;
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
  inferenceLimits: inferenceLimitDraft(user),
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

function projectedBalance(current: string, amount: string, direction: 'increase' | 'decrease') {
  const base = scaledAmount(current.replace(/^-/, ''), 3);
  const change = scaledAmount(amount, 3);
  if (base === null || change === null) return '—';
  const after =
    BigInt(base) * (current.startsWith('-') ? -1n : 1n) +
    BigInt(change) * (direction === 'increase' ? 1n : -1n);
  return (after < 0n ? '-' : '') + decimalAmount((after < 0n ? -after : after).toString(), 3);
}

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

function ManagedCallerKeyControl({
  mayRevoke,
  user,
  role,
  account,
  onAuthorityLoss,
}: {
  mayRevoke: boolean;
  user: AdminUser;
  role: ManagementRole;
  account: string;
  onAuthorityLoss?: () => void;
}) {
  const { t } = useTranslation();
  const formatDateTime = useDateTimeFormatter();
  const client = useQueryClient();
  const [confirmGeneration, setConfirmGeneration] = useState<string | null>(null);
  const queryKey = managedUserKeys.callerKey(role, account, user.id);
  const metadata = useQuery({
    queryKey,
    queryFn: ({ signal }) => getManagedCallerKey(role, user.id, signal),
    retry: false,
  });
  const operation = useRetainedOperation(
    (input: { generation: string }, key, context) =>
      revokeManagedCallerKey(role, user.id, input.generation, key, context.signal),
    async (_input, error, context) => {
      if (error !== null) return;
      const current = await getManagedCallerKey(role, user.id, context.signal);
      context.commit(() => client.setQueryData(queryKey, current));
    },
    managementRoot(role),
  );
  const authorityError = metadata.error ?? operation.error ?? operation.refreshError;
  useEffect(() => {
    if (isUnauthorized(authorityError) || isManagementForbidden(authorityError))
      onAuthorityLoss?.();
  }, [authorityError, onAuthorityLoss]);
  const reload = async () => {
    const result = await metadata.refetch();
    if (!result.error && operation.outcome === 'conflict') operation.reset();
  };
  const check = () => {
    if (operation.outcome === 'unknown' && operation.variables) {
      operation.mutate(operation.variables);
    } else if (operation.isSuccess) {
      void operation.refresh();
    } else {
      void reload();
    }
  };
  const locked =
    operation.isPending ||
    metadata.isFetching ||
    operation.outcome === 'unknown' ||
    operation.outcome === 'conflict' ||
    operation.outcome === 'refresh-failed';

  return (
    <Card className="nb-stack">
      <h2>{t('management.users.callerKeyTitle')}</h2>
      {metadata.isPending ? <LoadingState /> : null}
      {metadata.error ? <ErrorState error={metadata.error} onRetry={() => void reload()} /> : null}
      {metadata.data && !metadata.error && operation.outcome !== 'refresh-failed' ? (
        <>
          <p>
            {t(
              metadata.data.active
                ? 'management.users.callerKeyActive'
                : 'management.users.callerKeyEmpty',
            )}
          </p>
          <dl className="nb-facts nb-facts--inline">
            {metadata.data.created_at !== null ? (
              <>
                <dt>{t('management.users.callerKeyCreated')}</dt>
                <dd>{formatDateTime(metadata.data.created_at)}</dd>
              </>
            ) : null}
            <dt>{t('management.users.callerKeyUpdated')}</dt>
            <dd>{formatDateTime(metadata.data.updated_at)}</dd>
          </dl>
          {metadata.data.active && mayRevoke ? (
            <div className="nb-actions">
              <Button
                type="button"
                variant="danger-outline"
                disabled={locked}
                onClick={() => setConfirmGeneration(metadata.data.generation)}
              >
                {t('management.users.callerKeyRevoke')}
              </Button>
            </div>
          ) : null}
        </>
      ) : null}
      {operation.isSuccess && operation.data ? (
        <p role="status">
          {t(
            operation.data.revoked
              ? 'management.users.callerKeyRevoked'
              : 'management.users.callerKeyNoChange',
          )}
        </p>
      ) : null}
      <OutcomeNote
        outcome={profileOutcome(operation.outcome, check)}
        busy={operation.isPending || metadata.isFetching}
        body={
          operation.outcome === 'conflict'
            ? t('management.users.callerKeyConflict')
            : operation.outcome === 'unknown'
              ? t('management.users.callerKeyUnknown')
              : undefined
        }
      />
      {operation.outcome === 'failed' && operation.error ? (
        <ErrorState error={operation.error} />
      ) : null}
      <ConfirmDialog
        open={confirmGeneration !== null}
        danger
        title={t('management.users.callerKeyConfirmTitle')}
        description={t('management.users.callerKeyConfirmBody', { user: user.username })}
        confirmLabel={t('management.users.callerKeyRevoke')}
        onCancel={() => setConfirmGeneration(null)}
        onConfirm={() => {
          if (!mayRevoke || confirmGeneration === null || locked) return;
          const generation = confirmGeneration;
          setConfirmGeneration(null);
          operation.mutate({ generation });
        }}
        confirmDisabled={locked}
      />
    </Card>
  );
}

function UserAuthority({
  user,
  refresh,
  onClose,
  role,
  account,
  renderDeletion,
  renderSecurity,
  onAuthorityLoss,
  trainee = false,
  actorLevel = trainee ? 5 : 6,
  permissions,
}: {
  user: AdminUser;
  refresh: () => Promise<unknown>;
  onClose: () => void;
  role: ManagementRole;
  trainee?: boolean;
  actorLevel?: 5 | 6;
  permissions?: StewardPermissions;
  account: string;
  onAuthorityLoss?: () => void;
  renderDeletion?: (user: AdminUser, refresh: () => Promise<unknown>) => ReactNode;
  renderSecurity?: (user: AdminUser) => ReactNode;
}) {
  const formatDateTime = useDateTimeFormatter();
  const { t } = useTranslation();
  const [draft, setDraft] = useState<UserDraft>(() => draftFor(user));
  const [previousUser, setPreviousUser] = useState(user);
  if (previousUser.revision !== user.revision) {
    const prior = draftFor(previousUser),
      latest = draftFor(user);
    const next = { ...draft, inferenceLimits: { ...draft.inferenceLimits } };
    for (const key of ['endpointLimit', 'lang', 'level', 'discordGatePolicy'] as const)
      if (draft[key] === prior[key]) Object.assign(next, { [key]: latest[key] });
    for (const { key } of INFERENCE_LIMITS)
      if (draft.inferenceLimits[key] === prior.inferenceLimits[key])
        next.inferenceLimits[key] = latest.inferenceLimits[key];
    setDraft(next);
    setPreviousUser(user);
  }
  const [permissionDirty, setPermissionDirty] = useState(false);
  const tabID = useId();
  const [tab, setTab] = useState<
    'overview' | 'limits' | 'economy' | 'resources' | 'penalties' | 'donations' | 'permissions'
  >('overview');
  const [penaltiesVisited, setPenaltiesVisited] = useState(false);
  const [coordinating, setCoordinating] = useState(false);
  const [confirmedUser, setConfirmedUser] = useState<AdminUser | null>(null);
  const [confirm, setConfirm] = useState<'ban' | 'unban' | null>(null);
  const [economyConfirm, setEconomyConfirm] = useState<EconomyIntent | null>(null);
  const editable =
    role === 'admin' ||
    (!user.is_admin && user.id !== account && user.level.effective < actorLevel);
  const policy = permissions ?? defaultStewardPermissions(actorLevel);
  const may = (key: StewardPermission) =>
    role === 'admin' || actorLevel === 6 || policy.permissions[key];
  const limits = role === 'steward' && actorLevel === 5 ? policy.limits : undefined;
  const endpointMax = Number(limits?.['users.endpoint_limit_max'] ?? 10000);
  const profileEditable = editable && may('users.profile');
  const levelEditable = editable && may('users.level');
  const limitsEditable = editable && may('users.limits');
  const economyTargets = (['balance', 'game_balance', 'donation_credit'] as const).filter(
    (target) =>
      editable && (target === 'donation_credit' ? role === 'admin' : may(`users.${target}`)),
  );
  const target = economyTargets.includes(draft.economyTarget)
    ? draft.economyTarget
    : economyTargets[0];
  const amount = scaledAmount(draft.economyAmount, 3);
  const amountMax =
    limits && target && target !== 'donation_credit'
      ? BigInt(limits[`users.${target}_adjustment_max_milli`])
      : null;
  const amountValid =
    amount !== null && BigInt(amount) > 0n && (amountMax === null || BigInt(amount) <= amountMax);
  const maxBanSeconds = Number(limits?.['users.ban_duration_max_seconds'] ?? 316224000);
  const callerKeyEditable =
    !user.is_admin &&
    (role === 'admin' || (user.id !== account && user.level.effective < actorLevel));
  const canBan = editable && may('users.ban');
  const canUnban = editable && may('users.unban');
  const banActionAllowed = user.is_banned ? canUnban : canBan;
  const canManagePermissions = user.level.effective === 5 && (role === 'admin' || actorLevel === 6);
  const canReadDonations =
    role === 'admin' ||
    actorLevel === 6 ||
    (permissions ?? defaultStewardPermissions(actorLevel)).permissions['users.donations.read'];
  const root = managementRoot(role);
  const reconcile = async () => {
    await refresh();
  };
  const patch = useRetainedOperation(
    (input: EconomyIntent, key) =>
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
  const endpoint = nullableLimit(draft.endpointLimit, endpointMax < 10000 ? 1 : 0, endpointMax);
  const inferenceLimits = Object.fromEntries(
    INFERENCE_LIMITS.map(({ key, max }) => [
      key,
      parseInferenceLimit(draft.inferenceLimits[key], inferenceCeiling(key, max, limits)),
    ]),
  );
  const invalidLimits =
    (profileEditable &&
      draft.endpointLimit !== (user.endpoint_limit ?? '') &&
      endpoint === undefined) ||
    (limitsEditable &&
      INFERENCE_LIMITS.some(
        ({ key }) =>
          draft.inferenceLimits[key] !== (user[key] ?? '') && inferenceLimits[key] === undefined,
      ));

  const baseline =
    confirmedUser && BigInt(confirmedUser.revision) > BigInt(user.revision) ? confirmedUser : user;
  const profileChanged =
    (limitsEditable &&
      INFERENCE_LIMITS.some(({ key }) => draft.inferenceLimits[key] !== (baseline[key] ?? ''))) ||
    (profileEditable &&
      (draft.endpointLimit !== (baseline.endpoint_limit ?? '') ||
        draft.lang !== baseline.lang ||
        (role === 'admin' && draft.discordGatePolicy !== baseline.discord_gate_policy)));
  const levelChanged =
    levelEditable && (draft.level ? Number(draft.level) : null) !== baseline.level.manual;

  const profileBusy = coordinating || profile.isPending || manualLevel.isPending;
  const saveLimits = async () => {
    if ((!editable && !limitsEditable) || invalidLimits || profileBusy) return;
    const profileBody = {
      mode: 'profile',
      ...(limitsEditable
        ? Object.fromEntries(
            Object.entries(inferenceLimits).filter(
              ([key, value]) =>
                value !== undefined && value !== baseline[key as keyof typeof baseline],
            ),
          )
        : {}),
      ...(profileEditable && endpoint !== undefined && endpoint !== baseline.endpoint_limit
        ? { endpoint_limit: endpoint }
        : {}),
      ...(profileEditable && draft.lang && draft.lang !== baseline.lang
        ? { lang: draft.lang }
        : {}),
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
    if (isUnauthorized(mutationError) || isManagementForbidden(mutationError)) onAuthorityLoss?.();
  }, [mutationError, onAuthorityLoss]);
  const economyUnknown = patch.outcome === 'unknown';
  const economyLocked = patch.isPending || economyUnknown;
  const checkEconomy = () => void (patch.isSuccess ? patch.refresh() : patch.check());
  const economyFeedback =
    patch.outcome !== 'idle' && patch.outcome !== 'pending' ? (
      <div className="user-save-result">
        {patch.outcome === 'conflict' ? (
          <Note
            tone="warn"
            action={
              <Button type="button" size="sm" onClick={checkEconomy}>
                {t('common.outcome.reload')}
              </Button>
            }
          >
            {t('management.users.economyConflict')}
          </Note>
        ) : (
          <OutcomeNote
            outcome={profileOutcome(patch.outcome, checkEconomy)}
            body={economyUnknown ? t('management.users.economyUnknown') : undefined}
          />
        )}
        {patch.error && patch.outcome !== 'conflict' ? <ErrorState error={patch.error} /> : null}
        {patch.refreshError ? (
          <ErrorState error={patch.refreshError} onRetry={checkEconomy} />
        ) : null}
      </div>
    ) : null;

  const banControls = banActionAllowed ? (
    <Card>
      <h2>{t('management.users.banSectionTitle')}</h2>
      {!user.is_banned && (
        <Fields>
          <Field label={t('management.users.banReason')} size="long">
            {(field) => (
              <input
                {...field}
                maxLength={1024}
                value={draft.banReason}
                onChange={(event) => setDraft({ ...draft, banReason: event.target.value })}
              />
            )}
          </Field>
          <Field label={t('management.users.banDurationDays')} size="short">
            {(field) => (
              <input
                {...field}
                type="number"
                min="1"
                max={Math.floor(maxBanSeconds / 86400)}
                value={draft.banDuration}
                onChange={(event) => setDraft({ ...draft, banDuration: event.target.value })}
              />
            )}
          </Field>
        </Fields>
      )}
      <FormActions>
        {user.is_banned ? (
          <Button variant="danger-outline" type="button" onClick={() => setConfirm('unban')}>
            {t('management.users.unban')}
          </Button>
        ) : (
          <Button
            variant="danger-outline"
            type="button"
            disabled={
              !draft.banReason.trim() ||
              (banSeconds === undefined
                ? !may('users.ban_permanent')
                : !Number.isSafeInteger(banSeconds) || banSeconds < 1 || banSeconds > maxBanSeconds)
            }
            onClick={() => setConfirm('ban')}
          >
            {t('management.users.ban')}
          </Button>
        )}
      </FormActions>
      {ban.error || unban.error ? <ErrorState error={ban.error ?? unban.error} /> : null}
    </Card>
  ) : null;

  return (
    <div className="ops-stack user-authority">
      {permissionDirty && (
        <DraftLeaveGuard
          dirty
          allowNavigation={({ currentLocation, nextLocation }) =>
            currentLocation.pathname === nextLocation.pathname &&
            new URLSearchParams(nextLocation.search).get('user') === user.id
          }
        />
      )}
      <Card>
        <div className="user-authority__head">
          <Button variant="ghost" type="button" className="user-authority__back" onClick={onClose}>
            <Icon name="arrow-left" />
            {t('management.users.backToList')}
          </Button>
          <h2>{user.username}</h2>
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
          { value: 'resources', label: t('management.resources.title') },
          { value: 'penalties', label: t('management.users.tabPenalties') },
          { value: 'donations', label: t('management.permissions.history') },
          ...(canManagePermissions
            ? [{ value: 'permissions', label: t('management.permissions.title') }]
            : []),
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
        className="nb-stack"
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
              <CreditNumber value={formatCredits(user.balance)} />{' '}
              {t('management.users.creditsBalance')} ·{' '}
              <CreditNumber value={formatCredits(user.game_balance)} />{' '}
              {t('management.users.gameBalance')} ·{' '}
              <CreditNumber value={formatCredits(user.donation_credit)} />{' '}
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
            {may('logs.read') && (
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
            )}
            {may('risk.read') && (
              <Link
                className="nb-btn nb-btn--secondary"
                to={
                  role === 'admin'
                    ? routePath('admin', 'admin-risk-audit') +
                      `?audit_tab=user_ips&audit_ip_user=${user.id}`
                    : `/steward?tab=risk&audit_tab=user_ips&audit_ip_user=${user.id}`
                }
              >
                {t('common.audit.inspectUserIPs')}
              </Link>
            )}
            {may('users.loans.read') && (
              <LoanHistory role={role} account={account} userID={user.id} />
            )}
          </div>
        </Card>
        {callerKeyEditable && may('users.caller_keys.read') ? (
          <ManagedCallerKeyControl
            mayRevoke={may('users.caller_keys.revoke')}
            key={JSON.stringify([role, account, trainee, user.id, user.level.effective])}
            user={user}
            role={role}
            account={account}
            onAuthorityLoss={onAuthorityLoss}
          />
        ) : null}
        {renderSecurity?.(user)}
      </section>
      {profileEditable || levelEditable || limitsEditable ? (
        <>
          <section
            role="tabpanel"
            id={`${tabID}-limits-panel`}
            aria-labelledby={`${tabID}-limits`}
            hidden={tab !== 'limits'}
          >
            <Card>
              <h2>
                {t('management.users.manageLimitsTitle')}
                {levelEditable ? ' / ' + t('management.users.levelSectionTitle') : ''}
              </h2>
              <p>{t('management.users.limitHint')}</p>
              <Fold plain title={t('common.operations.management.details')}>
                <p>{t('management.users.revisionGuard', { revision: user.revision })}</p>
              </Fold>
              {limitsEditable ? (
                <InferenceLimitFields
                  user={baseline}
                  limits={limits}
                  value={draft.inferenceLimits}
                  onChange={(inferenceLimits) => setDraft({ ...draft, inferenceLimits })}
                />
              ) : (
                <InferenceLimitSummary user={user} />
              )}
              <Fold plain title={t('management.users.inferenceLimitRules')}>
                <p>{t('management.users.inferenceLimitHelp')}</p>
              </Fold>
              {profileEditable || levelEditable ? (
                <Fields>
                  <Field
                    label={t('management.users.endpointLimit')}
                    size="short"
                    help={
                      profileEditable && limits
                        ? t('common.governance.ceiling', { max: endpointMax })
                        : undefined
                    }
                  >
                    {(field) => (
                      <input
                        {...field}
                        inputMode="numeric"
                        disabled={!profileEditable}
                        value={draft.endpointLimit}
                        onChange={(event) =>
                          setDraft({ ...draft, endpointLimit: event.target.value })
                        }
                      />
                    )}
                  </Field>
                  <Field label={t('management.users.levelControl')} size="short">
                    {(field) => (
                      <select
                        {...field}
                        disabled={!levelEditable}
                        value={draft.level}
                        onChange={(event) => setDraft({ ...draft, level: event.target.value })}
                      >
                        <option value="">{t('management.users.levelManualNone')}</option>
                        {(role === 'admin'
                          ? [1, 2, 3, 4, 5, 6]
                          : [1, 2, 3, 4, 5].filter((level) => level < actorLevel)
                        ).map((level) => (
                          <option key={level} value={level}>
                            {level}
                          </option>
                        ))}
                      </select>
                    )}
                  </Field>
                  <Field label={t('management.users.language')} size="short">
                    {(field) => (
                      <select
                        {...field}
                        disabled={!profileEditable}
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
                    )}
                  </Field>
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
                              discordGatePolicy: event.target
                                .value as UserDraft['discordGatePolicy'],
                            })
                          }
                        >
                          <option value="inherit">
                            {t('management.users.discordGateInherit')}
                          </option>
                          <option value="require">
                            {t('management.users.discordGateRequire')}
                          </option>
                          <option value="exempt">{t('management.users.discordGateExempt')}</option>
                        </select>
                      )}
                    </Field>
                  ) : null}
                </Fields>
              ) : null}
              {invalidLimits ? (
                <p className="field-error" role="alert">
                  {t('management.users.limitInvalid')}
                </p>
              ) : null}
              <FormActions>
                <Button
                  variant="primary"
                  type="button"
                  disabled={profileBusy || invalidLimits || (!profileChanged && !levelChanged)}
                  onClick={() => void saveLimits()}
                >
                  {t('management.users.saveProfile')}
                </Button>
              </FormActions>
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
              <dl className="nb-facts nb-facts--inline">
                <div>
                  <dt>{t('management.users.endpointLimit')}</dt>
                  <dd className="nb-amount">{user.effective_endpoint_limit}</dd>
                </div>
                <div>
                  <dt>{t('management.users.language')}</dt>
                  <dd>{user.lang || t('management.users.languageDefault')}</dd>
                </div>
              </dl>
              <InferenceLimitSummary user={user} />
            </Card>
          </section>
        </>
      )}
      {editable ? (
        <>
          <section
            role="tabpanel"
            id={`${tabID}-economy-panel`}
            aria-labelledby={`${tabID}-economy`}
            hidden={tab !== 'economy'}
          >
            <Card>
              <h2>{t('management.users.economyTitle')}</h2>
              {economyTargets.length === 0 ? (
                <StewardPermissionNotice />
              ) : (
                <>
                  <p>{t('management.users.economyPositiveHint')}</p>
                  <Fields columns={2}>
                    <Fields columns={1} legend={t('management.users.economyTarget')}>
                      <Segmented
                        label={t('management.users.economyTarget')}
                        value={target ?? 'balance'}
                        disabled={economyLocked}
                        options={economyTargets.map((value) => ({
                          value,
                          label: t(economyTargetLabels[value]),
                        }))}
                        onChange={(economyTarget) => setDraft({ ...draft, economyTarget })}
                      />
                    </Fields>
                    <Fields columns={1} legend={t('management.users.economyDirection')}>
                      <Segmented
                        label={t('management.users.economyDirection')}
                        value={draft.economyDirection}
                        disabled={economyLocked}
                        options={[
                          { value: 'increase', label: t('management.users.economyIncrease') },
                          { value: 'decrease', label: t('management.users.economyDecrease') },
                        ]}
                        onChange={(economyDirection) => setDraft({ ...draft, economyDirection })}
                      />
                    </Fields>
                  </Fields>
                  <Fields columns={1}>
                    <Field
                      label={t('management.users.economyPositiveAmount')}
                      size="medium"
                      help={
                        amountMax !== null
                          ? t('common.governance.ceiling', {
                              max: decimalAmount(amountMax.toString(), 3),
                            })
                          : undefined
                      }
                    >
                      {(field) => (
                        <Affix
                          {...field}
                          aria-label={t('management.users.economyPositiveAmount')}
                          unit={t(economyTargetLabels[target ?? 'balance'])}
                          inputMode="decimal"
                          value={draft.economyAmount}
                          disabled={economyLocked}
                          placeholder="1.5"
                          onChange={(event) =>
                            setDraft({ ...draft, economyAmount: event.target.value })
                          }
                        />
                      )}
                    </Field>
                    <Field
                      label={t('management.users.economyReason')}
                      size="long"
                      help={t('management.users.reasonRequired')}
                    >
                      {(field) => (
                        <input
                          {...field}
                          aria-label={t('management.users.economyReason')}
                          required
                          maxLength={1024}
                          value={draft.economyReason}
                          disabled={economyLocked}
                          onChange={(event) =>
                            setDraft({ ...draft, economyReason: event.target.value })
                          }
                        />
                      )}
                    </Field>
                  </Fields>
                  <FormActions>
                    <Button
                      variant="primary"
                      type="button"
                      disabled={
                        patch.isPending || !amountValid || !target || !draft.economyReason.trim()
                      }
                      onClick={() =>
                        setEconomyConfirm(
                          economyUnknown && patch.variables
                            ? patch.variables
                            : {
                                revision: user.revision,
                                body: {
                                  mode: 'economy',
                                  target: target ?? 'balance',
                                  direction: draft.economyDirection,
                                  amount: draft.economyAmount,
                                  reason: draft.economyReason.trim(),
                                },
                              },
                        )
                      }
                    >
                      {t(
                        economyUnknown
                          ? 'management.users.economyRetry'
                          : 'management.users.economySubmit',
                      )}
                    </Button>
                  </FormActions>
                  {!economyConfirm ? economyFeedback : null}
                </>
              )}
            </Card>
          </section>
          <section
            role="tabpanel"
            id={`${tabID}-penalties-panel`}
            aria-labelledby={`${tabID}-penalties`}
            hidden={tab !== 'penalties'}
          >
            {penaltiesVisited && may('users.penalties.read') ? (
              <Card>
                <PenaltyHistory inline role={role} account={account} userID={user.id} />
              </Card>
            ) : null}
            {banControls}
          </section>
          {(tab === 'overview' || tab === 'penalties') && (banActionAllowed || renderDeletion) ? (
            <Card className="user-danger">
              <h2>{t('management.users.dangerTitle')}</h2>
              {banActionAllowed && (
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
                    {t(
                      user.is_banned
                        ? 'management.users.unbanAction'
                        : 'management.users.banAction',
                    )}
                  </Button>
                </div>
              )}
              {renderDeletion ? (
                <div className="user-danger-row">
                  <p>{t('management.users.deleteConsequence')}</p>
                  {renderDeletion(user, refresh)}
                </div>
              ) : null}
            </Card>
          ) : null}
        </>
      ) : (
        <>
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
            {penaltiesVisited && may('users.penalties.read') ? (
              <Card>
                <PenaltyHistory inline role={role} account={account} userID={user.id} />
              </Card>
            ) : null}
            {banControls}
          </section>
        </>
      )}
      {tab === 'donations' ? (
        <section
          role="tabpanel"
          id={`${tabID}-donations-panel`}
          aria-labelledby={`${tabID}-donations`}
        >
          {canReadDonations ? (
            <UserDonationHistory
              role={role}
              accountId={account}
              userId={user.id}
              enabled
              onCapabilityLoss={onAuthorityLoss}
            />
          ) : (
            <StewardPermissionNotice permission="users.donations.read" />
          )}
        </section>
      ) : null}
      {tab === 'resources' ? (
        <section
          role="tabpanel"
          id={`${tabID}-resources-panel`}
          aria-labelledby={`${tabID}-resources`}
        >
          <UserResources
            role={role}
            account={account}
            userId={user.id}
            editable={
              editable &&
              (role === 'admin' ||
                actorLevel === 6 ||
                (permissions ?? defaultStewardPermissions(actorLevel)).permissions[
                  'users.resources'
                ])
            }
          />
        </section>
      ) : null}
      {(tab === 'permissions' || permissionDirty) && canManagePermissions ? (
        <section
          role="tabpanel"
          hidden={tab !== 'permissions'}
          id={`${tabID}-permissions-panel`}
          aria-labelledby={`${tabID}-permissions`}
        >
          <StewardPermissionEditor
            role={role}
            account={account}
            userId={user.id}
            onDirtyChange={setPermissionDirty}
          />
        </section>
      ) : null}
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
            if (confirm === 'ban' && canBan)
              ban.mutate(
                {
                  revision: user.revision,
                  reason: draft.banReason.trim(),
                  duration_seconds: banSeconds ?? null,
                },
                { onSuccess: () => setConfirm(null) },
              );
            else if (confirm === 'unban' && canUnban)
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
          confirmLabel={t(
            economyUnknown ? 'management.users.economyRetry' : 'management.users.economySubmit',
          )}
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
          <dl className="nb-facts nb-facts--inline">
            <div>
              <dt>{t('management.users.economyTarget')}</dt>
              <dd>{t(economyTargetLabels[economyConfirm.body.target])}</dd>
            </div>
            <div>
              <dt>{t('management.users.economyDirection')}</dt>
              <dd>
                {t(
                  economyConfirm.body.direction === 'increase'
                    ? 'management.users.economyIncrease'
                    : 'management.users.economyDecrease',
                )}
              </dd>
            </div>
            <div>
              <dt>{t('management.users.economyPositiveAmount')}</dt>
              <dd className="nb-amount">{economyConfirm.body.amount}</dd>
            </div>
            <div>
              <dt>{t('management.users.balances')}</dt>
              <dd>
                {user[economyConfirm.body.target]} →{' '}
                {projectedBalance(
                  user[economyConfirm.body.target],
                  economyConfirm.body.amount,
                  economyConfirm.body.direction,
                )}
              </dd>
            </div>
            <div>
              <dt>{t('management.users.economyReason')}</dt>
              <dd>{economyConfirm.body.reason}</dd>
            </div>
          </dl>
          {economyFeedback}
        </ConfirmDialog>
      ) : null}
    </div>
  );
}

interface UserManagementProps {
  trainee?: boolean;
  actorLevel?: 5 | 6;
  permissions?: StewardPermissions;
  account: string;
  scopeReady: boolean;
  sessionError: unknown;
  role: ManagementRole;
  onAuthorityLoss?: () => void;
  renderDeletion?: (user: AdminUser, refresh: () => Promise<unknown>) => ReactNode;
  renderSecurity?: (user: AdminUser) => ReactNode;
  /** Rendered inside a page that already owns the h1. */
  embedded?: boolean;
}

export function UserManagement({
  account,
  scopeReady,
  sessionError,
  role,
  onAuthorityLoss,
  renderDeletion,
  renderSecurity,
  trainee = false,
  actorLevel = trainee ? 5 : 6,
  permissions,
  embedded = false,
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
      previousQuery.queryKey[10] === accountState
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
  const batchScope = JSON.stringify([
    account,
    role,
    query,
    banned,
    level,
    userID,
    discordID,
    accountState,
    pager.page,
    pager.pageSize,
  ]);
  const [limitSelection, setLimitSelection] = useState<{ scope: string; ids: string[] }>({
    scope: '',
    ids: [],
  });
  const [batchBusy, setBatchBusy] = useState(false);
  useEffect(() => {
    // Returning to a previous page starts a fresh explicit selection.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setLimitSelection({ scope: batchScope, ids: [] });
  }, [batchScope]);

  const batchPolicy =
    role === 'steward' && actorLevel === 5
      ? (permissions ?? defaultStewardPermissions(actorLevel))
      : undefined;
  const mayBatch = !batchPolicy || batchPolicy.permissions['users.limits'];
  const eligibleUsers = (users.data?.data ?? []).flatMap((entry) =>
    entry.account_state === 'active' &&
    mayBatch &&
    canEditInferenceLimits(entry.user, role, account, actorLevel === 5)
      ? [entry.user]
      : [],
  );
  const limitSelected =
    limitSelection.scope === batchScope
      ? limitSelection.ids.filter((id) => eligibleUsers.some((user) => user.id === id))
      : [];
  const setLimitSelected = (ids: string[]) => setLimitSelection({ scope: batchScope, ids });
  const detailUnavailable =
    isUnauthorized(users.error) ||
    isManagementForbidden(users.error) ||
    isUnauthorized(detail.error) ||
    isManagementForbidden(detail.error) ||
    isNotFoundError(detail.error) ||
    isUnauthorized(deletedDetail.error) ||
    isManagementForbidden(deletedDetail.error) ||
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
      isManagementForbidden(users.error) ||
      isUnauthorized(detail.error) ||
      isManagementForbidden(detail.error) ||
      isUnauthorized(deletedDetail.error) ||
      isManagementForbidden(deletedDetail.error)
    ) {
      if (onAuthorityLoss) onAuthorityLoss();
      else if ([users.error, detail.error, deletedDetail.error].some(isUnauthorized))
        clearStationSession(client, role);
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
    if (batchBusy) return;
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
  const authorityError = [users.error, detail.error, deletedDetail.error].find(
    (error) => isUnauthorized(error) || isManagementForbidden(error),
  );
  if (authorityError) return <ErrorState error={authorityError} />;
  return (
    <div className="page ops-page user-management">
      {embedded ? (
        <SectionHead
          title={t('management.users.title')}
          description={t('management.users.description')}
        />
      ) : (
        <PageHeader
          title={t('management.users.title')}
          description={t('management.users.description')}
        />
      )}
      <div className={`nb-md${selection ? ' has-selection' : ''}`}>
        <div className="nb-md__list ops-stack" ref={listRef} tabIndex={-1}>
          <Card>
            <FilterBar
              query={users}
              activeCount={
                [userID, discordID, banned, level, accountState !== 'all'].filter(Boolean).length
              }
              onReset={() => {
                if (batchBusy) return;
                setQueryDraft('');
                setUserIDDraft('');
                setDiscordIDDraft('');
                commitListState('', '', '', '', 'all', '');
              }}
              onSubmit={() => {
                commitListState(
                  queryDraft.trim(),
                  banned,
                  level,
                  userIDDraft,
                  accountState,
                  discordIDDraft,
                );
              }}
              search={
                <Field label={t('common.search')} size="medium">
                  {(field) => (
                    <input
                      {...field}
                      aria-label={t('management.users.searchAria')}
                      placeholder={t('management.users.searchPlaceholder')}
                      value={queryDraft}
                      onChange={(event) => setQueryDraft(event.target.value)}
                    />
                  )}
                </Field>
              }
              secondary={
                <>
                  <Field label={t('management.users.userId')} size="short">
                    {(field) => (
                      <input
                        {...field}
                        aria-label={t('management.users.userId')}
                        aria-invalid={invalidUserIDDraft}
                        placeholder="#ID"
                        inputMode="numeric"
                        maxLength={64}
                        value={userIDDraft}
                        onChange={(event) => setUserIDDraft(event.target.value)}
                      />
                    )}
                  </Field>
                  <Field label={t('management.users.discordId')} size="short">
                    {(field) => (
                      <input
                        {...field}
                        aria-label={t('management.users.discordId')}
                        aria-invalid={invalidDiscordIDDraft}
                        placeholder="Discord ID"
                        inputMode="numeric"
                        maxLength={20}
                        value={discordIDDraft}
                        onChange={(event) => setDiscordIDDraft(event.target.value)}
                      />
                    )}
                  </Field>
                  <Field label={t('common.account.stateFilter')} size="short">
                    {(field) => (
                      <select
                        {...field}
                        disabled={batchBusy}
                        value={accountState}
                        onChange={(event) =>
                          commitListState(
                            query,
                            banned,
                            level,
                            userID,
                            event.target.value as AccountState,
                          )
                        }
                      >
                        <option value="all">{accountLabels.allAccounts}</option>
                        <option value="active">{accountLabels.activeAccounts}</option>
                        <option value="deleted">{accountLabels.deletedAccounts}</option>
                      </select>
                    )}
                  </Field>
                  <Field label={t('management.users.filterStatus')} size="short">
                    {(field) => (
                      <select
                        {...field}
                        disabled={batchBusy}
                        value={banned}
                        onChange={(event) =>
                          commitListState(query, event.target.value as typeof banned, level, userID)
                        }
                      >
                        <option value="">{t('common.all')}</option>
                        <option value="false">{t('management.users.active')}</option>
                        <option value="true">{t('management.users.banned')}</option>
                      </select>
                    )}
                  </Field>
                  <Field label={t('management.users.level')} size="short">
                    {(field) => (
                      <select
                        {...field}
                        disabled={batchBusy}
                        value={level}
                        onChange={(event) =>
                          commitListState(query, banned, event.target.value, userID)
                        }
                      >
                        <option value="">{t('common.all')}</option>
                        {[1, 2, 3, 4, 5, 6].map((value) => (
                          <option key={value} value={value}>
                            {value}
                          </option>
                        ))}
                      </select>
                    )}
                  </Field>
                </>
              }
            />
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
          </Card>
          <Card>
            {embedded ? <h2>{t('management.users.listTitle')}</h2> : null}
            {accountState === 'deleted' ? <p>{accountHistoryCopy(t).coverage}</p> : null}
            {scopeReady &&
            !sessionError &&
            !users.error &&
            users.data &&
            eligibleUsers.length > 0 ? (
              <UserLimitBatch
                key={batchScope}
                role={role}
                users={eligibleUsers}
                limits={batchPolicy?.limits}
                selected={limitSelected}
                onSelected={setLimitSelected}
                onBusy={setBatchBusy}
                loading={users.isFetching}
                refresh={async () => {
                  const result = await users.refetch();
                  if (result.error) throw result.error;
                  if (selected) {
                    const current = await detail.refetch();
                    if (current.error) throw current.error;
                  }
                }}
              />
            ) : null}
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
                      key: 'limit_selection',
                      header: (
                        <span className="user-list-selection-heading">
                          <span className="nb-sr">{t('management.users.limitSelection')}</span>
                          <TableCheckbox
                            aria-label={t('management.users.selectPageLimits')}
                            checked={
                              eligibleUsers.length > 0 &&
                              limitSelected.length === eligibleUsers.length
                            }
                            indeterminate={
                              limitSelected.length > 0 &&
                              limitSelected.length < eligibleUsers.length
                            }
                            disabled={batchBusy || users.isFetching || eligibleUsers.length === 0}
                            onChange={(event) =>
                              setLimitSelected(
                                event.target.checked ? eligibleUsers.map((user) => user.id) : [],
                              )
                            }
                          />
                        </span>
                      ),
                      cell: 'select',
                      render: (entry) => (
                        <span className="user-list-selection">
                          {entry.account_state === 'active' &&
                          mayBatch &&
                          canEditInferenceLimits(entry.user, role, account, actorLevel === 5) ? (
                            <TableCheckbox
                              aria-label={t('management.users.selectLimitUser', {
                                user: entry.user.username,
                              })}
                              checked={limitSelected.includes(entry.user.id)}
                              disabled={batchBusy || users.isFetching}
                              onChange={(event) =>
                                setLimitSelected(
                                  event.target.checked
                                    ? [...limitSelected, entry.user.id]
                                    : limitSelected.filter((id) => id !== entry.user.id),
                                )
                              }
                            />
                          ) : null}
                        </span>
                      ),
                    },

                    {
                      key: 'user',
                      header: (
                        <span className="user-list-identity-heading">
                          {t('management.users.username')}
                        </span>
                      ),
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
                              title={deleted ? accountLabels.deleted : entry.user.username}
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
                            <span
                              title={
                                '#' +
                                (id ?? accountLabels.unknown) +
                                ' · ' +
                                (discord ?? t('management.users.discordUnlinked'))
                              }
                            >
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
                      mobileLabel: t('management.users.level'),
                      header: (
                        <span className="user-list-level-heading">
                          {t('management.users.level')}
                        </span>
                      ),
                      cell: 'meta',
                      render: (entry) => (
                        <span className="user-list-level">
                          {entry.account_state === 'deleted'
                            ? entry.deleted.effective_level === null
                              ? accountLabels.unknown
                              : t('management.users.levelValue', {
                                  level: entry.deleted.effective_level,
                                })
                            : entry.user.level.display_name ||
                              t('management.users.levelValue', {
                                level: entry.user.level.effective,
                              })}
                        </span>
                      ),
                    },
                    {
                      key: 'status',
                      header: (
                        <span className="user-list-status-heading">
                          {t('management.users.status')}
                        </span>
                      ),
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
                      header: (
                        <span
                          className="user-list-balance-heading"
                          title={t('management.users.creditsBalance')}
                        >
                          {t('management.users.creditsBalance')}
                        </span>
                      ),
                      cell: 'meta',
                      align: 'num',
                      mobileLabel: t('management.users.creditsBalance'),
                      render: (entry) => {
                        const balance =
                          entry.account_state === 'deleted'
                            ? entry.deleted.general_balance
                            : entry.user.balance;
                        return (
                          <span className="user-list-balance">
                            {balance === null ? (
                              accountLabels.unknown
                            ) : (
                              <span className="nb-amount">
                                <CreditNumber value={formatCredits(balance)} />
                              </span>
                            )}
                          </span>
                        );
                      },
                    },
                  ]}
                />
              </div>
            )}
            {scopeReady && !sessionError && !users.error && users.data ? (
              <PagePagination
                metadata={users.data.pagination}
                requestedPage={pager.page}
                busy={users.isFetching || batchBusy}
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
          ) : !selected ||
            detailUnavailable ||
            detail.isPending ||
            (detail.error && !detail.data) ? (
            <Button variant="ghost" className="user-detail-back" type="button" onClick={closeUser}>
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
            ) : detail.error && !detail.data ? (
              <ErrorState error={detail.error} onRetry={() => void detail.refetch()} />
            ) : (
              <UserAuthority
                key={detail.data.id}
                user={detail.data}
                role={role}
                account={account}
                actorLevel={actorLevel}
                permissions={permissions}
                renderDeletion={renderDeletion}
                renderSecurity={renderSecurity}
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
