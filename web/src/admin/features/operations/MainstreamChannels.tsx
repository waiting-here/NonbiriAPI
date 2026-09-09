import { useEffect, useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useSearchState } from '@shared/operations/useSearchState';
import { useTranslation } from 'react-i18next';
import {
  captureStationSession,
  clearStationSession,
  StationSessionChangedError,
  stationSessionMatches,
} from '@shared/charityManagement';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import {
  Card,
  EmptyState,
  ErrorState,
  LoadingState,
  PageHeader,
  StatusBadge,
} from '@shared/components/States';
import { conflictOrUnknown } from '@shared/operations/api';
import { PagePagination } from '@shared/operations/PagePagination';
import { useUrlPagePager } from '@shared/operations/useUrlPagePager';
import { ApiError, isForbidden, isNotFoundError, isUnauthorized } from '@shared/query/http';
import { formatDateTime } from '@shared/utils/datetime';
import { adminKeys, useAdminSession } from '../../data';
import {
  adminMainstreamChannelKeys,
  createAdminMainstreamChannel,
  getAdminMainstreamChannel,
  MAINSTREAM_CHANNEL_CATEGORIES,
  MAINSTREAM_CONNECTOR_TYPES,
  patchAdminMainstreamChannel,
  retireAdminMainstreamChannel,
  type AdminMainstreamChannel,
  type AdminMainstreamChannelCreate,
  type AdminMainstreamChannelPatch,
  type MainstreamChannelCategory,
  type MainstreamChannelListState,
  type MainstreamConnectorType,
} from './channels';
import { adminMainstreamChannelPageKeys, useAdminMainstreamChannelPage } from './channelPage';
import { useRetainedOperation } from './useRetainedOperation';
import '@shared/operations/operations.css';

type ChannelDraft = AdminMainstreamChannelCreate;

function isAuthorityLoss(error: unknown): boolean {
  return (
    isUnauthorized(error) ||
    (isForbidden(error) && !(error instanceof ApiError && error.code === 'elevated_required'))
  );
}

const CATEGORY_LABEL_KEYS: Record<MainstreamChannelCategory, string> = {
  subscription: 'admin.mainstreamChannels.category.subscription',
  api_platform: 'admin.mainstreamChannels.category.apiPlatform',
};

const CONNECTOR_LABEL_KEYS: Record<MainstreamConnectorType, string> = {
  'openai-compatible': 'admin.mainstreamChannels.connector.openaiCompatible',
  'anthropic-compatible': 'admin.mainstreamChannels.connector.anthropicCompatible',
};

const STATE_LABEL_KEYS: Record<'active' | 'retired', string> = {
  active: 'admin.mainstreamChannels.state.active',
  retired: 'admin.mainstreamChannels.state.retired',
};

const CHANNEL_LIST_TYPE = 'mainstream-channels';
const CHANNEL_STATE_PARAM = 'state';

function channelDetailKey(accountID: string | undefined, channelID: string) {
  return accountID && channelID
    ? ([...adminMainstreamChannelKeys.root, 'detail', accountID, channelID] as const)
    : (['admin', 'operations', 'mainstream-channels', 'detail', 'none', 'none'] as const);
}

function channelStateFromSearch(searchParams: URLSearchParams): MainstreamChannelListState {
  const value = searchParams.get(CHANNEL_STATE_PARAM);
  return value === 'retired' || value === 'all' ? value : 'active';
}

function needsChannelStateNormalization(searchParams: URLSearchParams): boolean {
  const values = searchParams.getAll(CHANNEL_STATE_PARAM);
  return values.length !== 1 || !['active', 'retired', 'all'].includes(values[0] ?? '');
}

function draftFor(channel?: AdminMainstreamChannel): ChannelDraft {
  return {
    name: channel?.name ?? '',
    category: channel?.category ?? 'subscription',
    connector_type: channel?.connector_type ?? 'openai-compatible',
    base_url: channel?.base_url ?? '',
    enabled: channel?.enabled ?? true,
  };
}

function changedPatch(
  channel: AdminMainstreamChannel,
  draft: ChannelDraft,
): AdminMainstreamChannelPatch {
  const patch: AdminMainstreamChannelPatch = { expected_revision: channel.revision };
  if (draft.name !== channel.name) patch.name = draft.name;
  if (draft.category !== channel.category) patch.category = draft.category;
  if (draft.connector_type !== channel.connector_type) patch.connector_type = draft.connector_type;
  if (draft.base_url !== channel.base_url) patch.base_url = draft.base_url;
  if (draft.enabled !== channel.enabled) patch.enabled = draft.enabled;
  return patch;
}

