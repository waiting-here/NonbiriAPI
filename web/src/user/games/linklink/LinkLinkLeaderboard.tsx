import { useState } from 'react';
import { Card, ErrorState, LoadingState } from '@shared/components/States';
import { useGameCopy } from '../copy';
import { LINKLINK_SPECS, type LinkLinkSpec } from '../common/types';
import { formatCredits } from '../common/strict';
import { useLinkLinkLeaderboard } from './api';

export function LinkLinkLeaderboard({
  spec,
  onSpecChange,
}: {
  readonly spec: LinkLinkSpec;
  readonly onSpecChange: (spec: LinkLinkSpec) => void;
}) {
  const { text } = useGameCopy();
  const [days, setDays] = useState<7 | 30>(7);
  const query = useLinkLinkLeaderboard(spec, days);
  const rows = [...(query.data?.rows ?? []), ...(query.data?.me ? [query.data.me] : [])];
  return (
    <Card className="linklink-leaderboard">
      <h2>{text('linklink.leaderboard.title', { days })}</h2>
      <p>{text('linklink.leaderboard.help')}</p>
      <div className="game-state-actions">
        <label>
          {text('linklink.leaderboard.spec')}
          <select
            value={spec}
            onChange={(event) => onSpecChange(event.target.value as LinkLinkSpec)}
          >
            {LINKLINK_SPECS.map((value) => (
              <option key={value} value={value}>
                {value}
              </option>
            ))}
          </select>
        </label>
        <label>
          {text('linklink.leaderboard.window')}
          <select value={days} onChange={(event) => setDays(event.target.value === '7' ? 7 : 30)}>
            <option value="7">{text('linklink.leaderboard.days', { days: 7 })}</option>
            <option value="30">{text('linklink.leaderboard.days', { days: 30 })}</option>
          </select>
        </label>
        <button
          type="button"
          className="btn btn-secondary"
          disabled={query.isFetching}
          onClick={() => void query.refetch()}
        >
          {text('fishing.leaderboard.refresh')}
        </button>
      </div>
      {query.isPending ? <LoadingState label={text('common.loading')} /> : null}
      {query.error ? <ErrorState error={query.error} onRetry={() => void query.refetch()} /> : null}
      {query.isSuccess && rows.length === 0 ? <p>{text('linklink.leaderboard.empty')}</p> : null}
      {rows.length > 0 ? (
        <div className="linklink-ranks-scroll">
          <table className="data-table">
            <thead>
              <tr>
                <th>{text('linklink.leaderboard.rank')}</th>
                <th>{text('linklink.leaderboard.player')}</th>
                <th>{text('linklink.summary.score')}</th>
                <th>{text('linklink.leaderboard.achieved')}</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((row) => (
                <tr key={row.rank} className={row.isMe ? 'is-me' : undefined}>
                  <td>{row.rank}</td>
                  <td>
                    {row.identity.kind === 'public'
                      ? row.identity.displayName
                      : text('fishing.leaderboard.anonymous')}
                    {row.isMe ? ` · ${text('fishing.leaderboard.me')}` : ''}
                  </td>
                  <td>{formatCredits(row.score)}</td>
                  <td>
                    <time dateTime={new Date(row.achievedAt * 1000).toISOString()}>
                      {new Date(row.achievedAt * 1000).toLocaleString()}
                    </time>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : null}
    </Card>
  );
}
