import { useState } from 'react';
import { formatDateTime } from '@shared/utils/datetime';
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
import { type AdminLogFilter, useAdminLogs, useAdminUsage } from '../data';
import { CompactNumber } from '@shared/components/CompactNumber';
import { formatCompact, formatCount, type FormattedNumber } from '@shared/utils/formatNumber';

const PAGE_SIZE_OPTIONS = [10, 20, 50] as const;

/** Convert a datetime-local field value to unix seconds; empty means unset. */
function datetimeLocalToUnix(value: string): number | undefined {
  if (!value.trim()) return undefined;
  const millis = Date.parse(value);
  if (!Number.isFinite(millis)) return undefined;
  return Math.max(0, Math.floor(millis / 1000));
}

function usageValue(value: number | undefined): FormattedNumber {
  return value === undefined ? { display: '—', exact: '—', abbreviated: false } : formatCompact(value);
}

export function LogsPage() {
  const { t } = useTranslation();
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState<number>(PAGE_SIZE_OPTIONS[1]);
  // Draft inputs feed appliedFilter only via Apply; a changed filter always
  // restarts offset paging at page 1 rather than mixing pages across filters.
  const [draftUserId, setDraftUserId] = useState('');
  const [draftModel, setDraftModel] = useState('');
  const [draftStatus, setDraftStatus] = useState('');
  const [draftFrom, setDraftFrom] = useState('');
  const [draftTo, setDraftTo] = useState('');
  const [appliedFilter, setAppliedFilter] = useState<AdminLogFilter>({});
  const usage = useAdminUsage();
  const logs = useAdminLogs(page, appliedFilter, pageSize);

  const applyFilter = () => {
    const next: AdminLogFilter = {};
    const userId = draftUserId.trim();
    if (userId && /^\d+$/.test(userId)) next.userId = userId;
    const model = draftModel.trim();
    if (model) next.model = model.slice(0, 160);
    // The server status filter is one exact 100..599 code, so the field is a
    // numeric input, not a band; a band would silently drop other codes in
    // the same range.
    const status = draftStatus.trim();
    if (status && /^\d{3}$/.test(status)) {
      const code = Number(status);
      if (code >= 100 && code <= 599) next.status = status;
    }
    const fromUnix = datetimeLocalToUnix(draftFrom);
    const toUnix = datetimeLocalToUnix(draftTo);
    if (fromUnix !== undefined) next.fromUnix = fromUnix;
    if (toUnix !== undefined) next.toUnix = toUnix;
    setAppliedFilter(next);
    setPage(1);
  };

  const resetFilter = () => {
    setDraftUserId('');
    setDraftModel('');
    setDraftStatus('');
    setDraftFrom('');
    setDraftTo('');
    setAppliedFilter({});
    setPage(1);
  };

  const changePageSize = (size: number) => {
    setPageSize(size);
    setPage(1);
  };

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
              <strong className="metric-value">{formatCount(usage.data.total_requests).display}</strong>
            </div>
            <div className="metric-card">
              <p>{t('common.tokens.input')}</p>
              <strong className="metric-value">
                <CompactNumber value={formatCompact(usage.data.total_prompt_tokens)} />
              </strong>
            </div>
            <div className="metric-card">
              <p>{t('common.tokens.output')}</p>
              <strong className="metric-value">
                <CompactNumber value={formatCompact(usage.data.total_completion_tokens)} />
              </strong>
            </div>
            <div className="metric-card">
              <p>{t('admin.dashboard.unknownUsage')}</p>
              <strong className="metric-value">
                {formatCount(usage.data.total_unknown_usage_requests).display}
              </strong>
            </div>
          </div>
        )}
      </Card>
      <Card>
        <div className="card-title-row">
          <h2>{t('admin.logs.logsTitle')}</h2>
          <span className="muted">{t('common.page', { page })}</span>
        </div>
        <form
          className="filter-bar"
          onSubmit={(event) => {
            event.preventDefault();
            applyFilter();
          }}
        >
          <label>
            <span>{t('common.userId')}</span>
            <input
              type="number"
              min="1"
              step="1"
              inputMode="numeric"
              value={draftUserId}
              onChange={(event) => setDraftUserId(event.target.value)}
              aria-label={t('common.filterUserIdAria')}
            />
          </label>
          <label>
            <span>{t('common.model')}</span>
            <input
              type="text"
              value={draftModel}
              maxLength={160}
              onChange={(event) => setDraftModel(event.target.value)}
              aria-label={t('common.filterModelAria')}
            />
          </label>
          <label>
            <span>{t('common.status')}</span>
            <input
              type="number"
              min="100"
              max="599"
              step="1"
              value={draftStatus}
              onChange={(event) => setDraftStatus(event.target.value)}
              aria-label={t('common.filterStatusAria')}
            />
          </label>
          <label>
            <span>{t('common.from')}</span>
            <input
              type="datetime-local"
              value={draftFrom}
              onChange={(event) => setDraftFrom(event.target.value)}
              aria-label={t('common.filterFromAria')}
            />
          </label>
          <label>
            <span>{t('common.to')}</span>
            <input
              type="datetime-local"
              value={draftTo}
              onChange={(event) => setDraftTo(event.target.value)}
              aria-label={t('common.filterToAria')}
            />
          </label>
          <div className="filter-actions">
            <button type="submit" className="btn btn-quiet">
              {t('common.applyFilter')}
            </button>
            <button type="button" className="btn btn-link" onClick={resetFilter}>
              {t('common.resetFilter')}
            </button>
          </div>
        </form>
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
                            {t('common.tokens.inputShort')}: <CompactNumber value={usageValue(log.prompt_tokens)} />
                            <br />
                            {t('common.tokens.outputShort')}: <CompactNumber value={usageValue(log.completion_tokens)} />
                            <br />
                            {t('common.tokens.totalShort')}: <CompactNumber value={usageValue(log.total_tokens)} />
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
                      <td><ReadOnlyValue value={formatDateTime(log.started_at)} /></td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <Pagination
              page={page}
              hasNext={logs.data.hasNext}
              onChange={setPage}
              pageSize={pageSize}
              pageSizeOptions={PAGE_SIZE_OPTIONS}
              onPageSizeChange={changePageSize}
              onJumpToPage={setPage}
            />
          </>
        )}
      </Card>
    </div>
  );
}
