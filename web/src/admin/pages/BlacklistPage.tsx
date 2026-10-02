import { useEffect, useState, type FormEvent } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router';
import { Card, EmptyState, ErrorState, LoadingState, PageHeader } from '@shared/components/States';
import { PagePagination } from '@shared/operations/PagePagination';
import { useUrlPagePager } from '@shared/operations/useUrlPagePager';
import { useSearchState } from '@shared/operations/useSearchState';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { ReasonText } from '@shared/components/ReasonText';
import { isForbidden, isUnauthorized } from '@shared/query/http';
import { clearStationSession } from '@shared/charityManagement';
import { useDateTimeFormatter } from '@shared/utils/datetime';
import { useAdminSession } from '../data';
import type { ManagementRole } from '@shared/operations/managedUsers';
import {
  addBlacklist,
  getBlacklist,
  getBlacklistEvents,
  removeBlacklist,
  validDiscordID,
} from '../features/operations/blacklist';
import '@shared/operations/operations.css';

export function BlacklistPage() {
  const session = useAdminSession();
  return (
    <BlacklistManagement
      role="admin"
      accountID={session.data ? `admin:${session.data.admin.username}` : undefined}
      sessionError={session.error}
      sessionFetching={session.isFetching}
      refreshSession={() => void session.refetch()}
    />
  );
}

interface BlacklistManagementProps {
  role: ManagementRole;
  accountID?: string;
  sessionError: unknown;
  sessionFetching: boolean;
  refreshSession: () => void;
  onAuthorityLoss?: () => void;
}

