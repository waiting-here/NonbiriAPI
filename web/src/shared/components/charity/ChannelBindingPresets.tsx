import { formatCount } from '@shared/utils/formatNumber';
import { useEffect, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { Link, useLocation } from 'react-router';
import { charityKeys, type CharityRole } from '@shared/operations/charity';
import {
  getManagedCharityModelsPage,
  validManagementSearch,
} from '@shared/operations/charityModelPages';
import {
  getBindingPreset,
  getPresetChannels,
  saveBindingPreset,
  type BindingPreset,
  type PresetChannel,
  type PresetMapping,
  type PresetSaveInput,
} from '@shared/operations/charityBindingBatch';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { useSearchState } from '@shared/operations/useSearchState';
import { useUrlPagePager } from '@shared/operations/useUrlPagePager';
import { usePagePager } from '@shared/operations/usePagePager';
import { PagePagination } from '@shared/operations/PagePagination';
import { listReturnPath } from '@shared/operations/listReturn';
import { isForbidden, isUnauthorized } from '@shared/query/http';
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
} from '../ui';
import { DraftLeaveGuard, DraftLeaveScope } from './DraftLeaveScope';
import './workflow.css';

export function ChannelBindingPresets({
  role,
  accountId,
  onCapabilityLoss,
}: {
  role: CharityRole;
  accountId: string;
  onCapabilityLoss?: () => void;
}) {
  const { t } = useTranslation();
  const location = useLocation();
  const [params, setParams] = useSearchState();
  const q = params.get('q') ?? '';
  const state =
    params.get('state') === 'retired'
      ? 'retired'
      : params.get('state') === 'all'
        ? 'all'
        : 'active';
  const [search, setSearch] = useState(q);
  const [channel, setChannel] = useState<PresetChannel | null>(null);
  const [visited, setVisited] = useState<PresetChannel[]>([]);
  const pager = useUrlPagePager({
    station: role === 'admin' ? 'admin' : 'user',
    listType: 'channel-binding-presets',
    scopeKey: accountId,
    pageParam: 'page',
    pageSizeParam: 'page_size',
  });
  const list = useQuery({
    queryKey: [
      ...charityKeys.root(role),
      'binding-presets',
      accountId,
      q,
      state,
      pager.page,
      pager.pageSize,
    ],
    queryFn: ({ signal }) => getPresetChannels(role, q, state, pager.page, pager.pageSize, signal),
    retry: false,
  });
  const lost = isForbidden(list.error) || isUnauthorized(list.error);
  useEffect(() => {
    if (lost) onCapabilityLoss?.();
  }, [lost, onCapabilityLoss]);
  const back = listReturnPath(location.state, role === 'admin' ? '/charity' : '/steward');
  if (lost) return <p role="alert">{t('common.operations.charity.accessLost')}</p>;
  return (
    <DraftLeaveScope>
      <div className="page ops-page nb-form">
        <PageHeader
          title={t('common.bindingPresets.title')}
          description={t('common.bindingPresets.help')}
          back={
            channel ? (
              <Button type="button" variant="ghost" onClick={() => setChannel(null)}>
                {t('common.bindingPresets.backChannels')}
              </Button>
            ) : (
              <Link to={back}>{t('common.bindingPresets.backCharity')}</Link>
            )
          }
        />
        {visited.map((entry) => (
          <div key={entry.channel_id} hidden={channel?.channel_id !== entry.channel_id}>
            <PresetWorkspace
              role={role}
              accountId={accountId}
              channel={entry}
              onCapabilityLoss={onCapabilityLoss}
            />
          </div>
        ))}
        {!channel ? (
          <>
            <FilterBar
              activeCount={q ? 1 : 0}
              secondaryLabel={t('common.filter')}
              onSubmit={() => {
                if (validManagementSearch(search))
                  setParams((previous) => {
                    const next = new URLSearchParams(previous);
                    next.set('q', search.trim());
                    next.set('page', '1');
                    return next;
                  });
              }}
              search={
                <>
                  <Field label={t('common.bindingPresets.searchChannels')}>
                    {(props) => (
                      <input
                        {...props}
                        type="search"
                        placeholder={t('common.bindingPresets.searchPlaceholder')}
                        value={search}
                        onChange={(event) => setSearch(event.target.value)}
                      />
                    )}
                  </Field>
                  <Button type="submit" disabled={!validManagementSearch(search)}>
                    {t('common.search')}
                  </Button>
                </>
              }
              secondaryAlwaysVisible
              secondary={
                <Field label={t('common.status')}>
                  {(props) => (
                    <select
                      {...props}
                      value={state}
                      onChange={(event) =>
                        setParams((previous) => {
                          const next = new URLSearchParams(previous);
                          next.set('state', event.target.value);
                          next.set('page', '1');
                          return next;
                        })
                      }
                    >
                      <option value="active">{t('common.bindingPresets.active')}</option>
                      <option value="retired">{t('common.bindingPresets.retired')}</option>
                      <option value="all">{t('common.all')}</option>
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
                    caption={t('common.bindingPresets.channels')}
                    rows={list.data.data}
                    rowKey={(row) => row.channel_id}
                    columns={[
                      {
                        key: 'name',
                        header: t('common.bindingPresets.channel'),
                        mobileLabel: t('common.bindingPresets.channel'),
                        cell: 'title',
                        render: (row) => (
                          <span className="nb-stack nb-stack--sm">
                            <strong>{row.name}</strong>
                            <small className="nb-workflow-id mono">{row.base_url}</small>
                          </span>
                        ),
                      },
                      {
                        key: 'state',
                        header: t('common.status'),
                        mobileLabel: t('common.status'),
                        cell: 'status',
                        render: (row) => (
                          <StatusBadge
                            active={row.enabled && row.state === 'active'}
                            label={t(
                              row.state === 'retired'
                                ? 'common.bindingPresets.retired'
                                : row.enabled
                                  ? 'common.enabled'
                                  : 'common.disabled',
                            )}
                          />
                        ),
                      },
                      {
                        key: 'count',
                        header: t('common.bindingPresets.mappings'),
                        mobileLabel: t('common.bindingPresets.mappings'),
                        cell: 'meta',
                        align: 'num',
                        render: (row) => formatCount(row.mapping_count).display,
                      },
                      {
                        key: 'action',
                        header: t('common.workflow.actions'),
                        mobileLabel: t('common.workflow.actions'),
                        cell: 'action',
                        render: (row) => (
                          <Button
                            type="button"
                            disabled={list.isFetching}
                            onClick={() => {
                              setVisited((previous) =>
                                previous.some((entry) => entry.channel_id === row.channel_id)
                                  ? previous.map((entry) =>
                                      entry.channel_id === row.channel_id ? row : entry,
                                    )
                                  : [...previous, row],
                              );
                              setChannel(row);
                            }}
                          >
                            {t(
                              row.state === 'retired'
                                ? 'common.bindingPresets.view'
                                : 'common.bindingPresets.configure',
                            )}
                          </Button>
                        ),
                      },
                    ]}
                  />
                ) : (
                  <EmptyState
                    title={t('common.bindingPresets.noChannels')}
                    body={t('common.bindingPresets.noChannelsHelp')}
                  />
                )}
                <PagePagination
                  metadata={list.data.pagination}
                  requestedPage={pager.page}
                  onPageChange={pager.setPage}
                  onPageSizeChange={pager.setPageSize}
                  busy={list.isFetching}
                />
              </>
            )}
          </>
        ) : null}
      </div>
    </DraftLeaveScope>
  );
}

