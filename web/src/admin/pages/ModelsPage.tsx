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
import { ADMIN_PAGE_SIZE, useAdminModels } from '../data';

export function ModelsPage() {
  const { t } = useTranslation();
  const models = useAdminModels();
  const [page, setPage] = useState(1);

  return (
    <div className="page">
      <PageHeader
        eyebrow={t('app.name')}
        title={t('admin.models.title')}
        description={t('admin.models.description')}
      />
      <Card>
        <div className="card-title-row">
          <h2>{t('admin.models.listTitle')}</h2>
          {models.data ? <span className="muted">{models.data.length}</span> : null}
        </div>
        {models.isPending ? (
          <LoadingState />
        ) : models.error ? (
          <ErrorState error={models.error} onRetry={() => void models.refetch()} />
        ) : models.data.length === 0 ? (
          <EmptyState title={t('admin.models.empty')} body={t('admin.models.emptyBody')} />
        ) : (
          <>
          <div className="table-wrap">
            <table>
              <caption>{t('admin.models.listTitle')}</caption>
              <thead>
                <tr>
                  <th scope="col">{t('admin.models.owner')}</th>
                  <th scope="col">{t('admin.models.fullName')}</th>
                  <th scope="col">{t('admin.models.strategy')}</th>
                  <th scope="col">{t('admin.models.bindings')}</th>
                  <th scope="col">{t('admin.models.created')}</th>
                </tr>
              </thead>
              <tbody>
                {models.data.slice((page - 1) * ADMIN_PAGE_SIZE, page * ADMIN_PAGE_SIZE).map((model) => (
                  <tr key={model.id}>
                    <td><ReadOnlyValue value={model.user_id} /></td>
                    <td>
                      <strong className="mono">{model.full_name}</strong>
                      <span className="table-note">{model.provider} / {model.model}</span>
                    </td>
                    <td>
                      <StatusBadge
                        active={model.route_strategy === 'ordered'}
                        label={t(`admin.models.${model.route_strategy}`)}
                      />
                    </td>
                    <td>{model.binding_count}</td>
                    <td><ReadOnlyValue value={model.created_at} /></td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <Pagination
            page={page}
            hasNext={page * ADMIN_PAGE_SIZE < models.data.length}
            onChange={setPage}
          />
          </>
        )}
      </Card>
    </div>
  );
}
