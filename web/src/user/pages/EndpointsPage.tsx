import { useState } from 'react';
import { Link, useParams } from 'react-router';
import { PageHeader } from '@shared/components/States';
import {
  ConnectorLabel,
  CoreEmpty,
  CoreErrorPanel,
  CoreLoading,
  CoreTime,
  CoreUserGate,
  SafeCopyValue,
  StatusPill,
} from '../features/core/components';
import { EndpointDetail } from '../features/core/EndpointDetail';
import { EndpointWizard } from '../features/core/EndpointWizard';
import { useCoreCopy } from '../features/core/copy';
import { CORE_ROUTE_PATHS } from '../features/core/descriptors';
import { useEndpointsPage } from '../features/core/queries';
import type { UserProfile } from '../features/core/types';
import '../features/core/core.css';

function EndpointList({ user }: { user: UserProfile }) {
  const { t } = useCoreCopy();
  const [cursorStack, setCursorStack] = useState<Array<string | undefined>>([undefined]);
  const [creating, setCreating] = useState(false);
  const cursor = cursorStack.at(-1);
  const endpoints = useEndpointsPage(user.id, cursor);

  return (
    <div className="page core-page core-stack">
      <PageHeader
        icon="resources"
        title={t('endpoints.title')}
        description={t('endpoints.description')}
        actions={
          <button type="button" className="btn btn-primary" onClick={() => setCreating(true)}>
            {t('endpoints.create')}
          </button>
        }
      />

      {creating ? (
        <EndpointWizard
          accountId={user.id}
          onClose={() => setCreating(false)}
          onCreated={() => void endpoints.refetch()}
        />
      ) : null}

      {endpoints.isPending ? (
        <CoreLoading />
      ) : endpoints.error ? (
        <CoreErrorPanel error={endpoints.error} onRetry={() => void endpoints.refetch()} />
      ) : endpoints.data.data.length === 0 && cursorStack.length === 1 ? (
        <CoreEmpty
          title={t('endpoints.emptyTitle')}
          body={t('endpoints.emptyBody')}
          action={
            <button type="button" className="btn btn-primary" onClick={() => setCreating(true)}>
              {t('endpoints.create')}
            </button>
          }
        />
      ) : (
        <section className="core-card">
          <ul className="core-endpoint-list">
            {endpoints.data.data.map((endpoint) => (
              <li key={endpoint.id} className="core-endpoint-card">
                <div className="core-endpoint-card__top">
                  <div>
                    <strong>
                      <ConnectorLabel value={endpoint.connector_type} />
                    </strong>
                  </div>
                  <StatusPill tone={endpoint.enabled ? 'success' : 'neutral'}>
                    {endpoint.enabled ? t('common.enabled') : t('common.disabled')}
                  </StatusPill>
                </div>
                <dl className="core-detail-list">
                  <div>
                    <dt>{t('endpoints.baseUrl')}</dt>
                    <dd>
                      <SafeCopyValue value={endpoint.base_url} label={t('endpoints.baseUrl')} />
                    </dd>
                  </div>
                  <div>
                    <dt>{t('endpoints.note')}</dt>
                    <dd>{endpoint.note || t('common.notSet')}</dd>
                  </div>
                  <div>
                    <dt>{t('endpoints.keyCount')}</dt>
                    <dd className="core-number">{endpoint.key_count}</dd>
                  </div>
                  <div>
                    <dt>{t('common.updated')}</dt>
                    <dd>
                      <CoreTime value={endpoint.updated_at} />
                    </dd>
                  </div>
                </dl>
                <div className="core-row-actions">
                  <span />
                  <Link
                    className="btn btn-secondary"
                    to={CORE_ROUTE_PATHS.endpointDetail(endpoint.id)}
                  >
                    {t('endpoints.manage')}
                  </Link>
                </div>
              </li>
            ))}
          </ul>
          {cursorStack.length > 1 || endpoints.data.next_cursor ? (
            <nav className="core-pagination" aria-label={t('endpoints.title')}>
              <button
                type="button"
                className="btn btn-secondary"
                disabled={cursorStack.length <= 1}
                onClick={() => setCursorStack((current) => current.slice(0, -1))}
              >
                {t('common.previous')}
              </button>
              <button
                type="button"
                className="btn btn-secondary"
                disabled={!endpoints.data.next_cursor}
                onClick={() => {
                  if (endpoints.data.next_cursor) {
                    setCursorStack((current) => [
                      ...current,
                      endpoints.data.next_cursor ?? undefined,
                    ]);
                  }
                }}
              >
                {t('common.next')}
              </button>
            </nav>
          ) : null}
        </section>
      )}
    </div>
  );
}

export function EndpointsPage() {
  const { endpointId } = useParams<{ endpointId?: string }>();
  return (
    <CoreUserGate>
      {(user) =>
        endpointId ? (
          <EndpointDetail
            key={`${user.id}:${endpointId}`}
            accountId={user.id}
            endpointId={endpointId}
          />
        ) : (
          <EndpointList key={user.id} user={user} />
        )
      }
    </CoreUserGate>
  );
}
