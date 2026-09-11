import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useSearchState } from '@shared/operations/useSearchState';
import { CharityPriceTable, type CharityPriceRow } from '@shared/components/CharityPriceTable';
import { CopyValue } from '@shared/components/CopyValue';
import { Card, EmptyState, ErrorState, LoadingState, StatusBadge } from '@shared/components/States';
import { PagePagination } from '@shared/operations/PagePagination';
import { useUrlPagePager } from '@shared/operations/useUrlPagePager';
import type { PageSize } from '@shared/operations/pageNumbers';
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
        userMilli: model.pricing.userPriceMilli,
        discountedUserMilli: model.pricing.discountedUserPriceMilli,
      },
    ];
  }
  return [
    {
      label: translate('user.charity.uncachedInputPrice'),
      userMilli: model.pricing.userPricesMilli.uncachedInput,
      discountedUserMilli: model.pricing.discountedUserPricesMilli.uncachedInput,
    },
    {
      label: translate('user.charity.cacheWriteInputPrice'),
      userMilli: model.pricing.userPricesMilli.cacheWriteInput,
      discountedUserMilli: model.pricing.discountedUserPricesMilli.cacheWriteInput,
    },
    {
      label: translate('user.charity.cacheReadInputPrice'),
      userMilli: model.pricing.userPricesMilli.cacheReadInput,
      discountedUserMilli: model.pricing.discountedUserPricesMilli.cacheReadInput,
    },
    {
      label: translate('user.charity.outputPrice'),
      userMilli: model.pricing.userPricesMilli.output,
      discountedUserMilli: model.pricing.discountedUserPricesMilli.output,
    },
  ];
}

function descriptionToggleKey(expanded: boolean): string {
  return expanded
    ? 'user.charity.catalog.collapseDescription'
    : 'user.charity.catalog.expandDescription';
}

function discountProps(model: CatalogModel) {
  return {
    enabled: model.discount.enabled,
    percent: model.discount.percent,
    ...(model.discount.startAt !== null ? { startAt: model.discount.startAt } : {}),
    ...(model.discount.endAt !== null ? { endAt: model.discount.endAt } : {}),
  };
}

function CatalogModelCard({
  model,
  serverNow,
  expanded,
  onToggleDescription,
}: {
  model: CatalogModel;
  serverNow: number;
  expanded: boolean;
  onToggleDescription: () => void;
}) {
  const { t } = useTranslation();
  const levels = model.allowedLevels.map((level) => t('user.charity.catalog.level', { level }));
  const description = model.publicDescription;
  return (
    <li className="economy-catalog-item">
      <div className="economy-catalog-item__heading">
        <div className="economy-catalog-item__name">
          <CopyValue value={model.fullName} label={t('user.charity.modelName')} />
          <span className="economy-catalog-item__provider">
            {model.provider} / {model.model}
          </span>
        </div>
        <div className="economy-catalog-item__statuses">
          <StatusBadge
            active={model.enabled}
            label={model.enabled ? t('common.enabled') : t('common.disabled')}
          />
          <span className="economy-catalog-item__availability">
            {t(AVAILABILITY_COPY[model.availability])}
          </span>
          {!model.levelAllowed ? (
            <span
              className={`economy-catalog-item__resource-state${model.currentlyAvailable ? ' is-available' : ''}`}
              aria-label={t('user.charity.catalog.resourceAvailability')}
            >
              {model.currentlyAvailable
                ? t('user.charity.catalog.currentlyAvailable')
                : t('user.charity.catalog.currentlyUnavailable')}
            </span>
          ) : null}
        </div>
      </div>
      <CharityPriceTable
        mode={model.pricing.mode}
        rows={priceRows(model, t)}
        serverNow={serverNow}
        discount={discountProps(model)}
      />
      {description ? (
        <div className="economy-catalog-item__description-block">
          <p className={`economy-catalog-item__description${expanded ? ' is-expanded' : ''}`}>
            {description}
          </p>
          <button
            type="button"
            className="btn btn-quiet economy-catalog-item__description-toggle"
            aria-expanded={expanded}
            onClick={onToggleDescription}
          >
            {t(descriptionToggleKey(expanded))}
          </button>
        </div>
      ) : null}
      <dl className="economy-catalog-item__meta">
        <div>
          <dt>{t('user.charity.catalog.allowedLevels')}</dt>
          <dd>
            {levels.length > 0 ? levels.join(', ') : t('user.charity.catalog.noAllowedLevels')}
          </dd>
        </div>
        <div>
          <dt>{t('user.charity.catalog.yourAccess')}</dt>
          <dd>
            {model.levelAllowed
              ? t('user.charity.catalog.allowedForMe')
              : t('user.charity.catalog.deniedForMe')}
          </dd>
        </div>
      </dl>
    </li>
  );
}