function PresetWorkspace({
  role,
  accountId,
  channel,
  onCapabilityLoss,
}: {
  role: CharityRole;
  accountId: string;
  channel: PresetChannel;
  onCapabilityLoss?: () => void;
}) {
  const detail = useQuery({
    queryKey: [...charityKeys.root(role), 'binding-preset', accountId, channel.channel_id],
    queryFn: ({ signal }) => getBindingPreset(role, channel.channel_id, signal),
    retry: false,
  });
  useEffect(() => {
    if (isForbidden(detail.error) || isUnauthorized(detail.error)) onCapabilityLoss?.();
  }, [detail.error, onCapabilityLoss]);
  if (!detail.data)
    return detail.error ? (
      <ErrorState error={detail.error} onRetry={() => void detail.refetch()} />
    ) : (
      <LoadingState />
    );
  return (
    <>
      {detail.error ? (
        <ErrorState error={detail.error} onRetry={() => void detail.refetch()} />
      ) : null}
      <PresetEditor
        role={role}
        accountId={accountId}
        channel={channel}
        value={detail.data}
        onCapabilityLoss={onCapabilityLoss}
      />
    </>
  );
}

function PresetEditor({
  role,
  accountId,
  channel,
  value,
  onCapabilityLoss,
}: {
  role: CharityRole;
  accountId: string;
  channel: PresetChannel;
  value: BindingPreset;
  onCapabilityLoss?: () => void;
}) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const [baseline, setBaseline] = useState(value);
  const [draft, setDraft] = useState(value.mappings);
  const [revisions, setRevisions] = useState(value);
  const [choosing, setChoosing] = useState<number | null>(null);
  const [readError, setReadError] = useState<unknown>(null);
  const [reading, setReading] = useState(false);
  const signature = (rows: PresetMapping[]) =>
    JSON.stringify(
      rows.map(({ upstream_model_id, charity_model_id }) => ({
        upstream_model_id,
        charity_model_id,
      })),
    );
  const dirty = signature(draft) !== signature(baseline.mappings);
  const save = useRetainedOperation<PresetSaveInput, BindingPreset>(
    (input, key, context) =>
      saveBindingPreset(role, channel.channel_id, input, key, context.signal),
    () => client.invalidateQueries({ queryKey: charityKeys.root(role) }, { throwOnError: true }),
    charityKeys.root(role),
  );
  const lost = [save.error, readError].some((error) => isForbidden(error) || isUnauthorized(error));
  useEffect(() => {
    if (lost) onCapabilityLoss?.();
  }, [lost, onCapabilityLoss]);
  const locked =
    save.isPending || save.outcome === 'unknown' || save.outcome === 'refresh-failed' || reading;
  const readOnly = channel.state === 'retired';
  const valid =
    draft.every(
      (row) =>
        validManagementSearch(row.upstream_model_id, 512) &&
        row.upstream_model_id.trim().length > 0,
    ) &&
    new Set(draft.map((row) => `${row.charity_model_id}:${row.upstream_model_id}`)).size ===
      draft.length;
  const reread = async () => {
    setReading(true);
    setReadError(null);
    try {
      const latest = await getBindingPreset(role, channel.channel_id);
      setBaseline(latest);
      setRevisions(latest);
      save.reset();
    } catch (error) {
      setReadError(error);
    } finally {
      setReading(false);
    }
  };
  const submit = (input: PresetSaveInput) =>
    save.mutate(input, {
      onSuccess: (result) => {
        setBaseline(result);
        setRevisions(result);
        setDraft(result.mappings);
      },
    });
  if (lost) return <p role="alert">{t('common.operations.charity.accessLost')}</p>;
  return (
    <Panel>
      <PanelBody>
        <div className="nb-form">
          <DraftLeaveGuard
            dirty={dirty || save.outcome === 'unknown'}
            allowNavigation={({ currentLocation, nextLocation }) =>
              currentLocation.pathname === nextLocation.pathname
            }
          />
          <h2>{channel.name}</h2>
          <p className="mono nb-workflow-id">{channel.base_url}</p>
          <Note>
            {t(readOnly ? 'common.bindingPresets.readOnly' : 'common.bindingPresets.mappingHelp')}
          </Note>
          {draft.length ? (
            <DataTable
              caption={t('common.bindingPresets.mappings')}
              rows={draft.map((mapping, index) => ({ ...mapping, index }))}
              rowKey={(row) => String(row.index)}
              columns={[
                {
                  key: 'upstream',
                  header: t('common.bindingBatch.upstream'),
                  mobileLabel: t('common.bindingBatch.upstream'),
                  cell: 'title',
                  render: (row) => (
                    <Field
                      label={t('common.bindingPresets.upstreamLabel', { number: row.index + 1 })}
                    >
                      {(props) => (
                        <input
                          {...props}
                          value={row.upstream_model_id}
                          className="mono"
                          maxLength={512}
                          disabled={
                            locked || choosing !== null || readOnly || save.outcome === 'conflict'
                          }
                          onChange={(event) => {
                            setDraft((previous) =>
                              previous.map((entry, ordinal) =>
                                ordinal === row.index
                                  ? { ...entry, upstream_model_id: event.target.value }
                                  : entry,
                              ),
                            );
                            save.reset();
                          }}
                        />
                      )}
                    </Field>
                  ),
                },
                {
                  key: 'target',
                  header: t('common.workflow.model'),
                  mobileLabel: t('common.workflow.model'),
                  cell: 'meta',
                  render: (row) => (
                    <div className="nb-form">
                      <span className="nb-workflow-id">
                        {row.full_name ?? t('common.bindingPresets.deletedTarget')}
                      </span>
                      {!readOnly ? (
                        <Button
                          type="button"
                          disabled={locked || choosing !== null || save.outcome === 'conflict'}
                          onClick={() => setChoosing(row.index)}
                        >
                          {t('common.bindingPresets.changeTarget')}
                        </Button>
                      ) : null}
                    </div>
                  ),
                },
                {
                  key: 'action',
                  header: t('common.workflow.actions'),
                  mobileLabel: t('common.workflow.actions'),
                  cell: 'action',
                  render: (row) =>
                    !readOnly ? (
                      <Button
                        type="button"
                        disabled={locked || choosing !== null || save.outcome === 'conflict'}
                        onClick={() => {
                          setDraft((previous) =>
                            previous.filter((_, ordinal) => ordinal !== row.index),
                          );
                          save.reset();
                        }}
                      >
                        {t('common.operations.charity.remove')}
                      </Button>
                    ) : null,
                },
              ]}
            />
          ) : (
            <EmptyState
              title={t('common.bindingPresets.empty')}
              body={t('common.bindingPresets.emptyHelp')}
            />
          )}
          {!readOnly ? (
            <Button
              type="button"
              disabled={
                locked || choosing !== null || draft.length >= 16 || save.outcome === 'conflict'
              }
              onClick={() => setChoosing(-1)}
            >
              {t('common.bindingPresets.addMapping')}
            </Button>
          ) : null}
          {draft.length >= 16 ? <Note>{t('common.bindingPresets.limit')}</Note> : null}
          {choosing !== null ? (
            <PresetTargetPicker
              role={role}
              accountId={accountId}
              locked={locked}
              onCancel={() => setChoosing(null)}
              onChoose={(row) => {
                const entry = {
                  charity_model_id: row.id,
                  full_name: row.full_name,
                  binding_revision: row.binding_revision,
                };
                setDraft((previous) =>
                  choosing < 0
                    ? [...previous, { ...entry, upstream_model_id: '' }]
                    : previous.map((mapping, index) =>
                        index === choosing ? { ...mapping, ...entry } : mapping,
                      ),
                );
                setChoosing(null);
                save.reset();
              }}
            />
          ) : null}
          {!valid ? <Note tone="warn">{t('common.bindingPresets.invalid')}</Note> : null}
          {save.error && save.outcome !== 'conflict' ? <ErrorState error={save.error} /> : null}
          {readError ? <ErrorState error={readError} onRetry={() => void reread()} /> : null}
          <OutcomeNote
            outcome={
              save.outcome === 'conflict'
                ? { kind: 'conflict', reload: () => void reread() }
                : save.outcome === 'unknown'
                  ? { kind: 'unknown' }
                  : save.outcome === 'refresh-failed'
                    ? { kind: 'savedRefreshFailed', recheck: () => void save.refresh() }
                    : save.isSuccess
                      ? { kind: 'saved' }
                      : { kind: 'idle' }
            }
            busy={locked}
          />
          {save.outcome === 'unknown' && save.variables ? (
            <Button type="button" disabled={save.isPending} onClick={() => submit(save.variables!)}>
              {t('common.donationReview.retrySame')}
            </Button>
          ) : null}
          {!readOnly ? (
            <SaveBar
              dirtyCount={dirty ? 1 : 0}
              busy={locked || save.outcome === 'conflict'}
              saveDisabled={!valid || choosing !== null}
              saveLabel={t('common.save')}
              discardLabel={t('common.cancel')}
              dirtyLabel={() => t('common.bindingPresets.unsaved')}
              onDiscard={() => {
                setDraft(baseline.mappings);
                setChoosing(null);
                save.reset();
              }}
              onSave={() =>
                submit({
                  expected_channel_revision: revisions.channel_revision,
                  expected_preset_revision: revisions.preset_revision,
                  mappings: draft.map(({ upstream_model_id, charity_model_id }) => ({
                    upstream_model_id,
                    charity_model_id,
                  })),
                })
              }
            />
          ) : null}
        </div>
      </PanelBody>
    </Panel>
  );
}