function validDraft(draft: ChannelDraft): boolean {
  const name = Array.from(draft.name);
  const baseURL = Array.from(draft.base_url);
  if (name.length < 1 || name.length > 128 || draft.name.trim() !== draft.name) return false;
  if (baseURL.length < 1 || baseURL.length > 4_096) return false;
  if (new TextEncoder().encode(draft.base_url).byteLength > 4_096) return false;
  return [...draft.name, ...draft.base_url].every((character) => {
    const point = character.codePointAt(0) ?? 0;
    return point >= 0x20 && !(point >= 0x7f && point <= 0x9f);
  });
}

function ChannelForm({
  mode,
  channel,
  busy,
  error,
  canWrite = true,
  onSubmit,
  onCancel,
}: {
  mode: 'create' | 'edit';
  channel?: AdminMainstreamChannel;
  busy: boolean;
  error: unknown;
  canWrite?: boolean;
  onSubmit: (draft: ChannelDraft) => void;
  onCancel?: () => void;
}) {
  const { t } = useTranslation();
  const [draft, setDraft] = useState<ChannelDraft>(() => draftFor(channel));
  const [invalid, setInvalid] = useState(false);
  const canEdit = canWrite && (mode === 'create' || channel?.state === 'active');

  return (
    <form
      className="ops-stack"
      onSubmit={(event) => {
        event.preventDefault();
        const valid = validDraft(draft);
        setInvalid(!valid);
        if (valid) onSubmit(draft);
      }}
    >
      <div className="ops-field-grid">
        <label>
          <span>{t('admin.mainstreamChannels.form.name')}</span>
          <input
            value={draft.name}
            maxLength={128}
            autoComplete="off"
            disabled={!canEdit || busy}
            onChange={(event) => setDraft({ ...draft, name: event.target.value })}
          />
        </label>
        <label>
          <span>{t('admin.mainstreamChannels.form.category')}</span>
          <select
            value={draft.category}
            disabled={!canEdit || busy}
            onChange={(event) =>
              setDraft({ ...draft, category: event.target.value as MainstreamChannelCategory })
            }
          >
            {MAINSTREAM_CHANNEL_CATEGORIES.map((value) => (
              <option key={value} value={value}>
                {t(CATEGORY_LABEL_KEYS[value])}
              </option>
            ))}
          </select>
        </label>
        <label>
          <span>{t('admin.mainstreamChannels.form.connector')}</span>
          <select
            value={draft.connector_type}
            disabled={!canEdit || busy}
            onChange={(event) =>
              setDraft({ ...draft, connector_type: event.target.value as MainstreamConnectorType })
            }
          >
            {MAINSTREAM_CONNECTOR_TYPES.map((value) => (
              <option key={value} value={value}>
                {t(CONNECTOR_LABEL_KEYS[value])}
              </option>
            ))}
          </select>
        </label>
        <label>
          <span>{t('admin.mainstreamChannels.form.baseUrl')}</span>
          <input
            value={draft.base_url}
            maxLength={4_096}
            autoComplete="off"
            disabled={!canEdit || busy}
            onChange={(event) => setDraft({ ...draft, base_url: event.target.value })}
          />
        </label>
      </div>
      <label className="checkbox-label">
        <input
          type="checkbox"
          checked={draft.enabled}
          disabled={!canEdit || busy}
          onChange={(event) => setDraft({ ...draft, enabled: event.target.checked })}
        />
        <span>{t('admin.mainstreamChannels.form.enabled')}</span>
      </label>
      {invalid ? (
        <p className="inline-notice">{t('admin.mainstreamChannels.validation.invalid')}</p>
      ) : null}
      {error ? <ErrorState error={error} /> : null}
      <div className="ops-actions">
        <button className="btn btn-primary" type="submit" disabled={!canEdit || busy}>
          {busy
            ? t('common.working')
            : t(
                mode === 'create'
                  ? 'admin.mainstreamChannels.actions.create'
                  : 'admin.mainstreamChannels.actions.save',
              )}
        </button>
        {onCancel ? (
          <button className="btn btn-secondary" type="button" disabled={busy} onClick={onCancel}>
            {t('common.cancel')}
          </button>
        ) : null}
      </div>
    </form>
  );
}

