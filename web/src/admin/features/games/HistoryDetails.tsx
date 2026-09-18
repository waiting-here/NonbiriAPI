import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { ErrorState, LoadingState } from '@shared/components/States';
import { DuelDialog } from '../../../user/games/common/duel/Dialog';
import { getMatch, getRounds, type Dataset, type JSONValue } from './history';
import { useGameAdminText, type GameID } from './copy';
import '../../../user/games/common/duel/duel.css';

function FactDisclosure({ label, value }: { label: string; value: JSONValue }) {
  const [open, setOpen] = useState(false);
  return (
    <details onToggle={(event) => setOpen(event.currentTarget.open)}>
      <summary>{label}</summary>
      {open && <Facts value={value} />}
    </details>
  );
}
function Facts({ value }: { value: JSONValue }) {
  const t = useGameAdminText();
  // Expand individual facts without rendering an entire long match at once.
  if (value === null) return <span>{t('无', 'None')}</span>;
  if (typeof value !== 'object') return <span className="admin-duel-value">{String(value)}</span>;
  const entries = Array.isArray(value)
    ? value.map((v, i) => [String(i + 1), v] as const)
    : Object.entries(value);
  return (
    <dl className="admin-duel-facts">
      {entries.map(([key, value]) => (
        <div key={key}>
          <dt>{key}</dt>
          <dd>
            {value && typeof value === 'object' ? (
              <FactDisclosure
                label={`${t('展开', 'Expand')} · ${Array.isArray(value) ? value.length : Object.keys(value).length} ${t('项', 'items')}`}
                value={value}
              />
            ) : (
              <Facts value={value} />
            )}
          </dd>
        </div>
      ))}
    </dl>
  );
}
export function HistoryDetails({
  game,
  dataset,
  id,
  onClose,
}: {
  game: GameID;
  dataset: Dataset;
  id: string;
  onClose: () => void;
}) {
  const t = useGameAdminText(),
    [cursors, setCursors] = useState<(string | null)[]>([null]),
    after = cursors[cursors.length - 1];
  const detail = useQuery({
    queryKey: ['admin', 'duel', game, dataset, id],
    queryFn: ({ signal }) => getMatch(game, dataset, id, signal),
    retry: false,
  });
  const rounds = useQuery({
    queryKey: ['admin', 'duel', game, dataset, id, 'rounds', after],
    queryFn: ({ signal }) => getRounds(game, dataset, id, after, signal),
    enabled: Boolean(detail.data) && !detail.error,
    retry: false,
  });
  return (
    <DuelDialog
      className="admin-duel-dialog"
      title={t('完整对战过程', 'Complete match record')}
      onClose={onClose}
    >
      <p className="admin-duel-id">
        {dataset === 'recent'
          ? t('原局ID', 'Original match ID')
          : t('匿名资料编号', 'Anonymous record ID')}
        : {id}
      </p>
      {detail.isPending ? (
        <LoadingState />
      ) : detail.error ? (
        <ErrorState error={detail.error} onRetry={() => void detail.refetch()} />
      ) : (
        detail.data && (
          <>
            <p>
              {t('规则版本', 'Rules version')}: {detail.data.facts.rules_version} ·{' '}
              {t('票价', 'Entry')}: {detail.data.facts.ticket} · {t('奖金', 'Prize')}:{' '}
              {detail.data.facts.prize}
            </p>
            {detail.data.recent && (
              <p>
                {detail.data.recent.participants
                  .map(
                    (p, i) =>
                      `${t('席位', 'Seat')} ${i}: ${p.user_id === null ? t('账号已删除', 'Deleted account') : `${p.display_name} (${p.user_id})`} · ${t('通用／游戏投入', 'General/game paid')} ${p.general_paid}/${p.game_paid}`,
                  )
                  .join(' · ')}
              </p>
            )}
            <FactDisclosure
              label={t('初始配置与资源', 'Initial configuration and resources')}
              value={detail.data.facts.initial}
            />
            <FactDisclosure label={t('最终状态', 'Final state')} value={detail.data.facts.final} />
            <FactDisclosure
              label={t('终止时已锁定的方案', 'Plans locked at termination')}
              value={detail.data.facts.terminal_actions}
            />
            <h3>{t('逐轮记录', 'Round records')}</h3>
            {rounds.isPending ? (
              <LoadingState />
            ) : rounds.error ? (
              <ErrorState error={rounds.error} onRetry={() => void rounds.refetch()} />
            ) : (
              rounds.data && (
                <>
                  {!rounds.data.items.length && (
                    <p>{t('本局未完成任何轮次。', 'No rounds were completed.')}</p>
                  )}
                  {rounds.data.items.map((round) => (
                    <details key={round.round} className="admin-duel-round">
                      <summary>
                        {t('第', 'Round')} {round.round} {t('轮', '')} ·{' '}
                        {round.timeouts
                          .map(
                            (timeout, i) =>
                              `${t('席位', 'Seat')} ${i}: ${timeout ? t('超时', 'Timeout') : t('已确认', 'Confirmed')}`,
                          )
                          .join(' · ')}
                      </summary>
                      <FactDisclosure
                        label={t('轮初变化', 'Round-start changes')}
                        value={round.start_events}
                      />
                      <FactDisclosure
                        label={t('结算前状态', 'Before settlement')}
                        value={round.before}
                      />
                      <FactDisclosure
                        label={t(
                          '方案、结算事件与随机结果',
                          'Plans, settlement events, and random results',
                        )}
                        value={round.facts}
                      />
                      <FactDisclosure
                        label={t('结算后状态', 'After settlement')}
                        value={round.after}
                      />
                    </details>
                  ))}
                  <div className="ops-actions">
                    <button
                      className="btn btn-secondary"
                      type="button"
                      disabled={cursors.length === 1}
                      onClick={() => setCursors(cursors.slice(0, -1))}
                    >
                      {t('上一页轮次', 'Previous rounds')}
                    </button>
                    <span>
                      {t('第', 'Page')} {cursors.length} {t('页', '')}
                    </span>
                    <button
                      className="btn btn-secondary"
                      type="button"
                      disabled={!rounds.data.next_cursor}
                      onClick={() => setCursors([...cursors, rounds.data.next_cursor])}
                    >
                      {t('下一页轮次', 'Next rounds')}
                    </button>
                  </div>
                </>
              )
            )}
          </>
        )
      )}
    </DuelDialog>
  );
}
