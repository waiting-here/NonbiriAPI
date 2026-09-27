import { useEffect, useState, type FormEvent } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router';
import { Card, EmptyState, ErrorState, LoadingState, PageHeader } from '@shared/components/States';
import { PagePagination } from '@shared/operations/PagePagination';
import { useUrlPagePager } from '@shared/operations/useUrlPagePager';
import { useSearchState } from '@shared/operations/useSearchState';
import { operationKey } from '@shared/operations/api';
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

const labels = {
  en: { title: 'Discord blacklist', description: 'Listed Discord IDs cannot register. Existing accounts are permanently banned and lose sessions and caller keys. Removing an entry does not unban an existing account.', addTitle: 'Add to blacklist', reason: 'Reason', add: 'Add and permanently ban', added: 'Added to the blacklist. Any existing account is permanently banned.', removed: 'Removed from the blacklist. Unban existing accounts separately in user management if needed.', invalid: 'Enter a valid Discord ID and a note of up to 2,000 characters.', invalidID: 'Enter a valid numeric Discord ID or actor user ID.', search: 'Search Discord ID or first note', applySearch: 'Search', empty: 'No matching blacklist entries', emptyBody: 'Use the form above to add a Discord ID.', account: 'Account', noAccount: 'No current account', created: 'Added', action: 'Action', remove: 'Remove from blacklist', firstActor: 'First actor', actorKind: 'First actor type', actorUserID: 'First actor user ID', exactDiscord: 'Exact Discord ID', events: 'Events', eventNote: 'Additional note', noEvents: 'No additional events', previous: 'Previous', next: 'Next' },
  zh: { title: 'Discord 黑名单', description: '名单中的 Discord ID 无法注册；已有账号会立即永久封禁并撤销登录会话与调用密钥。移除名单不会自动解封已有账号。', addTitle: '加入黑名单', reason: '原因', add: '加入并永久封禁', added: '已加入黑名单，已有账号已永久封禁。', removed: '已移除黑名单；已有账号如需恢复，请在用户管理中单独解封。', invalid: '请填写有效 Discord ID 和最多 2000 字的说明。', invalidID: '请填写有效的纯数字 Discord ID 或发起人站内 ID。', search: '搜索 Discord ID 或首次说明', applySearch: '查询', empty: '没有匹配的黑名单记录', emptyBody: '可以通过上方表单添加 Discord ID。', account: '站内账号', noAccount: '当前无账号', created: '加入时间', action: '操作', remove: '移除黑名单', firstActor: '首次发起人', actorKind: '首次发起类型', actorUserID: '首次发起人站内 ID', exactDiscord: '精确 Discord ID', events: '追加记录', eventNote: '追加说明', noEvents: '没有追加记录', previous: '上一页', next: '下一页' },
} as const;

export function BlacklistPage() {
  const session = useAdminSession();
  return <BlacklistManagement
    role="admin"
    accountID={session.data ? `admin:${session.data.admin.username}` : undefined}
    sessionError={session.error}
    sessionFetching={session.isFetching}
    refreshSession={() => void session.refetch()}
  />;
}

interface BlacklistManagementProps {
  role: ManagementRole;
  accountID?: string;
  sessionError: unknown;
  sessionFetching: boolean;
  refreshSession: () => void;
  onAuthorityLoss?: () => void;
}