function PresetTargetPicker({
  role,
  accountId,
  locked,
  onChoose,
  onCancel,
}: {
  role: CharityRole;
  accountId: string;
  locked: boolean;
  onChoose: (model: { id: string; full_name: string; binding_revision: string }) => void;
  onCancel: () => void;
}) {
  const { t } = useTranslation();
  const [q, setQ] = useState('');
  const [draft, setDraft] = useState('');
  const pager = usePagePager({
    station: role === 'admin' ? 'admin' : 'user',
    listType: 'preset-target-models',
    scopeKey: accountId,
    resetKey: q,
  });
  const models = useQuery({
    queryKey: [
      ...charityKeys.root(role),
      'preset-targets',
      accountId,
      q,
      pager.page,
      pager.pageSize,
    ],
    queryFn: ({ signal }) =>
      getManagedCharityModelsPage(role, q, '', pager.page, pager.pageSize, signal),
    retry: false,
  });
  return (
    <section className="nb-form" aria-label={t('common.bindingPresets.chooseTarget')}>
      <h3>{t('common.bindingPresets.chooseTarget')}</h3>
      <FilterBar
        activeCount={q ? 1 : 0}
        secondaryLabel={t('common.filter')}
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
            <Button type="submit" disabled={!validManagementSearch(draft)}>
              {t('common.search')}
            </Button>
          </>
        }
      />
      {models.isPending ? (
        <LoadingState />
      ) : models.error ? (
        <ErrorState error={models.error} onRetry={() => void models.refetch()} />
      ) : (
        <>
          <DataTable
            caption={t('common.bindingPresets.chooseTarget')}
            rows={models.data.data}
            rowKey={(row) => row.id}
            columns={[
              {
                key: 'name',
                header: t('common.workflow.model'),
                mobileLabel: t('common.workflow.model'),
                cell: 'title',
                render: (row) => <span className="nb-workflow-id">{row.full_name}</span>,
              },
              {
                key: 'action',
                header: t('common.workflow.actions'),
                mobileLabel: t('common.workflow.actions'),
                cell: 'action',
                render: (row) => (
                  <Button
                    type="button"
                    disabled={locked || models.isFetching}
                    onClick={() => onChoose(row)}
                  >
                    {t('common.workflow.chooseTarget')}
                  </Button>
                ),
              },
            ]}
          />
          <PagePagination
            metadata={models.data.pagination}
            requestedPage={pager.page}
            onPageChange={pager.setPage}
            onPageSizeChange={pager.setPageSize}
            busy={models.isFetching || locked}
          />
        </>
      )}
      <Button type="button" disabled={locked} onClick={onCancel}>
        {t('common.cancel')}
      </Button>
    </section>
  );
}
