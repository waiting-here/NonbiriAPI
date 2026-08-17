import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Card,
  EmptyState,
  ErrorState,
  LoadingState,
  PageHeader,
  Pagination,
  ReadOnlyValue,
} from '@shared/components/States';
import { useAdminLogs, useAdminUsage } from '../data';

function number(value: number): string {
  return value.toLocaleString();
}

function usageText(value: number | undefined): string {
  return value === undefined ? '—' : number(value);
}

export function LogsPage() {
  const { t } = useTranslation();
  const [page, setPage] = useState(1);
  const usage = useAdminUsage();
  const logs = useAdminLogs(page);

  return (
    <div className="page">
      <PageHeader
        eyebrow={t('app.name')}
        title={t('admin.logs.title')}
        description={t('admin.logs.description')}
      />
      <Card>
        <div className="card-title-row">
          <h2>{t('admin.logs.usageTitle')}</h2>
        </div>
        {usage.isPending ? (
          <LoadingState />
        ) : usage.error ? (
          <ErrorState error={usage.error} onRetry={() => void usage.refetch()} />
        ) : (
          <div className="metric-grid">
            <div className="metric-card">
              <p>{t('admin.dashboard.requests')}</p>
              <strong className="metric-value">{number(usage.data.total_requests)}</strong>
            </div>
            <div className="metric-card">
              <p>{t('admin.dashboard.promptTokens')}</p>
              <strong className="metric-value">{number(usage.data.total_prompt_tokens)}</strong>
            </div>
            <div className="metric-card">
              <p>{t('admin.dashboard.completionTokens')}</p>
              <strong className="metric-value">{number(usage.data.total_completion_tokens)}</strong>
            </div>
            <div className="metric-card">
              <p>{t('admin.dashboard.unknownUsage')}</p>
              <strong className="metric-value">{number(usage.data.total_unknown_usage_requests)}</strong>
            </div>
          </div>
        )}
      </Card>
      <Card>
        <div className="card-title-row">
          <h2>{t('admin.logs.logsTitle')}</h2>
          <span className="muted">{t('common.page', { page })}</span>
        </div>
        {logs.isPending ? <LoadingState /> : logs.error ? <ErrorState error={logs.error} onRetry={() => void logs.refetch()} /> : logs.data.items.length === 0 ? (
          <EmptyState title={t('admin.logs.noLogs')} body={t('admin.logs.noLogsBody')} />
        ) : (
          <>
            <div className="table-wrap">
              <table>
                <caption>{t('admin.logs.logsTitle')}</caption>
                <thead>
                  <tr>
                    <th scope="col">{t('admin.logs.id')}</th>
                    <th scope="col">{t('admin.logs.user')}</th>
                    <th scope="col">{t('admin.logs.model')}</th>
                    <th scope="col">{t('admin.logs.statusCode')}</th>
                    <th scope="col">{t('admin.logs.duration')}</th>
                    <th scope="col">{t('admin.logs.usage')}</th>
                    <th scope="col">{t('admin.logs.diagnostic')}</th>
                    <th scope="col">{t('admin.logs.started')}</th>
                  </tr>
                </thead>
                <tbody>
                  {logs.data.items.map((log) => (
                    <tr key={log.id}>
                      <td><ReadOnlyValue value={log.id} /></td>
                      <td><ReadOnlyValue value={log.user_id} /></td>
                      <td>
                        <span className="mono">{log.model}</span>
                        <span className="table-note">{log.upstream_model_id}</span>
                      </td>
                      <td>{log.status_code || '—'}</td>
                      <td>{log.duration_ms} ms</td>
                      <td>
                        {log.usage_unknown ? (
                          <span className="status-badge is-inactive">{t('admin.logs.unknownUsage')}</span>
                        ) : (
                          <span className="table-note">
                            {t('admin.logs.promptTokens')}: {usageText(log.prompt_tokens)}
                            <br />
                            {t('admin.logs.completionTokens')}: {usageText(log.completion_tokens)}
                            <br />
                            {t('admin.logs.totalTokens')}: {usageText(log.total_tokens)}
                          </span>
                        )}
                      </td>
                      <td>
                        {log.error_code || log.error_diag ? (
                          <details className="diagnostic">
                            <summary>{log.error_code || t('common.showDetails')}</summary>
                            {log.error_diag ? <p>{log.error_diag}</p> : null}
                          </details>
                        ) : (
                          '—'
                        )}
                      </td>
                      <td><ReadOnlyValue value={log.started_at} /></td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <Pagination page={page} hasNext={logs.data.hasNext} onChange={setPage} />
          </>
        )}
      </Card>
    </div>
  );
}