function CatalogFilters({
  filter,
  onQuerySubmit,
  onAccessChange,
  onLevelChange,
  onAvailabilityChange,
  onReset,
}: {
  filter: CatalogFilter;
  onQuerySubmit: (query: string) => void;
  onAccessChange: (value: CatalogAccessFilter) => void;
  onLevelChange: (value: CatalogLevelFilter) => void;
  onAvailabilityChange: (value: CatalogAvailabilityFilter) => void;
  onReset: () => void;
}) {
  const { t } = useTranslation();
  const [queryDraft, setQueryDraft] = useState(filter.query);
  const appliedLabel = t('user.charity.catalog.filterApplied');
  return (
    <div className="economy-catalog-filters">
      <form
        className="economy-catalog-search"
        onSubmit={(event) => {
          event.preventDefault();
          onQuerySubmit(queryDraft);
        }}
      >
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
      </form>
      <label
        className={`economy-catalog-filter${filter.allowedLevel !== 'all' ? ' is-applied' : ''}`}
      >
        <span>{t('user.charity.catalog.levelFilter')}</span>
        <select
          aria-label={t('user.charity.catalog.levelFilter')}
          value={filter.allowedLevel}
          onChange={(event) => onLevelChange(event.target.value as CatalogLevelFilter)}
        >
          <option value="all">{t('user.charity.catalog.levelAll')}</option>
          <option value="1">{t('user.charity.catalog.level', { level: 1 })}</option>
          <option value="2">{t('user.charity.catalog.level', { level: 2 })}</option>
          <option value="3">{t('user.charity.catalog.level', { level: 3 })}</option>
          <option value="4">{t('user.charity.catalog.level', { level: 4 })}</option>
          <option value="5">{t('user.charity.catalog.level', { level: 5 })}</option>
        </select>
        {filter.allowedLevel !== 'all' ? (
          <span className="economy-catalog-filter__applied">{appliedLabel}</span>
        ) : null}
      </label>
      <label
        className={`economy-catalog-filter${filter.allowedForMe !== 'all' ? ' is-applied' : ''}`}
      >
        <span>{t('user.charity.catalog.accessFilter')}</span>
        <select
          aria-label={t('user.charity.catalog.accessFilter')}
          value={filter.allowedForMe}
          onChange={(event) => onAccessChange(event.target.value as CatalogAccessFilter)}
        >
          <option value="all">{t('user.charity.catalog.accessAll')}</option>
          <option value="true">{t('user.charity.catalog.accessAllowed')}</option>
          <option value="false">{t('user.charity.catalog.accessDenied')}</option>
        </select>
        {filter.allowedForMe !== 'all' ? (
          <span className="economy-catalog-filter__applied">{appliedLabel}</span>
        ) : null}
      </label>
      <label
        className={`economy-catalog-filter${filter.currentlyAvailable !== 'all' ? ' is-applied' : ''}`}
      >
        <span>{t('user.charity.catalog.availabilityFilter')}</span>
        <select
          aria-label={t('user.charity.catalog.availabilityFilter')}
          value={filter.currentlyAvailable}
          onChange={(event) =>
            onAvailabilityChange(event.target.value as CatalogAvailabilityFilter)
          }
        >
          <option value="all">{t('user.charity.catalog.availabilityAll')}</option>
          <option value="true">{t('user.charity.catalog.availabilityAllowed')}</option>
          <option value="false">{t('user.charity.catalog.availabilityDenied')}</option>
        </select>
        {filter.currentlyAvailable !== 'all' ? (
          <span className="economy-catalog-filter__applied">{appliedLabel}</span>
        ) : null}
      </label>
      <button
        type="button"
        className="btn btn-quiet economy-catalog-filter-reset"
        onClick={onReset}
      >
        {t('user.charity.catalog.resetFilters')}
      </button>
    </div>
  );
}

export function CharityCatalogPanel({ accountID }: { accountID: string | undefined }) {
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
  const catalog = useCharityCatalog(accountID, filter);

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
  const resetFilters = () => {
    setExpanded(new Set());
    setSearchParams((previous) => {
      const current = readCharityCatalogUrlState(previous).filters;
      const next = writeCharityCatalogFilters(previous, {
        ...DEFAULT_CHARITY_CATALOG_FILTERS,
        query: current.query,
      });
      next.delete('page');
      next.set('page', '1');
      return next;
    });
  };
  const changePageSize = (pageSize: PageSize) => {
    setExpanded(new Set());
    pager.setPageSize(pageSize);
  };
  const priceCount = pageData?.models.length ?? 0;

  return (
    <Card className="economy-catalog-card">
      <div className="card-title-row">
        <div>
          <h2>{t('user.charity.catalog.title')}</h2>
          <p>{t('user.charity.catalog.description')}</p>
          <p>{t('common.operations.charity.embeddingBillingHelp')}</p>
        </div>
        {pageData ? (
          <StatusBadge
            active={pageData.donationIntake === 'open'}
            label={t(`user.charity.intakeState.${pageData.donationIntake}`)}
          />
        ) : null}
      </div>
      <CatalogFilters
        key={urlState.filters.query}
        filter={filter}
        onQuerySubmit={submitQuery}
        onAccessChange={changeAccess}
        onLevelChange={changeLevel}
        onAvailabilityChange={changeAvailability}
        onReset={resetFilters}
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
              body={t('user.charity.catalog.emptyBody')}
            />
          ) : (
            <ul className="economy-catalog-list" aria-label={t('user.charity.catalog.modelsList')}>
              {pageData.models.map((model) => (
                <CatalogModelCard
                  key={model.id}
                  model={model}
                  serverNow={pageData.serverNow}
                  expanded={expanded.has(model.id)}
                  onToggleDescription={() => toggleDescription(model.id)}
                />
              ))}
            </ul>
          )}
          <div className="economy-catalog-pagination">
            <PagePagination
              metadata={pageData.pagination}
              busy={busy}
              onPageChange={(page) => {
                setExpanded(new Set());
                pager.setPage(page);
              }}
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
