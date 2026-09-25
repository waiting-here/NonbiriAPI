import { useState, type FormEvent } from 'react';
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
import {
  addBlacklist,
  blacklistKeys,
  getBlacklist,
  removeBlacklist,
  validDiscordID,
} from '../features/operations/blacklist';
import '@shared/operations/operations.css';

export function BlacklistPage() {
  const formatDateTime = useDateTimeFormatter();
  const { t } = useTranslation();
  const client = useQueryClient();
  const session = useAdminSession();
  const accountID = session.data ? `admin:${session.data.admin.username}` : undefined;
  const [params, setParams] = useSearchState();
  const q = params.get('q') ?? '';
  const pager = useUrlPagePager({
    station: 'admin',
    listType: 'blacklist',
    scopeKey: accountID ?? 'anonymous',
    scopeReady: Boolean(accountID),
    resetKey: q,
  });
  const result = useQuery({
    queryKey: [...blacklistKeys, accountID, q, pager.page, pager.pageSize],
    queryFn: ({ signal }) => getBlacklist(pager.page, pager.pageSize, q, signal),
    enabled: Boolean(accountID) && !session.isFetching,
    retry: false,
  });
  const [discordID, setDiscordID] = useState('');
  const [reason, setReason] = useState('');
  const [search, setSearch] = useState(q);
  const [validation, setValidation] = useState('');
  const [notice, setNotice] = useState('');
  const mutation = useMutation({
    retry: false,
    mutationFn: (input: { id: string; reason: string; add: boolean; key: string }) =>
      input.add
        ? addBlacklist(input.id, input.reason, input.key)
        : removeBlacklist(input.id, input.key),
    onSuccess: (_, input) => {
      setNotice(t(input.add ? 'admin.blacklist.added' : 'admin.blacklist.removed'));
      if (input.add) {
        setDiscordID('');
        setReason('');
      }
    },
    onError: (error) => {
      if (isUnauthorized(error) || isForbidden(error)) clearStationSession(client, 'admin');
    },
    onSettled: () => client.invalidateQueries({ queryKey: blacklistKeys }),
  });
  const busy =
    !accountID ||
    Boolean(session.error) ||
    mutation.isPending ||
    result.isFetching ||
    session.isFetching;
  const submit = (event: FormEvent) => {
    event.preventDefault();
    setValidation('');
    setNotice('');
    mutation.reset();
    const id = discordID.trim();
    const why = reason.trim();
    if (!validDiscordID(id) || !why || [...why].length > 1024) {
      setValidation(t('admin.blacklist.invalid'));
      return;
    }
    mutation.mutate({ id, reason: why, add: true, key: operationKey() });
  };
  const applySearch = (event: FormEvent) => {
    event.preventDefault();
    setValidation('');
    if (search.trim() && !validDiscordID(search.trim())) {
      setValidation(t('admin.blacklist.invalidID'));
      return;
    }
    setParams((previous) => {
      const next = new URLSearchParams(previous);
      if (search.trim()) next.set('q', search.trim());
      else next.delete('q');
      next.set('page', '1');
      return next;
    });
  };
  return (
    <div className="page ops-page">
      <PageHeader
        title={t('admin.blacklist.title')}
        description={t('admin.blacklist.description')}
      />
      <Card>
        <h2>{t('admin.blacklist.addTitle')}</h2>
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
            <span>{t('admin.blacklist.reason')}</span>
            <input
              value={reason}
              onChange={(event) => setReason(event.target.value)}
              maxLength={1024}
              required
              disabled={busy}
            />
          </label>
          <div className="ops-actions">
            <button type="submit" className="btn btn-primary" disabled={busy}>
              {t('admin.blacklist.add')}
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
            <span>{t('admin.blacklist.search')}</span>
            <input
              value={search}
              onChange={(event) => setSearch(event.target.value)}
              inputMode="numeric"
              maxLength={20}
            />
          </label>
          <button className="btn btn-secondary" disabled={busy} type="submit">
            {t('admin.blacklist.applySearch')}
          </button>
        </form>
        {session.error ? (
          <ErrorState error={session.error} onRetry={() => void session.refetch()} />
        ) : result.error ? (
          <ErrorState error={result.error} onRetry={() => void result.refetch()} />
        ) : !result.data ? (
          <LoadingState />
        ) : (
          <>
            {result.data.data.length === 0 ? (
              <EmptyState
                title={t('admin.blacklist.empty')}
                body={t('admin.blacklist.emptyBody')}
              />
            ) : (
              <div className="ops-table-scroll">
                <table className="ops-table ops-table--responsive">
                  <thead>
                    <tr>
                      <th>Discord ID</th>
                      <th>{t('admin.blacklist.reason')}</th>
                      <th>{t('admin.blacklist.account')}</th>
                      <th>{t('admin.blacklist.created')}</th>
                      <th>{t('admin.blacklist.action')}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {result.data.data.map((item) => (
                      <tr key={item.discord_id}>
                        <td data-label="Discord ID">{item.discord_id}</td>
                        <td className="ops-wrap" data-label={t('admin.blacklist.reason')}>
                          {item.reason}
                        </td>
                        <td data-label={t('admin.blacklist.account')}>
                          {item.user_id ? (
                            <Link to={`/users?q=${encodeURIComponent(item.discord_id)}`}>
                              {item.user_id}
                            </Link>
                          ) : (
                            t('admin.blacklist.noAccount')
                          )}
                        </td>
                        <td data-label={t('admin.blacklist.created')}>
                          {formatDateTime(item.created_at)}
                        </td>
                        <td className="ops-cell-wide" data-label={t('admin.blacklist.action')}>
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
                            {t('admin.blacklist.remove')}
                          </button>
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
    </div>
  );
}
