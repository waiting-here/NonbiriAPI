import { Link, useLocation, useNavigate } from 'react-router';
import { KeyLimitSummary } from '@shared/components/KeyRoutingLimits';
import { PagePagination } from '@shared/operations/PagePagination';
import { useUrlPagePager } from '@shared/operations/useUrlPagePager';
import { useKeyBindingsPage } from './browseData';
import { CoreErrorPanel, CoreLoading, StatusPill } from './components';
import { useCoreCopy } from './copy';
import type { Endpoint, EndpointKey, KeyBindingView, Model } from './types';

export function BindingPreviewList({
  items,
  showSource = false,
}: {
  items: readonly KeyBindingView[];
  showSource?: boolean;
}) {
  const { t } = useCoreCopy();
  return (
    <ul className="core-routing-list">
      {items.map((item) => (
        <li key={item.id}>
          <div>
            <Link
              className="core-mono"
              to={`/models?model_id=${encodeURIComponent(item.model_id)}`}
            >
              {item.model_full_name}
            </Link>
            <span className="core-mono"> → {item.upstream_model_id}</span>
            {showSource ? (
              <>
                <div className="core-muted">
                  {item.endpoint_note || item.endpoint_base_url} ·{' '}
                  {item.key_note || `${item.display_head}…${item.display_tail}`}
                </div>
                <KeyLimitSummary concurrency={item.max_concurrency} rpm={item.max_rpm} />
              </>
            ) : null}
          </div>
          <StatusPill tone={item.state === 'available' ? 'success' : 'warning'}>
            {t(`browse.state.${item.state}`)}
          </StatusPill>
        </li>
      ))}
    </ul>
  );
}

export function EndpointBrowseSummary({ endpoint }: { endpoint: Endpoint }) {
  const { t } = useCoreCopy();
  const summary = endpoint.browse;
  return (
    <div className="core-resource-summary">
      <StatusPill tone={summary?.state === 'available' ? 'success' : 'warning'}>
        {summary ? t(`browse.endpoint.${summary.state}`) : t('common.unknown')}
      </StatusPill>
      <span>{t('browse.keyCount', { count: endpoint.key_count })}</span>
      <span>{t('browse.modelCount', { count: summary?.model_count ?? '—' })}</span>
    </div>
  );
}

export function ModelBrowseSummary({ model }: { model: Model }) {
  const { t } = useCoreCopy();
  const summary = model.browse;
  return (
    <div>
      <p
        className={
          summary && summary.available_binding_count !== '0'
            ? 'core-inline-success'
            : 'core-inline-warning'
        }
      >
        {summary
          ? t('browse.availableConnections', {
              available: summary.available_binding_count,
              total: model.binding_count,
            })
          : t('endpoints.routingUnknown')}
        {model.binding_count === '0' ? ` · ${t('browse.noConnections')}` : null}
      </p>
      {summary && summary.preview.length > 0 ? (
        <BindingPreviewList items={summary.preview} showSource />
      ) : null}
    </div>
  );
}

function KeyBindingPages({
  accountId,
  endpointId,
  keyId,
}: {
  accountId: string;
  endpointId: string;
  keyId: string;
}) {
  const pager = useUrlPagePager({
    station: 'user',
    listType: 'key-bindings',
    scopeKey: `${accountId}:${endpointId}:${keyId}`,
    scopeReady: Boolean(accountId),
    pageParam: `routes_${keyId}_page`,
    pageSizeParam: `routes_${keyId}_page_size`,
  });
  const query = useKeyBindingsPage(accountId, endpointId, keyId, {
    page: pager.page,
    pageSize: pager.pageSize,
  });
  if (query.error)
    return <CoreErrorPanel compact error={query.error} onRetry={() => void query.refetch()} />;
  if (!query.data) return <CoreLoading compact />;
  return (
    <div aria-busy={query.isFetching}>
      <BindingPreviewList items={query.data.data} />
      <PagePagination
        metadata={query.data.pagination}
        requestedPage={pager.page}
        busy={query.isFetching}
        onPageChange={pager.setPage}
        onPageSizeChange={pager.setPageSize}
      />
    </div>
  );
}

export function KeyBrowseSummary({
  accountId,
  endpointId,
  keyData,
  onRefresh,
}: {
  accountId: string;
  endpointId: string;
  keyData: EndpointKey;
  onRefresh: () => void;
}) {
  const { t } = useCoreCopy();
  const location = useLocation();
  const navigate = useNavigate();
  const open = new URLSearchParams(location.search).has(`routes_${keyData.id}_page`);
  const summary = keyData.browse;
  if (!summary)
    return (
      <div>
        <p className="core-inline-warning">{t('endpoints.routingUnknown')}</p>
        <button type="button" className="btn btn-secondary" onClick={onRefresh}>
          {t('common.refresh')}
        </button>
      </div>
    );
  return (
    <section className="core-card">
      <div className="core-card__header">
        <h3>{t('endpoints.routing')}</h3>
      </div>
      <p>
        {t('browse.modelCount', { count: summary.model_count })} ·{' '}
        {t('browse.availableConnections', {
          available: summary.available_binding_count,
          total: summary.binding_count,
        })}
      </p>
      {summary.binding_count === '0' ? (
        <p className="core-muted">{t('endpoints.routingNone')}</p>
      ) : (
        <>
          {!open ? <BindingPreviewList items={summary.preview} /> : null}
          <button
            type="button"
            className="btn btn-secondary"
            aria-expanded={open}
            onClick={() => {
              const params = new URLSearchParams(location.search);
              if (open) {
                params.delete(`routes_${keyData.id}_page`);
                params.delete(`routes_${keyData.id}_page_size`);
              } else {
                params.set(`routes_${keyData.id}_page`, '1');
              }
              navigate(
                { pathname: location.pathname, search: params.toString() },
                { state: location.state },
              );
            }}
          >
            {open
              ? t('common.close')
              : t('browse.allConnections', { count: summary.binding_count })}
          </button>
          {open ? (
            <KeyBindingPages accountId={accountId} endpointId={endpointId} keyId={keyData.id} />
          ) : null}
        </>
      )}
    </section>
  );
}
