import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { CharityPriceTable, type CharityPriceRow } from '@shared/components/CharityPriceTable';
import { CopyValue } from '@shared/components/CopyValue';
import { Card, EmptyState, ErrorState, LoadingState, StatusBadge } from '@shared/components/States';
import { PagePagination } from '@shared/operations/PagePagination';
import { PAGE_SIZES, type PageSize } from '@shared/operations/pageNumbers';
import {
  useCharityCatalog,
  type CatalogAccessFilter,
  type CatalogAvailability,
  type CatalogFilter,
  type CatalogModel,
} from './catalog';
import './catalog.css';

const DEFAULT_PAGE_SIZE: PageSize = 20;
const PAGE_SIZE_STORAGE_KEY = 'nonbiri:user:charity-catalog-page-size:v1';
const MAX_QUERY_CODE_POINTS = 128;
const MAX_QUERY_BYTES = 512;
const AVAILABILITY_COPY: Record<CatalogAvailability, string> = {
  feature_disabled: 'user.charity.catalog.availability.feature_disabled',
  model_disabled: 'user.charity.catalog.availability.model_disabled',
  level_denied: 'user.charity.catalog.availability.level_denied',
  no_usable_key: 'user.charity.catalog.availability.no_usable_key',
  available: 'user.charity.catalog.availability.available',
};

function readPageSize(): PageSize {
  if (typeof window === 'undefined') return DEFAULT_PAGE_SIZE;
  try {
    const value = Number(window.localStorage.getItem(PAGE_SIZE_STORAGE_KEY));
    if (PAGE_SIZES.includes(value as PageSize)) return value as PageSize;
  } catch {
    // A blocked or unavailable browser storage falls back to this session.
  }
  return DEFAULT_PAGE_SIZE;
}

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
  queryDraft,
  onQueryDraftChange,
  onQuerySubmit,
  onAccessChange,
}: {
  filter: CatalogFilter;
  queryDraft: string;
  onQueryDraftChange: (value: string) => void;
  onQuerySubmit: () => void;
  onAccessChange: (value: CatalogAccessFilter) => void;
}) {
  const { t } = useTranslation();
  return (
    <div className="economy-catalog-filters">
      <form
        className="economy-catalog-search"
        onSubmit={(event) => {
          event.preventDefault();
          onQuerySubmit();
        }}
      >
        <label>
          <span>{t('user.charity.catalog.search')}</span>
          <input
            type="search"
            value={queryDraft}
            maxLength={MAX_QUERY_BYTES}
            onChange={(event) => onQueryDraftChange(boundQueryDraft(event.target.value))}
          />
        </label>
        <button type="submit" className="btn btn-secondary">
          {t('common.search')}
        </button>
      </form>
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
    </div>
  );
}

export function CharityCatalogPanel({ accountID }: { accountID: string | undefined }) {
  const { t } = useTranslation();
  const [filter, setFilter] = useState<CatalogFilter>({
    page: '1',
    pageSize: readPageSize(),
    query: '',
    allowedForMe: 'all',
  });
  const [queryDraft, setQueryDraft] = useState('');
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set());
  const catalog = useCharityCatalog(accountID, filter);

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
  const submitQuery = () => {
    setExpanded(new Set());
    setFilter((current) => ({ ...current, query: queryDraft, page: '1' }));
  };
  const changeAccess = (allowedForMe: CatalogAccessFilter) => {
    setExpanded(new Set());
    setFilter((current) => ({ ...current, allowedForMe, page: '1' }));
  };
  const changePageSize = (pageSize: PageSize) => {
    setExpanded(new Set());
    try {
      window.localStorage.setItem(PAGE_SIZE_STORAGE_KEY, String(pageSize));
    } catch {
      // Storage is an optional preference; the current session still updates.
    }
    setFilter((current) => ({ ...current, pageSize, page: '1' }));
  };
  const priceCount = pageData?.models.length ?? 0;

  return (
    <Card className="economy-catalog-card">
      <div className="card-title-row">
        <div>
          <h2>{t('user.charity.catalog.title')}</h2>
          <p>{t('user.charity.catalog.description')}</p>
        </div>
        {pageData ? (
          <StatusBadge
            active={pageData.donationIntake === 'open'}
            label={t(`user.charity.intakeState.${pageData.donationIntake}`)}
          />
        ) : null}
      </div>
      <CatalogFilters
        filter={filter}
        queryDraft={queryDraft}
        onQueryDraftChange={setQueryDraft}
        onQuerySubmit={submitQuery}
        onAccessChange={changeAccess}
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
                setFilter((current) => ({ ...current, page }));
              }}
              onPageSizeChange={changePageSize}
            />
          </div>
        </div>
      ) : (
        <LoadingState />
      )}
    </Card>
  );
}
