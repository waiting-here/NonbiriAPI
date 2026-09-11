import { useEffect, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useSearchState } from '@shared/operations/useSearchState';
import { useTranslation } from 'react-i18next';
import { clearStationSession } from '@shared/charityManagement';
import {
  Card,
  EmptyState,
  ErrorState,
  LoadingState,
  PageHeader,
  StatusBadge,
} from '@shared/components/States';
import { PagePagination } from '@shared/operations/PagePagination';
import { useUrlPagePager } from '@shared/operations/useUrlPagePager';
import { isForbidden, isUnauthorized } from '@shared/query/http';
import { amount } from '@shared/operations/wire';
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
        platform: String(period.pumps_bp.platform),
        welfare: String(period.pumps_bp.welfare),
        next_pool: String(period.pumps_bp.next_pool),
      }
    : emptyPeriodDraft();

interface ActivitiesPageContentProps {
  account: string;
  scopeReady: boolean;
  sessionError: unknown;
}

function ActivitiesPageContent({ account, scopeReady, sessionError }: ActivitiesPageContentProps) {
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
    confirmed: false,
  });
  const period = thursday.data?.period ?? null;
  const scheduledPeriod =
    period && ['configured', 'open', 'settling'].includes(period.state) ? period : null;
  const schedule = scheduledPeriod ?? nextSchedule;
  const authorityPeriodRevision = thursday.data?.period?.revision ?? (thursday.data ? 'none' : '');
  const configDraft = config.data ? (configOverride ? configOverride : config.data) : null;
  const periodDraft = periodOverride ?? draftForPeriod(period);
  const configUnchanged = JSON.stringify(configDraft) === JSON.stringify(config.data);
  const configStale = configDraft?.revision !== config.data?.revision;
  const configDependency =
    configDraft &&
    !configDraft.master_enabled &&
    (configDraft.welfare.enabled || configDraft.thursday.enabled)
      ? t('admin.activities.config.masterRequired')
      : configDraft?.thursday.enabled && !period
        ? t('admin.activities.config.periodRequired')
        : null;
  const adjustmentConfirmed =
    adjustment.confirmed && adjustment.authorityRevision === authorityPeriodRevision;
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
            platform: Number(input.platform),
            welfare: Number(input.welfare),
            next_pool: Number(input.next_pool),
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
      setAdjustment((current) => ({ ...current, confirmed: false }));
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
  const currentPoolDecreaseBlocked =
    adjustment.direction === 'decrease' &&
    period?.current_pool_id === adjustment.poolId &&
    (period.state === 'open' || period.state === 'settling');
  const editPeriod = (patch: Partial<PeriodDraft>) => {
    setPeriodOverride((current) => ({ ...(current ?? periodDraft), ...patch }));
  };
  const submitPeriod = () => {
    const revision = thursdayMutationRevision(config.data, period);
    if (!periodAuthorityReady || periodLocked || revision === null) return;
    const submittedSchedule = scheduledPeriod ?? nextThursdaySchedule(Date.now());
    if (!scheduledPeriod) setNextSchedule(submittedSchedule);
    savePeriod.mutate(
      {
        ...periodDraft,
        period_key: submittedSchedule.period_key,
        opens_at: submittedSchedule.opens_at,
        expected_revision: revision,
      },
      { onSuccess: () => setPeriodOverride(null) },
    );
  };
  const editConfig = (next: ActivitiesConfig) => {
    setConfigOverride(next);
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
      isForbidden(config.error) ||
      isUnauthorized(thursday.error) ||
      isForbidden(thursday.error) ||
      isUnauthorized(pools.error) ||
      isForbidden(pools.error)
    ) {
      clearStationSession(client, 'admin');
    }
  }, [client, config.error, pools.error, thursday.error]);
  const commitPoolFilters = (nextType: '' | Pool['pool_type'], nextState: '' | Pool['state']) => {
    setAdjustment({
      poolId: '',
      revision: '',
      authorityRevision: '',
      direction: 'increase',
      amount: '',
      reason: '',
      confirmed: false,
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
      <PageHeader
        title={t('admin.activities.title')}
        description={t('admin.activities.description')}
      />
      <Card>
        <h2>{t('admin.activities.config.title')}</h2>
        {sessionError ? (
          <ErrorState error={sessionError} />
        ) : config.error ? (
          <ErrorState error={config.error} onRetry={() => void config.refetch()} />
        ) : config.isPending || !configDraft ? (
          <LoadingState />
        ) : (
          <>
            <p>{t('admin.activities.config.revisionHint')}</p>
            <div className="ops-field-grid">
              <label className="checkbox-label">
                <input
                  type="checkbox"
                  checked={configDraft.master_enabled}
                  disabled={!scopeReady || saveConfig.isPending}
                  onChange={(event) =>
                    editConfig({ ...configDraft, master_enabled: event.target.checked })
                  }
                />
                <span>{t('admin.activities.config.masterEnabled')}</span>
              </label>
              <label className="checkbox-label">
                <input
                  type="checkbox"
                  checked={configDraft.welfare.enabled}
                  disabled={!scopeReady || saveConfig.isPending}
                  onChange={(event) =>
                    editConfig({
                      ...configDraft,
                      welfare: { ...configDraft.welfare, enabled: event.target.checked },
                    })
                  }
                />
                <span>{t('admin.activities.config.welfareEnabled')}</span>
              </label>
              <label>
                <span>{t('admin.activities.config.welfareThreshold')}</span>
                <input
                  value={configDraft.welfare.threshold}
                  disabled={!scopeReady || saveConfig.isPending}
                  onChange={(event) =>
                    editConfig({
                      ...configDraft,
                      welfare: { ...configDraft.welfare, threshold: event.target.value },
                    })
                  }
                />
              </label>
              <label>
                <span>{t('admin.activities.config.welfareCap')}</span>
                <input
                  value={configDraft.welfare.cap}
                  disabled={!scopeReady || saveConfig.isPending}
                  onChange={(event) =>
                    editConfig({
                      ...configDraft,
                      welfare: { ...configDraft.welfare, cap: event.target.value },
                    })
                  }
                />
              </label>
              <label className="checkbox-label">
                <input
                  type="checkbox"
                  checked={configDraft.thursday.enabled}
                  disabled={!scopeReady || saveConfig.isPending}
                  onChange={(event) =>
                    editConfig({ ...configDraft, thursday: { enabled: event.target.checked } })
                  }
                />
                <span>{t('admin.activities.config.thursdayEnabled')}</span>
              </label>
            </div>
            {saveConfig.error ? <ErrorState error={saveConfig.error} /> : null}
            {configDependency ? (
              <p role="alert" className="field-error">
                {configDependency}
              </p>
            ) : null}
            {configStale ? (
              <p role="alert" className="field-error">
                {t('admin.activities.config.changedElsewhere')}
              </p>
            ) : null}
            <button
              className="btn btn-primary"
              type="button"
              disabled={
                !scopeReady ||
                saveConfig.isPending ||
                configUnchanged ||
                configStale ||
                Boolean(configDependency)
              }
              onClick={() =>
                saveConfig.mutate(configDraft, { onSuccess: () => setConfigOverride(null) })
              }
            >
              {t('admin.activities.config.save')}
            </button>
            {configOverride ? (
              <button
                type="button"
                className="btn btn-secondary"
                disabled={!scopeReady || saveConfig.isPending}
                onClick={() => setConfigOverride(null)}
              >
                {t('common.cancel')}
              </button>
            ) : null}
          </>
        )}
      </Card>
      <Card>
        <h2>{t('admin.activities.thursday.title')}</h2>
        {sessionError ? (
          <ErrorState error={sessionError} />
        ) : thursday.isPending ? (
          <LoadingState />
        ) : thursday.error ? (
          <ErrorState error={thursday.error} onRetry={() => void thursday.refetch()} />
        ) : period ? (
          <>
            <dl className="ops-kv">
              <dt>{t('admin.activities.fields.state')}</dt>
              <dd>
                <StatusBadge
                  active={period.state === 'open'}
                  danger={period.state === 'configuration_error'}
                  label={periodStateLabels[period.state]}
                />
              </dd>
              <dt>{t('admin.activities.thursday.periodRevision')}</dt>
              <dd>
                {period.period_key} / {period.revision}
              </dd>
              <dt>{t('admin.activities.period.windowBeijing')}</dt>
              <dd>
                {formatBeijingTime(period.opens_at)} — {formatBeijingTime(period.closes_at)}
              </dd>
              <dt>{t('admin.activities.thursday.entryLimit')}</dt>
              <dd>
                {period.entry} {t('admin.activities.units.credits')} / {period.per_user_limit}
              </dd>
              <dt>{t('admin.activities.thursday.literature')}</dt>
              <dd>{period.literature}</dd>
              {period.settlement ? (
                <>
                  <dt>{t('admin.activities.thursday.processed')}</dt>
                  <dd>
                    {period.settlement.processed_count} / {period.settlement.contribution_count}
                  </dd>
                  <dt>{t('admin.activities.thursday.payoutRollover')}</dt>
                  <dd>
                    {period.settlement.payout_total} / {period.settlement.rollover}
                  </dd>
                </>
              ) : null}
            </dl>
            {period.state === 'settling' ? (
              <button
                className="btn btn-danger"
                type="button"
                disabled={!scopeReady || resume.isPending}
                onClick={() => resume.mutate({ id: period.id, revision: period.revision })}
              >
                {t('admin.activities.thursday.resume')}
              </button>
            ) : null}
          </>
        ) : (
          <EmptyState
            title={t('admin.activities.thursday.emptyTitle')}
            body={t('admin.activities.thursday.emptyBody')}
          />
        )}
      </Card>
      <Card>
        <h2>
          {scheduledPeriod
            ? t('admin.activities.period.updateTitle')
            : t('admin.activities.period.createTitle')}
        </h2>
        {sessionError ? (
          <ErrorState error={sessionError} />
        ) : !scopeReady ? (
          <LoadingState />
        ) : (
          <>
            <p>
              {periodLocked
                ? t('admin.activities.period.lockedHint')
                : scheduledPeriod
                  ? t('admin.activities.period.configuredHint')
                  : t('admin.activities.period.scheduleHint')}
            </p>
            <dl className="ops-kv">
              <dt>{t('admin.activities.period.windowBeijing')}</dt>
              <dd>
                {formatBeijingTime(schedule.opens_at)} — {formatBeijingTime(schedule.closes_at)}
              </dd>
            </dl>
            <fieldset
              className="ops-field-grid"
              disabled={!periodAuthorityReady || periodLocked || savePeriod.isPending}
            >
              <label>
                <span>{t('admin.activities.period.entry')}</span>
                <input
                  value={periodDraft.entry}
                  onChange={(event) => editPeriod({ entry: event.target.value })}
                />
              </label>
              <label>
                <span>{t('admin.activities.period.perUserLimit')}</span>
                <input
                  type="number"
                  min="1"
                  max="1000"
                  value={periodDraft.per_user_limit}
                  onChange={(event) => editPeriod({ per_user_limit: event.target.value })}
                />
              </label>
              <label>
                <span>{t('admin.activities.period.platformBp')}</span>
                <input
                  type="number"
                  min="0"
                  max="9999"
                  value={periodDraft.platform}
                  onChange={(event) => editPeriod({ platform: event.target.value })}
                />
              </label>
              <label>
                <span>{t('admin.activities.period.welfareBp')}</span>
                <input
                  type="number"
                  min="0"
                  max="9999"
                  value={periodDraft.welfare}
                  onChange={(event) => editPeriod({ welfare: event.target.value })}
                />
              </label>
              <label>
                <span>{t('admin.activities.period.nextPoolBp')}</span>
                <input
                  type="number"
                  min="0"
                  max="9999"
                  value={periodDraft.next_pool}
                  onChange={(event) => editPeriod({ next_pool: event.target.value })}
                />
              </label>
            </fieldset>
            <label className="ops-form-field">
              <span>{t('admin.activities.period.literature')}</span>
              <textarea
                disabled={!periodAuthorityReady || periodLocked || savePeriod.isPending}
                value={periodDraft.literature}
                onChange={(event) => editPeriod({ literature: event.target.value })}
              />
            </label>
            {savePeriod.error ? <ErrorState error={savePeriod.error} /> : null}
            <button
              className="btn btn-primary"
              type="button"
              disabled={
                !scopeReady ||
                !periodAuthorityReady ||
                periodLocked ||
                savePeriod.isPending ||
                !periodDraft.literature ||
                !validPositiveAmount(periodDraft.entry)
              }
              onClick={submitPeriod}
            >
              {t('admin.activities.period.save')}
            </button>
            {periodOverride ? (
              <button
                type="button"
                className="btn btn-secondary"
                disabled={!scopeReady || savePeriod.isPending}
                onClick={() => setPeriodOverride(null)}
              >
                {t('common.cancel')}
              </button>
            ) : null}
          </>
        )}
      </Card>
      <Card>
        <h2>{t('admin.activities.pools.title')}</h2>
        <div className="ops-toolbar">
          <label>
            <span>{t('admin.activities.pools.filterType')}</span>
            <select
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
          </label>
          <label>
            <span>{t('admin.activities.pools.filterState')}</span>
            <select
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
          </label>
        </div>
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
            <div className="ops-table-scroll">
              <table className="ops-table ops-table--responsive">
                <thead>
                  <tr>
                    <th>{t('admin.activities.pools.typeState')}</th>
                    <th>{t('admin.activities.pools.period')}</th>
                    <th>{t('admin.activities.pools.balance')}</th>
                    <th>{t('admin.activities.pools.revision')}</th>
                    <th>{t('admin.activities.pools.adjust')}</th>
                  </tr>
                </thead>
                <tbody>
                  {pools.data.data.map((pool) => (
                    <tr key={pool.id}>
                      <td data-label={t('admin.activities.pools.typeState')}>
                        {poolTypeLabels[pool.pool_type]} / {poolStateLabels[pool.state]}
                      </td>
                      <td data-label={t('admin.activities.pools.period')}>
                        {pool.period_id ??
                          t(
                            pool.pool_type === 'welfare'
                              ? 'admin.activities.pools.singleton'
                              : 'admin.activities.pools.unboundPeriod',
                          )}
                      </td>
                      <td data-label={t('admin.activities.pools.balance')}>
                        {pool.balance} {t('admin.activities.units.credits')}
                      </td>
                      <td data-label={t('admin.activities.pools.revision')}>{pool.revision}</td>
                      <td className="ops-cell-wide" data-label={t('admin.activities.pools.adjust')}>
                        <button
                          className="btn btn-secondary"
                          type="button"
                          disabled={!scopeReady || pool.state !== 'open'}
                          onClick={() =>
                            setAdjustment({
                              poolId: pool.id,
                              revision: pool.revision,
                              authorityRevision: authorityPeriodRevision,
                              direction: 'increase',
                              amount: '',
                              reason: '',
                              confirmed: false,
                            })
                          }
                        >
                          {t('admin.activities.pools.select')}
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
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
      </Card>
      {scopeReady && adjustment.poolId ? (
        <Card className={adjustment.direction === 'decrease' ? 'ops-danger' : ''}>
          <h2>{t('admin.activities.adjustment.title')}</h2>
          <p>
            {t('admin.activities.adjustment.selected', {
              poolId: adjustment.poolId,
              revision: adjustment.revision,
            })}
          </p>
          {currentPoolDecreaseBlocked ? (
            <p className="inline-notice" role="status">
              {t('admin.activities.adjustment.decreaseBlocked')}
            </p>
          ) : null}
          <div className="ops-field-grid">
            <label>
              <span>{t('admin.activities.adjustment.direction')}</span>
              <select
                value={adjustment.direction}
                onChange={(event) =>
                  setAdjustment({
                    ...adjustment,
                    authorityRevision: authorityPeriodRevision,
                    direction: event.target.value as typeof adjustment.direction,
                    confirmed: false,
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
            </label>
            <label>
              <span>{t('admin.activities.adjustment.amount')}</span>
              <input
                value={adjustment.amount}
                onChange={(event) => setAdjustment({ ...adjustment, amount: event.target.value })}
              />
            </label>
            <label>
              <span>{t('admin.activities.adjustment.reason')}</span>
              <input
                value={adjustment.reason}
                maxLength={1024}
                onChange={(event) => setAdjustment({ ...adjustment, reason: event.target.value })}
              />
            </label>
          </div>
          {adjustment.direction === 'decrease' ? (
            <label className="checkbox-label">
              <input
                type="checkbox"
                checked={adjustmentConfirmed}
                onChange={(event) =>
                  setAdjustment({
                    ...adjustment,
                    authorityRevision: authorityPeriodRevision,
                    confirmed: event.target.checked,
                  })
                }
              />
              <span>{t('admin.activities.adjustment.confirmDecrease')}</span>
            </label>
          ) : null}
          {adjust.error ? <ErrorState error={adjust.error} /> : null}
          <div className="ops-actions">
            <button
              className={adjustment.direction === 'decrease' ? 'btn btn-danger' : 'btn btn-primary'}
              type="button"
              disabled={
                poolAuthorityBlocked ||
                currentPoolDecreaseBlocked ||
                adjust.isPending ||
                !validPositiveAmount(adjustment.amount) ||
                !adjustment.reason.trim() ||
                (adjustment.direction === 'decrease' && !adjustmentConfirmed)
              }
              onClick={() =>
                adjust.mutate(adjustment, {
                  onSuccess: () =>
                    setAdjustment({
                      poolId: '',
                      revision: '',
                      authorityRevision: '',
                      direction: 'increase',
                      amount: '',
                      reason: '',
                      confirmed: false,
                    }),
                })
              }
            >
              {t('admin.activities.adjustment.apply')}
            </button>
            <button
              className="btn btn-link"
              type="button"
              onClick={() =>
                setAdjustment({
                  poolId: '',
                  revision: '',
                  authorityRevision: '',
                  direction: 'increase',
                  amount: '',
                  reason: '',
                  confirmed: false,
                })
              }
            >
              {t('admin.activities.adjustment.cancel')}
            </button>
          </div>
        </Card>
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