export function BlacklistManagement({
  role,
  accountID,
  sessionError,
  sessionFetching,
  refreshSession,
  onAuthorityLoss,
}: BlacklistManagementProps) {
  const formatDateTime = useDateTimeFormatter();
  const { t } = useTranslation();
  const label = {
    title: t('common.blacklist.title'),
    description: t('common.blacklist.description'),
    addTitle: t('common.blacklist.addTitle'),
    reason: t('common.blacklist.reason'),
    add: t('common.blacklist.add'),
    added: t('common.blacklist.added'),
    removed: t('common.blacklist.removed'),
    invalid: t('common.blacklist.invalid'),
    invalidID: t('common.blacklist.invalidID'),
    search: t('common.blacklist.search'),
    applySearch: t('common.blacklist.applySearch'),
    empty: t('common.blacklist.empty'),
    emptyBody: t('common.blacklist.emptyBody'),
    account: t('common.blacklist.account'),
    noAccount: t('common.blacklist.noAccount'),
    created: t('common.blacklist.created'),
    action: t('common.blacklist.action'),
    remove: t('common.blacklist.remove'),
    firstActor: t('common.blacklist.firstActor'),
    actorKind: t('common.blacklist.actorKind'),
    actorUserID: t('common.blacklist.actorUserID'),
    exactDiscord: t('common.blacklist.exactDiscord'),
    events: t('common.blacklist.events'),
    eventNote: t('common.blacklist.eventNote'),
    noEvents: t('common.blacklist.noEvents'),
    previous: t('common.blacklist.previous'),
    next: t('common.blacklist.next'),
  };
  const actorLabels = {
    admin: t('common.blacklist.actorAdmin'),
    steward6: t('common.blacklist.actorSteward'),
    automatic: t('common.blacklist.actorAutomatic'),
    unknown: t('common.blacklist.actorUnknown'),
  };
  const client = useQueryClient();
  const [params, setParams] = useSearchState();
  const q = params.get('q') ?? '';
  const actorKind = params.get('actor_kind') ?? '';
  const actorUserID = params.get('actor_user_id') ?? '';
  const filterDiscordID = params.get('discord_id') ?? '';
  const listKeys = role === 'admin' ? ['admin', 'blacklist'] : ['user', 'steward', 'blacklist'];
  const pager = useUrlPagePager({
    station: role === 'admin' ? 'admin' : 'user',
    listType: 'blacklist',
    scopeKey: accountID ?? 'anonymous',
    scopeReady: Boolean(accountID),
    resetKey: `${q}|${actorKind}|${actorUserID}|${filterDiscordID}`,
  });
  const result = useQuery({
    queryKey: [
      ...listKeys,
      accountID,
      q,
      actorKind,
      actorUserID,
      filterDiscordID,
      pager.page,
      pager.pageSize,
    ],
    queryFn: ({ signal }) =>
      getBlacklist(
        role,
        pager.page,
        pager.pageSize,
        q,
        actorKind,
        actorUserID,
        filterDiscordID,
        signal,
      ),
    enabled: Boolean(accountID) && !sessionFetching,
    retry: false,
  });
  const [discordID, setDiscordID] = useState('');
  const [reason, setReason] = useState('');
  const [search, setSearch] = useState(q);
  const [actorDraft, setActorDraft] = useState(actorUserID);
  const [discordDraft, setDiscordDraft] = useState(filterDiscordID);
  const [kindDraft, setKindDraft] = useState(actorKind);
  const [selected, setSelected] = useState('');
  const [eventsPage, setEventsPage] = useState(1);
  const events = useQuery({
    queryKey: [...listKeys, 'events', accountID, selected, eventsPage],
    queryFn: ({ signal }) => getBlacklistEvents(role, selected, String(eventsPage), signal),
    enabled: Boolean(accountID && selected),
    retry: false,
  });
  const [validation, setValidation] = useState('');
  const [notice, setNotice] = useState('');
  type BlacklistIntent = { id: string; reason: string; add: boolean };
  const [confirmation, setConfirmation] = useState<BlacklistIntent | null>(null);
  const mutation = useRetainedOperation<BlacklistIntent, void>(
    async (input, key, context) => {
      if (input.add) await addBlacklist(role, input.id, input.reason, key, context.signal);
      else await removeBlacklist(input.id, key, context.signal);
      context.commit(() => {
        setNotice(input.add ? label.added : label.removed);
        if (input.add) {
          setDiscordID('');
          setReason('');
        } else if (selected === input.id) setSelected('');
      });
    },
    () => client.invalidateQueries({ queryKey: listKeys }, { throwOnError: true }),
    listKeys,
  );
  useEffect(() => {
    if (isUnauthorized(mutation.error) || isForbidden(mutation.error)) {
      onAuthorityLoss?.();
    }
  }, [mutation.error, onAuthorityLoss]);
  useEffect(() => {
    if (
      isUnauthorized(result.error) ||
      isForbidden(result.error) ||
      isUnauthorized(events.error) ||
      isForbidden(events.error)
    ) {
      if (onAuthorityLoss) onAuthorityLoss();
      else clearStationSession(client, role);
    }
  }, [client, events.error, onAuthorityLoss, result.error, role]);
  const busy =
    !accountID ||
    Boolean(sessionError) ||
    mutation.isPending ||
    mutation.outcome === 'unknown' ||
    result.isFetching ||
    sessionFetching;
  const submit = (event: FormEvent) => {
    event.preventDefault();
    setValidation('');
    setNotice('');
    mutation.reset();
    const id = discordID.trim();
    const why = reason.replace(/\r\n?/g, '\n');
    if (!validDiscordID(id) || !why.trim() || [...why].length > 2000) {
      setValidation(label.invalid);
      return;
    }
    setConfirmation({ id, reason: why, add: true });
  };
  const applySearch = (event: FormEvent) => {
    event.preventDefault();
    setValidation('');
    if (
      (discordDraft.trim() && !validDiscordID(discordDraft.trim())) ||
      (actorDraft.trim() &&
        (!/^[1-9][0-9]{0,18}$/.test(actorDraft.trim()) ||
          BigInt(actorDraft.trim()) > 9223372036854775807n))
    ) {
      setValidation(label.invalidID);
      return;
    }
    setParams((previous) => {
      const next = new URLSearchParams(previous);
      if (search.trim()) next.set('q', search.trim());
      else next.delete('q');
      if (discordDraft.trim()) next.set('discord_id', discordDraft.trim());
      else next.delete('discord_id');
      if (actorDraft.trim()) next.set('actor_user_id', actorDraft.trim());
      else next.delete('actor_user_id');
      if (kindDraft) next.set('actor_kind', kindDraft);
      else next.delete('actor_kind');
      next.set('page', '1');
      return next;
    });
  };
  return (
    <div className="page ops-page">
      <PageHeader title={label.title} description={label.description} />
      <Card>
        <h2>{label.addTitle}</h2>
        <form
          onSubmit={submit}
          className="ops-toolbar"
          onChange={() => {
            setNotice('');
            setValidation('');
          }}
        >
          <label className="ops-form-field">
            <span>Discord ID</span>
            <input
              value={discordID}
              onChange={(event) => setDiscordID(event.target.value)}
              inputMode="numeric"
              autoComplete="off"
              maxLength={20}
              required
              disabled={busy}
            />
          </label>
          <label className="ops-form-field">
            <span>{label.reason}</span>
            <textarea
              value={reason}
              onChange={(event) => setReason(event.target.value)}
              maxLength={8000}
              required
              disabled={busy}
            />
          </label>
          <div className="ops-actions">
            <button type="submit" className="btn btn-primary" disabled={busy}>
              {label.add}
            </button>
          </div>
        </form>
        {validation ? <p role="alert">{validation}</p> : null}
        {notice ? <p role="status">{notice}</p> : null}
        {mutation.error ? (
          <ErrorState
            error={mutation.error}
            onRetry={() => {
              if (mutation.variables) mutation.mutate(mutation.variables);
            }}
          />
        ) : null}
        {mutation.refreshError ? (
          <ErrorState error={mutation.refreshError} onRetry={() => void mutation.refresh()} />
        ) : null}
      </Card>
      <Card>
        <form onSubmit={applySearch} className="ops-field-grid">
          <label className="ops-form-field">
            <span>{label.search}</span>
            <input
              value={search}
              onChange={(event) => setSearch(event.target.value)}
              maxLength={512}
            />
          </label>
          <label className="ops-form-field">
            <span>{label.exactDiscord}</span>
            <input
              value={discordDraft}
              onChange={(event) => setDiscordDraft(event.target.value)}
              inputMode="numeric"
              maxLength={20}
            />
          </label>
          <label className="ops-form-field">
            <span>{label.actorKind}</span>
            <select value={kindDraft} onChange={(event) => setKindDraft(event.target.value)}>
              <option value="">{t('common.blacklist.all')}</option>
              <option value="admin">{actorLabels.admin}</option>
              <option value="steward6">{actorLabels.steward6}</option>
              <option value="automatic">{actorLabels.automatic}</option>
              <option value="unknown">{actorLabels.unknown}</option>
            </select>
          </label>
          <label className="ops-form-field">
            <span>{label.actorUserID}</span>
            <input
              value={actorDraft}
              onChange={(event) => setActorDraft(event.target.value)}
              inputMode="numeric"
              maxLength={19}
            />
          </label>
          <button className="btn btn-secondary" disabled={busy} type="submit">
            {label.applySearch}
          </button>
        </form>
        {sessionError ? (
          <ErrorState error={sessionError} onRetry={refreshSession} />
        ) : result.error ? (
          <ErrorState error={result.error} onRetry={() => void result.refetch()} />
        ) : !result.data ? (
          <LoadingState />
        ) : (
          <>
            {result.data.data.length === 0 ? (
              <EmptyState title={label.empty} body={label.emptyBody} />
            ) : (
              <div className="ops-table-scroll">
                <table className="ops-table ops-table--responsive">
                  <thead>
                    <tr>
                      <th>Discord ID</th>
                      <th>{label.reason}</th>
                      <th>{label.firstActor}</th>
                      <th>{label.account}</th>
                      <th>{label.created}</th>
                      <th>{label.action}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {result.data.data.map((item) => (
                      <tr key={item.discord_id}>
                        <td data-label="Discord ID">{item.discord_id}</td>
                        <td data-label={label.reason}>
                          <span className="ops-blacklist-note">{item.reason}</span>
                        </td>
                        <td data-label={label.firstActor}>
                          {actorLabels[item.first_actor_kind]}
                          {item.first_actor_user_id ? ` #${item.first_actor_user_id}` : ''}
                        </td>
                        <td data-label={label.account}>
                          {item.user_id ? (
                            <Link
                              to={
                                role === 'admin'
                                  ? `/users?account_state=all&discord_id=${encodeURIComponent(item.discord_id)}`
                                  : `/steward?tab=users&account_state=all&discord_id=${encodeURIComponent(item.discord_id)}`
                              }
                            >
                              {item.user_id}
                            </Link>
                          ) : (
                            label.noAccount
                          )}
                        </td>
                        <td data-label={label.created}>{formatDateTime(item.created_at)}</td>
                        <td className="ops-cell-wide" data-label={label.action}>
                          <button
                            type="button"
                            className="btn btn-secondary ops-action-button"
                            disabled={busy}
                            onClick={() => {
                              setSelected(item.discord_id);
                              setEventsPage(1);
                            }}
                          >
                            {label.events}
                          </button>
                          {role === 'admin' ? (
                            <button
                              type="button"
                              className="btn btn-secondary ops-action-button"
                              disabled={busy}
                              onClick={() => {
                                setNotice('');
                                mutation.mutate({
                                  id: item.discord_id,
                                  reason: '',
                                  add: false,
                                });
                              }}
                            >
                              {label.remove}
                            </button>
                          ) : null}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
            <PagePagination
              metadata={result.data.pagination}
              requestedPage={pager.page}
              busy={busy}
              onPageChange={pager.setPage}
              onPageSizeChange={pager.setPageSize}
            />
          </>
        )}
      </Card>
      {selected ? (
        <Card>
          <div className="ops-actions">
            <h2>
              {label.events}: {selected}
            </h2>
            <button type="button" className="btn btn-quiet" onClick={() => setSelected('')}>
              {t('common.blacklist.close')}
            </button>
          </div>
          {events.isPending ? (
            <LoadingState />
          ) : events.error ? (
            <ErrorState error={events.error} onRetry={() => void events.refetch()} />
          ) : events.data.data.length === 0 ? (
            <p>{label.noEvents}</p>
          ) : (
            <ul>
              {events.data.data.map((event) => (
                <li key={event.id}>
                  {formatDateTime(event.created_at)} · {actorLabels[event.actor_kind]}
                  {event.actor_user_id ? ` #${event.actor_user_id}` : ''} · {label.eventNote}:{' '}
                  <ReasonText reason={event.safe_note} reasonCodes={event.reason_codes} />
                </li>
              ))}
            </ul>
          )}
          <div className="ops-actions">
            <button
              type="button"
              className="btn btn-secondary"
              disabled={eventsPage <= 1 || events.isFetching}
              onClick={() => setEventsPage(eventsPage - 1)}
            >
              {label.previous}
            </button>
            <button
              type="button"
              className="btn btn-secondary"
              disabled={
                !events.data ||
                BigInt(events.data.pagination.page) >= BigInt(events.data.pagination.total_pages) ||
                events.isFetching
              }
              onClick={() => setEventsPage(eventsPage + 1)}
            >
              {label.next}
            </button>
          </div>
        </Card>
      ) : null}
      <ConfirmDialog
        open={
          confirmation !== null &&
          Boolean(accountID) &&
          !isForbidden(mutation.error) &&
          !isUnauthorized(mutation.error)
        }
        title={label.addTitle}
        description={
          <>
            <p>Discord ID: {confirmation?.id}</p>
            <p>{label.description}</p>
            <p>
              {label.reason}: {confirmation?.reason}
            </p>
          </>
        }
        confirmLabel={label.add}
        danger
        busy={mutation.isPending}
        onCancel={() => setConfirmation(null)}
        onConfirm={() => {
          if (confirmation) {
            mutation.mutate(confirmation);
            setConfirmation(null);
          }
        }}
      />
    </div>
  );
}
