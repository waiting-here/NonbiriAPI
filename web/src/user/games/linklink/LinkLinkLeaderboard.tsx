import { CreditNumber } from '@shared/components/CreditNumber';
import { useState } from 'react';
import { Card, ErrorState, LoadingState } from '@shared/components/States';
import { Field } from '@shared/components/ui/Field';
import { useDateTimeFormatter } from '@shared/utils/datetime';
import { useGameCopy } from '../copy';
import { LINKLINK_SPECS, type LinkLinkSpec } from '../common/types';
import { formatCredits } from '../common/strict';
import { PublicGameIdentity } from '../common/PublicGameIdentity';
import { useLinkLinkLeaderboard } from './api';

export function LinkLinkLeaderboard({
  spec,
  onSpecChange,
}: {
  readonly spec: LinkLinkSpec;
  readonly onSpecChange: (spec: LinkLinkSpec) => void;
}) {
  const { text } = useGameCopy();
  const formatDateTime = useDateTimeFormatter();
  const [days, setDays] = useState<7 | 30>(7);
  const query = useLinkLinkLeaderboard(spec, days);
  const rows = [...(query.data?.rows ?? []), ...(query.data?.me ? [query.data.me] : [])];
  return (
    <Card className="linklink-leaderboard">
      <div className="rank-heading__top">
        <h2>{text('linklink.leaderboard.title', { days })}</h2>
        <button
          type="button"
          className="nb-btn nb-btn--secondary nb-btn--sm"
          disabled={query.isFetching}
          onClick={() => void query.refetch()}
        >
          {text('fishing.leaderboard.refresh')}
        </button>
      </div>
      <p className="nb-small nb-muted">{text('linklink.leaderboard.help')}</p>
      <div className="game-state-actions">
        <Field label={text('linklink.leaderboard.spec')}>
          {(props) => (
            <select
              {...props}
              value={spec}
              onChange={(event) => onSpecChange(event.target.value as LinkLinkSpec)}
            >
              {LINKLINK_SPECS.map((value) => (
                <option key={value} value={value}>
                  {value}
                </option>
              ))}
            </select>
          )}
        </Field>
        <Field label={text('linklink.leaderboard.window')}>
          {(props) => (
            <select
              {...props}
              value={days}
              onChange={(event) => setDays(event.target.value === '7' ? 7 : 30)}
            >
              <option value="7">{text('linklink.leaderboard.days', { days: 7 })}</option>
              <option value="30">{text('linklink.leaderboard.days', { days: 30 })}</option>
            </select>
          )}
        </Field>
      </div>
      {query.isPending ? <LoadingState label={text('common.loading')} /> : null}
      {query.error ? <ErrorState error={query.error} onRetry={() => void query.refetch()} /> : null}
      {query.isSuccess && rows.length === 0 ? (
        <p className="nb-small nb-muted">{text('linklink.leaderboard.empty')}</p>
      ) : null}
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
                    <PublicGameIdentity
                      identity={row.identity}
                      anonymousLabel={text('linklink.leaderboard.anonymous')}
                      isMe={row.isMe}
                      meLabel={text('fishing.leaderboard.me')}
                    />
                  </td>
                  <td className="nb-num">
                    <CreditNumber value={formatCredits(row.score)} />
                  </td>
                  <td>
                    <time dateTime={new Date(row.achievedAt * 1000).toISOString()}>
                      {formatDateTime(new Date(row.achievedAt * 1000).toISOString())}
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
