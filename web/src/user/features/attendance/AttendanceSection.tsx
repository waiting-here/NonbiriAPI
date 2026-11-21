import { useId, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { SectionHead, EmptyState, ErrorState, LoadingState } from '@shared/components/States';
import { Button, Note, OutcomeNote, Panel, StatusBadge } from '@shared/components/ui';
import { MarkdownText } from '@shared/components/MarkdownText';
import { PagePagination } from '@shared/operations/PagePagination';
import { responseOutcomeUnknown } from '@shared/operations/api';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { formatCount } from '@shared/utils/formatNumber';
import { formatOffset } from '@shared/time';
import { creditHistoryKeys } from '../credits/queries';
import { limitedActivityKeys } from '../limitedactivities/queries';
import { RewardSummary } from '@shared/rewards/RewardSummary';
import { getAssets, rewardKeys, type Asset } from '@shared/rewards/api';
import {
  attendanceKeys,
  attendanceMetadata,
  signIn,
  type AttendancePage,
  type AttendancePeriod,
} from '@shared/attendance/api';
import { useAttendancePage } from '@shared/attendance/queries';
import { CampaignCover } from '@shared/attendance/CampaignCover';
import { MilestoneHeading } from '@shared/attendance/MilestoneHeading';
import '@shared/rewards/rewards.css';
import './attendance.css';

const statusLabels = {
  upcoming: 'user.attendance.status.upcoming',
  active: 'user.attendance.status.active',
  ended: 'user.attendance.status.ended',
};
const modeLabels = {
  cumulative: 'user.attendance.mode.cumulative',
  consecutive: 'user.attendance.mode.consecutive',
};
const actionLabels = {
  retry: 'user.attendance.actions.retry',
  signed: 'user.attendance.actions.signed',
  upcoming: 'user.attendance.actions.upcoming',
  ended: 'user.attendance.actions.ended',
  paused: 'user.attendance.actions.paused',
  signIn: 'user.attendance.actions.signIn',
};

function AttendanceCard({
  period,
  account,
  assets,
  single = false,
}: {
  period: AttendancePeriod;
  account: string;
  assets: Asset[];
  single?: boolean;
}) {
  const { t } = useTranslation();
  const [expanded, setExpanded] = useState(false);
  const rewardsId = useId();
  const client = useQueryClient();
  const root = attendanceKeys.root(false, account);
  const operation = useRetainedOperation(
    async (input: { id: string; date: string }, key, context) => {
      const result = await signIn(input.id, input.date, key, context.signal);
      context.commit(() =>
        client.setQueriesData<AttendancePage>({ queryKey: root }, (page) =>
          page
            ? {
                ...page,
                items: page.items.map((item) =>
                  item.id === result.period.id ? result.period : item,
                ),
              }
            : page,
        ),
      );
      return result;
    },
    async (_input, error, context) => {
      context.assertCurrent();
      if (!error || responseOutcomeUnknown(error)) {
        void client.invalidateQueries({ queryKey: ['user', 'session'] });
        void client.invalidateQueries({ queryKey: creditHistoryKeys.account(account) });
        void client.invalidateQueries({ queryKey: rewardKeys.user(account) });
        void client.invalidateQueries({ queryKey: limitedActivityKeys.root(account) });
      }
      await client.invalidateQueries({ queryKey: root });
      context.assertCurrent();
    },
    root,
  );
  const unknown = operation.outcome === 'unknown';
  const count = period.mode === 'consecutive' ? period.current_streak : period.total_days;
  const maxDays = period.tiers.reduce((max, tier) => Math.max(max, tier.days), 0);
  const progressPercent = maxDays > 0 ? Math.min(100, Math.round((count / maxDays) * 100)) : 0;
  const awardedCount = period.tiers.filter((tier) => tier.awarded).length;
  const allAwarded = period.tiers.length > 0 && awardedCount === period.tiers.length;
  const nextTierDays =
    period.status === 'active' ? period.tiers.find((tier) => !tier.awarded)?.days : undefined;
  const milestones = period.tiers;
  const hasFeatured = milestones.some((tier) => tier.featured);
  const preview =
    !single && milestones.length > 4
      ? milestones.filter(
          (tier, index) =>
            tier.featured ||
            tier.days === nextTierDays ||
            (!hasFeatured && (index === 0 || index === milestones.length - 1)),
        )
      : milestones;
  const visibleTiers = expanded ? milestones : preview;
  const action = unknown
    ? 'retry'
    : period.signed_today
      ? 'signed'
      : period.status === 'upcoming'
        ? 'upcoming'
        : period.status === 'ended'
          ? 'ended'
          : !period.available
            ? 'paused'
            : 'signIn';
  return (
    <Panel
      className={single ? 'attendance-card attendance-card--single' : 'attendance-card'}
      aria-label={period.title}
    >
      <div className="attendance-card__layout">
        <div className="attendance-card__hero">
          <CampaignCover cover={period.cover} className="attendance-card__media" />
          <div className="attendance-card__intro">
            <div className="attendance-card__title-row">
              <h3>{period.title}</h3>
              <StatusBadge
                tone={
                  period.status === 'active'
                    ? 'ok'
                    : period.status === 'upcoming'
                      ? 'info'
                      : 'neutral'
                }
              >
                {t(statusLabels[period.status])}
              </StatusBadge>
            </div>
            <p className="attendance-card__dates">
              <time dateTime={period.start_date}>{period.start_date}</time>
              <span aria-hidden="true"> — </span>
              <time dateTime={period.end_date}>{period.end_date}</time>
              <span aria-hidden="true"> · </span>
              <span>
                {t('user.attendance.siteDate', {
                  date: period.site_date,
                  zone: formatOffset(period.timezone_offset_minutes * 60),
                })}
              </span>
            </p>
            {period.description ? (
              <div className="attendance-card__description">
                <MarkdownText>{period.description}</MarkdownText>
              </div>
            ) : null}
          </div>
        </div>
        <div className="attendance-card__main">
          <div className="attendance-progress">
            <div className="attendance-progress__top">
              <div className="attendance-progress__summary">
                <div className="attendance-progress__meta">
                  <span className="attendance-progress__label">{t(modeLabels[period.mode])}</span>
                  {period.mode === 'consecutive' ? (
                    <span className="attendance-progress__best">
                      {t(
                        period.best_streak === 1
                          ? 'user.attendance.best_one'
                          : 'user.attendance.best_other',
                        {
                          amount: formatCount(period.best_streak).display,
                        },
                      )}
                    </span>
                  ) : null}
                </div>
                <div className="attendance-progress__count">
                  <strong>
                    {t(count === 1 ? 'user.attendance.days_one' : 'user.attendance.days_other', {
                      amount: formatCount(count).display,
                    })}
                  </strong>
                  {maxDays > 0 ? (
                    <span className="attendance-progress__goal">
                      / {formatCount(maxDays).display}
                    </span>
                  ) : null}
                </div>
              </div>
              <Button
                type="button"
                variant="primary"
                disabled={operation.isPending || (!unknown && !period.can_sign_in)}
                onClick={() =>
                  operation.mutate(
                    unknown && operation.variables
                      ? operation.variables
                      : { id: period.id, date: period.site_date },
                  )
                }
              >
                {t(operation.isPending ? 'user.attendance.signing' : actionLabels[action])}
              </Button>
            </div>
            {maxDays > 0 ? (
              <div className="attendance-progress__bar" aria-hidden="true">
                <span
                  className={
                    allAwarded
                      ? 'attendance-progress__fill is-complete'
                      : 'attendance-progress__fill'
                  }
                  style={{ width: `${progressPercent}%` }}
                />
              </div>
            ) : null}
          </div>
          {unknown && (
            <OutcomeNote outcome={{ kind: 'unknown' }} body={t('user.attendance.retryHelp')} />
          )}
          {operation.error && !unknown ? <ErrorState error={operation.error} /> : null}
          {operation.outcome === 'refresh-failed' && (
            <OutcomeNote
              outcome={{ kind: 'savedRefreshFailed', recheck: () => void operation.refresh() }}
            />
          )}
          {operation.data && (
            <Note
              tone="ok"
              title={t('user.attendance.success', { date: operation.data.site_date })}
            >
              {operation.data.awards.length > 0 ? (
                <div className="reward-stack">
                  {operation.data.awards.map((award) => (
                    <div key={award.days}>
                      <MilestoneHeading
                        days={award.days}
                        featured={milestones.find((tier) => tier.days === award.days)?.featured}
                      />
                      <RewardSummary items={award.results} assets={assets} results />
                    </div>
                  ))}
                </div>
              ) : (
                t(
                  period.status === 'ended'
                    ? 'user.attendance.actions.ended'
                    : 'user.attendance.nextVisit',
                )
              )}
            </Note>
          )}
          <div className="attendance-tiers-block">
            <div className="attendance-tiers-head">
              <span>{t('user.attendance.rewards')}</span>
              {period.tiers.length > 0 ? (
                <span className="attendance-tiers-count">
                  {formatCount(awardedCount).display} / {formatCount(period.tiers.length).display}
                </span>
              ) : null}
            </div>
            <ol
              id={rewardsId}
              className="attendance-tiers"
              aria-label={t('user.attendance.rewards')}
            >
              {visibleTiers.map((tier) => {
                const isNext = tier.days === nextTierDays;
                const tierClass = [
                  'attendance-tier',
                  tier.featured ? 'attendance-milestone--featured' : '',
                  tier.awarded ? 'is-awarded' : '',
                  isNext ? 'is-next' : '',
                ]
                  .filter(Boolean)
                  .join(' ');
                return (
                  <li key={tier.days} className={tierClass}>
                    <div className="attendance-tier__head">
                      <MilestoneHeading days={tier.days} featured={tier.featured} />
                      <StatusBadge tone={tier.awarded ? 'ok' : isNext ? 'accent' : 'neutral'}>
                        {t(tier.awarded ? 'user.attendance.awarded' : 'user.attendance.notReached')}
                      </StatusBadge>
                    </div>
                    <RewardSummary
                      items={tier.awarded && tier.results ? tier.results : tier.rewards}
                      assets={assets}
                      results={tier.awarded && !!tier.results}
                    />
                  </li>
                );
              })}
            </ol>
            {preview.length < milestones.length && (
              <Button
                type="button"
                variant="ghost"
                className="attendance-tiers-toggle"
                aria-controls={rewardsId}
                aria-expanded={expanded}
                onClick={() => setExpanded(!expanded)}
              >
                {t(expanded ? 'attendanceDisplay.collapse' : 'attendanceDisplay.showAll', {
                  count: milestones.length,
                })}
              </Button>
            )}
          </div>
        </div>
      </div>
    </Panel>
  );
}

export function AttendanceSection({ account }: { account: string }) {
  const { t } = useTranslation();
  const { query, pager } = useAttendancePage(false, account);
  const assets = useQuery({
    queryKey: [...rewardKeys.user(account), 'assets'],
    queryFn: ({ signal }) => getAssets(false, signal),
    enabled: !!account,
  });
  const items = query.data?.items ?? [];
  const paused = query.data?.available === false;
  const showPager = !!query.data && (query.data.total > query.data.page_size || pager.page !== '1');
  return (
    <section className="attendance-section" aria-label={t('user.attendance.title')}>
      <SectionHead title={t('user.attendance.title')} description={t('user.attendance.intro')} />
      {query.isPending ? (
        <LoadingState />
      ) : query.error ? (
        <ErrorState error={query.error} onRetry={() => void query.refetch()} />
      ) : null}
      {assets.error && <ErrorState error={assets.error} onRetry={() => void assets.refetch()} />}
      {paused && items.length > 0 ? <Note tone="warn">{t('user.attendance.paused')}</Note> : null}
      {query.data && items.length === 0 ? (
        <EmptyState
          illustration={paused ? 'maintenance' : 'empty'}
          title={t('user.attendance.empty')}
          body={
            paused
              ? `${t('user.attendance.paused')} ${t('user.attendance.emptyBody')}`
              : t('user.attendance.emptyBody')
          }
        />
      ) : null}
      {items.length > 0 ? (
        <div
          className="attendance-grid"
          data-count={items.length === 1 ? 'single' : items.length === 2 ? 'pair' : 'multi'}
        >
          {items.map((period) => (
            <AttendanceCard
              key={account + ':' + period.id}
              period={period}
              account={account}
              assets={assets.data ?? []}
              single={items.length === 1}
            />
          ))}
        </div>
      ) : null}
      {query.data && showPager ? (
        <PagePagination
          metadata={attendanceMetadata(query.data)}
          requestedPage={pager.page}
          onPageChange={pager.setPage}
          onPageSizeChange={pager.setPageSize}
          busy={query.isFetching}
        />
      ) : null}
    </section>
  );
}
