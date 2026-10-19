import { useEffect, useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
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
  deletePriceTemplate,
  getPriceTemplates,
  savePriceTemplate,
  type CharityPriceTemplate,
} from '@shared/operations/charityPriceTemplates';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { usePagePager } from '@shared/operations/usePagePager';
import { PagePagination } from '@shared/operations/PagePagination';
import { isForbidden, isUnauthorized } from '@shared/query/http';
import { ConfirmDialog } from '../ConfirmDialog';
import { EmptyState, ErrorState, LoadingState } from '../States';
import { Button, DataTable, Field, FilterBar, Fold, Note, Toggle } from '../ui';
import { PriceConfigSummary } from './PriceConfigSummary';

interface PriceToolsProps {
  role: CharityRole;
  current: CharityPriceConfig | null;
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

function PriceTemplates({ role, current, onApply, onCapabilityLoss }: PriceToolsProps) {
  const { t } = useTranslation();
  const client = useQueryClient();
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
  const [metadata, setMetadata] = useState({ name: '', description: '', enabled: true });
  const [deleting, setDeleting] = useState(false);
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
  const refresh = () => client.invalidateQueries({ queryKey: root }, { throwOnError: true });
  const save = useRetainedOperation<Parameters<typeof savePriceTemplate>[1], CharityPriceTemplate>(
    (input, key) => savePriceTemplate(role, input, key),
    refresh,
    charityKeys.root(role),
  );
  const remove = useRetainedOperation<{ id: string; revision: string }, void>(
    (input, key) => deletePriceTemplate(role, input.id, input.revision, key),
    refresh,
    charityKeys.root(role),
  );
  const lost = [query.error, save.error, remove.error].some(
    (error) => isForbidden(error) || isUnauthorized(error),
  );
  useEffect(() => {
    if (lost) onCapabilityLoss?.();
  }, [lost, onCapabilityLoss]);
  const busy =
    save.isPending ||
    remove.isPending ||
    save.outcome === 'unknown' ||
    save.outcome === 'refresh-failed' ||
    remove.outcome === 'unknown' ||
    remove.outcome === 'refresh-failed';
  const valid =
    metadata.name.trim().length > 0 &&
    Array.from(metadata.name.trim()).length <= 80 &&
    Array.from(metadata.description).length <= 500;
  const choose = (row: CharityPriceTemplate) => {
    setSelected(row);
    setMetadata({ name: row.name, description: row.description, enabled: row.enabled });
    save.reset();
    remove.reset();
  };
  const submit = (source: CharityPriceConfig, replace: boolean) =>
    save.mutate(
      {
        ...priceSnapshot(source),
        ...metadata,
        name: metadata.name.trim(),
        ...(replace && selected ? { id: selected.id, expected_revision: selected.revision } : {}),
      },
      { onSuccess: choose },
    );
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
                    disabled={busy || query.isFetching}
                    onClick={() => choose(row)}
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
            disabled={!selected.enabled || busy}
            onClick={() => onApply(priceSnapshot(selected), selected.name, selected.revision)}
          >
            {t('common.workflow.fillTemplate')}
          </Button>
        </section>
      ) : null}
      {role === 'admin' ? (
        <Fold title={t('common.workflow.maintainTemplates')} plain>
          <fieldset className="nb-form" disabled={busy}>
            <Field label={t('common.workflow.templateName')}>
              {(props) => (
                <input
                  {...props}
                  value={metadata.name}
                  maxLength={160}
                  onChange={(event) => setMetadata({ ...metadata, name: event.target.value })}
                />
              )}
            </Field>
            <Field label={t('common.workflow.templateDescription')}>
              {(props) => (
                <textarea
                  {...props}
                  value={metadata.description}
                  onChange={(event) =>
                    setMetadata({ ...metadata, description: event.target.value })
                  }
                />
              )}
            </Field>
            <Toggle
              label={t('common.workflow.templateEnabled')}
              checked={metadata.enabled}
              onChange={(enabled) => setMetadata({ ...metadata, enabled })}
            />
            <div className="nb-inline">
              <Button
                type="button"
                disabled={!valid || !current}
                onClick={() => current && submit(current, false)}
              >
                {t('common.workflow.createTemplate')}
              </Button>
              {selected ? (
                <>
                  <Button type="button" disabled={!valid} onClick={() => submit(selected, true)}>
                    {t('common.workflow.saveTemplateMetadata')}
                  </Button>
                  <Button
                    type="button"
                    disabled={!valid || !current}
                    onClick={() => current && submit(current, true)}
                  >
                    {t('common.workflow.overwriteTemplate')}
                  </Button>
                  <Button type="button" variant="danger" onClick={() => setDeleting(true)}>
                    {t('common.workflow.deleteTemplate')}
                  </Button>
                </>
              ) : null}
            </div>
          </fieldset>
        </Fold>
      ) : null}
      {save.error ? (
        <ErrorState error={save.error} />
      ) : remove.error ? (
        <ErrorState error={remove.error} />
      ) : null}
      {save.outcome === 'unknown' && save.variables ? (
        <Button type="button" onClick={() => save.mutate(save.variables!, { onSuccess: choose })}>
          {t('common.donationReview.retrySame')}
        </Button>
      ) : null}
      {remove.outcome === 'unknown' && remove.variables ? (
        <Button
          type="button"
          onClick={() => remove.mutate(remove.variables!, { onSuccess: () => setSelected(null) })}
        >
          {t('common.donationReview.retrySame')}
        </Button>
      ) : null}
      {save.refreshError ? (
        <ErrorState error={save.refreshError} onRetry={() => void save.refresh()} />
      ) : null}
      {remove.refreshError ? (
        <ErrorState error={remove.refreshError} onRetry={() => void remove.refresh()} />
      ) : null}
      {deleting && selected ? (
        <ConfirmDialog
          open
          title={t('common.workflow.deleteTemplate')}
          description={t('common.workflow.deleteTemplateHelp')}
          confirmLabel={t('common.workflow.deleteTemplate')}
          danger
          onCancel={() => setDeleting(false)}
          onConfirm={() => {
            setDeleting(false);
            remove.mutate(
              { id: selected.id, revision: selected.revision },
              { onSuccess: () => setSelected(null) },
            );
          }}
          busy={remove.isPending}
        />
      ) : null}
    </div>
  );
}
