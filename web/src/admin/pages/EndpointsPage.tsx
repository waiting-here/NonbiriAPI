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
  StatusBadge,
} from '@shared/components/States';
import { ADMIN_PAGE_SIZE, useAdminEndpoints } from '../data';

export function EndpointsPage() {
  const { t } = useTranslation();
  const endpoints = useAdminEndpoints();
  const [page, setPage] = useState(1);

  return (
    <div className="page">
      <PageHeader
        eyebrow={t('app.name')}
        title={t('admin.endpoints.title')}
        description={t('admin.endpoints.description')}
      />
      <Card>
        <div className="card-title-row">
          <h2>{t('admin.endpoints.listTitle')}</h2>
          {endpoints.data ? <span className="muted">{endpoints.data.length}</span> : null}
        </div>
        {endpoints.isPending ? (
          <LoadingState />
        ) : endpoints.error ? (
          <ErrorState error={endpoints.error} onRetry={() => void endpoints.refetch()} />
        ) : endpoints.data.length === 0 ? (
          <EmptyState title={t('admin.endpoints.empty')} body={t('admin.endpoints.emptyBody')} />
        ) : (
          <>
          <div className="table-wrap">
            <table>
              <caption>{t('admin.endpoints.listTitle')}</caption>
              <thead>
                <tr>
                  <th scope="col">{t('admin.endpoints.owner')}</th>
                  <th scope="col">{t('admin.endpoints.baseUrl')}</th>
                  <th scope="col">{t('admin.endpoints.connector')}</th>
                  <th scope="col">{t('admin.endpoints.keys')}</th>
                  <th scope="col">{t('admin.endpoints.status')}</th>
                  <th scope="col">{t('admin.endpoints.created')}</th>
                  <th scope="col">{t('admin.endpoints.updated')}</th>
                </tr>
              </thead>
              <tbody>
                {endpoints.data.slice((page - 1) * ADMIN_PAGE_SIZE, page * ADMIN_PAGE_SIZE).map((endpoint) => (
                  <tr key={endpoint.id}>
                    <td><ReadOnlyValue value={endpoint.user_id} /></td>
                    <td>
                      <span className="mono">{endpoint.base_url}</span>
                      {endpoint.note ? <span className="table-note">{endpoint.note}</span> : null}
                    </td>
                    <td>{endpoint.connector_type}</td>
                    <td>{endpoint.key_count}</td>
                    <td><StatusBadge active={endpoint.enabled} /></td>
                    <td><ReadOnlyValue value={endpoint.created_at} /></td>
                    <td><ReadOnlyValue value={endpoint.updated_at} /></td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <Pagination
            page={page}
            hasNext={page * ADMIN_PAGE_SIZE < endpoints.data.length}
            onChange={setPage}
          />
          </>
        )}
      </Card>
    </div>
  );
}
