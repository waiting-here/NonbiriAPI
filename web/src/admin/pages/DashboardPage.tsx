import { Link } from 'react-router';
import { useTranslation } from 'react-i18next';
import {
  Card,
  EmptyState,
  ErrorState,
  LoadingState,
  PageHeader,
} from '@shared/components/States';
import { useAdminEndpoints, useAdminModels, useAdminUsage } from '../data';

function number(value: number): string {
  return value.toLocaleString();
}

function UsageMetric({ label, value }: { label: string; value: number }) {
  return (
    <div className="metric-card">
      <p>{label}</p>
      <strong className="metric-value">{number(value)}</strong>
    </div>
  );
}

function OverviewCard({
  title,
  count,
  loading,
  error,
  emptyTitle,
  emptyBody,
  link,
  linkLabel,
}: {
  title: string;
  count: number | undefined;
  loading: boolean;
  error: unknown;
  emptyTitle: string;
  emptyBody: string;
  link: string;
  linkLabel: string;
}) {
  return (
    <Card>
      <div className="card-title-row">
        <h2>{title}</h2>
        {count !== undefined ? <strong className="metric-inline">{number(count)}</strong> : null}
      </div>
      {loading ? <LoadingState /> : error ? <ErrorState error={error} /> : count === 0 ? <EmptyState title={emptyTitle} body={emptyBody} /> : null}
      <div className="form-actions">
        <Link className="btn btn-secondary" to={link}>
          {linkLabel}
        </Link>
      </div>
    </Card>
  );
}

export function DashboardPage() {
  const { t } = useTranslation();
  const usage = useAdminUsage();
  const endpoints = useAdminEndpoints();
  const models = useAdminModels();

  return (
    <div className="page">
      <PageHeader
        eyebrow={t('app.name')}
        title={t('admin.dashboard.title')}
        description={t('admin.dashboard.description')}
      />

      <section aria-labelledby="dashboard-usage-title">
        <h2 id="dashboard-usage-title" className="section-title">
          {t('admin.dashboard.usageTitle')}
        </h2>
        {usage.isPending ? (
          <LoadingState />
        ) : usage.error ? (
          <ErrorState error={usage.error} onRetry={() => void usage.refetch()} />
        ) : (
          <div className="metric-grid">
            <UsageMetric label={t('admin.dashboard.requests')} value={usage.data.total_requests} />
            <UsageMetric label={t('admin.dashboard.promptTokens')} value={usage.data.total_prompt_tokens} />
            <UsageMetric
              label={t('admin.dashboard.completionTokens')}
              value={usage.data.total_completion_tokens}
            />
            <UsageMetric
              label={t('admin.dashboard.unknownUsage')}
              value={usage.data.total_unknown_usage_requests}
            />
          </div>
        )}
      </section>

      <div className="split-grid">
        <OverviewCard
          title={t('admin.dashboard.endpointsTitle')}
          count={endpoints.data?.length}
          loading={endpoints.isPending}
          error={endpoints.error}
          emptyTitle={t('admin.endpoints.empty')}
          emptyBody={t('admin.endpoints.emptyBody')}
          link="/endpoints"
          linkLabel={t('admin.dashboard.viewEndpoints')}
        />
        <OverviewCard
          title={t('admin.dashboard.modelsTitle')}
          count={models.data?.length}
          loading={models.isPending}
          error={models.error}
          emptyTitle={t('admin.models.empty')}
          emptyBody={t('admin.models.emptyBody')}
          link="/models"
          linkLabel={t('admin.dashboard.viewModels')}
        />
      </div>
    </div>
  );
}
