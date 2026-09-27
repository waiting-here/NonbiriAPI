import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Link, useLocation } from 'react-router';
import { useTranslation } from 'react-i18next';
import { Card, ErrorState, LoadingState } from './States';
import { useDateTimeFormatter } from '@shared/utils/datetime';
import { getDeletionDuelAborts, type DeletedAccount, type ManagementRole } from '@shared/operations/managedUsers';
import { accountCopy, accountHistoryCopy } from './accountCopy';

export function DeletedAccountCard({ account, role, onClose }: { account: DeletedAccount; role: ManagementRole; onClose: () => void }) {
  const { i18n } = useTranslation();
  const label = i18n.language.startsWith('zh') ? accountCopy.zh : accountCopy.en;
  const history = i18n.language.startsWith('zh') ? accountHistoryCopy.zh : accountHistoryCopy.en;
  const formatDateTime = useDateTimeFormatter();
  const location = useLocation();
  const [abortPage, setAbortPage] = useState(1);
  const canReadAborts = role === 'admin' && Boolean(account.discord_id && /^[1-9][0-9]{0,19}$/.test(account.discord_id));
  const aborts = useQuery({
    queryKey: ['admin', 'deletion-duel-aborts', account.discord_id, abortPage],
    queryFn: ({ signal }) => getDeletionDuelAborts(account.discord_id!, String(abortPage), signal),
    enabled: canReadAborts,
    retry: false,
  });
  const show = (value: string | number | null) => value === null ? label.unknown : String(value);
  const time = (value: number | null) => value === null ? label.unknown : formatDateTime(value);
  const penalty = (value: DeletedAccount['ban']) => value.state === 'unknown' || value.active_at_deletion === null
    ? label.unknown
    : `${value.active_at_deletion ? label.active : label.inactive}${value.reason ? ` · ${label.reason}: ${value.reason}` : ''}${value.until !== null ? ` · ${label.until}: ${formatDateTime(value.until)}` : ''}`;
  const sameIdentityPath = role === 'admin'
    ? `/users?account_state=all&discord_id=${encodeURIComponent(account.discord_id ?? '')}`
    : `/steward?tab=users&account_state=all&discord_id=${encodeURIComponent(account.discord_id ?? '')}`;
  const returnPath = `${location.pathname}${location.search}`;
  const alertParams = new URLSearchParams({ alert_id: account.alert_id ?? '' });
  if (returnPath.startsWith('/users') && returnPath.length <= 2048) {
    alertParams.set('return_to', returnPath);
  }
  return (
    <div className="ops-stack">
      <Card>
        <div className="ops-actions">
          <h2>{label.deleted} #{account.record_id}</h2>
          <button type="button" className="btn btn-quiet" onClick={onClose}>{label.close}</button>
        </div>
        <dl className="ops-kv">
          <dt>{label.formerID}</dt><dd>{show(account.former_user_id)}</dd>
          <dt>{label.discord}</dt><dd>{show(account.discord_id)}</dd>
          <dt>{label.registered}</dt><dd>{time(account.registered_at)}</dd>
          <dt>{label.deletedAt}</dt><dd>{time(account.deleted_at)}</dd>
          <dt>{label.level}</dt><dd>{show(account.effective_level)}</dd>
          <dt>{label.source}</dt><dd>{history.sources[account.source]}</dd>
          <dt>{label.ban}</dt><dd>{penalty(account.ban)}</dd>
          <dt>{label.pause}</dt><dd>{penalty(account.charity_pause)}</dd>
          <dt>{label.blacklist}</dt><dd>{history.actions[account.blacklist_action]}</dd>
          {account.blacklist_reason_codes.length > 0 ? <><dt>{label.reason}</dt><dd>{account.blacklist_reason_codes.map((code) => history.reasons[code]).join(' · ')}</dd></> : null}
          <dt>{label.general}</dt><dd>{show(account.general_balance)}</dd>
          <dt>{label.game}</dt><dd>{show(account.game_balance)}</dd>
          <dt>{label.donation}</dt><dd>{show(account.donation_credit)}</dd>
          <dt>{label.paper}</dt><dd>{show(account.sketch_paper)}</dd>
          <dt>{label.brush}</dt><dd>{show(account.sketch_brush)}</dd>
        </dl>
        <p>{history.coverage}</p>
        {account.discord_id ? <Link to={sameIdentityPath}>{label.sameIdentity}</Link> : null}
        {role === 'admin' && account.alert_id ? <p><Link to={`/alerts?${alertParams.toString()}`}>{label.alert}: #{account.alert_id}</Link></p> : null}
      </Card>
      {canReadAborts ? (
        <Card>
          <h3>{label.aborts}</h3>
          {aborts.isPending ? <LoadingState /> : aborts.error ? <ErrorState error={aborts.error} onRetry={() => void aborts.refetch()} /> : aborts.data.data.length === 0 ? <p>{label.noAborts}</p> : (
            <ul>{aborts.data.data.map((item) => <li key={item.id}>{history.games[item.game_key]} · {label.match} {item.match_id} · {label.occurred} {formatDateTime(item.occurred_at)}</li>)}</ul>
          )}
          <div className="ops-actions">
            <button type="button" className="btn btn-secondary" disabled={abortPage <= 1 || aborts.isFetching} onClick={() => setAbortPage(abortPage - 1)}>{label.previous}</button>
            <button type="button" className="btn btn-secondary" disabled={!aborts.data || BigInt(aborts.data.pagination.page) >= BigInt(aborts.data.pagination.total_pages) || aborts.isFetching} onClick={() => setAbortPage(abortPage + 1)}>{label.next}</button>
          </div>
        </Card>
      ) : null}
    </div>
  );
}
