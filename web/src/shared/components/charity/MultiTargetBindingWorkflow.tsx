import { useEffect, useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import {
  charityKeys,
  getManagedDonation,
  suggestedBindingServices,
  type CharityRole,
} from '@shared/operations/charity';
import {
  getManagedCharityModel,
  validManagementSearch,
} from '@shared/operations/charityModelPages';
import {
  addKeyBindings,
  MAX_MANUAL_BINDINGS,
  getBindingOptions,
  type BindingOption,
  type BindingMatrixReceipt,
  type ManualBindingInput,
} from '@shared/operations/charityBindingBatch';
import type { ManagedDonationKeySummary } from '@shared/operations/donationPages';
import { usePagePager } from '@shared/operations/usePagePager';
import { useDetailNavigation } from '@shared/operations/useDetailNavigation';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { useSearchState } from '@shared/operations/useSearchState';
import { PagePagination } from '@shared/operations/PagePagination';
import { isManagementForbidden, isNotFoundError, isUnauthorized } from '@shared/query/http';
import { getManagedBindingCandidatesPage } from '@shared/operations/charityPages';
import { ConfirmDialog } from '../ConfirmDialog';
import {
  CharityBindingPicker,
  type CandidateFilters,
  type CharitySelection,
} from '../CharityBindingPicker';
import { BindingServiceEditor } from './BindingRouteGuide';
import { DonationKeyModels } from '../DonationKeyModels';
import { DraftLeaveGuard } from './DraftLeaveScope';
import { CharityModelScope } from '../CharityModelScope';
import { EmptyState, ErrorState, LoadingState, SectionHead } from '../States';
import { Button, DataTable, Field, FilterBar, Note, OutcomeNote, StatusBadge } from '../ui';
import { BindingBatchResults, type BindingResultLabel } from './BindingBatchResults';

interface Selection extends BindingResultLabel {
  target: BindingOption;
  candidate: CharitySelection;
  unavailable?: boolean;
}
interface Submission {
  body: ManualBindingInput;
  labels: BindingResultLabel[];
  identities: string[];
}
export function MultiTargetBindingWorkflow({
  role,
  accountId,
  item,
  onBack,
  onCreate,
  initialTarget = '',
  onCapabilityLoss,
}: {
  role: CharityRole;
  accountId: string;
  item: ManagedDonationKeySummary;
  onBack: () => void;
  onCreate: () => void;
  initialTarget?: string;
  onCapabilityLoss?: () => void;
}) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const [, setParams] = useSearchState();
  const keyModels = useRef<HTMLDivElement>(null);
  const initialApplied = useRef('');
  const matching = useRef<AbortController | null>(null);
  useEffect(() => () => matching.current?.abort(), []);
  const [proposals, setProposals] = useState<Selection[]>([]);
  const [checked, setChecked] = useState<string[]>([]);
  const [matchedQuery, setMatchedQuery] = useState<string | null>(null);
  const [preview, setPreview] = useState(false);
  const [target, setTarget] = useState<BindingOption | null>(null);
  const [basket, setBasket] = useState<Record<string, Selection>>({});
  const [basketSearch, setBasketSearch] = useState('');
  const basketSearchInput = useRef<HTMLInputElement>(null);
  const [donationRevision, setDonationRevision] = useState(item.donation_revision);
  const [q, setQ] = useState('');
  const [draft, setDraft] = useState('');
  const [pickerBlocked, setPickerBlocked] = useState(true);
  const candidateFilters = useState<CandidateFilters>();
  const [reading, setReading] = useState(false);
  const [readError, setReadError] = useState<unknown>(null);
  const pager = usePagePager({
    station: role === 'admin' ? 'admin' : 'user',
    listType: 'donation-binding-targets',
    scopeKey: `${accountId}:${item.key_id}`,
    resetKey: q,
  });
  const models = useQuery({
    queryKey: [
      ...charityKeys.root(role),
      'binding-options',
      accountId,
      item.key_id,
      q,
      pager.page,
      pager.pageSize,
    ],
    queryFn: ({ signal }) =>
      getBindingOptions(role, item.donation_id, item.key_id, q, pager.page, pager.pageSize, signal),
    retry: false,
  });
  const initial = useQuery({
    queryKey: [...charityKeys.root(role), 'initial-binding-target', accountId, initialTarget],
    queryFn: ({ signal }) => getManagedCharityModel(role, initialTarget, signal),
    enabled: Boolean(initialTarget),
    retry: false,
  });
  useEffect(() => {
    if (initial.data && initialApplied.current !== initial.data.id) {
      initialApplied.current = initial.data.id;
      setTarget({
        charity_model_id: initial.data.id,
        full_name: initial.data.full_name,
        enabled: initial.data.enabled,
        binding_revision: initial.data.binding_revision,
      });
    }
  }, [initial.data]);
  const refresh = () =>
    client.invalidateQueries({ queryKey: charityKeys.root(role) }, { throwOnError: true });
  const add = useRetainedOperation<Submission, BindingMatrixReceipt>(
    (input, key, context) =>
      addKeyBindings(role, item.donation_id, item.key_id, input.body, key, context.signal),
    refresh,
    charityKeys.root(role),
  );
  const lost = [models.error, initial.error, add.error, readError].some(
    (error) => isManagementForbidden(error) || isUnauthorized(error),
  );
  useEffect(() => {
    if (lost) onCapabilityLoss?.();
  }, [lost, onCapabilityLoss]);
  const locked =
    add.isPending || add.outcome === 'unknown' || add.outcome === 'refresh-failed' || reading;
  const entries = Object.entries(basket);
  const filteredEntries = entries.filter(([, entry]) =>
    `${entry.full_name}\n${entry.upstream_model_id}`
      .toLowerCase()
      .includes(basketSearch.trim().toLowerCase()),
  );
  const basketPager = usePagePager({
    station: role === 'admin' ? 'admin' : 'user',
    listType: 'multi-target-binding-draft',
    scopeKey: `${accountId}:${item.key_id}`,
    resetKey: basketSearch,
  });
  const basketPages = Math.max(1, Math.ceil(filteredEntries.length / basketPager.pageSize));
  const basketPage = Math.min(Number(basketPager.page), basketPages);
  const currentEntries = entries.filter(
    ([, entry]) => entry.target.charity_model_id === target?.charity_model_id,
  );
  const selected = Object.fromEntries(
    currentEntries.map(([, entry]) => [
      `${item.key_id}:${entry.upstream_model_id}`,
      entry.candidate,
    ]),
  );
  const { listRef, detailRef, remember } = useDetailNavigation(target?.charity_model_id ?? '');
  const reread = async () => {
    setReading(true);
    setReadError(null);
    try {
      const ids = [...new Set(entries.map(([, entry]) => entry.target.charity_model_id))];
      const donation = await getManagedDonation(role, item.donation_id);
      const targets = new Array<readonly [string, string | undefined]>(ids.length);
      let next = 0;
      let failed = false;
      let failure: unknown;
      await Promise.all(
        Array.from({ length: Math.min(4, ids.length) }, async () => {
          while (next < ids.length && !failed) {
            const index = next++;
            const id = ids[index]!;
            try {
              const model = await getManagedCharityModel(role, id);
              targets[index] = [id, model.binding_revision];
            } catch (error) {
              if (isNotFoundError(error)) targets[index] = [id, undefined];
              else {
                failed = true;
                failure = error;
              }
            }
          }
        }),
      );
      if (failed) throw failure;
      const revisions = new Map(targets);
      setDonationRevision(donation.revision);
      setBasket((previous) =>
        Object.fromEntries(
          Object.entries(previous).map(([id, entry]) => [
            id,
            {
              ...entry,
              target: {
                ...entry.target,
                binding_revision:
                  revisions.get(entry.target.charity_model_id) ?? entry.target.binding_revision,
              },
              unavailable: revisions.get(entry.target.charity_model_id) === undefined,
            },
          ]),
        ),
      );
      if (
        target &&
        revisions.has(target.charity_model_id) &&
        !revisions.get(target.charity_model_id)
      )
        setTarget(null);
      await refresh();
      add.reset();
    } catch (error) {
      setReadError(error);
    } finally {
      setReading(false);
    }
  };
  const submit = (input: Submission) =>
    add.mutate(input, {
      onSuccess: (receipt) => {
        const completed = new Set(
          input.identities.filter((_, index) =>
            ['added', 'already_bound'].includes(receipt.items[0]!.results[index]!),
          ),
        );
        const revisions = new Map(
          receipt.models.map((model) => [model.charity_model_id, model.binding_revision]),
        );
        setBasket((previous) =>
          Object.fromEntries(
            Object.entries(previous)
              .filter(([id]) => !completed.has(id))
              .map(([id, entry]) => [
                id,
                {
                  ...entry,
                  target: {
                    ...entry.target,
                    binding_revision:
                      revisions.get(entry.target.charity_model_id) ?? entry.target.binding_revision,
                  },
                },
              ]),
          ),
        );
        setTarget((previous) =>
          previous
            ? {
                ...previous,
                binding_revision:
                  revisions.get(previous.charity_model_id) ?? previous.binding_revision,
              }
            : null,
        );
      },
    });
  const identityFor = (row: Selection) => row.target.charity_model_id + ':' + row.upstream_model_id;
  const findMatches = async () => {
    matching.current?.abort();
    const controller = new AbortController();
    matching.current = controller;
    setReading(true);
    setReadError(null);
    setProposals([]);
    setChecked([]);
    setMatchedQuery(null);
    const matches: Selection[] = [];
    try {
      for (let targetPage = 1; ; targetPage++) {
        const options = await getBindingOptions(
          role,
          item.donation_id,
          item.key_id,
          q,
          String(targetPage),
          100,
          controller.signal,
        );
        for (const model of options.data) {
          const full = model.full_name.replace(/^\[公益\]/, '');
          const short = full.slice(full.indexOf('/') + 1);
          if (!short) continue;
          let page = 1;
          for (;;) {
            const result = await getManagedBindingCandidatesPage(
              role,
              model.charity_model_id,
              {
                donation_id: item.donation_id,
                donation_key_id: item.key_id,
                q: short,
                include_bound: false,
                key_state: 'all',
              },
              String(page),
              100,
              controller.signal,
            );
            for (const row of result.data) {
              if (
                (row.upstream_model_id !== full && row.upstream_model_id !== short) ||
                !row.capabilities.can_bind ||
                row.current_binding_id
              )
                continue;
              const candidate = {
                ...row,
                note: item.safe_note ?? '',
                selected_native_services: suggestedBindingServices(row),
              };
              matches.push({
                target: model,
                candidate,
                full_name: model.full_name,
                upstream_model_id: row.upstream_model_id,
              });
            }
            if (page >= Number(result.pagination.total_pages)) break;
            page++;
          }
        }
        if (targetPage >= Number(options.pagination.total_pages)) break;
      }
      if (!controller.signal.aborted) {
        setProposals(matches);
        setMatchedQuery(q);
      }
    } catch (error) {
      if (!controller.signal.aborted) setReadError(error);
    } finally {
      if (!controller.signal.aborted) setReading(false);
    }
  };
  const selectUnambiguous = () => {
    const targets = new Map<string, number>();
    const routes = new Map<string, number>();
    for (const row of proposals) {
      targets.set(row.target.charity_model_id, (targets.get(row.target.charity_model_id) ?? 0) + 1);
      routes.set(row.upstream_model_id, (routes.get(row.upstream_model_id) ?? 0) + 1);
    }
    const eligible = proposals.filter(
      (row) =>
        !basket[identityFor(row)] &&
        targets.get(row.target.charity_model_id) === 1 &&
        routes.get(row.upstream_model_id) === 1,
    );
    setChecked(
      [...new Set([...checked, ...eligible.map(identityFor)])].slice(
        0,
        MAX_MANUAL_BINDINGS - entries.length,
      ),
    );
  };
  const submitBasket = () =>
    submit({
      body: {
        expected_donation_revision: donationRevision,
        targets: entries.map(([, row]) => ({
          charity_model_id: row.target.charity_model_id,
          expected_binding_revision: row.target.binding_revision,
          upstream_model_id: row.upstream_model_id,
          native_services: row.candidate.selected_native_services ?? [],
        })),
      },
      labels: entries.map(([, row]) => ({
        full_name: row.full_name,
        upstream_model_id: row.upstream_model_id,
      })),
      identities: entries.map(([id]) => id),
    });
  const submitDisabled =
    locked ||
    add.outcome === 'conflict' ||
    entries.length === 0 ||
    entries.some(([, entry]) => !entry.candidate.selected_native_services?.length) ||
    entries.some(([, entry]) => entry.unavailable) ||
    (Boolean(target) && pickerBlocked);
  if (lost) return <p role="alert">{t('common.operations.charity.accessLost')}</p>;
  return (
    <div className="nb-form" ref={listRef} tabIndex={-1}>
      <DraftLeaveGuard
        dirty={entries.length > 0 || add.outcome === 'unknown'}
        allowNavigation={({ currentLocation, nextLocation }) =>
          currentLocation.pathname === nextLocation.pathname &&
          new URLSearchParams(nextLocation.search).get('binding_key') === item.key_id &&
          new URLSearchParams(nextLocation.search).get('donation_id') === item.donation_id
        }
      />
      <ConfirmDialog
        open={preview}
        title={t('common.governance.reviewBindings')}
        description={t('common.workflow.addCount', { count: entries.length })}
        confirmLabel={t('common.workflow.addCount', { count: entries.length })}
        onCancel={() => setPreview(false)}
        confirmDisabled={submitDisabled}
        onConfirm={() => {
          setPreview(false);
          submitBasket();
        }}
      >
        <ul>
          {entries.map(([id, row]) => (
            <li key={id}>
              {row.full_name} ← {row.upstream_model_id} ·{' '}
              {row.candidate.selected_native_services?.join(', ')}
            </li>
          ))}
        </ul>
      </ConfirmDialog>
      <SectionHead
        title={t('common.workflow.addConnections')}
        description={t('common.bindingBatch.fixedKeyHelp', {
          name: item.safe_note || t('common.bindingBatch.keyNumber', { id: item.key_id }),
        })}
        back={
          <Button type="button" variant="ghost" disabled={locked} onClick={onBack}>
            {t('common.workflow.returnDonation')}
          </Button>
        }
        actions={
          <>
            <Button
              type="button"
              variant="primary"
              disabled={submitDisabled}
              onClick={() => setPreview(true)}
            >
              {t('common.workflow.addCount', { count: entries.length })}
            </Button>
            <Button type="button" disabled={locked} onClick={onCreate}>
              {t('common.workflow.createThenBind')}
            </Button>
          </>
        }
      />
      {target ? (
        <div className="nb-form" ref={detailRef} tabIndex={-1}>
          <Button type="button" variant="ghost" disabled={locked} onClick={() => setTarget(null)}>
            {t('common.bindingBatch.backTargets')}
          </Button>
          <h2 className="nb-workflow-id">{target.full_name}</h2>
          <CharityModelScope modelID={role === 'steward' ? target.charity_model_id : undefined}>
            <CharityBindingPicker
              key={target.charity_model_id}
              role={role}
              modelId={target.charity_model_id}
              fixedKey={item}
              retainedCandidateFilters={candidateFilters}
              showSelectionSummary={false}
              capacity={MAX_MANUAL_BINDINGS - entries.length + currentEntries.length}
              selected={selected}
              locked={locked || add.outcome === 'conflict'}
              onReadStateChange={setPickerBlocked}
              onCapabilityLoss={onCapabilityLoss}
              onInspectBindings={() => {
                setParams((previous) => {
                  const next = new URLSearchParams(previous);
                  next.set(`key_models_${item.key_id}`, 'open');
                  return next;
                });
                requestAnimationFrame(() => keyModels.current?.scrollIntoView({ block: 'start' }));
              }}
              onChange={(next) => {
                const revisionTarget = currentEntries[0]?.[1].target ?? target;
                setBasket((previous) => ({
                  ...Object.fromEntries(
                    Object.entries(previous).filter(
                      ([, entry]) => entry.target.charity_model_id !== target.charity_model_id,
                    ),
                  ),
                  ...Object.fromEntries(
                    Object.values(next).map((candidate) => [
                      `${target.charity_model_id}:${candidate.upstream_model_id}`,
                      {
                        target: revisionTarget,
                        candidate,
                        full_name: target.full_name,
                        upstream_model_id: candidate.upstream_model_id,
                      },
                    ]),
                  ),
                }));
                if (add.outcome !== 'conflict') add.reset();
              }}
            />
            <div ref={keyModels}>
              <DonationKeyModels
                role={role}
                accountId={accountId}
                donationId={item.donation_id}
                keyId={item.key_id}
                onCapabilityLoss={onCapabilityLoss}
              />
            </div>
          </CharityModelScope>
        </div>
      ) : (
        <>
          <FilterBar
            query={models}
            activeCount={q ? 1 : 0}
            onReset={() => {
              setDraft('');
              setQ('');
            }}
            onSubmit={() => {
              if (validManagementSearch(draft)) setQ(draft.trim());
            }}
            search={
              <>
                <Field label={t('common.operations.charity.searchModels')}>
                  {(props) => (
                    <input
                      {...props}
                      type="search"
                      placeholder={t('common.workflow.modelSearch')}
                      value={draft}
                      onChange={(event) => setDraft(event.target.value)}
                    />
                  )}
                </Field>
              </>
            }
          />
          <Button
            type="button"
            disabled={locked || !models.data?.data.length}
            onClick={() => void findMatches()}
          >
            {t('common.governance.matchCandidates')}
          </Button>
          {matchedQuery === q && !proposals.length && (
            <Note>{t('common.operations.charity.noCandidatesBody')}</Note>
          )}
          {matchedQuery === q && proposals.length > 0 && (
            <fieldset>
              <legend>{t('common.governance.reviewBindings')}</legend>
              <Button
                type="button"
                disabled={locked || entries.length >= MAX_MANUAL_BINDINGS}
                onClick={selectUnambiguous}
              >
                {t('common.governance.selectUnique')}
              </Button>
              {proposals.map((row) => {
                const id = identityFor(row);
                return (
                  <label key={id}>
                    <input
                      type="checkbox"
                      checked={checked.includes(id)}
                      disabled={locked || !!basket[id]}
                      onChange={(event) =>
                        setChecked(
                          event.target.checked
                            ? [...checked, id]
                            : checked.filter((value) => value !== id),
                        )
                      }
                    />
                    {row.full_name} ← {row.upstream_model_id}
                  </label>
                );
              })}
              <Button
                type="button"
                disabled={
                  locked || !checked.length || entries.length + checked.length > MAX_MANUAL_BINDINGS
                }
                onClick={() => {
                  setBasket((prior) => ({
                    ...prior,
                    ...Object.fromEntries(
                      proposals
                        .filter((row) => checked.includes(identityFor(row)))
                        .map((row) => [identityFor(row), row]),
                    ),
                  }));
                  setProposals([]);
                  setChecked([]);
                  setMatchedQuery(null);
                }}
              >
                {t('common.governance.confirmCandidates')}
              </Button>
            </fieldset>
          )}
          {models.isPending ? (
            <LoadingState />
          ) : models.error ? (
            <ErrorState error={models.error} onRetry={() => void models.refetch()} />
          ) : (
            <>
              {models.data.data.length ? (
                <DataTable
                  caption={t('common.workflow.chooseTarget')}
                  rows={models.data.data}
                  rowKey={(row) => row.charity_model_id}
                  columns={[
                    {
                      key: 'model',
                      header: t('common.workflow.model'),
                      mobileLabel: t('common.workflow.model'),
                      cell: 'title',
                      render: (row) => <strong className="nb-workflow-id">{row.full_name}</strong>,
                    },
                    {
                      key: 'state',
                      header: t('common.operations.charity.state'),
                      mobileLabel: t('common.operations.charity.state'),
                      cell: 'status',
                      render: (row) => (
                        <StatusBadge tone={row.enabled ? 'ok' : 'neutral'}>
                          {t(row.enabled ? 'common.enabled' : 'common.disabled')}
                        </StatusBadge>
                      ),
                    },
                    {
                      key: 'select',
                      header: t('common.workflow.actions'),
                      mobileLabel: t('common.workflow.actions'),
                      cell: 'action',
                      align: 'action',
                      render: (row) => (
                        <Button
                          type="button"
                          disabled={locked || models.isFetching}
                          onClick={() => {
                            remember();
                            setTarget(row);
                          }}
                        >
                          {t('common.bindingBatch.chooseUpstream')}
                        </Button>
                      ),
                    },
                  ]}
                />
              ) : (
                <EmptyState
                  title={t('common.workflow.noModels')}
                  body={t('common.workflow.trySearch')}
                />
              )}
              <PagePagination
                metadata={models.data.pagination}
                requestedPage={pager.page}
                onPageChange={pager.setPage}
                onPageSizeChange={pager.setPageSize}
                busy={models.isFetching || locked}
              />
            </>
          )}
        </>
      )}
      {entries.length ? (
        <section className="nb-form" aria-label={t('common.bindingBatch.selected')}>
          <h2>{t('common.bindingBatch.selectedCount', { count: entries.length })}</h2>
          <p role="status">
            {t('common.bindingBatch.selectionCapacity', {
              selected: entries.length,
              capacity: MAX_MANUAL_BINDINGS,
            })}
          </p>
          <Field label={t('common.bindingBatch.searchSelected')}>
            {(props) => (
              <input
                {...props}
                type="search"
                placeholder={t('common.bindingBatch.searchPlaceholder')}
                ref={basketSearchInput}
                value={basketSearch}
                onChange={(event) => setBasketSearch(event.target.value)}
              />
            )}
          </Field>
          {filteredEntries.length ? (
            <>
              <DataTable
                caption={t('common.bindingBatch.selected')}
                rows={filteredEntries.slice(
                  (basketPage - 1) * basketPager.pageSize,
                  basketPage * basketPager.pageSize,
                )}
                rowKey={([id]) => id}
                columns={[
                  {
                    key: 'target',
                    header: t('common.workflow.model'),
                    mobileLabel: t('common.workflow.model'),
                    cell: 'title',
                    render: ([, row]) => (
                      <span className="nb-workflow-id">
                        {row.full_name}
                        {row.unavailable ? ` · ${t('common.bindingPresets.deletedTarget')}` : ''}
                      </span>
                    ),
                  },
                  {
                    key: 'upstream',
                    header: t('common.bindingBatch.upstream'),
                    mobileLabel: t('common.bindingBatch.upstream'),
                    cell: 'meta',
                    render: ([id, row]) => (
                      <div className="nb-stack nb-stack--sm">
                        <span className="mono nb-workflow-id">{row.upstream_model_id}</span>
                        <BindingServiceEditor
                          guide={row.candidate.binding_guide}
                          source={row.candidate}
                          options={row.candidate.native_services}
                          value={row.candidate.selected_native_services ?? []}
                          disabled={locked || add.outcome === 'conflict'}
                          onChange={(services) => {
                            setBasket((previous) => ({
                              ...previous,
                              [id]: {
                                ...row,
                                candidate: {
                                  ...row.candidate,
                                  selected_native_services: services,
                                },
                              },
                            }));
                            add.reset();
                          }}
                        />
                      </div>
                    ),
                  },
                  {
                    key: 'remove',
                    header: t('common.workflow.actions'),
                    mobileLabel: t('common.workflow.actions'),
                    cell: 'action',
                    render: ([id, row]) => (
                      <Button
                        type="button"
                        aria-label={t('common.workflow.removeSelection', {
                          id: `${row.full_name}: ${row.upstream_model_id}`,
                        })}
                        disabled={locked || add.outcome === 'conflict'}
                        onClick={() => {
                          setBasket((previous) =>
                            Object.fromEntries(
                              Object.entries(previous).filter(([key]) => key !== id),
                            ),
                          );
                          basketSearchInput.current?.focus();
                        }}
                      >
                        {t('common.operations.charity.remove')}
                      </Button>
                    ),
                  },
                ]}
              />
              <PagePagination
                metadata={{
                  page: String(basketPage),
                  page_size: basketPager.pageSize,
                  total_items: String(filteredEntries.length),
                  total_pages: String(basketPages),
                }}
                requestedPage={basketPager.page}
                onPageChange={basketPager.setPage}
                onPageSizeChange={basketPager.setPageSize}
                busy={locked}
              />
            </>
          ) : (
            <EmptyState
              title={t('common.bindingBatch.noSelectedMatches')}
              body={t('common.bindingBatch.noSelectedMatchesHelp')}
            />
          )}
        </section>
      ) : null}
      {add.outcome === 'conflict' ? (
        <Note
          tone="warn"
          action={
            <Button type="button" disabled={reading} onClick={() => void reread()}>
              {t('common.workflow.refreshSelection')}
            </Button>
          }
        >
          {t('common.workflow.bindingConflict')}
        </Note>
      ) : null}
      {readError ? <ErrorState error={readError} onRetry={() => void reread()} /> : null}
      {add.error && add.outcome !== 'conflict' ? <ErrorState error={add.error} /> : null}
      <OutcomeNote
        outcome={
          add.outcome === 'refresh-failed'
            ? { kind: 'savedRefreshFailed', recheck: () => void add.refresh() }
            : add.outcome === 'unknown'
              ? { kind: 'unknown' }
              : { kind: 'idle' }
        }
        busy={add.isPending}
      />
      {add.data && add.variables ? (
        <BindingBatchResults
          role={role}
          receipt={add.data}
          labels={add.variables.labels}
          scopeKey={`${accountId}:${item.key_id}`}
          keyNames={{ [item.key_id]: item.safe_note }}
        />
      ) : null}

      {add.outcome === 'unknown' && add.variables ? (
        <Button type="button" disabled={add.isPending} onClick={() => submit(add.variables!)}>
          {t('common.donationReview.retrySame')}
        </Button>
      ) : null}
      {entries.length >= MAX_MANUAL_BINDINGS ? <Note>{t('common.bindingBatch.limit')}</Note> : null}
    </div>
  );
}
