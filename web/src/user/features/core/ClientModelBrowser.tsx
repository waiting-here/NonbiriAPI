import { useEffect, useId, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router';
import { CopyValue } from '@shared/components/CopyValue';
import { ModelTypesSummary } from '@shared/components/ModelTypesField';
import { Button, Field, FilterBar } from '@shared/components/ui';
import type { ModelType } from '@shared/modelTypes';
import { PagePagination } from '@shared/operations/PagePagination';
import {
  DEFAULT_CHARITY_CATALOG_FILTERS,
  useCharityCatalog,
  validCatalogQuery,
} from '../economy/catalog';
import { CoreEmpty, CoreErrorPanel, CoreLoading } from './components';
import { useNumberedModels } from './modelNumberedQueries';
import type { PageWindow } from './pageTypes';
import { canonicalResourceFilters } from './resourceFilters';

export type Source = 'charity' | 'own';
export type ClientModel = { name: string; types: ModelType[] };
export type CatalogView = PageWindow & { query: string; draft: string };

export interface ModelBrowserProps {
  accountId: string;
  source: Source;
  view: CatalogView;
  updateView: (changes: Partial<CatalogView>) => void;
  onChoose: (model: ClientModel) => void;
  selected?: ClientModel;
}

export default function ModelBrowser({
  accountId,
  source,
  view,
  updateView,
  onChoose,
  selected,
}: ModelBrowserProps) {
  const { t } = useTranslation();
  const isCharity = source === 'charity';
  const pageWindow = { page: view.page, pageSize: view.pageSize };
  const charity = useCharityCatalog(isCharity ? accountId : undefined, {
    ...DEFAULT_CHARITY_CATALOG_FILTERS,
    query: view.query,
    ...pageWindow,
  });
  const own = useNumberedModels(accountId, pageWindow, !isCharity, {
    connection_state: 'available',
    q: isCharity ? '' : view.query,
  });
  const query = isCharity ? charity : own;
  const models = isCharity
    ? charity.data?.models.map((model) => ({ name: model.fullName, types: model.modelTypes }))
    : own.data?.data.map((model) => ({ name: model.full_name, types: model.model_types }));
  const [invalid, setInvalid] = useState(false);
  const resultsTitle = useRef<HTMLHeadingElement>(null);
  const resultsId = useId();
  const focusNextPage = useRef(false);
  const pagination = query.data?.pagination;
  useEffect(() => {
    if (focusNextPage.current && !query.isPlaceholderData && !query.isPending) {
      resultsTitle.current?.focus();
      focusNextPage.current = false;
    }
  }, [pagination, query.isPending, query.isPlaceholderData]);

  const search = () => {
    const next = view.draft.trim();
    if (isCharity) {
      if (!validCatalogQuery(next)) {
        setInvalid(true);
        return;
      }
    } else {
      try {
        canonicalResourceFilters('models', { q: next });
      } catch {
        setInvalid(true);
        return;
      }
    }
    setInvalid(false);
    updateView({ query: next, page: '1' });
  };
  const reset = () => {
    setInvalid(false);
    updateView({ draft: '', query: '', page: '1' });
  };
  const changePage = (window: Partial<PageWindow>) => {
    if (window.page === view.page && (window.pageSize ?? view.pageSize) === view.pageSize) return;
    focusNextPage.current = true;
    updateView(window);
  };

  return (
    <div className="api-model-browser">
      <FilterBar
        role="search"
        search={
          <Field
            label={t('user.core.keys.searchLabel')}
            error={
              invalid
                ? t('user.core.keys.searchInvalid', { limit: isCharity ? 128 : 512 })
                : undefined
            }
          >
            {(field) => (
              <input
                {...field}
                type="search"
                value={view.draft}
                placeholder={t('user.core.keys.searchHint')}
                onChange={(event) => {
                  setInvalid(false);
                  updateView({ draft: event.target.value });
                }}
              />
            )}
          </Field>
        }
        onSubmit={search}
        onReset={reset}
        resetDisabled={!view.query && !view.draft}
        activeCount={view.query ? 1 : 0}
      />
      <div className="api-model-browser__head">
        <h3 ref={resultsTitle} tabIndex={-1}>
          {t('user.core.keys.availableModels')}
        </h3>
        <Button
          type="button"
          size="sm"
          variant="ghost"
          disabled={query.isFetching}
          onClick={() => void query.refetch()}
        >
          {t('common.refresh')}
        </Button>
      </div>
      {query.isError ? (
        <CoreErrorPanel compact error={query.error} onRetry={() => void query.refetch()} />
      ) : !models || query.isPlaceholderData ? (
        <CoreLoading compact />
      ) : models.length === 0 ? (
        <CoreEmpty
          title={t(view.query ? 'user.core.keys.noMatches' : 'user.core.keys.noAvailableModels')}
          body={t(
            view.query
              ? 'user.core.keys.trySearch'
              : isCharity
                ? 'user.core.keys.emptyCharity'
                : 'user.core.keys.emptyOwn',
          )}
          illustration={view.query ? 'search' : 'empty'}
        />
      ) : (
        <ul className="api-model-results">
          {models.map((model, index) => (
            <li key={model.name}>
              <Button
                type="button"
                variant="ghost"
                className="api-model-option"
                aria-current={selected?.name === model.name || undefined}
                onClick={() => onChoose(model)}
                aria-label={t('user.core.keys.chooseNamedModel', { name: model.name })}
                aria-describedby={resultsId + index}
              >
                <span className="api-model-results__identity">
                  <code>{model.name}</code>
                  <span id={resultsId + index}>
                    <ModelTypesSummary value={model.types} />
                  </span>
                </span>
                <span className="api-model-option__action">
                  {t(
                    selected?.name === model.name
                      ? 'user.core.keys.selectedModel'
                      : 'user.core.keys.useModel',
                  )}
                </span>
              </Button>
              <CopyValue
                showValue={false}
                value={model.name}
                label={model.name}
                variant="secondary"
              />
            </li>
          ))}
        </ul>
      )}
      {pagination && !query.isError && !query.isPlaceholderData ? (
        <PagePagination
          metadata={pagination}
          requestedPage={view.page}
          busy={query.isFetching}
          onPageChange={(page) => changePage({ page })}
          onPageSizeChange={(pageSize) => changePage({ page: '1', pageSize })}
        />
      ) : null}
      <Link to={isCharity ? '/charity' : '/models'}>
        {t(isCharity ? 'user.core.keys.catalogPrices' : 'user.core.keys.manageModels')}
      </Link>
    </div>
  );
}
