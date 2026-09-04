import { useEffect, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { clearStationSession } from '@shared/charityManagement';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { Card, EmptyState, ErrorState, LoadingState, PageHeader, StatusBadge } from '@shared/components/States';
import { CursorPagination } from '@shared/operations/CursorPagination';
import { conflictOrUnknown } from '@shared/operations/api';
import { useCursorPager } from '@shared/operations/useCursorPager';
import { ApiError, isForbidden, isNotFoundError, isUnauthorized } from '@shared/query/http';
import { formatDateTime } from '@shared/utils/datetime';
import {
  adminMainstreamChannelKeys,
  createAdminMainstreamChannel,
  getAdminMainstreamChannel,
  getAdminMainstreamChannels,
  MAINSTREAM_CHANNEL_CATEGORIES,
  MAINSTREAM_CONNECTOR_TYPES,
  patchAdminMainstreamChannel,
  retireAdminMainstreamChannel,
  type AdminMainstreamChannel,
  type AdminMainstreamChannelCreate,
  type AdminMainstreamChannelPatch,
  type MainstreamChannelCategory,
  type MainstreamConnectorType,
} from './channels';
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

function draftFor(channel?: AdminMainstreamChannel): ChannelDraft {
  return {
    name: channel?.name ?? '',
    category: channel?.category ?? 'subscription',
    connector_type: channel?.connector_type ?? 'openai-compatible',
    base_url: channel?.base_url ?? '',
    enabled: channel?.enabled ?? true,
  };
}

function changedPatch(channel: AdminMainstreamChannel, draft: ChannelDraft): AdminMainstreamChannelPatch {
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
  onSubmit,
  onCancel,
}: {
  mode: 'create' | 'edit';
  channel?: AdminMainstreamChannel;
  busy: boolean;
  error: unknown;
  onSubmit: (draft: ChannelDraft) => void;
  onCancel?: () => void;
}) {
  const { t } = useTranslation();
  const [draft, setDraft] = useState<ChannelDraft>(() => draftFor(channel));
  const [invalid, setInvalid] = useState(false);
  const canEdit = mode === 'create' || channel?.state === 'active';

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
      {invalid ? <p className="inline-notice">{t('admin.mainstreamChannels.validation.invalid')}</p> : null}
      {error ? <ErrorState error={error} /> : null}
      <div className="ops-actions">
        <button className="btn btn-primary" type="submit" disabled={!canEdit || busy}>
          {busy ? t('common.working') : t(mode === 'create' ? 'admin.mainstreamChannels.actions.create' : 'admin.mainstreamChannels.actions.save')}
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
      <dd>{channel.id} / {channel.revision}</dd>
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
  const pager = useCursorPager();
  const [state, setState] = useState<'active' | 'retired' | 'all'>('active');
  const [selected, setSelected] = useState('');
  const [retireTarget, setRetireTarget] = useState<AdminMainstreamChannel | null>(null);
  const channels = useQuery({
    queryKey: adminMainstreamChannelKeys.list(state, pager.cursor),
    queryFn: ({ signal }) => getAdminMainstreamChannels(state, pager.cursor, 50, signal),
    retry: false,
  });
  const detail = useQuery({
    queryKey: selected
      ? adminMainstreamChannelKeys.detail(selected)
      : (['admin', 'operations', 'mainstream-channels', 'detail', 'none'] as const),
    queryFn: ({ signal }) => getAdminMainstreamChannel(selected, signal),
    retry: false,
    enabled: Boolean(selected),
  });
  const reconcile = async () => {
    await Promise.all([channels.refetch(), selected ? detail.refetch() : Promise.resolve()]);
  };
  const create = useRetainedOperation(
    (input: ChannelDraft, key) => createAdminMainstreamChannel(input, key),
    reconcile,
    adminMainstreamChannelKeys.root,
  );
  const patch = useRetainedOperation(
    (input: { id: string; patch: AdminMainstreamChannelPatch }, key) =>
      patchAdminMainstreamChannel(input.id, input.patch, key),
    reconcile,
    adminMainstreamChannelKeys.root,
  );
  const retire = useRetainedOperation(
    (input: { id: string; revision: string }, key) =>
      retireAdminMainstreamChannel(input.id, input.revision, key),
    reconcile,
    adminMainstreamChannelKeys.root,
  );
  const authorityError = channels.error ?? detail.error ?? create.error ?? patch.error ?? retire.error;

  /* eslint-disable react-hooks/set-state-in-effect -- Authority loss must clear selected state and mutation UI. */
  useEffect(() => {
    if (isAuthorityLoss(authorityError)) {
      setSelected('');
      setRetireTarget(null);
      create.reset();
      patch.reset();
      retire.reset();
      clearStationSession(client, 'admin');
    } else if (isNotFoundError(detail.error)) {
      setSelected('');
      setRetireTarget(null);
    }
  }, [authorityError, client, create, detail.error, patch, retire]);
  /* eslint-enable react-hooks/set-state-in-effect */

  const mutationError = create.error ?? patch.error ?? retire.error;
  const conflictNotice = conflictOrUnknown(mutationError);
  const selectedChannel = detail.data;

  return (
    <div className="ops-stack">
      <Card>
        <div className="ops-toolbar">
          <label>
            <span>{t('admin.mainstreamChannels.filters.state')}</span>
            <select
              value={state}
              onChange={(event) => {
                pager.reset();
                setState(event.target.value as typeof state);
              }}
            >
              <option value="active">{t(STATE_LABEL_KEYS.active)}</option>
              <option value="retired">{t(STATE_LABEL_KEYS.retired)}</option>
              <option value="all">{t('admin.mainstreamChannels.state.all')}</option>
            </select>
          </label>
        </div>
        {channels.isPending ? (
          <LoadingState />
        ) : channels.error ? (
          <ErrorState error={channels.error} onRetry={() => void channels.refetch()} />
        ) : channels.data.data.length === 0 ? (
          <EmptyState
            title={t('admin.mainstreamChannels.empty.title')}
            body={t('admin.mainstreamChannels.empty.body')}
          />
        ) : (
          <>
            <div className="ops-table-scroll">
              <table className="ops-table">
                <thead>
                  <tr>
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
                  {channels.data.data.map((channel) => (
                    <tr key={channel.id}>
                      <td>{channel.name}<small>{channel.id}</small></td>
                      <td>{t(CATEGORY_LABEL_KEYS[channel.category])}</td>
                      <td>{t(CONNECTOR_LABEL_KEYS[channel.connector_type])}</td>
                      <td className="ops-wrap">{channel.base_url}</td>
                      <td>
                        <StatusBadge
                          active={channel.enabled}
                          danger={channel.state === 'retired'}
                          label={t(channel.enabled ? 'common.enabled' : 'common.disabled')}
                        />
                      </td>
                      <td>{channel.revision}</td>
                      <td>{formatDateTime(channel.updated_at)}</td>
                      <td>
                        <button
                          className="btn btn-secondary"
                          type="button"
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
            <CursorPagination
              page={pager.page}
              nextCursor={channels.data.next_cursor}
              onPrevious={pager.previous}
              onNext={pager.next}
            />
          </>
        )}
      </Card>

      <Card>
        <h2>{t('admin.mainstreamChannels.create.title')}</h2>
        <p>{t('admin.mainstreamChannels.create.description')}</p>
        <ChannelForm
          mode="create"
          busy={create.isPending}
          error={create.error}
          onSubmit={(draft) =>
            create.mutate(draft, {
              onSuccess: (channel) => {
                setSelected(channel.id);
                pager.reset();
              },
            })
          }
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
                    key={`${selectedChannel.id}:${selectedChannel.revision}`}
                    mode="edit"
                    channel={selectedChannel}
                    busy={patch.isPending}
                    error={patch.error}
                    onCancel={() => setSelected('')}
                    onSubmit={(draft) => {
                      const input = changedPatch(selectedChannel, draft);
                      if (Object.keys(input).length === 1) return;
                      patch.mutate({ id: selectedChannel.id, patch: input });
                    }}
                  />
                  <div className="ops-danger">
                    <h3>{t('admin.mainstreamChannels.retire.title')}</h3>
                    <p>{t('admin.mainstreamChannels.retire.description')}</p>
                    <button
                      className="btn btn-danger"
                      type="button"
                      disabled={retire.isPending || patch.isPending}
                      onClick={() => setRetireTarget(selectedChannel)}
                    >
                      {t('admin.mainstreamChannels.actions.retire')}
                    </button>
                  </div>
                </>
              ) : (
                <p className="inline-notice">{t('admin.mainstreamChannels.detail.retiredImmutable')}</p>
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
      {retire.error && !conflictNotice ? <ErrorState error={retire.error} /> : null}
      {retireTarget ? (
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
            retire.mutate({ id: target.id, revision: target.revision });
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
