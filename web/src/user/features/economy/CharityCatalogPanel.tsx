import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useSearchState } from '@shared/operations/useSearchState';
import { CharityPriceTable, type CharityPriceRow } from '@shared/components/CharityPriceTable';
import { DataTable, FilterBar } from '@shared/components/ui';
import { CopyValue } from '@shared/components/CopyValue';
import { RecentCharitySuccess } from '@shared/components/DonationControlFacts';
import { Card, EmptyState, ErrorState, LoadingState, StatusBadge } from '@shared/components/States';
import { PagePagination } from '@shared/operations/PagePagination';
import { useUrlPagePager } from '@shared/operations/useUrlPagePager';
import { type PageSize } from '@shared/operations/pageNumbers';
import {
  canonicalCharityCatalogSearch,
  charityCatalogFilterKey,
  DEFAULT_CHARITY_CATALOG_FILTERS,
  readCharityCatalogUrlState,
  writeCharityCatalogFilters,
  useCharityCatalog,
  type CatalogAccessFilter,
  type CatalogAvailability,
  type CatalogAvailabilityFilter,
  type CatalogFilter,
  type CatalogLevelFilter,
  type CatalogModel,
} from './catalog';
import './catalog.css';

const MAX_QUERY_CODE_POINTS = 128;
const MAX_QUERY_BYTES = 512;
const AVAILABILITY_COPY: Record<CatalogAvailability, string> = {
  feature_disabled: 'user.charity.catalog.availability.feature_disabled',
  model_disabled: 'user.charity.catalog.availability.model_disabled',
  level_denied: 'user.charity.catalog.availability.level_denied',
  no_usable_key: 'user.charity.catalog.availability.no_usable_key',
  available: 'user.charity.catalog.availability.available',
};

function boundQueryDraft(value: string): string {
  const encoder = new TextEncoder();
  let result = '';
  let codePoints = 0;
  let bytes = 0;
  for (const character of value) {
    if (codePoints >= MAX_QUERY_CODE_POINTS) break;
    const characterBytes = encoder.encode(character).byteLength;
    if (bytes + characterBytes > MAX_QUERY_BYTES) break;
    result += character;
    codePoints += 1;
    bytes += characterBytes;
  }
  return result;
}

function priceRows(model: CatalogModel, translate: (key: string) => string): CharityPriceRow[] {
  if (model.pricing.mode === 'per_request') {
    return [
      {
        label: translate('user.charity.requestPrice'),
        compactLabel: translate('user.charity.presentation.perCall'),
        userMilli: model.pricing.userPriceMilli,
        discountedUserMilli: model.pricing.discountedUserPriceMilli,
      },
    ];
  }
  return [
    {
      label: translate('user.charity.uncachedInputPrice'),
      compactLabel: translate('user.charity.presentation.input'),
      userMilli: model.pricing.userPricesMilli.uncachedInput,
      discountedUserMilli: model.pricing.discountedUserPricesMilli.uncachedInput,
    },
    {
      label: translate('user.charity.cacheWriteInputPrice'),
      compactLabel: translate('user.charity.presentation.cacheWrite'),
      userMilli: model.pricing.userPricesMilli.cacheWriteInput,
      discountedUserMilli: model.pricing.discountedUserPricesMilli.cacheWriteInput,
    },
    {
      label: translate('user.charity.cacheReadInputPrice'),
      compactLabel: translate('user.charity.presentation.cacheRead'),
      userMilli: model.pricing.userPricesMilli.cacheReadInput,
      discountedUserMilli: model.pricing.discountedUserPricesMilli.cacheReadInput,
    },
    {
      label: translate('user.charity.outputPrice'),
      compactLabel: translate('user.charity.presentation.output'),
      userMilli: model.pricing.userPricesMilli.output,
      discountedUserMilli: model.pricing.discountedUserPricesMilli.output,
    },
  ];
}

function discountProps(model: CatalogModel) {
  return {
    enabled: model.discount.enabled,
    percent: model.discount.percent,
    ...(model.discount.startAt !== null ? { startAt: model.discount.startAt } : {}),
    ...(model.discount.endAt !== null ? { endAt: model.discount.endAt } : {}),
  };
}