function ChannelDetails({ channel }: { channel: AdminMainstreamChannel }) {
  const { t } = useTranslation();
  return (
    <dl className="ops-kv">
      <dt>{t('admin.mainstreamChannels.detail.idRevision')}</dt>
      <dd>
        {channel.id} / {channel.revision}
      </dd>
      <dt>{t('admin.mainstreamChannels.detail.category')}</dt>
      <dd>{t(CATEGORY_LABEL_KEYS[channel.category])}</dd>
      <dt>{t('admin.mainstreamChannels.detail.connector')}</dt>
      <dd>{t(CONNECTOR_LABEL_KEYS[channel.connector_type])}</dd>
      <dt>{t('admin.mainstreamChannels.detail.baseUrl')}</dt>
      <dd className="ops-wrap">{channel.base_url}</dd>
      <dt>{t('admin.mainstreamChannels.detail.enabled')}</dt>
      <dd>{t(channel.enabled ? 'common.enabled' : 'common.disabled')}</dd>
      <dt>{t('admin.mainstreamChannels.detail.state')}</dt>
      <dd>{t(STATE_LABEL_KEYS[channel.state])}</dd>
      <dt>{t('admin.mainstreamChannels.detail.created')}</dt>
      <dd>{formatDateTime(channel.created_at)}</dd>
      <dt>{t('admin.mainstreamChannels.detail.updated')}</dt>
      <dd>{formatDateTime(channel.updated_at)}</dd>
      {channel.retired_at !== null ? (
        <>
          <dt>{t('admin.mainstreamChannels.detail.retired')}</dt>
          <dd>{formatDateTime(channel.retired_at)}</dd>
        </>
      ) : null}
    </dl>
  );
}

