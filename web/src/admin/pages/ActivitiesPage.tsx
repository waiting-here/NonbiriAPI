import { displayAmount } from '../features/economyaudit/api';
import { AttendanceManagement } from '../features/attendance/AttendanceManagement';
import { isActivityLiterature } from '@shared/utils/activityLiterature';
import { useEffect, useRef, useState } from 'react';
import { Link } from 'react-router';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useSearchState } from '@shared/operations/useSearchState';
import { useTranslation } from 'react-i18next';
import { clearStationSession } from '@shared/charityManagement';
import { DraftLeaveGuard } from '@shared/components/charity/DraftLeaveScope';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { CreditNumber } from '@shared/components/CreditNumber';
import { EmptyState, ErrorState, LoadingState, PageHeader } from '@shared/components/States';
import { PagePagination } from '@shared/operations/PagePagination';
import { useUrlPagePager } from '@shared/operations/useUrlPagePager';
import { isForbidden, isUnauthorized } from '@shared/query/http';
import { amount } from '@shared/operations/wire';
import { loanMilli, validLoanConfig } from '@shared/operations/loans';
import {
  Affix,
  Button,
  DataTable,
  Field,
  Fields,
  FilterBar,
  FormActions,
  Note,
  Panel,
  PanelBody,
  PanelHead,
  SaveBar,
  StatusBadge,
  Toggle,
} from '@shared/components/ui';
import { formatCount } from '@shared/utils/formatNumber';
import { formatCredits } from '../../user/games/common/strict';
import { useLoanText } from '@shared/components/loanCopy';
import { percentBP } from '../features/games/config';
import { formatBeijingTime, nextThursdaySchedule } from '../features/operations/thursdaySchedule';
import {
  adjustPool,
  patchActivitiesConfig,
  putThursdayNext,
  resumeThursday,
  thursdayMutationRevision,
  type ActivitiesConfig,
  type Period,
  type Pool,
} from '../features/operations/economy';
import {
  adminPageKeys,
  getAdminActivitiesConfig,
  getAdminPoolsPage,
  getAdminThursday,
} from '../features/operations/adminPages';
import { useAdminSession } from '../data';
import { useRetainedOperation } from '../features/operations/useRetainedOperation';
import '@shared/operations/operations.css';

function LoanConfiguration({
  config,
  disabled,
  onChange,
}: {
  config: ActivitiesConfig;
  disabled: boolean;
  onChange: (value: ActivitiesConfig) => void;
}) {
  const text = useLoanText();
  const { t } = useTranslation();
  const valid = validLoanConfig(config);
  const principal = valid ? BigInt(config.loan_tiers[0]) : 0n;
  const credits = (milli: bigint) => {
    const fraction = (milli % 1000n).toString().padStart(3, '0').replace(/0+$/, '');
    return `${formatCount(milli / 1000n).display}${fraction ? `.${fraction}` : ''}`;
  };
  return (
    <Panel>
      <PanelHead
        title={text('赛博网贷', 'Cyber loan')}
        actions={
          <Toggle
            label={text('启用借款', 'Enable loans')}
            checked={config.loan_enabled}
            disabled={disabled}
            onChange={(loan_enabled) => onChange({ ...config, loan_enabled })}
          />
        }
      />
      <PanelBody>
        <fieldset className="nb-fieldset" disabled={disabled}>
          <Fields legend={text('借款额度', 'Loan amounts')}>
            {config.loan_tiers.map((value, index) => (
              <Field
                size="short"
                key={index}
                label={t('common.operations.management.loanTier', { tier: index + 1 })}
              >
                {(props) => (
                  <Affix
                    {...props}
                    unit={t('admin.activities.units.credits')}
                    inputMode="numeric"
                    maxLength={13}
                    value={value}
                    onChange={(event) => {
                      const loan_tiers: [string, string, string] = [...config.loan_tiers];
                      loan_tiers[index] = event.target.value;
                      onChange({ ...config, loan_tiers });
                    }}
                  />
                )}
              </Field>
            ))}
          </Fields>
          <Fields legend={text('到账与还款', 'Disbursement and repayment')}>
            <Field
              size="short"
              label={text('到账比例', 'Disbursement ratio')}
              help={text(
                '大于 0、小于 1，最多三位小数。',
                'Between 0 and 1, with up to three decimal places.',
              )}
            >
              {(props) => (
                <Affix
                  {...props}
                  unit={text('× 额度', '× amount')}
                  inputMode="decimal"
                  maxLength={17}
                  value={config.loan_a}
                  onChange={(event) => onChange({ ...config, loan_a: event.target.value })}
                />
              )}
            </Field>
            <Field
              size="short"
              label={text('还款比例', 'Repayment ratio')}
              help={text(
                '大于 1，最多三位小数。',
                'Greater than 1, with up to three decimal places.',
              )}
            >
              {(props) => (
                <Affix
                  {...props}
                  unit={text('× 额度', '× amount')}
                  inputMode="decimal"
                  maxLength={17}
                  value={config.loan_b}
                  onChange={(event) => onChange({ ...config, loan_b: event.target.value })}
                />
              )}
            </Field>
          </Fields>
        </fieldset>
        {valid ? (
          <Note>
            {text(
              `借 ${formatCount(principal).display} → 到账 ${credits(principal * loanMilli(config.loan_a))} 游戏积分，需还 ${credits(principal * loanMilli(config.loan_b))} 通用积分。`,
              `Borrow ${formatCount(principal).display} → receive ${credits(principal * loanMilli(config.loan_a))} game credits; repay ${credits(principal * loanMilli(config.loan_b))} general credits.`,
            )}
          </Note>
        ) : (
          <Note tone="bad">
            {text(
              '请检查三档递增额度和比例；各项金额不得超过 9,000,000,000,000 积分。',
              'Check the three increasing amounts and ratios. No resulting amount may exceed 9,000,000,000,000 credits.',
            )}
          </Note>
        )}
      </PanelBody>
    </Panel>
  );
}

