import { useEffect, useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { charityKeys } from '@shared/operations/charity';
import { validManagementSearch } from '@shared/operations/charityModelPages';
import {
  deletePriceTemplate,
  getPriceTemplate,
  getPriceTemplates,
  savePriceTemplate,
  type CharityPriceTemplate,
} from '@shared/operations/charityPriceTemplates';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { useSearchState } from '@shared/operations/useSearchState';
import { useUrlPagePager } from '@shared/operations/useUrlPagePager';
import { PagePagination } from '@shared/operations/PagePagination';
import { isForbidden, isUnauthorized } from '@shared/query/http';
import { useDateTimeFormatter } from '@shared/utils/datetime';
import { DraftLeaveGuard } from './DraftLeaveScope';
import { ConfirmDialog } from '../ConfirmDialog';
import { EmptyState, ErrorState, LoadingState, PageHeader, StatusBadge } from '../States';
import {
  Button,
  DataTable,
  Field,
  FilterBar,
  Note,
  OutcomeNote,
  Panel,
  PanelBody,
  SaveBar,
  Toggle,
} from '../ui';
import { PriceConfigFields } from './PriceConfigFields';
import { PriceConfigBrief } from './PriceConfigSummary';
import { draftPriceConfig, priceDraft, priceDraftError, priceDraftSignature } from './priceDraft';
import { oneParam, selectedID } from './managementFields';

const root = [...charityKeys.root('admin'), 'price-templates'] as const;

export function PriceTemplateManagement({
  accountId,
  onCapabilityLoss,
}: {
  accountId: string;
  onCapabilityLoss?: () => void;
}) {
  const { t } = useTranslation();
  const formatTime = useDateTimeFormatter();
  const [params, setParams] = useSearchState();
  const rawQuery = oneParam(params, 'template_q');
  const q = validManagementSearch(rawQuery) ? rawQuery : '';
  const rawState = oneParam(params, 'template_state');
  const state = rawState === 'enabled' || rawState === 'disabled' ? rawState : 'all';
  const target =
    oneParam(params, 'price_template') === 'new' ? 'new' : selectedID(params, 'price_template');
  const [search, setSearch] = useState({ q, text: q });
  const searchText = search.q === q ? search.text : q;
  const pager = useUrlPagePager({
    station: 'admin',
    listType: 'price-template-management',
    scopeKey: accountId,
    pageParam: 'templates_page',
    pageSizeParam: 'templates_page_size',
  });
  const list = useQuery({
    queryKey: [...root, 'list', accountId, q, state, pager.page, pager.pageSize],
    queryFn: ({ signal }) =>
      getPriceTemplates('admin', q, state, pager.page, pager.pageSize, signal),
    retry: false,
  });
  const detail = useQuery({
    queryKey: [...root, 'detail', accountId, target],
    queryFn: ({ signal }) => getPriceTemplate('admin', target, signal),
    enabled: Boolean(target && target !== 'new'),
    retry: false,
  });
  const lost = [list.error, detail.error].some(
    (error) => isForbidden(error) || isUnauthorized(error),
  );
  useEffect(() => {
    if (lost) onCapabilityLoss?.();
  }, [lost, onCapabilityLoss]);
  const listNode = useRef<HTMLDivElement>(null);
  const returnPosition = useRef<{ top: number; id?: string } | null>(null);
  const previousTarget = useRef(target);
  const initialListView = useRef(true);
  useEffect(() => {
    if (initialListView.current && !target) {
      initialListView.current = false;
      listNode.current?.focus({ preventScroll: true });
      listNode.current?.scrollIntoView?.({ block: 'start', behavior: 'instant' });
    }
    if (previousTarget.current && !target && returnPosition.current) {
      const { top, id } = returnPosition.current;
      (id ? document.getElementById(id) : listNode.current)?.focus({ preventScroll: true });
      window.scrollTo?.({ top, behavior: 'instant' });
    }
    previousTarget.current = target;
  }, [target]);
  const select = (id: string) => {
    const active = document.activeElement;
    returnPosition.current = {
      top: window.scrollY,
      id: active instanceof HTMLElement ? active.id : undefined,
    };
    setParams((next) => {
      next.set('price_template', id);
      return next;
    });
  };
  const back = () =>
    setParams((next) => {
      next.delete('price_template');
      return next;
    });
  const filter = (query: string, nextState = state) =>
    setParams((next) => {
      next.set('template_q', query);
      next.set('template_state', nextState);
      next.set('templates_page', '1');
      return next;
    });
  if (lost) return <Note tone="bad">{t('common.operations.charity.accessLost')}</Note>;
  if (target)
    return target === 'new' ? (
      <PriceTemplateEditor key="new" onBack={back} onCapabilityLoss={onCapabilityLoss} />
    ) : detail.isPending ? (
      <LoadingState />
    ) : detail.error ? (
      <>
        <Button onClick={back}>{t('common.templateManagement.back')}</Button>
        <ErrorState error={detail.error} onRetry={() => void detail.refetch()} />
      </>
    ) : (
      <PriceTemplateEditor
        key={target}
        initial={detail.data}
        onBack={back}
        onCapabilityLoss={onCapabilityLoss}
      />
    );
  return (
    <div className="nb-form nb-template-list" ref={listNode} tabIndex={-1}>
      <PageHeader
        title={t('common.workflow.templates')}
        description={t('common.templateManagement.help')}
        back={
          params.has('charity_model') || params.get('model_workspace') === 'new' ? (
            <Button
              variant="ghost"
              onClick={() =>
                setParams((next) => {
                  next.set('charity_section', 'models');
                  return next;
                })
              }
            >
              {t('common.templateManagement.backModel')}
            </Button>
          ) : undefined
        }
        actions={
          <Button id="new-price-template" variant="primary" onClick={() => select('new')}>
            {t('common.workflow.createTemplate')}
          </Button>
        }
      />
      <FilterBar
        activeCount={state === 'all' ? 0 : 1}
        onReset={() => {
          setSearch({ q: '', text: '' });
          filter('', 'all');
        }}
        onSubmit={() => {
          if (validManagementSearch(searchText)) filter(searchText.trim());
        }}
        search={
          <>
            <Field label={t('common.search')}>
              {(props) => (
                <input
                  {...props}
                  type="search"
                  placeholder={t('common.workflow.templateSearch')}
                  value={searchText}
                  onChange={(e) => setSearch({ q, text: e.target.value })}
                />
              )}
            </Field>
          </>
        }
        secondary={
          <Field label={t('common.workflow.templateState')}>
            {(props) => (
              <select
                {...props}
                value={state}
                onChange={(e) => filter(q, e.target.value as typeof state)}
              >
                <option value="all">{t('common.all')}</option>
                <option value="enabled">{t('common.enabled')}</option>
                <option value="disabled">{t('common.disabled')}</option>
              </select>
            )}
          </Field>
        }
      />
      {list.isPending ? (
        <LoadingState />
      ) : list.error ? (
        <ErrorState error={list.error} onRetry={() => void list.refetch()} />
      ) : (
        <>
          {list.data.data.length ? (
            <DataTable
              caption={t('common.workflow.templates')}
              rows={list.data.data}
              rowKey={(row) => row.id}
              columns={[
                {
                  key: 'name',
                  header: t('common.workflow.templateName'),
                  cell: 'title',
                  render: (row) => (
                    <>
                      <strong className="nb-template-name" title={row.name}>
                        {row.name}
                      </strong>
                      <span className="nb-sub nb-template-description" title={row.description}>
                        {row.description}
                      </span>
                    </>
                  ),
                },
                {
                  key: 'state',
                  header: t('common.workflow.templateState'),
                  cell: 'status',
                  render: (row) => <StatusBadge active={row.enabled} />,
                },
                {
                  key: 'price',
                  header: t('common.operations.charity.pricing'),
                  cell: 'meta',
                  render: (row) => <PriceConfigBrief config={row} role="admin" />,
                },
                {
                  key: 'updated',
                  header: t('common.templateManagement.updated'),
                  cell: 'meta',
                  render: (row) => formatTime(row.updated_at),
                },
                {
                  key: 'actions',
                  header: t('common.workflow.actions'),
                  cell: 'action',
                  render: (row) => (
                    <Button
                      id={`edit-price-template-${row.id}`}
                      disabled={list.isFetching}
                      onClick={() => select(row.id)}
                    >
                      {t('common.edit')}
                    </Button>
                  ),
                },
              ]}
            />
          ) : (
            <EmptyState
              title={t('common.workflow.noTemplates')}
              body={t('common.templateManagement.empty')}
            />
          )}
          <PagePagination
            alwaysShowJump
            metadata={list.data.pagination}
            requestedPage={pager.page}
            onPageChange={pager.setPage}
            onPageSizeChange={pager.setPageSize}
            busy={list.isFetching}
          />
        </>
      )}
    </div>
  );
}

function PriceTemplateEditor({
  initial,
  onBack,
  onCapabilityLoss,
}: {
  initial?: CharityPriceTemplate;
  onBack: () => void;
  onCapabilityLoss?: () => void;
}) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const [base, setBase] = useState(initial);
  const [draft, setDraft] = useState(() => priceDraft(initial));
  const [metadata, setMetadata] = useState(() => ({
    name: initial?.name ?? '',
    description: initial?.description ?? '',
    enabled: initial?.enabled ?? true,
  }));
  const signature = () => JSON.stringify([metadata, priceDraftSignature(draft)]);
  const [saved, setSaved] = useState(signature);
  const [deleting, setDeleting] = useState(false);
  const [latestLoaded, setLatestLoaded] = useState(false);
  const heading = useRef<HTMLDivElement>(null);
  useEffect(() => {
    heading.current?.focus({ preventScroll: true });
    heading.current?.scrollIntoView?.({ block: 'start', behavior: 'instant' });
  }, []);
  const accept = (value: CharityPriceTemplate) => {
    setBase(value);
    const fields = priceDraft(value);
    const meta = { name: value.name, description: value.description, enabled: value.enabled };
    setDraft(fields);
    setMetadata(meta);
    setSaved(JSON.stringify([meta, priceDraftSignature(fields)]));
    setLatestLoaded(false);
  };
  const refresh = () => client.invalidateQueries({ queryKey: root }, { throwOnError: true });
  const refreshAfterDelete = async () => {
    await client.invalidateQueries({ queryKey: [...root, 'list'] }, { throwOnError: true });
    await client.invalidateQueries(
      {
        queryKey: root,
        predicate: (query) =>
          query.queryKey[root.length] !== 'detail' &&
          query.queryKey[root.length] !== 'reload' &&
          query.queryKey[root.length] !== 'list',
      },
      { throwOnError: true },
    );
  };
  const save = useRetainedOperation<Parameters<typeof savePriceTemplate>[1], CharityPriceTemplate>(
    (input, key) => savePriceTemplate('admin', input, key),
    refresh,
    charityKeys.root('admin'),
  );
  const remove = useRetainedOperation<{ id: string; revision: string }, void>(
    (input, key) => deletePriceTemplate('admin', input.id, input.revision, key),
    refreshAfterDelete,
    charityKeys.root('admin'),
  );
  useEffect(() => {
    if (remove.outcome === 'confirmed') onBack();
  }, [remove.outcome, onBack]);
  const latest = useQuery({
    queryKey: [...root, 'reload', base?.id],
    queryFn: ({ signal }) => getPriceTemplate('admin', base!.id, signal),
    enabled: false,
    retry: false,
  });
  const lost = [save.error, remove.error, latest.error].some(
    (error) => isForbidden(error) || isUnauthorized(error),
  );
  useEffect(() => {
    if (lost) onCapabilityLoss?.();
  }, [lost, onCapabilityLoss]);
  const locked =
    save.isPending ||
    remove.isPending ||
    save.outcome === 'unknown' ||
    remove.outcome === 'unknown' ||
    save.outcome === 'refresh-failed' ||
    remove.outcome === 'refresh-failed';
  const validation = priceDraftError(draft);
  const valid =
    metadata.name.trim().length > 0 &&
    Array.from(metadata.name.trim()).length <= 80 &&
    Array.from(metadata.description).length <= 500 &&
    !validation;
  const dirty = signature() !== saved;
  const resetOutcome = () => {
    save.reset();
    remove.reset();
    setLatestLoaded(false);
  };
  const submit = () => {
    const config = draftPriceConfig(draft);
    if (!valid || !config || locked) return;
    save.mutate(
      {
        ...config,
        ...metadata,
        name: metadata.name.trim(),
        ...(base ? { id: base.id, expected_revision: base.revision } : {}),
      },
      { onSuccess: accept },
    );
  };
  const reload = async () => {
    const result = await latest.refetch();
    if (result.data) {
      setBase(result.data);
      resetOutcome();
      setLatestLoaded(true);
    }
  };
  if (lost) return <Note tone="bad">{t('common.operations.charity.accessLost')}</Note>;
  const outcome = save.outcome !== 'idle' ? save : remove;
  return (
    <div className="nb-form nb-template-editor" ref={heading} tabIndex={-1}>
      <DraftLeaveGuard dirty={dirty && !remove.isSuccess} />
      <PageHeader
        title={base ? t('common.templateManagement.edit') : t('common.workflow.createTemplate')}
        description={
          base
            ? t('common.templateManagement.target', { name: base.name, revision: base.revision })
            : t('common.templateManagement.newHelp')
        }
        back={
          <Button variant="ghost" disabled={locked} onClick={onBack}>
            {t('common.templateManagement.back')}
          </Button>
        }
        actions={
          base ? (
            <Button variant="danger" disabled={locked} onClick={() => setDeleting(true)}>
              {t('common.workflow.deleteTemplate')}
            </Button>
          ) : undefined
        }
      />
      <Panel>
        <PanelBody>
          <fieldset className="nb-form nb-price-fields-section" disabled={locked}>
            <Field label={t('common.workflow.templateName')}>
              {(props) => (
                <input
                  {...props}
                  value={metadata.name}
                  maxLength={160}
                  onChange={(event) => {
                    resetOutcome();
                    setMetadata({ ...metadata, name: event.target.value });
                  }}
                />
              )}
            </Field>
            <Field label={t('common.workflow.templateDescription')}>
              {(props) => (
                <textarea
                  {...props}
                  rows={3}
                  value={metadata.description}
                  onChange={(event) => {
                    resetOutcome();
                    setMetadata({ ...metadata, description: event.target.value });
                  }}
                />
              )}
            </Field>
            <Toggle
              label={t('common.workflow.templateEnabled')}
              checked={metadata.enabled}
              onChange={(enabled) => {
                resetOutcome();
                setMetadata({ ...metadata, enabled });
              }}
            />
            <PriceConfigFields
              role="admin"
              value={draft}
              onChange={(fields) => {
                const next = { ...draft, ...fields };
                if (priceDraftSignature(next) !== priceDraftSignature(draft)) resetOutcome();
                setDraft((current) => ({ ...current, ...fields }));
              }}
              discountError={validation === 'discountDates' || validation === 'discountPercent'}
              recurringError={validation === 'recurringDiscount'}
            />
          </fieldset>
        </PanelBody>
      </Panel>
      {validation ? (
        <Note tone="bad">
          {validation === 'recurringDiscount'
            ? t('common.operations.charity.validation.recurringDiscount')
            : t(`common.operations.charity.validation.${validation}`)}
        </Note>
      ) : null}
      {save.error && save.outcome !== 'conflict' && save.outcome !== 'unknown' ? (
        <ErrorState error={save.error} />
      ) : null}
      {remove.error && remove.outcome !== 'conflict' && remove.outcome !== 'unknown' ? (
        <ErrorState error={remove.error} />
      ) : null}
      {outcome.outcome === 'conflict' ? (
        <>
          <OutcomeNote
            outcome={{ kind: 'conflict' }}
            body={t('common.templateManagement.conflict')}
          />
          <Button disabled={latest.isFetching} onClick={() => void reload()}>
            {t('common.templateManagement.loadLatest')}
          </Button>
        </>
      ) : null}
      {latest.error ? <ErrorState error={latest.error} onRetry={() => void reload()} /> : null}
      {latestLoaded ? (
        <Note>
          {t('common.templateManagement.latestLoaded', {
            name: base?.name,
            revision: base?.revision,
          })}
        </Note>
      ) : null}
      {save.outcome === 'confirmed' ? <OutcomeNote outcome={{ kind: 'saved' }} /> : null}
      {outcome.outcome === 'refresh-failed' ? (
        <OutcomeNote
          outcome={{ kind: 'savedRefreshFailed', recheck: () => void outcome.refresh() }}
        />
      ) : null}
      {outcome.outcome === 'unknown' ? (
        <>
          <OutcomeNote outcome={{ kind: 'unknown' }} />
          <Button
            disabled={outcome.isPending}
            onClick={() => {
              if (save.outcome === 'unknown' && save.variables)
                save.mutate(save.variables, { onSuccess: accept });
              else if (remove.variables) remove.mutate(remove.variables);
            }}
          >
            {t('common.donationReview.retrySame')}
          </Button>
        </>
      ) : null}
      <SaveBar
        dirtyCount={dirty ? 1 : 0}
        busy={locked}
        saveDisabled={!valid}
        scope={base?.name ?? t('common.workflow.createTemplate')}
        saveLabel={
          base
            ? t('common.templateManagement.save', { name: base.name })
            : t('common.workflow.createTemplate')
        }
        discardLabel={t('common.templateManagement.discard')}
        dirtyLabel={() => t('common.templateManagement.unsaved')}
        onSave={submit}
        onDiscard={() => {
          if (base) accept(base);
          else {
            const fields = priceDraft();
            const meta = { name: '', description: '', enabled: true };
            setDraft(fields);
            setMetadata(meta);
            setSaved(JSON.stringify([meta, priceDraftSignature(fields)]));
          }
          resetOutcome();
        }}
      />
      {deleting && base ? (
        <ConfirmDialog
          open
          danger
          title={t('common.templateManagement.delete', { name: base.name })}
          description={t('common.workflow.deleteTemplateHelp')}
          confirmLabel={t('common.workflow.deleteTemplate')}
          onCancel={() => setDeleting(false)}
          onConfirm={() => {
            setDeleting(false);
            remove.mutate({ id: base.id, revision: base.revision });
          }}
        />
      ) : null}
    </div>
  );
}
