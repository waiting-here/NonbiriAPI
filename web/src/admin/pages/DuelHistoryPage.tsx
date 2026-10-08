import { SimplePager } from '@shared/operations/SimplePager';
import { useState, type FormEvent } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Link } from 'react-router';
import { useTranslation } from 'react-i18next';
import { Card, EmptyState, ErrorState, LoadingState, PageHeader } from '@shared/components/States';
import { useDateTimeFormatter } from '@shared/utils/datetime';
import { useSiteTimeOffset } from '@shared/components/timeContextValue';
import { TimeContextNotice } from '@shared/components/TimeContext';
import { resolveFixedLocalTime } from '@shared/time';
import {
  gameLabel,
  modeLabel,
  modesFor,
  useGameAdminText,
  type GameID,
} from '../features/games/copy';
import { getHistory, type Dataset, type Selection } from '../features/games/history';
import { HistoryDetails } from '../features/games/HistoryDetails';
import { ExportPanel } from '../features/games/ExportPanel';
import '@shared/operations/operations.css';
import '../features/games/history.css';

interface Filter {
  game: GameID;
  dataset: Dataset;
  mode: string;
  version: string;
  outcome: string;
  from: string;
  to: string;
}
const initialFilter: Filter = {
  game: 'bidding',
  dataset: 'recent',
  mode: '',
  version: '',
  outcome: '',
  from: '',
  to: '',
};
function selectionFor(f: Filter, offsetMinutes: number | null): Selection {
  const selection: Selection = {};
  if (f.mode) selection.mode = f.mode;
  if (f.version) {
    const n = Number(f.version);
    if (!/^[1-9][0-9]*$/.test(f.version) || !Number.isSafeInteger(n) || n > 2147483647)
      throw new Error('version');
    selection.rules_version = n;
  }
  if (f.outcome) selection.outcome = f.outcome;
  if (f.from || f.to) {
    if ((f.from || f.to) && offsetMinutes === null) throw new Error('site time unavailable');
    if (f.from)
      selection.from = resolveFixedLocalTime(
        f.from.length === 16 ? `${f.from}:00` : f.from,
        offsetMinutes!,
      ).instant;
    if (f.to)
      selection.to = resolveFixedLocalTime(
        f.to.length === 16 ? `${f.to}:00` : f.to,
        offsetMinutes!,
      ).instant;
    if (
      [selection.from, selection.to].some(
        (v) => v !== undefined && (!Number.isSafeInteger(v) || v < 0),
      ) ||
      (selection.from !== undefined && selection.to !== undefined && selection.from > selection.to)
    )
      throw new Error('date');
  }
  return selection;
}
function HistoryResults({
  game,
  dataset,
  selection,
}: {
  game: GameID;
  dataset: Dataset;
  selection: Selection;
}) {
  const formatDateTime = useDateTimeFormatter();
  const { t: text } = useTranslation();
  const t = useGameAdminText(),
    [cursors, setCursors] = useState<(string | null)[]>([null]),
    [selected, setSelected] = useState<string | null>(null),
    after = cursors[cursors.length - 1];
  const query = useQuery({
    queryKey: ['admin', 'duel-history', game, dataset, selection, after],
    queryFn: ({ signal }) => getHistory(game, dataset, selection, after, signal),
    retry: false,
  });
  return (
    <>
      <Card>
        <h2>
          {gameLabel(game, t)} ·{' '}
          {dataset === 'recent'
            ? t('近30天完整历史', 'Recent complete history')
            : t('长期匿名资料', 'Anonymous archive')}
        </h2>
        {query.isPending ? (
          <LoadingState />
        ) : query.error ? (
          <ErrorState error={query.error} onRetry={() => void query.refetch()} />
        ) : (
          query.data && (
            <>
              {query.data.items.length === 0 ? (
                <EmptyState
                  title={t('没有符合条件的对局', 'No matching matches')}
                  body={t(
                    '只有已经结束的对局会出现在这里。',
                    'Only completed matches appear here.',
                  )}
                />
              ) : (
                <div className="admin-duel-history-list">
                  {query.data.items.map((item) => (
                    <article className="ops-subcard" key={item.match_ref}>
                      <div>
                        <h3>
                          {modeLabel(item.mode, t)} ·{' '}
                          {item.outcome === 'normal'
                            ? t('分出胜负', 'Decided')
                            : item.outcome === 'draw'
                              ? t('平局', 'Draw')
                              : t('系统取消', 'System cancelled')}
                        </h3>
                        <strong>
                          {item.scores[0]} : {item.scores[1]}
                        </strong>
                        {item.winner !== null && (
                          <span>
                            {' '}
                            ·{' '}
                            {text('admin.gameHistory.winner', {
                              seat: text(
                                item.winner === 0
                                  ? 'admin.gameHistory.firstPlayer'
                                  : 'admin.gameHistory.secondPlayer',
                              ),
                            })}
                          </span>
                        )}
                      </div>
                      <p>
                        {t('规则版本', 'Rules version')} {item.rules_version} · {t('票价', 'Entry')}{' '}
                        {item.ticket} · {t('奖金', 'Prize')} {item.prize}
                      </p>
                      {item.recent && (
                        <>
                          <p>{formatDateTime(item.recent.terminal_at)}</p>
                          <p>
                            {item.recent.participants.map((p, index) => (
                              <span key={index}>
                                {index > 0 ? ' / ' : ''}
                                {p.kind === 'bot' ? (
                                  `${p.display_name} · AI`
                                ) : p.user_id === null ? (
                                  t('账号已删除', 'Deleted account')
                                ) : (
                                  <>
                                    {p.display_name}{' '}
                                    <span title={text('common.userId')}>({p.user_id})</span>
                                  </>
                                )}
                              </span>
                            ))}
                          </p>
                        </>
                      )}
                      {dataset === 'anonymous' ? (
                        <p className="admin-duel-id">
                          {t('匿名资料编号', 'Anonymous record ID')}: {item.match_ref}
                        </p>
                      ) : null}
                      <button
                        className="nb-btn nb-btn--secondary"
                        type="button"
                        onClick={() => setSelected(item.match_ref)}
                      >
                        {t('查看完整过程', 'View complete record')}
                      </button>
                    </article>
                  ))}
                </div>
              )}
              <SimplePager
                page={cursors.length}
                hasMore={Boolean(query.data.next_cursor)}
                onPrev={() => setCursors(cursors.slice(0, -1))}
                onNext={() => setCursors([...cursors, query.data.next_cursor])}
                labels={{ previous: t('上一页', 'Previous page'), next: t('下一页', 'Next page') }}
              />
            </>
          )
        )}
      </Card>
      {selected && (
        <HistoryDetails
          game={game}
          dataset={dataset}
          id={selected}
          onClose={() => setSelected(null)}
        />
      )}
    </>
  );
}
export function DuelHistoryPage() {
  const siteOffset = useSiteTimeOffset();
  const t = useGameAdminText(),
    [draft, setDraft] = useState(initialFilter),
    [applied, setApplied] = useState({
      game: initialFilter.game,
      dataset: initialFilter.dataset,
      selection: {} as Selection,
    }),
    [error, setError] = useState(false);
  const edit = (patch: Partial<Filter>) => {
    setDraft({ ...draft, ...patch });
    setError(false);
  };
  const submit = (event: FormEvent) => {
    event.preventDefault();
    try {
      setApplied({
        game: draft.game,
        dataset: draft.dataset,
        selection: selectionFor(draft, siteOffset),
      });
      setError(false);
    } catch {
      setError(true);
    }
  };
  return (
    <div className="page ops-page admin-duel-page">
      <PageHeader
        back={<Link to="/games">{t('返回游戏配置', 'Back to game settings')}</Link>}
        title={t('对战历史与导出', 'Match history and exports')}
        description={t(
          '完整过程保留30天；到期后只保留移除身份与原始时间的匿名资料。',
          'Complete records remain available for 30 days, then become anonymous archives without identities or original timestamps.',
        )}
      />
      <Card>
        <form onSubmit={submit} className="ops-stack">
          <div className="ops-field-grid">
            <label>
              <span>{t('游戏', 'Game')}</span>
              <select
                value={draft.game}
                onChange={(e) => edit({ game: e.target.value as GameID, mode: '' })}
              >
                {(['bidding', 'likes', 'gwent'] as const).map((game) => (
                  <option key={game} value={game}>
                    {gameLabel(game, t)}
                  </option>
                ))}
              </select>
            </label>
            <label>
              <span>{t('数据集', 'Dataset')}</span>
              <select
                value={draft.dataset}
                onChange={(e) => edit({ dataset: e.target.value as Dataset })}
              >
                <option value="recent">{t('近30天完整历史', 'Recent 30 days')}</option>
                <option value="anonymous">{t('长期匿名资料', 'Anonymous archive')}</option>
              </select>
            </label>
            <label>
              <span>{t('模式', 'Mode')}</span>
              <select value={draft.mode} onChange={(e) => edit({ mode: e.target.value })}>
                <option value="">{t('全部模式', 'All modes')}</option>
                {modesFor(draft.game).map((mode) => (
                  <option key={mode} value={mode}>
                    {modeLabel(mode, t)}
                  </option>
                ))}
              </select>
            </label>
            <label>
              <span>{t('规则版本（留空为全部）', 'Rules version (blank for all)')}</span>
              <input
                type="text"
                inputMode="numeric"
                value={draft.version}
                maxLength={10}
                onChange={(e) => edit({ version: e.target.value })}
              />
            </label>
            <label>
              <span>{t('结果', 'Outcome')}</span>
              <select value={draft.outcome} onChange={(e) => edit({ outcome: e.target.value })}>
                <option value="">{t('全部结果', 'All outcomes')}</option>
                <option value="normal">{t('分出胜负', 'Decided')}</option>
                <option value="draw">{t('平局', 'Draw')}</option>
                <option value="system_cancelled">{t('系统取消', 'System cancelled')}</option>
              </select>
            </label>
            <>
              <label>
                <span>{t('结束时间从', 'Ended from')}</span>
                <input
                  type="datetime-local"
                  disabled={siteOffset === null}
                  value={draft.from}
                  onChange={(e) => edit({ from: e.target.value })}
                />
              </label>
              <label>
                <span>{t('结束时间至', 'Ended through')}</span>
                <input
                  type="datetime-local"
                  disabled={siteOffset === null}
                  value={draft.to}
                  onChange={(e) => edit({ to: e.target.value })}
                />
              </label>
            </>
          </div>
          <TimeContextNotice station="admin" />
          {error && (
            <p className="field-error" role="alert">
              {t('请检查版本号和时间范围。', 'Check the version and time range.')}
            </p>
          )}
          <button type="submit" className="nb-btn nb-btn--primary">
            {t('应用筛选', 'Apply filters')}
          </button>
        </form>
      </Card>
      {applied.dataset === 'anonymous' &&
      (applied.selection.from !== undefined || applied.selection.to !== undefined) ? null : (
        <HistoryResults key={JSON.stringify(applied)} {...applied} />
      )}
      <Card>
        <ExportPanel {...applied} />
      </Card>
    </div>
  );
}
