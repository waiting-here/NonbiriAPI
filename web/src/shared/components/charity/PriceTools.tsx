import { useEffect, useRef, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import {
  charityKeys,
  type CharityModel,
  type CharityPriceConfig,
  type CharityRole,
} from '@shared/operations/charity';
import {
  getManagedCharityModelsPage,
  validManagementSearch,
} from '@shared/operations/charityModelPages';
import {
  getPriceTemplates,
  type CharityPriceTemplate,
} from '@shared/operations/charityPriceTemplates';
import { usePagePager } from '@shared/operations/usePagePager';
import { useSearchState } from '@shared/operations/useSearchState';
import { PagePagination } from '@shared/operations/PagePagination';
import { isForbidden, isUnauthorized } from '@shared/query/http';
import { EmptyState, ErrorState, LoadingState } from '../States';
import { Button, DataTable, Field, FilterBar, Fold, Note } from '../ui';
import { PriceConfigSummary } from './PriceConfigSummary';

interface PriceToolsProps {
  role: CharityRole;
  onApply: (config: CharityPriceConfig, name: string, revision: string) => void;
  onCapabilityLoss?: () => void;
}

const priceSnapshot = (source: CharityPriceConfig): CharityPriceConfig => ({
  pricing: structuredClone(source.pricing),
  token_reserve_credits: source.token_reserve_credits,
  discount: { ...source.discount },
});

export function PriceTools(props: PriceToolsProps) {
  const { t } = useTranslation();
  return (
    <div className="nb-workflow-price-tools">
      <Fold title={t('common.workflow.templates')} lazy>
        <PriceTemplates {...props} />
      </Fold>
      <Fold title={t('common.workflow.reference')} lazy>
        <ModelPriceReference {...props} />
      </Fold>
    </div>
  );
}

function SearchForm({
  draft,
  setDraft,
  onSubmit,
  placeholder,
}: {
  draft: string;
  setDraft: (value: string) => void;
  onSubmit: () => void;
  placeholder: string;
}) {
  const { t } = useTranslation();
  return (
    <FilterBar
      activeCount={0}
      secondaryLabel={t('common.workflow.filters')}
      onSubmit={onSubmit}
      search={
        <>
          <Field label={t('common.search')}>
            {(props) => (
              <input
                {...props}
                type="search"
                value={draft}
                placeholder={placeholder}
                onChange={(event) => setDraft(event.target.value)}
              />
            )}
          </Field>
          <Button type="submit" disabled={!validManagementSearch(draft)}>
            {t('common.search')}
          </Button>
        </>
      }
    />
  );
}

function ModelPriceReference({ role, onApply, onCapabilityLoss }: PriceToolsProps) {
  const { t } = useTranslation();
  const [q, setQ] = useState('');
  const [draft, setDraft] = useState('');
  const [selected, setSelected] = useState<CharityModel | null>(null);
  const preview = useRef<HTMLElement>(null);
  useEffect(() => {
    if (selected) {
      preview.current?.focus({ preventScroll: true });
      preview.current?.scrollIntoView?.({ block: 'nearest', behavior: 'instant' });
    }
  }, [selected]);
  const pager = usePagePager({
    station: role === 'admin' ? 'admin' : 'user',
    listType: 'charity-price-reference',
    scopeKey: role,
    resetKey: q,
  });
  const query = useQuery({
    queryKey: [...charityKeys.root(role), 'price-reference', q, pager.page, pager.pageSize],
    queryFn: ({ signal }) =>
      getManagedCharityModelsPage(role, q, '', pager.page, pager.pageSize, signal),
    retry: false,
  });
  const lost = isForbidden(query.error) || isUnauthorized(query.error);
  useEffect(() => {
    if (lost) onCapabilityLoss?.();
  }, [lost, onCapabilityLoss]);
  if (lost) return <p role="alert">{t('common.operations.charity.accessLost')}</p>;
  return (
    <div className="nb-form">
      <Note>{t('common.workflow.referenceHelp')}</Note>
      <SearchForm
        draft={draft}
        setDraft={setDraft}
        placeholder={t('common.workflow.modelSearch')}
        onSubmit={() => {
          if (validManagementSearch(draft)) setQ(draft.trim());
        }}
      />
      {query.isPending ? (
        <LoadingState />
      ) : query.error ? (
        <ErrorState error={query.error} onRetry={() => void query.refetch()} />
      ) : (
        <>
          <DataTable
            caption={t('common.workflow.reference')}
            rowKey={(row) => row.id}
            rows={query.data.data}
            selectedKey={selected?.id}
            columns={[
              {
                key: 'name',
                header: t('common.workflow.model'),
                cell: 'title',
                render: (row) => <span className="mono nb-workflow-id">{row.full_name}</span>,
              },
              {
                key: 'view',
                header: t('common.workflow.actions'),
                cell: 'action',
                render: (row) => (
                  <Button
                    type="button"
                    onClick={() => setSelected(row)}
                    disabled={query.isFetching}
                  >
                    {t('common.workflow.viewPrice')}
                  </Button>
                ),
              },
            ]}
          />
          {query.data.data.length === 0 ? (
            <EmptyState
              title={t('common.workflow.noModels')}
              body={t('common.workflow.trySearch')}
            />
          ) : null}
          <PagePagination
            metadata={query.data.pagination}
            requestedPage={pager.page}
            onPageChange={pager.setPage}
            onPageSizeChange={pager.setPageSize}
            busy={query.isFetching}
          />
        </>
      )}
      {selected ? (
        <section aria-label={t('common.workflow.readOnlyPrice')} ref={preview} tabIndex={-1}>
          <h4 className="nb-workflow-id">{selected.full_name}</h4>
          <PriceConfigSummary config={selected} role={role} />
          <Button
            type="button"
            onClick={() => onApply(priceSnapshot(selected), selected.full_name, selected.revision)}
          >
            {t('common.workflow.fillReference')}
          </Button>
        </section>
      ) : null}
    </div>
  );
}

function PriceTemplates({ role, onApply, onCapabilityLoss }: PriceToolsProps) {
  const { t } = useTranslation();
  const [, setParams] = useSearchState();
  const [q, setQ] = useState('');
  const [draft, setDraft] = useState('');
  const [state, setState] = useState<'enabled' | 'disabled' | 'all'>('enabled');
  const [selected, setSelected] = useState<CharityPriceTemplate | null>(null);
  const preview = useRef<HTMLElement>(null);
  useEffect(() => {
    if (selected) {
      preview.current?.focus({ preventScroll: true });
      preview.current?.scrollIntoView?.({ block: 'nearest', behavior: 'instant' });
    }
  }, [selected]);
  const pager = usePagePager({
    station: role === 'admin' ? 'admin' : 'user',
    listType: 'charity-price-templates',
    scopeKey: role,
    resetKey: `${q}:${state}`,
  });
  const root = [...charityKeys.root(role), 'price-templates'] as const;
  const query = useQuery({
    queryKey: [...root, q, state, pager.page, pager.pageSize],
    queryFn: ({ signal }) => getPriceTemplates(role, q, state, pager.page, pager.pageSize, signal),
    retry: false,
  });
  const lost = isForbidden(query.error) || isUnauthorized(query.error);
  useEffect(() => {
    if (lost) onCapabilityLoss?.();
  }, [lost, onCapabilityLoss]);
  if (lost) return <p role="alert">{t('common.operations.charity.accessLost')}</p>;
  return (
    <div className="nb-form">
      <SearchForm
        draft={draft}
        setDraft={setDraft}
        placeholder={t('common.workflow.templateSearch')}
        onSubmit={() => {
          if (validManagementSearch(draft)) setQ(draft.trim());
        }}
      />
      {role === 'admin' ? (
        <Field label={t('common.workflow.templateState')}>
          {(props) => (
            <select
              {...props}
              value={state}
              onChange={(event) => setState(event.target.value as typeof state)}
            >
              <option value="enabled">{t('common.enabled')}</option>
              <option value="disabled">{t('common.disabled')}</option>
              <option value="all">{t('common.all')}</option>
            </select>
          )}
        </Field>
      ) : null}
      {query.isPending ? (
        <LoadingState />
      ) : query.error ? (
        <ErrorState error={query.error} onRetry={() => void query.refetch()} />
      ) : (
        <>
          <DataTable
            caption={t('common.workflow.templates')}
            rowKey={(row) => row.id}
            rows={query.data.data}
            selectedKey={selected?.id}
            columns={[
              {
                key: 'name',
                header: t('common.workflow.templateName'),
                cell: 'title',
                render: (row) => (
                  <>
                    <strong>{row.name}</strong>
                    <span className="nb-sub">{row.description}</span>
                  </>
                ),
              },
              {
                key: 'view',
                header: t('common.workflow.actions'),
                cell: 'action',
                render: (row) => (
                  <Button
                    type="button"
                    disabled={query.isFetching}
                    onClick={() => setSelected(row)}
                  >
                    {t('common.workflow.viewPrice')}
                  </Button>
                ),
              },
            ]}
          />
          {query.data.data.length === 0 ? (
            <EmptyState
              title={t('common.workflow.noTemplates')}
              body={t('common.workflow.trySearch')}
            />
          ) : null}
          <PagePagination
            metadata={query.data.pagination}
            requestedPage={pager.page}
            onPageChange={pager.setPage}
            onPageSizeChange={pager.setPageSize}
            busy={query.isFetching}
          />
        </>
      )}
      {selected ? (
        <section aria-label={t('common.workflow.readOnlyPrice')} ref={preview} tabIndex={-1}>
          <h4>
            {selected.name} · {t('common.workflow.revision', { revision: selected.revision })}
          </h4>
          <PriceConfigSummary config={selected} role={role} />
          <Button
            type="button"
            disabled={!selected.enabled}
            onClick={() => onApply(priceSnapshot(selected), selected.name, selected.revision)}
          >
            {t('common.workflow.fillTemplate')}
          </Button>
        </section>
      ) : null}
      {role === 'admin' ? (
        <Button
          type="button"
          onClick={() => {
            setSelected(null);
            setParams((next) => {
              next.set('charity_section', 'templates');
              return next;
            });
          }}
        >
          {t('common.workflow.maintainTemplates')}
        </Button>
      ) : null}
    </div>
  );
}