function validPositiveAmount(value: string): boolean {
  try {
    return BigInt(amount(value, 'positive activity amount', false).replace('.', '')) > 0n;
  } catch {
    return false;
  }
}

interface PeriodDraft {
  literature: string;
  entry: string;
  per_user_limit: string;
  platform: string;
  welfare: string;
  next_pool: string;
}

type PeriodMutation = PeriodDraft & {
  period_key: string;
  opens_at: number;
  expected_revision: string;
};

const emptyPeriodDraft = (): PeriodDraft => ({
  literature: '',
  entry: '',
  per_user_limit: '1',
  platform: '0',
  welfare: '0',
  next_pool: '0',
});

const draftForPeriod = (period: Period | null): PeriodDraft =>
  period
    ? {
        literature: period.literature,
        entry: period.entry,
        per_user_limit: String(period.per_user_limit),
        platform: String(period.pumps_bp.platform / 100),
        welfare: String(period.pumps_bp.welfare / 100),
        next_pool: String(period.pumps_bp.next_pool / 100),
      }
    : emptyPeriodDraft();

interface ActivitiesPageContentProps {
  account: string;
  scopeReady: boolean;
  sessionError: unknown;
}

function ActivitiesPageContent({ account, scopeReady, sessionError }: ActivitiesPageContentProps) {
  const text = useLoanText();
  const { t } = useTranslation();
  const client = useQueryClient();
  const [searchParams, setSearchParams] = useSearchState();
  const rawPoolType = searchParams.get('pool_type');
  const poolType: '' | Pool['pool_type'] =
    rawPoolType === 'welfare' || rawPoolType === 'thursday' ? rawPoolType : '';
  const rawPoolState = searchParams.get('state');
  const poolState: '' | Pool['state'] =
    rawPoolState === 'open' || rawPoolState === 'closed' ? rawPoolState : '';
  const poolPager = useUrlPagePager({
    station: 'admin',
    listType: 'admin.pools',
    scopeKey: account,
    scopeReady,
    resetKey: `${poolType}|${poolState}`,
  });
  const config = useQuery({
    queryKey: adminPageKeys.activitiesConfig(account),
    queryFn: ({ signal }) => getAdminActivitiesConfig(signal),
    retry: false,
    enabled: scopeReady,
  });
  const thursday = useQuery({
    queryKey: adminPageKeys.thursday(account),
    queryFn: ({ signal }) => getAdminThursday(signal),
    retry: false,
    enabled: scopeReady,
  });
  const pools = useQuery({
    queryKey: adminPageKeys.pools(account, poolType, poolState, poolPager.page, poolPager.pageSize),
    queryFn: ({ signal }) =>
      getAdminPoolsPage(poolType, poolState, poolPager.page, poolPager.pageSize, signal),
    retry: false,
    enabled: scopeReady,
    placeholderData: (previous, previousQuery) =>
      previousQuery?.queryKey[3] === account &&
      previousQuery.queryKey[4] === poolType &&
      previousQuery.queryKey[5] === poolState
        ? previous
        : undefined,
  });
  const [configOverride, setConfigOverride] = useState<ActivitiesConfig | null>(null);
  const [configBase, setConfigBase] = useState<ActivitiesConfig | null>(null);
  const [confirmResume, setConfirmResume] = useState(false);
  const [periodOverride, setPeriodOverride] = useState<PeriodDraft | null>(null);
  const [nextSchedule, setNextSchedule] = useState(() => nextThursdaySchedule(Date.now()));
  useEffect(() => {
    const refresh = () => setNextSchedule(nextThursdaySchedule(Date.now()));
    const timer = window.setTimeout(
      refresh,
      Math.max(0, nextSchedule.opens_at * 1_000 - Date.now()),
    );
    window.addEventListener('focus', refresh);
    document.addEventListener('visibilitychange', refresh);
    return () => {
      window.clearTimeout(timer);
      window.removeEventListener('focus', refresh);
      document.removeEventListener('visibilitychange', refresh);
    };
  }, [nextSchedule.opens_at]);
  const [adjustment, setAdjustment] = useState({
    poolId: '',
    revision: '',
    authorityRevision: '',
    direction: 'increase' as 'increase' | 'decrease',
    amount: '',
    reason: '',
  });
  const [confirmAdjustment, setConfirmAdjustment] = useState(false);
  const period = thursday.data?.period ?? null;
  const scheduledPeriod =
    period && ['configured', 'open', 'settling'].includes(period.state) ? period : null;
  const schedule = scheduledPeriod ?? nextSchedule;
  const authorityPeriodRevision = thursday.data?.period?.revision ?? (thursday.data ? 'none' : '');
  const onlyRevisionChanged =
    config.data &&
    configBase &&
    JSON.stringify({ ...configBase, revision: config.data.revision }) ===
      JSON.stringify(config.data);
  const configDraft =
    config.data && configOverride && onlyRevisionChanged
      ? { ...configOverride, revision: config.data.revision }
      : config.data
        ? (configOverride ?? config.data)
        : null;
  const periodDraft = periodOverride ?? draftForPeriod(period);
  const literature = periodDraft.literature.replaceAll('\r\n', '\n');
  const literatureError = isActivityLiterature(literature)
    ? null
    : t('admin.activities.period.literatureInvalid');
  const rates = [periodDraft.platform, periodDraft.welfare, periodDraft.next_pool].map(percentBP);
  const invalidPumps =
    rates.some((rate) => !Number.isFinite(rate)) ||
    rates.reduce((sum, rate) => sum + rate, 0) >= 10000;
  const configUnchanged = JSON.stringify(configDraft) === JSON.stringify(config.data);
  const configStale = configDraft?.revision !== config.data?.revision;
  const configDependency =
    configDraft?.master_enabled &&
    configDraft.thursday.enabled &&
    !(config.data?.master_enabled && config.data.thursday.enabled) &&
    !scheduledPeriod
      ? t('admin.activities.config.periodRequired')
      : null;
  const reconcile = async () => {
    await Promise.all([config.refetch(), thursday.refetch(), pools.refetch()]);
  };
  const saveConfig = useRetainedOperation(
    (input: ActivitiesConfig, key) =>
      patchActivitiesConfig(
        {
          expected_revision: input.revision,
          master_enabled: input.master_enabled,
          welfare: input.welfare,
          thursday: input.thursday,
          loan_enabled: input.loan_enabled,
          loan_tiers: input.loan_tiers,
          loan_a: input.loan_a,
          loan_b: input.loan_b,
        },
        key,
      ),
    reconcile,
  );
  const savePeriod = useRetainedOperation(
    (input: PeriodMutation, key) =>
      putThursdayNext(
        {
          expected_revision: input.expected_revision,
          period_key: input.period_key,
          opens_at: input.opens_at,
          literature: input.literature,
          entry: input.entry,
          per_user_limit: Number(input.per_user_limit),
          pumps_bp: {
            platform: percentBP(input.platform),
            welfare: percentBP(input.welfare),
            next_pool: percentBP(input.next_pool),
          },
        },
        key,
      ),
    reconcile,
  );
  const resume = useRetainedOperation(
    (input: { id: string; revision: string }, key) => resumeThursday(input.id, input.revision, key),
    reconcile,
  );
  const adjust = useRetainedOperation(
    (input: typeof adjustment, key) =>
      adjustPool(
        input.poolId,
        {
          direction: input.direction,
          amount: input.amount,
          reason: input.reason,
          expected_revision: input.revision,
          confirmation: input.direction === 'decrease' ? 'DECREASE' : '',
        },
        key,
      ),
    async () => {
      await reconcile();
    },
  );
  const periodLocked = period?.state === 'open' || period?.state === 'settling';
  const periodNeedsConfigRevision = period?.state !== 'configured';
  const periodAuthorityReady =
    thursday.data !== undefined &&
    !thursday.error &&
    !thursday.isFetching &&
    thursdayMutationRevision(config.data, period) !== null &&
    (!periodNeedsConfigRevision || (!config.error && !config.isFetching));
  const poolAuthorityBlocked = !scopeReady || Boolean(pools.error) || pools.isFetching;
  const selectedPool = pools.data?.data.find((pool) => pool.id === adjustment.poolId);
  const previewBalance =
    selectedPool && validPositiveAmount(adjustment.amount)
      ? loanMilli(selectedPool.balance) +
        (adjustment.direction === 'decrease' ? -1n : 1n) * loanMilli(adjustment.amount)
      : null;
  const poolStale =
    !!adjustment.poolId && (!selectedPool || selectedPool.revision !== adjustment.revision);
  const adjustHeading = useRef<HTMLSpanElement>(null);
  useEffect(() => {
    if (adjustment.poolId) {
      adjustHeading.current?.focus({ preventScroll: true });
      adjustHeading.current?.scrollIntoView?.({ block: 'center' });
    }
  }, [adjustment.poolId]);
  const adjustmentBlocked =
    poolAuthorityBlocked ||
    poolStale ||
    previewBalance === null ||
    previewBalance < 0n ||
    adjust.isPending ||
    !validPositiveAmount(adjustment.amount) ||
    !adjustment.reason.trim();
  const currentPoolDecreaseBlocked =
    adjustment.direction === 'decrease' &&
    period?.current_pool_id === adjustment.poolId &&
    (period.state === 'open' || period.state === 'settling');
  const editPeriod = (patch: Partial<PeriodDraft>) => {
    savePeriod.reset();
    setPeriodOverride((current) => ({ ...(current ?? periodDraft), ...patch }));
  };
  const submitPeriod = () => {
    const revision = thursdayMutationRevision(config.data, period);
    if (
      !periodAuthorityReady ||
      periodLocked ||
      revision === null ||
      literatureError ||
      invalidPumps
    )
      return;
    const submittedSchedule = scheduledPeriod ?? nextThursdaySchedule(Date.now());
    if (!scheduledPeriod) setNextSchedule(submittedSchedule);
    savePeriod.mutate(
      {
        ...periodDraft,
        literature,
        period_key: submittedSchedule.period_key,
        opens_at: submittedSchedule.opens_at,
        expected_revision: revision,
      },
      { onSuccess: () => setPeriodOverride(null) },
    );
  };
  const editConfig = (next: ActivitiesConfig) => {
    saveConfig.reset();
    if (!configOverride && config.data) setConfigBase(config.data);
    setConfigOverride(next);
  };
  const editAdjustment = (patch: Partial<typeof adjustment>) => {
    adjust.reset();
    setConfirmAdjustment(false);
    setAdjustment((current) => ({ ...current, ...patch }));
  };
  const submitAdjustment = () => {
    setConfirmAdjustment(false);
    adjust.mutate(adjustment, {
      onSuccess: () =>
        setAdjustment({
          poolId: '',
          revision: '',
          authorityRevision: '',
          direction: 'increase',
          amount: '',
          reason: '',
        }),
    });
  };
  const periodStateLabels: Record<Period['state'], string> = {
    configured: t('admin.activities.states.period.configured'),
    open: t('admin.activities.states.period.open'),
    settling: t('admin.activities.states.period.settling'),
    settled: t('admin.activities.states.period.settled'),
    configuration_error: t('admin.activities.states.period.configurationError'),
  };
  const poolTypeLabels: Record<Pool['pool_type'], string> = {
    welfare: t('admin.activities.states.poolType.welfare'),
    thursday: t('admin.activities.states.poolType.thursday'),
  };
  const poolStateLabels: Record<Pool['state'], string> = {
    open: t('admin.activities.states.pool.open'),
    closed: t('admin.activities.states.pool.closed'),
  };

  useEffect(() => {
    if (
      isUnauthorized(config.error) ||
      isUnauthorized(thursday.error) ||
      isUnauthorized(pools.error)
    ) {
      clearStationSession(client, 'admin');
    }
  }, [client, config.error, pools.error, thursday.error]);
  const commitPoolFilters = (nextType: '' | Pool['pool_type'], nextState: '' | Pool['state']) => {
    setConfirmAdjustment(false);
    setAdjustment({
      poolId: '',
      revision: '',
      authorityRevision: '',
      direction: 'increase',
      amount: '',
      reason: '',
    });
    setSearchParams((previous) => {
      const next = new URLSearchParams(previous);
      if (nextType) next.set('pool_type', nextType);
      else next.delete('pool_type');
      if (nextState) next.set('state', nextState);
      else next.delete('state');
      next.delete('page');
      next.set('page', '1');
      next.delete('page_size');
      next.set('page_size', String(poolPager.pageSize));
      return next;
    });
  };
  const authorityError = [sessionError, config.error, thursday.error, pools.error].find(
    (error) => isUnauthorized(error) || isForbidden(error),
  );
  if (authorityError) return <ErrorState error={authorityError} />;
  return (
    <div className="page ops-page">
      <DraftLeaveGuard
        dirty={
          (!configUnchanged && configOverride !== null) ||
          (periodOverride !== null &&
            JSON.stringify(periodOverride) !== JSON.stringify(draftForPeriod(period))) ||
          Boolean(adjustment.reason || adjustment.amount)
        }
        allowNavigation={({ currentLocation, nextLocation }) =>
          currentLocation.pathname === nextLocation.pathname &&
          currentLocation.search === nextLocation.search
        }
      />
      <PageHeader
        title={t('admin.activities.title')}
        description={t('admin.activities.description')}
        actions={
          configDraft ? (
            <Toggle
              label={t('admin.activities.config.masterEnabled')}
              checked={configDraft.master_enabled}
              disabled={!scopeReady || saveConfig.isPending}
              onChange={(master_enabled) => editConfig({ ...configDraft, master_enabled })}
            />
          ) : undefined
        }
      />
      <div className="nb-actions">
        <Link className="nb-btn nb-btn--secondary" to="/activities/gacha">
          {t('admin.activities.gacha', { defaultValue: '真·抽卡模拟器' })}
        </Link>
      </div>
      <nav className="nb-actions" aria-label={t('admin.activities.title')}>
        <a href="#activity-settings">{t('admin.activities.config.title')}</a>
        <a href="#thursday">{text('星期四排期', 'Thursday scheduling')}</a>
        <a href="#attendance">{t('admin.attendance.title')}</a>
        <a href="#pools">{t('admin.activities.pools.title')}</a>
      </nav>
      <section id="activity-settings" aria-label={t('admin.activities.config.title')}>
        {sessionError ? (
          <ErrorState error={sessionError} />
        ) : config.error ? (
          <ErrorState error={config.error} onRetry={() => void config.refetch()} />
        ) : config.isPending || !configDraft ? (
          <LoadingState />
        ) : (
          <>
            {!configDraft.master_enabled &&
            (configDraft.welfare.enabled || configDraft.thursday.enabled) ? (
              <Note>
                {text(
                  '总开关已关闭，子项设置将保留。',
                  'Activities are paused. Individual settings are retained.',
                )}
              </Note>
            ) : null}
            <Panel>
              <PanelHead
                title={text('每日游戏低保', 'Daily game welfare')}
                actions={
                  <Toggle
                    label={t('admin.activities.config.welfareEnabled')}
                    checked={configDraft.welfare.enabled}
                    disabled={!scopeReady || saveConfig.isPending}
                    onChange={(enabled) =>
                      editConfig({ ...configDraft, welfare: { ...configDraft.welfare, enabled } })
                    }
                  />
                }
              />
              <PanelBody>
                <Fields>
                  <Field
                    size="short"
                    label={t('admin.activities.config.welfareThreshold')}
                    help={text(
                      '游戏积分（含进行中对局的预扣）低于这个数才能领。',
                      'Game credits, including amounts held for active games, must be below this threshold.',
                    )}
                  >
                    {(props) => (
                      <Affix
                        {...props}
                        unit={t('admin.activities.units.credits')}
                        value={configDraft.welfare.threshold}
                        disabled={!scopeReady || saveConfig.isPending}
                        onChange={(event) =>
                          editConfig({
                            ...configDraft,
                            welfare: { ...configDraft.welfare, threshold: event.target.value },
                          })
                        }
                      />
                    )}
                  </Field>
                  <Field size="short" label={t('admin.activities.config.welfareCap')}>
                    {(props) => (
                      <Affix
                        {...props}
                        unit={t('admin.activities.units.credits')}
                        value={configDraft.welfare.cap}
                        disabled={!scopeReady || saveConfig.isPending}
                        onChange={(event) =>
                          editConfig({
                            ...configDraft,
                            welfare: { ...configDraft.welfare, cap: event.target.value },
                          })
                        }
                      />
                    )}
                  </Field>
                </Fields>
              </PanelBody>
            </Panel>
            <Toggle
              label={t('admin.activities.config.thursdayEnabled')}
              checked={configDraft.thursday.enabled}
              disabled={!scopeReady || saveConfig.isPending}
              onChange={(enabled) => editConfig({ ...configDraft, thursday: { enabled } })}
            />
            <LoanConfiguration
              config={configDraft}
              disabled={!scopeReady || saveConfig.isPending}
              onChange={editConfig}
            />
            {saveConfig.error ? <ErrorState error={saveConfig.error} /> : null}
            {configStale ? (
              <Note tone="warn">{t('admin.activities.config.changedElsewhere')}</Note>
            ) : null}
          </>
        )}
        {configDraft ? (
          <SaveBar
            dirtyCount={configUnchanged ? 0 : 1}
            scope={t('admin.activities.config.title')}
            busy={!scopeReady || saveConfig.isPending}
            saveDisabled={configStale || !validLoanConfig(configDraft) || Boolean(configDependency)}
            onSave={() =>
              saveConfig.mutate(configDraft, { onSuccess: () => setConfigOverride(null) })
            }
            onDiscard={() => setConfigOverride(null)}
            saveLabel={t('admin.activities.config.save')}
            discardLabel={t('common.cancel')}
            dirtyLabel={() =>
              text('活动设置有未保存的修改', 'Activity settings have unsaved changes')
            }
          />
        ) : null}
      </section>
      <section id="thursday" aria-label={text('疯狂星期四', 'Thursday event')}>
        <Panel>
          <PanelHead title={text('疯狂星期四', 'Thursday event')} />
          <PanelBody>
            {configDependency ? (
              <Note tone="warn">{t('admin.activities.config.periodRequired')}</Note>
            ) : null}
            {sessionError ? (
              <ErrorState error={sessionError} />
            ) : thursday.isPending ? (
              <LoadingState />
            ) : thursday.error ? (
              <ErrorState error={thursday.error} onRetry={() => void thursday.refetch()} />
            ) : period ? (
              <>
                <dl className="nb-facts nb-facts--inline">
                  <div>
                    <dt>{t('admin.activities.fields.state')}</dt>
                    <dd>
                      <StatusBadge
                        tone={
                          period.state === 'open'
                            ? 'ok'
                            : period.state === 'settling'
                              ? 'warn'
                              : period.state === 'configuration_error'
                                ? 'bad'
                                : 'neutral'
                        }
                      >
                        {periodStateLabels[period.state]}
                      </StatusBadge>
                    </dd>
                  </div>
                  <div>
                    <dt>{t('admin.activities.thursday.periodRevision')}</dt>
                    <dd>{period.period_key}</dd>
                  </div>
                  <div>
                    <dt>{t('admin.activities.period.windowBeijing')}</dt>
                    <dd>
                      {formatBeijingTime(period.opens_at)} — {formatBeijingTime(period.closes_at)}
                    </dd>
                  </div>
                  <div>
                    <dt>{t('admin.activities.thursday.entryLimit')}</dt>
                    <dd>
                      <CreditNumber value={formatCredits(period.entry)} />{' '}
                      {t('admin.activities.units.credits')} /{' '}
                      {formatCount(period.per_user_limit).display}
                    </dd>
                  </div>
                  <div>
                    <dt>{t('admin.activities.thursday.literature')}</dt>
                    <dd>{period.literature}</dd>
                  </div>
                  {period.settlement ? (
                    <>
                      <div>
                        <dt>{t('admin.activities.thursday.processed')}</dt>
                        <dd>
                          {formatCount(period.settlement.processed_count).display} /{' '}
                          {formatCount(period.settlement.contribution_count).display}
                        </dd>
                      </div>
                      <div>
                        <dt>{t('admin.activities.thursday.payoutRollover')}</dt>
                        <dd>
                          <CreditNumber value={formatCredits(period.settlement.payout_total)} />{' '}
                          {t('admin.activities.units.credits')} /{' '}
                          <CreditNumber value={formatCredits(period.settlement.rollover)} />{' '}
                          {t('admin.activities.units.credits')}
                        </dd>
                      </div>
                    </>
                  ) : null}
                </dl>
                {period.state === 'settling' ? (
                  <div className="nb-actions">
                    <button
                      className="nb-btn nb-btn--danger"
                      type="button"
                      disabled={!scopeReady || resume.isPending}
                      onClick={() => setConfirmResume(true)}
                    >
                      {t('admin.activities.thursday.resume')}
                    </button>
                  </div>
                ) : null}
              </>
            ) : (
              <Note>
                {t('admin.activities.thursday.emptyTitle')} ·{' '}
                {t('admin.activities.thursday.emptyBody')}
              </Note>
            )}
            <div className="ops-subcard">
              <h3>
                {scheduledPeriod
                  ? t('admin.activities.period.updateTitle')
                  : t('admin.activities.period.createTitle')}
              </h3>
              {sessionError ? (
                <ErrorState error={sessionError} />
              ) : !scopeReady ? (
                <LoadingState />
              ) : (
                <>
                  <p className="nb-small nb-muted">
                    {periodLocked
                      ? t('admin.activities.period.lockedHint')
                      : scheduledPeriod
                        ? t('admin.activities.period.configuredHint')
                        : t('admin.activities.period.scheduleHint')}
                  </p>
                  <Note>
                    {t('admin.activities.period.windowBeijing')} ·{' '}
                    <span>
                      {formatBeijingTime(schedule.opens_at)} —{' '}
                      {formatBeijingTime(schedule.closes_at)}
                    </span>
                  </Note>
                  <fieldset
                    className="nb-fieldset"
                    disabled={!periodAuthorityReady || periodLocked || savePeriod.isPending}
                  >
                    <Fields legend={text('参与规则', 'Participation')}>
                      <Field size="short" label={t('admin.activities.period.entry')}>
                        {(props) => (
                          <Affix
                            {...props}
                            unit={t('admin.activities.units.credits')}
                            value={periodDraft.entry}
                            onChange={(event) => editPeriod({ entry: event.target.value })}
                          />
                        )}
                      </Field>
                      <Field size="short" label={t('admin.activities.period.perUserLimit')}>
                        {(props) => (
                          <Affix
                            {...props}
                            unit={text('次', 'times')}
                            type="number"
                            min="1"
                            max="1000"
                            value={periodDraft.per_user_limit}
                            onChange={(event) => editPeriod({ per_user_limit: event.target.value })}
                          />
                        )}
                      </Field>
                    </Fields>
                    <Fields
                      legend={text('费用抽成', 'Fee allocation')}
                      hint={t('common.operations.management.feeHelp')}
                    >
                      <Field size="short" label={t('admin.activities.period.platformBp')}>
                        {(props) => (
                          <Affix
                            {...props}
                            unit="%"
                            inputMode="decimal"
                            value={periodDraft.platform}
                            onChange={(event) => editPeriod({ platform: event.target.value })}
                          />
                        )}
                      </Field>
                      <Field size="short" label={t('admin.activities.period.welfareBp')}>
                        {(props) => (
                          <Affix
                            {...props}
                            unit="%"
                            inputMode="decimal"
                            value={periodDraft.welfare}
                            onChange={(event) => editPeriod({ welfare: event.target.value })}
                          />
                        )}
                      </Field>
                      <Field size="short" label={t('admin.activities.period.nextPoolBp')}>
                        {(props) => (
                          <Affix
                            {...props}
                            unit="%"
                            inputMode="decimal"
                            value={periodDraft.next_pool}
                            onChange={(event) => editPeriod({ next_pool: event.target.value })}
                          />
                        )}
                      </Field>
                    </Fields>
                  </fieldset>
                  <Field
                    size="long"
                    label={t('admin.activities.period.literature')}
                    error={literatureError ?? undefined}
                  >
                    {(props) => (
                      <textarea
                        {...props}
                        rows={4}
                        disabled={!periodAuthorityReady || periodLocked || savePeriod.isPending}
                        aria-invalid={Boolean(literatureError)}
                        value={periodDraft.literature}
                        onChange={(event) => editPeriod({ literature: event.target.value })}
                      />
                    )}
                  </Field>
                  {invalidPumps && (
                    <Note tone="bad">
                      {text(
                        '每项百分比最多两位小数，合计须低于 100%。',
                        'Enter percentages with up to two decimal places and a total below 100%.',
                      )}
                    </Note>
                  )}
                  {savePeriod.error ? <ErrorState error={savePeriod.error} /> : null}
                  {!validPositiveAmount(periodDraft.entry) && (
                    <p id="period-entry-hint" className="nb-small nb-muted">
                      {text(
                        '请填写大于 0 的参与金额后保存排期。',
                        'Enter an entry amount greater than 0 before scheduling.',
                      )}
                    </p>
                  )}
                  <FormActions>
                    <button
                      className="nb-btn nb-btn--primary"
                      type="button"
                      disabled={
                        !scopeReady ||
                        !periodAuthorityReady ||
                        periodLocked ||
                        savePeriod.isPending ||
                        invalidPumps ||
                        Boolean(literatureError) ||
                        !validPositiveAmount(periodDraft.entry)
                      }
                      aria-describedby={
                        !validPositiveAmount(periodDraft.entry) ? 'period-entry-hint' : undefined
                      }
                      onClick={submitPeriod}
                    >
                      {text('保存并排期', 'Save and schedule')}
                    </button>
                    {periodOverride ? (
                      <button
                        type="button"
                        className="nb-btn nb-btn--secondary"
                        disabled={!scopeReady || savePeriod.isPending}
                        onClick={() => setPeriodOverride(null)}
                      >
                        {t('common.cancel')}
                      </button>
                    ) : null}
                  </FormActions>
                </>
              )}
            </div>
          </PanelBody>
        </Panel>
      </section>
      <AttendanceManagement key={account} account={account} enabled={scopeReady} />
      <section id="pools" aria-label={t('admin.activities.pools.title')}>
        <Panel>
          <PanelHead title={t('admin.activities.pools.title')} />
          <PanelBody>
            <FilterBar
              query={pools}
              activeCount={Number(Boolean(poolType)) + Number(Boolean(poolState))}

              search={
                <>
                  <Field size="short" label={t('admin.activities.pools.filterType')}>
                    {(props) => (
                      <select
                        {...props}
                        value={poolType}
                        disabled={!scopeReady}
                        onChange={(event) =>
                          commitPoolFilters(event.target.value as '' | Pool['pool_type'], poolState)
                        }
                      >
                        <option value="">{t('admin.activities.pools.allTypes')}</option>
                        <option value="welfare">{poolTypeLabels.welfare}</option>
                        <option value="thursday">{poolTypeLabels.thursday}</option>
                      </select>
                    )}
                  </Field>
                  <Field size="short" label={t('admin.activities.pools.filterState')}>
                    {(props) => (
                      <select
                        {...props}
                        value={poolState}
                        disabled={!scopeReady}
                        onChange={(event) =>
                          commitPoolFilters(poolType, event.target.value as '' | Pool['state'])
                        }
                      >
                        <option value="">{t('admin.activities.pools.allStates')}</option>
                        <option value="open">{poolStateLabels.open}</option>
                        <option value="closed">{poolStateLabels.closed}</option>
                      </select>
                    )}
                  </Field>
                  {poolType || poolState ? (
                    <Button
                      type="button"
                      variant="ghost"
                      disabled={!scopeReady}
                      onClick={() => commitPoolFilters('', '')}
                    >
                      {t('common.resetFilter')}
                    </Button>
                  ) : null}
                </>
              }
            />
            {sessionError ? (
              <ErrorState error={sessionError} />
            ) : pools.isPending ? (
              <LoadingState />
            ) : pools.error ? (
              <ErrorState error={pools.error} onRetry={() => void pools.refetch()} />
            ) : pools.data.data.length === 0 ? (
              <EmptyState
                title={t('admin.activities.pools.emptyTitle')}
                body={t('admin.activities.pools.emptyBody')}
              />
            ) : (
              <div aria-busy={pools.isFetching}>
                {pools.isFetching ? <LoadingState /> : null}
                <DataTable
                  dense
                  caption={t('admin.activities.pools.title')}
                  rows={pools.data.data}
                  rowKey={(pool) => pool.id}
                  selectedKey={adjustment.poolId}
                  columns={[
                    {
                      key: 'pool',
                      header: t('admin.activities.pools.typeState'),
                      cell: 'title',
                      render: (pool) => (
                        <div className="nb-actions">
                          <strong>{poolTypeLabels[pool.pool_type]}</strong>{' '}
                          <StatusBadge tone={pool.state === 'open' ? 'ok' : 'neutral'}>
                            {poolStateLabels[pool.state]}
                          </StatusBadge>
                        </div>
                      ),
                    },
                    {
                      key: 'period',
                      header: t('admin.activities.pools.period'),
                      mobileLabel: t('admin.activities.pools.period'),
                      cell: 'meta',
                      render: (pool) => (
                        <span title={pool.period_id ?? undefined}>
                          {pool.period_date ??
                            t(
                              pool.pool_type === 'welfare'
                                ? 'admin.activities.pools.singleton'
                                : 'admin.activities.pools.unboundPeriod',
                            )}
                        </span>
                      ),
                    },
                    {
                      key: 'balance',
                      header: t('admin.activities.pools.balance'),
                      mobileLabel: t('admin.activities.pools.balance'),
                      cell: 'meta',
                      align: 'num',
                      render: (pool) => (
                        <>
                          <CreditNumber value={formatCredits(pool.balance)} />{' '}
                          {t('admin.activities.units.credits')}
                        </>
                      ),
                    },
                    {
                      key: 'action',
                      header: t('admin.activities.pools.adjust'),
                      cell: 'action',
                      align: 'action',
                      render: (pool) => (
                        <button
                          className="nb-btn nb-btn--secondary nb-btn--sm"
                          type="button"
                          disabled={!scopeReady || pool.state !== 'open'}
                          onClick={() => {
                            adjust.reset();
                            setConfirmAdjustment(false);
                            setAdjustment({
                              poolId: pool.id,
                              revision: pool.revision,
                              authorityRevision: authorityPeriodRevision,
                              direction: 'increase',
                              amount: '',
                              reason: '',
                            });
                          }}
                        >
                          {t('admin.activities.pools.select')}
                        </button>
                      ),
                    },
                  ]}
                />
              </div>
            )}
            {scopeReady && !sessionError && !pools.error && pools.data ? (
              <PagePagination
                metadata={pools.data.pagination}
                requestedPage={poolPager.page}
                busy={pools.isFetching}
                onPageChange={poolPager.setPage}
                onPageSizeChange={poolPager.setPageSize}
              />
            ) : null}
          </PanelBody>
        </Panel>
      </section>
      {scopeReady && adjustment.poolId ? (
        <Panel tone={adjustment.direction === 'decrease' ? 'danger' : undefined}>
          <PanelHead
            title={
              <span
                tabIndex={-1}
                ref={adjustHeading}
              >{`${t('admin.activities.adjustment.title')}${selectedPool ? ` · ${poolTypeLabels[selectedPool.pool_type]}` : ''}`}</span>
            }
            description={t('admin.activities.adjustment.selected')}
          />
          <PanelBody>
            {selectedPool ? (
              <p>
                {t('admin.activities.pools.balance')}：
                <CreditNumber value={formatCredits(selectedPool.balance)} />{' '}
                {t('admin.activities.units.credits')}
              </p>
            ) : null}
            {currentPoolDecreaseBlocked ? (
              <Note tone="warn">{t('admin.activities.adjustment.decreaseBlocked')}</Note>
            ) : null}
            <Fields>
              <Field size="short" label={t('admin.activities.adjustment.direction')}>
                {(props) => (
                  <select
                    {...props}
                    value={adjustment.direction}
                    disabled={adjust.isPending}
                    onChange={(event) =>
                      editAdjustment({
                        direction: event.target.value as typeof adjustment.direction,
                      })
                    }
                  >
                    <option value="increase">{t('admin.activities.adjustment.increase')}</option>
                    <option
                      value="decrease"
                      disabled={
                        period?.current_pool_id === adjustment.poolId &&
                        (period.state === 'open' || period.state === 'settling')
                      }
                    >
                      {t('admin.activities.adjustment.decrease')}
                    </option>
                  </select>
                )}
              </Field>
              <Field size="short" label={t('admin.activities.adjustment.amount')}>
                {(props) => (
                  <Affix
                    {...props}
                    unit={t('admin.activities.units.credits')}
                    value={adjustment.amount}
                    disabled={adjust.isPending}
                    onChange={(event) => editAdjustment({ amount: event.target.value })}
                  />
                )}
              </Field>
              <Field size="long" label={t('admin.activities.adjustment.reason')}>
                {(props) => (
                  <input
                    {...props}
                    value={adjustment.reason}
                    maxLength={1024}
                    disabled={adjust.isPending}
                    onChange={(event) => editAdjustment({ reason: event.target.value })}
                  />
                )}
              </Field>
            </Fields>
            {poolStale && (
              <Note tone="warn">
                {t('admin.activities.config.changedElsewhere')}{' '}
                {selectedPool && (
                  <button
                    type="button"
                    onClick={() =>
                      editAdjustment({
                        revision: selectedPool.revision,
                        authorityRevision: authorityPeriodRevision,
                      })
                    }
                  >
                    {t('common.refresh')}
                  </button>
                )}
              </Note>
            )}
            {selectedPool && previewBalance !== null && (
              <p>
                {t('admin.activities.pools.balance')} {formatCredits(selectedPool.balance)} →{' '}
                {displayAmount(String(previewBalance))}
              </p>
            )}
            {adjust.error ? <ErrorState error={adjust.error} /> : null}
            <FormActions>
              <button
                className={
                  adjustment.direction === 'decrease'
                    ? 'nb-btn nb-btn--danger'
                    : 'nb-btn nb-btn--primary'
                }
                type="button"
                disabled={adjustmentBlocked || currentPoolDecreaseBlocked}
                onClick={() => {
                  setAdjustment({ ...adjustment, authorityRevision: authorityPeriodRevision });
                  setConfirmAdjustment(true);
                }}
              >
                {t('admin.activities.adjustment.apply')}
              </button>
              <button
                className="nb-btn nb-btn--secondary"
                type="button"
                disabled={adjust.isPending}
                onClick={() =>
                  setAdjustment({
                    poolId: '',
                    revision: '',
                    authorityRevision: '',
                    direction: 'increase',
                    amount: '',
                    reason: '',
                  })
                }
              >
                {t('admin.activities.adjustment.cancel')}
              </button>
            </FormActions>
          </PanelBody>
        </Panel>
      ) : null}
      {confirmResume && period && (
        <ConfirmDialog
          open
          danger
          title={text('恢复星期四活动结算？', 'Resume Thursday settlement?')}
          description={text(
            `将为期次 ${period.period_key} 继续执行结算。`,
            `Settlement will resume for ${period.period_key}.`,
          )}
          confirmLabel={text('恢复结算', 'Resume settlement')}
          cancelLabel={t('common.cancel')}
          onCancel={() => setConfirmResume(false)}
          onConfirm={() => {
            setConfirmResume(false);
            resume.mutate({ id: period.id, revision: period.revision });
          }}
        />
      )}
      {confirmAdjustment ? (
        <ConfirmDialog
          open
          danger={adjustment.direction === 'decrease'}
          title={t('admin.activities.adjustment.confirmTitle')}
          description={t(
            adjustment.direction === 'decrease'
              ? 'admin.activities.adjustment.confirmDecrease'
              : 'admin.activities.adjustment.confirmIncrease',
            {
              amount: adjustment.amount,
              reason: adjustment.reason,
              pool: selectedPool ? poolTypeLabels[selectedPool.pool_type] : '',
            },
          )}
          confirmLabel={t('admin.activities.adjustment.apply')}
          confirmDisabled={
            adjustmentBlocked ||
            currentPoolDecreaseBlocked ||
            adjustment.authorityRevision !== authorityPeriodRevision
          }
          onCancel={() => setConfirmAdjustment(false)}
          onConfirm={submitAdjustment}
        >
          {selectedPool && previewBalance !== null && (
            <p>
              {t('admin.activities.pools.balance')} {formatCredits(selectedPool.balance)} →{' '}
              {displayAmount(String(previewBalance))}
            </p>
          )}
        </ConfirmDialog>
      ) : null}
    </div>
  );
}

export function ActivitiesPage() {
  const session = useAdminSession();
  const account = session.data?.admin.username;
  const scopeReady = Boolean(account) && !session.error;
  return (
    <ActivitiesPageContent
      key={account ?? 'anonymous'}
      account={account ?? ''}
      scopeReady={scopeReady}
      sessionError={session.error}
    />
  );
}
