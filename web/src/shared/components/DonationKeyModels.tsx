import { useEffect } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useLocation } from 'react-router';
import { useTranslation } from 'react-i18next';
import { charityKeys, type CharityRole } from '@shared/operations/charity';
import {
  getDonationKeyModels,
  getDonationKeyModelBindings,
} from '@shared/operations/donationKeyModels';
import { useSearchState } from '@shared/operations/useSearchState';
import { useUrlPagePager } from '@shared/operations/useUrlPagePager';
import { PagePagination } from '@shared/operations/PagePagination';
import { managementResourceID } from '@shared/operations/charityModelPages';
import { isForbidden, isUnauthorized } from '@shared/query/http';
import { EmptyState, ErrorState, LoadingState } from './States';
import { useCharityModelScope } from './charityModelScopeContext';

interface Props {
  role: CharityRole;
  accountId: string;
  donationId: string;
  keyId: string;
  onCapabilityLoss?: () => void;
}

const bindingStateCopy = {
  available: 'common.keyModels.states.available',
  unavailable: 'common.keyModels.states.unavailable',
  ended: 'common.keyModels.states.ended',
  expired: 'common.keyModels.states.expired',
  pending: 'common.keyModels.states.pending',
  feature_disabled: 'common.keyModels.states.feature_disabled',
  model_disabled: 'common.keyModels.states.model_disabled',
  disabled: 'common.keyModels.states.disabled',
  suspended: 'common.keyModels.states.suspended',
} as const;

export function DonationKeyModels(props: Props) {
  const { t } = useTranslation();
  const [params, setParams] = useSearchState();
  const name = `key_models_${props.keyId}`;
  const open = params.get(name) === 'open';
  return (
    <section className="ops-subcard">
      <button
        type="button"
        className="btn btn-secondary"
        aria-expanded={open}
        onClick={() =>
          setParams((previous) => {
            const next = new URLSearchParams(previous);
            if (open) next.delete(name);
            else next.set(name, 'open');
            return next;
          })
        }
      >
        {t('common.keyModels.title')}
      </button>
      {open ? <KeyModelPages {...props} prefix={name} /> : null}
    </section>
  );
}