function CatalogFilters({
  filter,
  onQuerySubmit,
  onAccessChange,
  onLevelChange,
  onAvailabilityChange,
  onClear,
}: {
  filter: CatalogFilter;
  onQuerySubmit: (query: string) => void;
  onAccessChange: (value: CatalogAccessFilter) => void;
  onLevelChange: (value: CatalogLevelFilter) => void;
  onAvailabilityChange: (value: CatalogAvailabilityFilter) => void;
  onClear: () => void;
}) {
  const { t } = useTranslation();
  const [queryDraft, setQueryDraft] = useState(filter.query);
  const chips = [
    ...(filter.allowedForMe === 'all'
      ? []
      : [
          {
            key: 'access',
            label: t(
              filter.allowedForMe === 'true'
                ? 'user.charity.catalog.accessAllowed'
                : 'user.charity.catalog.accessDenied',
            ),
            removeLabel: t('user.charity.presentation.removeAccess'),
            onRemove: () => onAccessChange('all'),
          },
        ]),
    ...(filter.currentlyAvailable === 'all'
      ? []
      : [
          {
            key: 'availability',
            label: t(
              filter.currentlyAvailable === 'true'
                ? 'user.charity.catalog.availabilityAllowed'
                : 'user.charity.catalog.availabilityDenied',
            ),
            removeLabel: t('user.charity.presentation.removeAvailability'),
            onRemove: () => onAvailabilityChange('all'),
          },
        ]),
    ...(filter.allowedLevel === 'all'
      ? []
      : [
          {
            key: 'level',
            label: t('user.charity.catalog.level', { level: filter.allowedLevel }),
            removeLabel: t('user.charity.presentation.removeLevel'),
            onRemove: () => onLevelChange('all'),
          },
        ]),
  ];
  return (
    <div className="economy-catalog-filters">
      <FilterBar
        search={
          <div className="economy-catalog-search">
            <label>
              <span>{t('user.charity.catalog.search')}</span>
              <input
                type="search"
                value={queryDraft}
                maxLength={MAX_QUERY_BYTES}
                onChange={(event) => setQueryDraft(boundQueryDraft(event.target.value))}
              />
            </label>
            <button type="submit" className="btn btn-secondary">
              {t('common.search')}
            </button>
          </div>
        }
        secondaryLabel={t('user.charity.presentation.filters')}
        activeCount={chips.length}
        chips={chips}
        onClearAll={onClear}
        clearAllLabel={t('user.charity.presentation.clearFilters')}
        onSubmit={() => onQuerySubmit(queryDraft)}
        secondary={
          <>
            <label>
              <span>{t('user.charity.catalog.accessFilter')}</span>
              <select
                value={filter.allowedForMe}
                onChange={(event) => onAccessChange(event.target.value as CatalogAccessFilter)}
              >
                <option value="all">{t('user.charity.catalog.accessAll')}</option>
                <option value="true">{t('user.charity.catalog.accessAllowed')}</option>
                <option value="false">{t('user.charity.catalog.accessDenied')}</option>
              </select>
            </label>
            <label>
              <span>{t('user.charity.catalog.availabilityFilter')}</span>
              <select
                value={filter.currentlyAvailable}
                onChange={(event) =>
                  onAvailabilityChange(event.target.value as CatalogAvailabilityFilter)
                }
              >
                <option value="all">{t('user.charity.catalog.availabilityAll')}</option>
                <option value="true">{t('user.charity.catalog.availabilityAllowed')}</option>
                <option value="false">{t('user.charity.catalog.availabilityDenied')}</option>
              </select>
            </label>
            <label>
              <span>{t('user.charity.catalog.levelFilter')}</span>
              <select
                value={filter.allowedLevel}
                onChange={(event) => onLevelChange(event.target.value as CatalogLevelFilter)}
              >
                <option value="all">{t('user.charity.catalog.levelAll')}</option>
                {[1, 2, 3, 4, 5, 6].map((level) => (
                  <option key={level} value={level}>
                    {t('user.charity.catalog.level', { level })}
                  </option>
                ))}
              </select>
            </label>
          </>
        }
      />
    </div>
  );
}