export function MainstreamChannelsPanel() {
  const { t } = useTranslation();
  const client = useQueryClient();
  const [searchParams, setSearchParams] = useSearchState();
  const state = channelStateFromSearch(searchParams);
  const session = useAdminSession();
  const accountID = session.data?.admin.username;
  const scopeReady = Boolean(accountID) && !session.error;
  const pager = useUrlPagePager({
    station: 'admin',
    listType: CHANNEL_LIST_TYPE,
    scopeKey: accountID ?? 'anonymous',
    scopeReady,
    resetKey: state,
  });
  useEffect(() => {
    if (!needsChannelStateNormalization(searchParams)) return;
    setSearchParams(
      (previous) => {
        const next = new URLSearchParams(previous);
        next.delete(CHANNEL_STATE_PARAM);
        next.set(CHANNEL_STATE_PARAM, 'active');
        return next;
      },
      { replace: true },
    );
  }, [searchParams, setSearchParams]);
  const [selected, setSelected] = useState('');
  const [retireTarget, setRetireTarget] = useState<AdminMainstreamChannel | null>(null);
  const [authorityLoss, setAuthorityLoss] = useState<unknown>(null);
  const [accountScope, setAccountScope] = useState(accountID);
  const accountRef = useRef(accountID);

  const isCurrentAccount = (expectedAccountID: string): boolean =>
    accountRef.current === expectedAccountID &&
    client.getQueryData<{ admin?: { username?: unknown } }>(adminKeys.session)?.admin?.username ===
      expectedAccountID;

  const accountScopedRequest = async <T,>(
    expectedAccountID: string,
    request: () => Promise<T>,
  ): Promise<T> => {
    if (!isCurrentAccount(expectedAccountID)) throw new StationSessionChangedError();
    const station = captureStationSession(client, 'admin');
    try {
      const value = await request();
      if (
        !stationSessionMatches(client, 'admin', station) ||
        !isCurrentAccount(expectedAccountID)
      ) {
        throw new StationSessionChangedError();
      }
      return value;
    } catch (error) {
      if (
        !stationSessionMatches(client, 'admin', station) ||
        !isCurrentAccount(expectedAccountID)
      ) {
        throw new StationSessionChangedError();
      }
      if (isAuthorityLoss(error)) {
        setAuthorityLoss(error);
        clearStationSession(client, 'admin');
      }
      throw error;
    }
  };

  const channels = useAdminMainstreamChannelPage(
    accountID,
    state,
    pager.page,
    pager.pageSize,
    !session.isPending && !session.isFetching && !session.error,
  );
  const detail = useQuery({
    queryKey: channelDetailKey(accountID, selected),
    queryFn: ({ signal }) =>
      accountScopedRequest(accountID ?? '', () => getAdminMainstreamChannel(selected, signal)),
    retry: false,
    enabled:
      Boolean(selected) &&
      scopeReady &&
      accountScope === accountID &&
      !session.isPending &&
      !session.isFetching,
  });

  const operationAccountID = (variables: unknown): string | undefined => {
    if (!variables || typeof variables !== 'object') return undefined;
    const value = (variables as { accountID?: unknown }).accountID;
    return typeof value === 'string' ? value : undefined;
  };

  const reconcile = async (variables: unknown) => {
    const requestedAccountID = operationAccountID(variables);
    if (
      !requestedAccountID ||
      requestedAccountID !== accountID ||
      !isCurrentAccount(requestedAccountID)
    ) {
      return;
    }
    await Promise.all([
      client.invalidateQueries({ queryKey: adminMainstreamChannelPageKeys.root }),
      selected ? detail.refetch() : Promise.resolve(),
    ]);
  };
  const create = useRetainedOperation(
    (input: { accountID: string; draft: ChannelDraft }, key) =>
      accountScopedRequest(input.accountID, () => createAdminMainstreamChannel(input.draft, key)),
    reconcile,
    adminMainstreamChannelKeys.root,
  );
  const patch = useRetainedOperation(
    (input: { accountID: string; id: string; patch: AdminMainstreamChannelPatch }, key) =>
      accountScopedRequest(input.accountID, () =>
        patchAdminMainstreamChannel(input.id, input.patch, key),
      ),
    reconcile,
    adminMainstreamChannelKeys.root,
  );
  const retire = useRetainedOperation(
    (input: { accountID: string; id: string; revision: string }, key) =>
      accountScopedRequest(input.accountID, () =>
        retireAdminMainstreamChannel(input.id, input.revision, key),
      ),
    reconcile,
    adminMainstreamChannelKeys.root,
  );
  useEffect(() => {
    if (accountScope === accountID) return;
    const previousAccountID = accountScope;
    accountRef.current = accountID;
    // The account transition must synchronously close old-account controls.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setAccountScope(accountID);
    setSelected('');
    setRetireTarget(null);
    if (previousAccountID && accountID && previousAccountID !== accountID) {
      setAuthorityLoss(null);
    }
    create.reset();
    patch.reset();
    retire.reset();
  }, [accountID, accountScope, create, patch, retire]);

  const createError =
    accountID !== undefined && create.variables?.accountID === accountID ? create.error : undefined;
  const patchError =
    accountID !== undefined && patch.variables?.accountID === accountID ? patch.error : undefined;
  const retireError =
    accountID !== undefined && retire.variables?.accountID === accountID ? retire.error : undefined;
  const authorityError = channels.error ?? detail.error ?? createError ?? patchError ?? retireError;
  const authorityRevoked = Boolean(authorityLoss) || isAuthorityLoss(authorityError);
  const canWrite =
    Boolean(accountID) &&
    accountScope === accountID &&
    !session.error &&
    !session.isPending &&
    !session.isFetching &&
    !authorityRevoked;

  /* eslint-disable react-hooks/set-state-in-effect -- Authority loss must clear selected state and mutation UI. */
  useEffect(() => {
    if (isAuthorityLoss(authorityError)) {
      setAuthorityLoss(authorityError);
      setSelected('');
      setRetireTarget(null);
      create.reset();
      patch.reset();
      retire.reset();
      clearStationSession(client, 'admin');
    } else if (authorityLoss && session.data && !session.error) {
      setAuthorityLoss(null);
    } else if (isNotFoundError(detail.error)) {
      setSelected('');
      setRetireTarget(null);
    }
  }, [
    authorityError,
    authorityLoss,
    client,
    create,
    detail.error,
    patch,
    retire,
    session.data,
    session.error,
  ]);
  /* eslint-enable react-hooks/set-state-in-effect */

  const mutationError = createError ?? patchError ?? retireError;
  const conflictNotice = conflictOrUnknown(mutationError);
  const selectedChannel = detail.data;
  const pageData = channels.data;
  const busy = channels.isFetching;

  return (
    <div className="ops-stack">
      <Card>
        <div className="ops-toolbar">
          <label>
            <span>{t('admin.mainstreamChannels.filters.state')}</span>
            <select
              value={state}
              onChange={(event) => {
                const nextState = event.target.value as MainstreamChannelListState;
                if (nextState !== 'active' && nextState !== 'retired' && nextState !== 'all')
                  return;
                setSearchParams((previous) => {
                  const next = new URLSearchParams(previous);
                  next.set(CHANNEL_STATE_PARAM, nextState);
                  next.delete('page');
                  next.set('page', '1');
                  next.delete('page_size');
                  next.set('page_size', String(pager.pageSize));
                  return next;
                });
              }}
            >
              <option value="active">{t(STATE_LABEL_KEYS.active)}</option>
              <option value="retired">{t(STATE_LABEL_KEYS.retired)}</option>
              <option value="all">{t('admin.mainstreamChannels.state.all')}</option>
            </select>
          </label>
        </div>
        {session.error || authorityLoss ? (
          <ErrorState
            error={session.error ?? authorityLoss}
            onRetry={() => void session.refetch()}
          />
        ) : session.isPending || (channels.isPending && !pageData) ? (
          <LoadingState />
        ) : channels.error && !pageData ? (
          <ErrorState error={channels.error} onRetry={() => void channels.refetch()} />
        ) : pageData ? (
          <div aria-busy={busy}>
            {channels.error ? (
              <ErrorState error={channels.error} onRetry={() => void channels.refetch()} />
            ) : null}
            {pageData.data.length === 0 ? (
              <EmptyState
                title={t('admin.mainstreamChannels.empty.title')}
                body={t('admin.mainstreamChannels.empty.body')}
              />
            ) : (
              <>
                <div className="ops-table-scroll">
                  <table className="ops-table ops-table--responsive">
                    <thead>
                      <tr>
                        <th>{t('common.itemId')}</th>
                        <th>{t('admin.mainstreamChannels.table.name')}</th>
                        <th>{t('admin.mainstreamChannels.table.category')}</th>
                        <th>{t('admin.mainstreamChannels.table.connector')}</th>
                        <th>{t('admin.mainstreamChannels.table.baseUrl')}</th>
                        <th>{t('admin.mainstreamChannels.table.enabled')}</th>
                        <th>{t('admin.mainstreamChannels.table.revision')}</th>
                        <th>{t('admin.mainstreamChannels.table.updated')}</th>
                        <th>{t('admin.mainstreamChannels.table.actions')}</th>
                      </tr>
                    </thead>
                    <tbody>
                      {pageData.data.map((channel) => (
                        <tr key={channel.id}>
                          <td className="ops-id" data-label={t('common.itemId')}>
                            {channel.id}
                          </td>
                          <td
                            className="ops-cell-wide"
                            data-label={t('admin.mainstreamChannels.table.name')}
                          >
                            {channel.name}
                          </td>
                          <td data-label={t('admin.mainstreamChannels.table.category')}>
                            {t(CATEGORY_LABEL_KEYS[channel.category])}
                          </td>
                          <td data-label={t('admin.mainstreamChannels.table.connector')}>
                            {t(CONNECTOR_LABEL_KEYS[channel.connector_type])}
                          </td>
                          <td
                            className="ops-cell-wide ops-wrap"
                            data-label={t('admin.mainstreamChannels.table.baseUrl')}
                          >
                            {channel.base_url}
                          </td>
                          <td data-label={t('admin.mainstreamChannels.table.enabled')}>
                            <StatusBadge
                              active={channel.enabled}
                              danger={channel.state === 'retired'}
                              label={t(channel.enabled ? 'common.enabled' : 'common.disabled')}
                            />
                          </td>
                          <td data-label={t('admin.mainstreamChannels.table.revision')}>
                            {channel.revision}
                          </td>
                          <td data-label={t('admin.mainstreamChannels.table.updated')}>
                            {formatDateTime(channel.updated_at)}
                          </td>
                          <td
                            className="ops-cell-wide"
                            data-label={t('admin.mainstreamChannels.table.actions')}
                          >
                            <button
                              className="btn btn-secondary"
                              type="button"
                              disabled={
                                busy ||
                                !scopeReady ||
                                accountScope !== accountID ||
                                session.isPending ||
                                session.isFetching ||
                                Boolean(authorityLoss)
                              }
                              onClick={() => setSelected(channel.id)}
                            >
                              {t('admin.mainstreamChannels.actions.view')}
                            </button>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
                <PagePagination
                  metadata={pageData.pagination}
                  requestedPage={pager.page}
                  busy={busy}
                  onPageChange={pager.setPage}
                  onPageSizeChange={pager.setPageSize}
                />
              </>
            )}
            {pageData.data.length === 0 ? (
              <PagePagination
                metadata={pageData.pagination}
                requestedPage={pager.page}
                busy={busy}
                onPageChange={pager.setPage}
                onPageSizeChange={pager.setPageSize}
              />
            ) : null}
          </div>
        ) : (
          <LoadingState />
        )}
      </Card>

      <Card>
        <h2>{t('admin.mainstreamChannels.create.title')}</h2>
        <p>{t('admin.mainstreamChannels.create.description')}</p>
        <ChannelForm
          key={`create:${accountID ?? 'anonymous'}`}
          mode="create"
          busy={create.isPending}
          error={createError}
          canWrite={canWrite}
          onSubmit={(draft) => {
            if (!canWrite || !accountID) return;
            const submittedAccountID = accountID;
            create.mutate(
              { accountID: submittedAccountID, draft },
              {
                onSuccess: (channel) => {
                  if (isCurrentAccount(submittedAccountID)) setSelected(channel.id);
                },
              },
            );
          }}
        />
      </Card>

      {selected ? (
        <Card>
          {detail.isPending ? (
            <LoadingState />
          ) : detail.error ? (
            <ErrorState error={detail.error} onRetry={() => void detail.refetch()} />
          ) : selectedChannel ? (
            <>
              <h2>{t('admin.mainstreamChannels.detail.title')}</h2>
              <ChannelDetails channel={selectedChannel} />
              {selectedChannel.state === 'active' ? (
                <>
                  <h3>{t('admin.mainstreamChannels.edit.title')}</h3>
                  <ChannelForm
                    key={`${accountID ?? 'anonymous'}:${selectedChannel.id}:${selectedChannel.revision}`}
                    mode="edit"
                    channel={selectedChannel}
                    busy={patch.isPending}
                    error={patchError}
                    canWrite={canWrite}
                    onCancel={() => setSelected('')}
                    onSubmit={(draft) => {
                      if (!canWrite || !accountID) return;
                      const input = changedPatch(selectedChannel, draft);
                      if (Object.keys(input).length === 1) return;
                      patch.mutate({
                        accountID,
                        id: selectedChannel.id,
                        patch: input,
                      });
                    }}
                  />
                  <div className="ops-danger">
                    <h3>{t('admin.mainstreamChannels.retire.title')}</h3>
                    <p>{t('admin.mainstreamChannels.retire.description')}</p>
                    <button
                      className="btn btn-danger"
                      type="button"
                      disabled={!canWrite || retire.isPending || patch.isPending}
                      onClick={() => setRetireTarget(selectedChannel)}
                    >
                      {t('admin.mainstreamChannels.actions.retire')}
                    </button>
                  </div>
                </>
              ) : (
                <p className="inline-notice">
                  {t('admin.mainstreamChannels.detail.retiredImmutable')}
                </p>
              )}
            </>
          ) : null}
        </Card>
      ) : null}

      {mutationError && conflictNotice ? (
        <p className="inline-notice" role="status">
          {t('admin.mainstreamChannels.authorityChanged')}
        </p>
      ) : null}
      {retireError && !conflictNotice ? <ErrorState error={retireError} /> : null}
      {retireTarget && canWrite ? (
        <ConfirmDialog
          open
          danger
          busy={retire.isPending}
          title={t('admin.mainstreamChannels.retire.confirmTitle')}
          description={t('admin.mainstreamChannels.retire.confirmDescription', {
            name: retireTarget.name,
          })}
          confirmLabel={t('admin.mainstreamChannels.actions.retire')}
          onCancel={() => setRetireTarget(null)}
          onConfirm={() => {
            const target = retireTarget;
            setRetireTarget(null);
            if (!accountID || !canWrite) return;
            retire.mutate({ accountID, id: target.id, revision: target.revision });
          }}
        />
      ) : null}
    </div>
  );
}

export function MainstreamChannelsPage() {
  const { t } = useTranslation();
  return (
    <div className="page ops-page">
      <PageHeader
        title={t('admin.mainstreamChannels.title')}
        description={t('admin.mainstreamChannels.description')}
      />
      <MainstreamChannelsPanel />
    </div>
  );
}