export function BlacklistManagement({ role, accountID, sessionError, sessionFetching, refreshSession, onAuthorityLoss }: BlacklistManagementProps) {
  const formatDateTime = useDateTimeFormatter();
  const { i18n, t } = useTranslation();
  const zh = i18n.language.startsWith('zh');
  const local = zh ? labels.zh : labels.en;
  const actorLabels = zh
    ? { admin: '管理员', steward6: '6 级协管', automatic: '系统自动', unknown: '未知' }
    : { admin: 'Administrator', steward6: 'Level 6 steward', automatic: 'Automatic', unknown: 'Unknown' };
  const label = role === 'admin' ? {
    ...local,
    title: t('admin.blacklist.title'),
    description: t('admin.blacklist.description'),
    addTitle: t('admin.blacklist.addTitle'),
    reason: t('admin.blacklist.reason'),
    add: t('admin.blacklist.add'),
    added: t('admin.blacklist.added'),
    removed: t('admin.blacklist.removed'),
    invalid: t('admin.blacklist.invalid'),
    invalidID: t('admin.blacklist.invalidID'),
    search: t('admin.blacklist.search'),
    applySearch: t('admin.blacklist.applySearch'),
    empty: t('admin.blacklist.empty'),
    emptyBody: t('admin.blacklist.emptyBody'),
    account: t('admin.blacklist.account'),
    noAccount: t('admin.blacklist.noAccount'),
    created: t('admin.blacklist.created'),
    action: t('admin.blacklist.action'),
    remove: t('admin.blacklist.remove'),
  } : local;
  const client = useQueryClient();
  const [params, setParams] = useSearchState();
  const q = params.get('q') ?? '';
  const actorKind = params.get('actor_kind') ?? '';
  const actorUserID = params.get('actor_user_id') ?? '';
  const filterDiscordID = params.get('discord_id') ?? '';
  const listKeys = [role, 'blacklist'] as const;
  const pager = useUrlPagePager({
    station: role === 'admin' ? 'admin' : 'user',
    listType: 'blacklist',
    scopeKey: accountID ?? 'anonymous',
    scopeReady: Boolean(accountID),
    resetKey: `${q}|${actorKind}|${actorUserID}|${filterDiscordID}`,
  });
  const result = useQuery({
    queryKey: [...listKeys, accountID, q, actorKind, actorUserID, filterDiscordID, pager.page, pager.pageSize],
    queryFn: ({ signal }) => getBlacklist(role, pager.page, pager.pageSize, q, actorKind, actorUserID, filterDiscordID, signal),
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
  const mutation = useMutation({
    retry: false,
    mutationFn: (input: { id: string; reason: string; add: boolean; key: string }) =>
      input.add
        ? addBlacklist(role, input.id, input.reason, input.key)
        : removeBlacklist(input.id, input.key),
    onSuccess: (_, input) => {
      setNotice(input.add ? label.added : label.removed);
      if (input.add) {
        setDiscordID('');
        setReason('');
      } else if (selected === input.id) {
        setSelected('');
      }
    },
    onError: (error) => {
      if (isUnauthorized(error) || isForbidden(error)) {
        if (onAuthorityLoss) onAuthorityLoss();
        else clearStationSession(client, role);
      }
    },
    onSettled: () => client.invalidateQueries({ queryKey: listKeys }),
  });
  useEffect(() => {
    if (isUnauthorized(result.error) || isForbidden(result.error) || isUnauthorized(events.error) || isForbidden(events.error)) {
      if (onAuthorityLoss) onAuthorityLoss();
      else clearStationSession(client, role);
    }
  }, [client, events.error, onAuthorityLoss, result.error, role]);
  const busy =
    !accountID ||
    Boolean(sessionError) ||
    mutation.isPending ||
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
    mutation.mutate({ id, reason: why, add: true, key: operationKey() });
  };
  const applySearch = (event: FormEvent) => {
    event.preventDefault();
    setValidation('');
    if ((discordDraft.trim() && !validDiscordID(discordDraft.trim())) || (actorDraft.trim() && (!/^[1-9][0-9]{0,18}$/.test(actorDraft.trim()) || BigInt(actorDraft.trim()) > 9223372036854775807n))) {
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
      <PageHeader
        title={label.title}
        description={label.description}
      />
      <Card>
        <h2>{label.addTitle}</h2>
        <form onSubmit={submit} className="ops-toolbar">
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
            <input value={discordDraft} onChange={(event) => setDiscordDraft(event.target.value)} inputMode="numeric" maxLength={20} />
          </label>
          <label className="ops-form-field">
            <span>{label.actorKind}</span>
            <select value={kindDraft} onChange={(event) => setKindDraft(event.target.value)}>
              <option value="">{zh ? '全部' : 'All'}</option>
              <option value="admin">{actorLabels.admin}</option>
              <option value="steward6">{actorLabels.steward6}</option>
              <option value="automatic">{actorLabels.automatic}</option>
              <option value="unknown">{actorLabels.unknown}</option>
            </select>
          </label>
          <label className="ops-form-field">
            <span>{label.actorUserID}</span>
            <input value={actorDraft} onChange={(event) => setActorDraft(event.target.value)} inputMode="numeric" maxLength={19} />
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
              <EmptyState
                title={label.empty}
                body={label.emptyBody}
              />
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
                        <td className="ops-wrap" data-label={label.reason}>
                          {item.reason}
                        </td>
                        <td data-label={label.firstActor}>{actorLabels[item.first_actor_kind]}{item.first_actor_user_id ? ` #${item.first_actor_user_id}` : ''}</td>
                        <td data-label={label.account}>
                          {item.user_id ? (
                            <Link to={role === 'admin' ? `/users?account_state=all&discord_id=${encodeURIComponent(item.discord_id)}` : `/steward?tab=users&account_state=all&discord_id=${encodeURIComponent(item.discord_id)}`}>
                              {item.user_id}
                            </Link>
                          ) : (
                            label.noAccount
                          )}
                        </td>
                        <td data-label={label.created}>
                          {formatDateTime(item.created_at)}
                        </td>
                        <td className="ops-cell-wide" data-label={label.action}>
                          <button type="button" className="btn btn-secondary ops-action-button" disabled={busy} onClick={() => { setSelected(item.discord_id); setEventsPage(1); }}>{label.events}</button>
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
                                key: operationKey(),
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
            <h2>{label.events}: {selected}</h2>
            <button type="button" className="btn btn-quiet" onClick={() => setSelected('')}>{zh ? '关闭' : 'Close'}</button>
          </div>
          {events.isPending ? <LoadingState /> : events.error ? <ErrorState error={events.error} onRetry={() => void events.refetch()} /> : events.data.data.length === 0 ? <p>{label.noEvents}</p> : (
            <ul>{events.data.data.map((event) => <li key={event.id}>{formatDateTime(event.created_at)} · {actorLabels[event.actor_kind]}{event.actor_user_id ? ` #${event.actor_user_id}` : ''} · {label.eventNote}: {event.safe_note}</li>)}</ul>
          )}
          <div className="ops-actions">
            <button type="button" className="btn btn-secondary" disabled={eventsPage <= 1 || events.isFetching} onClick={() => setEventsPage(eventsPage - 1)}>{label.previous}</button>
            <button type="button" className="btn btn-secondary" disabled={!events.data || BigInt(events.data.pagination.page) >= BigInt(events.data.pagination.total_pages) || events.isFetching} onClick={() => setEventsPage(eventsPage + 1)}>{label.next}</button>
          </div>
        </Card>
      ) : null}
    </div>
  );
}
