import { useTranslation } from 'react-i18next';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { SectionHead, EmptyState, ErrorState, LoadingState } from '@shared/components/States';
import {
  Button,
  Note,
  OutcomeNote,
  Panel,
  PanelBody,
  PanelHead,
  StatusBadge,
} from '@shared/components/ui';
import { MarkdownText } from '@shared/components/MarkdownText';
import { PagePagination } from '@shared/operations/PagePagination';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { formatCount } from '@shared/utils/formatNumber';
import { formatOffset } from '@shared/time';
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
}: {
  period: AttendancePeriod;
  account: string;
  assets: Asset[];
}) {
  const { t } = useTranslation();
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
      await client.invalidateQueries({ queryKey: root });
      context.assertCurrent();
      if (!error) {
        void client.invalidateQueries({ queryKey: ['user', 'session'] });
        void client.invalidateQueries({ queryKey: ['user', 'credits'] });
        void client.invalidateQueries({ queryKey: rewardKeys.user(account) });
        void client.invalidateQueries({ queryKey: ['user', 'limited-activities', account] });
      }
    },
    root,
  );
  const unknown = operation.outcome === 'unknown';
  const count = period.mode === 'consecutive' ? period.current_streak : period.total_days;
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
    <Panel className="attendance-card" aria-label={period.title}>
      <div className="attendance-card__header">
        <PanelHead
          level={3}
          title={period.title}
          description={
            <StatusBadge tone={period.status === 'active' ? 'ok' : 'neutral'}>
              {t(statusLabels[period.status])}
            </StatusBadge>
          }
        />
        <img
          className="attendance-card__art"
          src="/assets/gacha/groups/a.webp"
          alt=""
          width={1672}
          height={941}
          loading="lazy"
        />
      </div>
      <PanelBody>
        <div className="attendance-card__content">
          <p className="attendance-card__dates">
            <time dateTime={period.start_date}>{period.start_date}</time>
            <span aria-hidden="true"> — </span>
            <time dateTime={period.end_date}>{period.end_date}</time>
          </p>
          {period.description && <MarkdownText>{period.description}</MarkdownText>}
          <div className="attendance-progress">
            <div>
              <span className="attendance-progress__label">{t(modeLabels[period.mode])}</span>
              <strong>
                {t(count === 1 ? 'user.attendance.days_one' : 'user.attendance.days_other', {
                  amount: formatCount(count).display,
                })}
              </strong>
            </div>
            {period.mode === 'consecutive' && (
              <p>
                {t(
                  period.best_streak === 1
                    ? 'user.attendance.best_one'
                    : 'user.attendance.best_other',
                  {
                    amount: formatCount(period.best_streak).display,
                  },
                )}
              </p>
            )}
          </div>
          <p className="attendance-card__today">
            {t('user.attendance.siteDate', {
              date: period.site_date,
              zone: formatOffset(period.timezone_offset_minutes * 60),
            })}
          </p>
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
                      <strong>
                        {t('user.attendance.tierDays', { amount: formatCount(award.days).display })}
                      </strong>
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
          <ol className="attendance-tiers" aria-label={t('user.attendance.rewards')}>
            {period.tiers.map((tier) => (
              <li
                key={tier.days}
                className={tier.awarded ? 'attendance-tier is-awarded' : 'attendance-tier'}
              >
                <div className="attendance-tier__head">
                  <strong>
                    {t('user.attendance.tierDays', { amount: formatCount(tier.days).display })}
                  </strong>
                  <StatusBadge tone={tier.awarded ? 'ok' : 'neutral'}>
                    {t(tier.awarded ? 'user.attendance.awarded' : 'user.attendance.notReached')}
                  </StatusBadge>
                </div>
                <RewardSummary
                  items={tier.awarded && tier.results ? tier.results : tier.rewards}
                  assets={assets}
                  results={tier.awarded && !!tier.results}
                />
              </li>
            ))}
          </ol>
        </div>
      </PanelBody>
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
  return (
    <section className="attendance-section" aria-label={t('user.attendance.title')}>
      <SectionHead title={t('user.attendance.title')} description={t('user.attendance.intro')} />
      {query.isPending ? (
        <LoadingState />
      ) : query.error ? (
        <ErrorState error={query.error} onRetry={() => void query.refetch()} />
      ) : null}
      {assets.error && <ErrorState error={assets.error} onRetry={() => void assets.refetch()} />}
      {query.data?.available === false && <Note tone="warn">{t('user.attendance.paused')}</Note>}
      {query.data?.items.length === 0 && (
        <EmptyState title={t('user.attendance.empty')} body={t('user.attendance.emptyBody')} />
      )}
      <div className="attendance-grid">
        {query.data?.items.map((period) => (
          <AttendanceCard
            key={account + ':' + period.id}
            period={period}
            account={account}
            assets={assets.data ?? []}
          />
        ))}
      </div>
      {query.data && (
        <PagePagination
          metadata={attendanceMetadata(query.data)}
          requestedPage={pager.page}
          onPageChange={pager.setPage}
          onPageSizeChange={pager.setPageSize}
          busy={query.isFetching}
        />
      )}
    </section>
  );
}