function KeyModelPages({
  role,
  accountId,
  donationId,
  keyId,
  onCapabilityLoss,
  prefix,
}: Props & { prefix: string }) {
  const charityModelID = useCharityModelScope();
  const { t } = useTranslation();
  const location = useLocation();
  const [params, setParams] = useSearchState();
  const expanded = params.get(`${prefix}_expanded`) ?? '';
  const pager = useUrlPagePager({
    station: role === 'admin' ? 'admin' : 'user',
    listType: 'donation-key-models',
    scopeKey: `${accountId}:${donationId}:${keyId}`,
    pageParam: `${prefix}_page`,
    pageSizeParam: `${prefix}_page_size`,
  });
  const query = useQuery({
    queryKey: [
      ...charityKeys.root(role),
      'key-models',
      charityModelID,
      accountId,
      donationId,
      keyId,
      pager.page,
      pager.pageSize,
    ],
    queryFn: ({ signal }) =>
      getDonationKeyModels(
        role,
        donationId,
        keyId,
        pager.page,
        pager.pageSize,
        signal,
        charityModelID,
      ),
    retry: false,
  });
  const lost = isForbidden(query.error) || isUnauthorized(query.error);
  useEffect(() => {
    if (lost) onCapabilityLoss?.();
  }, [lost, onCapabilityLoss]);
  useEffect(() => {
    const state = location.state as {
      restoreDonationKey?: string;
      donationKeyScroll?: number;
    } | null;
    if (state?.restoreDonationKey !== keyId || !query.data || query.isFetching || query.error)
      return;
    const scroll = state.donationKeyScroll;
    const frame = requestAnimationFrame(() => {
      if (typeof scroll === 'number' && Number.isFinite(scroll))
        window.scrollTo({ top: scroll, behavior: 'instant' });
      setParams((previous) => previous, {
        replace: true,
        state: { ...state, restoreDonationKey: undefined, donationKeyScroll: undefined },
      });
    });
    return () => cancelAnimationFrame(frame);
  }, [keyId, location.state, query.data, query.isFetching, query.error, setParams]);
  if (lost) return <p role="alert">{t('common.operations.charity.accessLost')}</p>;
  if (query.isPending) return <LoadingState />;
  if (query.error) return <ErrorState error={query.error} onRetry={() => void query.refetch()} />;
  return (
    <div className="ops-stack" role="region" aria-label={t('common.keyModels.title')}>
      <p>{t('common.keyModels.help')}</p>
      <button
        type="button"
        className="btn btn-quiet"
        disabled={query.isFetching}
        onClick={() => void query.refetch()}
      >
        {t('common.refresh')}
      </button>
      {query.data.data.length === 0 ? (
        <EmptyState title={t('common.keyModels.empty')} body={t('common.keyModels.emptyBody')} />
      ) : (
        query.data.data.map((model) => (
          <section className="ops-subcard" key={model.model_id}>
            <h4 className="ops-break">{model.full_name}</h4>
            <p>
              {t(
                model.enabled
                  ? model.available_binding_count === '0'
                    ? 'common.keyModels.unavailable'
                    : 'common.keyModels.available'
                  : 'common.keyModels.disabled',
              )}
            </p>
            <p>
              {t('common.keyModels.count', {
                count: model.binding_count,
                available: model.available_binding_count,
              })}
            </p>
            <div className="ops-actions">
              <button
                className="btn btn-secondary"
                type="button"
                aria-expanded={expanded === model.model_id}
                onClick={() =>
                  setParams((previous) => {
                    const next = new URLSearchParams(previous);
                    if (expanded === model.model_id) next.delete(`${prefix}_expanded`);
                    else next.set(`${prefix}_expanded`, model.model_id);
                    return next;
                  })
                }
              >
                {t('common.keyModels.connections')}
              </button>
              <button
                className="btn btn-quiet"
                type="button"
                onClick={() =>
                  setParams(
                    (previous) => {
                      const next = new URLSearchParams(previous);
                      next.set('charity_section', 'models');
                      next.set('charity_model', model.model_id);
                      next.set('model_from_key', keyId);
                      next.set('model_from_donation', donationId);
                      return next;
                    },
                    { state: { ...location.state, donationKeyScroll: window.scrollY } },
                  )
                }
              >
                {t('common.keyModels.manage')}
              </button>
            </div>
            {expanded === model.model_id && managementResourceID(expanded) ? (
              <BindingPages
                role={role}
                accountId={accountId}
                donationId={donationId}
                keyId={keyId}
                modelId={model.model_id}
                prefix={`${prefix}_${model.model_id}`}
                onCapabilityLoss={onCapabilityLoss}
              />
            ) : null}
          </section>
        ))
      )}
      <PagePagination
        metadata={query.data.pagination}
        requestedPage={pager.page}
        onPageChange={pager.setPage}
        onPageSizeChange={pager.setPageSize}
        busy={query.isFetching}
      />
    </div>
  );
}

function BindingPages({
  role,
  accountId,
  donationId,
  keyId,
  modelId,
  prefix,
  onCapabilityLoss,
}: Props & { modelId: string; prefix: string }) {
  const charityModelID = useCharityModelScope();
  const { t } = useTranslation();
  const pager = useUrlPagePager({
    station: role === 'admin' ? 'admin' : 'user',
    listType: 'donation-key-model-bindings',
    scopeKey: `${accountId}:${donationId}:${keyId}:${modelId}`,
    pageParam: `${prefix}_page`,
    pageSizeParam: `${prefix}_page_size`,
  });
  const query = useQuery({
    queryKey: [
      ...charityKeys.root(role),
      'key-model-bindings',
      charityModelID,
      accountId,
      donationId,
      keyId,
      modelId,
      pager.page,
      pager.pageSize,
    ],
    queryFn: ({ signal }) =>
      getDonationKeyModelBindings(
        role,
        donationId,
        keyId,
        modelId,
        pager.page,
        pager.pageSize,
        signal,
        charityModelID,
      ),
    retry: false,
  });
  const lost = isForbidden(query.error) || isUnauthorized(query.error);
  useEffect(() => {
    if (lost) onCapabilityLoss?.();
  }, [lost, onCapabilityLoss]);
  if (lost) return <p role="alert">{t('common.operations.charity.accessLost')}</p>;
  if (query.isPending) return <LoadingState />;
  if (query.error) return <ErrorState error={query.error} onRetry={() => void query.refetch()} />;
  return (
    <div className="ops-stack">
      <ul>
        {query.data.data.map((binding) => (
          <li key={binding.binding_id} className="ops-break">
            {binding.upstream_model_id} · {t(bindingStateCopy[binding.state])}
          </li>
        ))}
      </ul>
      <PagePagination
        metadata={query.data.pagination}
        requestedPage={pager.page}
        onPageChange={pager.setPage}
        onPageSizeChange={pager.setPageSize}
        busy={query.isFetching}
      />
    </div>
  );
}