export function CharityCatalogPanel({ accountID, enabled = true }: { accountID: string | undefined; enabled?: boolean }) {
  const { t } = useTranslation();
  const [searchParams, setSearchParams] = useSearchState();
  const urlState = readCharityCatalogUrlState(searchParams);
  const pager = useUrlPagePager({
    station: 'user',
    listType: 'charity-catalog',
    scopeKey: accountID ?? 'anonymous',
    scopeReady: Boolean(accountID),
    resetKey: charityCatalogFilterKey(urlState.filters),
  });
  const filter: CatalogFilter = {
    ...urlState.filters,
    page: pager.page,
    pageSize: pager.pageSize,
  };
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set());
  const catalog = useCharityCatalog(accountID, filter, enabled);

  useEffect(() => {
    if (!urlState.needsNormalization) return;
    const canonical = canonicalCharityCatalogSearch(searchParams);
    if (canonical.toString() !== searchParams.toString()) {
      setSearchParams(canonicalCharityCatalogSearch, { replace: true });
    }
  }, [searchParams, setSearchParams, urlState.needsNormalization]);

  const pageData = catalog.data;
  const busy = catalog.isFetching;
  const toggleDescription = (modelID: string) => {
    setExpanded((current) => {
      const next = new Set(current);
      if (next.has(modelID)) next.delete(modelID);
      else next.add(modelID);
      return next;
    });
  };
  const submitQuery = (query: string) => {
    setExpanded(new Set());
    setSearchParams(
      (previous) => {
        const next = writeCharityCatalogFilters(previous, {
          ...readCharityCatalogUrlState(previous).filters,
          query,
        });
        next.delete('page');
        next.set('page', '1');
        return next;
      },
      { replace: false },
    );
  };
  const updateFilter = (changes: Partial<typeof DEFAULT_CHARITY_CATALOG_FILTERS>) => {
    setExpanded(new Set());
    setSearchParams((previous) => {
      const current = readCharityCatalogUrlState(previous).filters;
      const next = writeCharityCatalogFilters(previous, { ...current, ...changes });
      next.delete('page');
      next.set('page', '1');
      return next;
    });
  };
  const changeAccess = (allowedForMe: CatalogAccessFilter) => updateFilter({ allowedForMe });
  const changeLevel = (allowedLevel: CatalogLevelFilter) => updateFilter({ allowedLevel });
  const changeAvailability = (currentlyAvailable: CatalogAvailabilityFilter) =>
    updateFilter({ currentlyAvailable });
  const clearFilters = () =>
    updateFilter({ allowedForMe: 'all', currentlyAvailable: 'all', allowedLevel: 'all' });
  const changePageSize = (pageSize: PageSize) => {
    setExpanded(new Set());
    pager.setPageSize(pageSize);
  };
  const priceCount = pageData?.models.length ?? 0;
  const changePage = (page: string) => {
    setExpanded(new Set());
    pager.setPage(page);
  };

  return (
    <Card className="economy-catalog-card">
      <h2>{t('user.charity.catalog.title')}</h2>
      <CatalogFilters
        key={urlState.filters.query}
        filter={filter}
        onQuerySubmit={submitQuery}
        onAccessChange={changeAccess}
        onLevelChange={changeLevel}
        onAvailabilityChange={changeAvailability}
        onClear={clearFilters}
      />
      {catalog.isPending && !pageData ? (
        <LoadingState />
      ) : catalog.error ? (
        <ErrorState error={catalog.error} onRetry={() => void catalog.refetch()} />
      ) : pageData ? (
        <div className="economy-catalog-results" aria-busy={busy}>
          {busy ? <LoadingState /> : null}
          {priceCount === 0 ? (
            <EmptyState
              title={t('user.charity.catalog.emptyTitle')}
              body={
                filter.allowedForMe === 'true' || filter.currentlyAvailable === 'true'
                  ? t('user.charity.presentation.filteredEmpty')
                  : t('user.charity.catalog.emptyBody')
              }
              action={
                filter.allowedForMe === 'true' || filter.currentlyAvailable === 'true' ? (
                  <button
                    type="button"
                    className="btn btn-secondary"
                    onClick={() => updateFilter({ allowedForMe: 'all', currentlyAvailable: 'all' })}
                  >
                    {t('user.charity.presentation.showAll')}
                  </button>
                ) : undefined
              }
            />
          ) : (
            <DataTable
              caption={t('user.charity.catalog.modelsList')}
              rows={pageData.models}
              rowKey={(model) => model.id}
              columns={[
                {
                  key: 'name',
                  header: t('user.charity.modelName'),
                  cell: 'title',
                  render: (model) => (
                    <>
                      <button
                        type="button"
                        className="charity-model-name"
                        aria-expanded={expanded.has(model.id)}
                        aria-controls={`charity-model-${model.id}`}
                        onClick={() => toggleDescription(model.id)}
                      >
                        <code>{model.fullName}</code>
                        <span aria-hidden="true">{expanded.has(model.id) ? '−' : '+'}</span>
                      </button>
                      {model.publicDescription ? (
                        <span className="nb-sub charity-model-preview">
                          {Array.from(model.publicDescription).slice(0, 60).join('')}
                        </span>
                      ) : null}
                    </>
                  ),
                },
                {
                  key: 'status',
                  header: t('user.charity.presentation.status'),
                  cell: 'status',
                  render: (model) => (
                    <StatusBadge
                      active={model.availability === 'available'}
                      label={t(AVAILABILITY_COPY[model.availability])}
                    />
                  ),
                },
                {
                  key: 'price',
                  header: t('user.charity.userPrice'),
                  mobileLabel: t('user.charity.userPrice'),
                  cell: 'meta',
                  render: (model) => (
                    <CharityPriceTable
                      compact
                      mode={model.pricing.mode}
                      rows={priceRows(model, t)}
                      serverNow={pageData.serverNow}
                      discount={discountProps(model)}
                    />
                  ),
                },
                {
                  key: 'levels',
                  header: t('user.charity.catalog.allowedLevels'),
                  mobileLabel: t('user.charity.catalog.allowedLevels'),
                  cell: 'meta',
                  render: (model) =>
                    model.allowedLevels.length
                      ? model.allowedLevels.map((level) => `L${level}`).join('、')
                      : t('user.charity.catalog.noAllowedLevels'),
                },
                {
                  key: 'copy',
                  header: t('common.copy'),
                  cell: 'action',
                  align: 'action',
                  render: (model) => (
                    <CopyValue
                      showValue={false}
                      value={model.fullName}
                      label={t('user.charity.modelName')}
                    />
                  ),
                },
              ]}
              renderDetail={(model) =>
                expanded.has(model.id) ? (
                  <section
                    id={`charity-model-${model.id}`}
                    className="charity-model-detail"
                    aria-label={model.fullName}
                  >
                    {model.publicDescription ? <p>{model.publicDescription}</p> : null}
                    <CharityPriceTable
                      mode={model.pricing.mode}
                      rows={priceRows(model, t)}
                      serverNow={pageData.serverNow}
                      discount={discountProps(model)}
                    />
                    <RecentCharitySuccess value={model.recentSuccess} />
                    <p>
                      {t('user.charity.catalog.yourAccess')}:{' '}
                      {t(
                        model.levelAllowed
                          ? 'user.charity.catalog.allowedForMe'
                          : 'user.charity.catalog.deniedForMe',
                      )}
                    </p>
                    {!model.levelAllowed ? (
                      <p>
                        {t('user.charity.catalog.resourceAvailability')}:{' '}
                        {t(
                          model.currentlyAvailable
                            ? 'user.charity.catalog.currentlyAvailable'
                            : 'user.charity.catalog.currentlyUnavailable',
                        )}
                      </p>
                    ) : null}
                  </section>
                ) : null
              }
            />
          )}
          <div className="economy-catalog-pagination">
            <PagePagination
              metadata={pageData.pagination}
              busy={busy}
              onPageChange={changePage}
              onPageSizeChange={changePageSize}
              requestedPage={filter.page}
            />
          </div>
        </div>
      ) : (
        <LoadingState />
      )}
    </Card>
  );
}
