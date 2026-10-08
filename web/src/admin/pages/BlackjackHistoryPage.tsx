import { SimplePager } from '@shared/operations/SimplePager';
import { useState } from 'react';
import { Link } from 'react-router';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import { Card, ErrorState, LoadingState, PageHeader } from '@shared/components/States';
import { useDateTimeFormatter } from '@shared/utils/datetime';
import { useAdminSession } from '../data';
import { useGameAdminText } from '../features/games/copy';
import {
  getBlackjackDetail,
  getBlackjackHistory,
  type BlackjackDataset,
} from '../features/games/blackjack';
import { ExportPanel } from '../features/games/ExportPanel';
import { BlackjackBoard, BlackjackSettlement } from '../../user/games/blackjack/Table';
import '../../user/games/bidding/bidding.css';
import '../../user/games/blackjack/blackjack.css';
import '@shared/operations/operations.css';

function Detail({
  dataset,
  id,
  back,
}: {
  readonly dataset: BlackjackDataset;
  readonly id: string;
  readonly back: () => void;
}) {
  const t = useGameAdminText();
  const session = useAdminSession();
  const detail = useQuery({
    queryKey: ['admin', session.data?.admin.username, 'blackjack', dataset, id],
    queryFn: ({ signal }) => getBlackjackDetail(dataset, id, signal),
    retry: false,
  });
  return (
    <Card>
      <button className="nb-btn nb-btn--secondary" onClick={back}>
        {t('返回列表', 'Back to list')}
      </button>
      {detail.isPending ? (
        <LoadingState />
      ) : detail.error ? (
        <ErrorState error={detail.error} onRetry={() => void detail.refetch()} />
      ) : (
        <>
          <p>{detail.data.id}</p>
          <div className="bidding-game blackjack-game">
            <BlackjackBoard
              table={{
                id,
                revision: '1',
                started_at: detail.data.recent?.started_at ?? 0,
                phase: detail.data.record.phase,
                deadline: 0,
                next_round_at: 0,
                terminal_at: detail.data.recent?.terminal_at ?? 0,
                reason: detail.data.record.reason,
                fact: detail.data.record.fact,
              }}
              ownSeat={null}
            />
            {detail.data.record.phase === 'result' ? (
              <BlackjackSettlement fact={detail.data.record.fact} seat={null} />
            ) : (
              <p>
                {t(
                  '系统取消，按原积分组成退款。',
                  'System cancelled; original payment assets refunded.',
                )}
              </p>
            )}
          </div>
          {detail.data.recent && (
            <section>
              <h3>{t('近期参与及账务', 'Recent participants and accounting')}</h3>
              {detail.data.recent.participants.map((p) => (
                <details key={p.seat}>
                  <summary>
                    #{p.seat + 1} ·{' '}
                    {p.user_id === null
                      ? t('已去身份', 'Deidentified')
                      : `${t('用户', 'User')} ${p.user_id}`}{' '}
                    · {t('游戏', 'Game')} {p.payment.game} / {t('通用', 'General')}{' '}
                    {p.payment.general}
                  </summary>
                  <ul>
                    {p.operations.map((id) => (
                      <li key={id}>
                        <code>{id}</code>
                      </li>
                    ))}
                  </ul>
                </details>
              ))}
            </section>
          )}
        </>
      )}
    </Card>
  );
}
export function BlackjackHistoryPage() {
  const formatDateTime = useDateTimeFormatter();
  const { t: text } = useTranslation();
  const t = useGameAdminText();
  const session = useAdminSession();
  const [dataset, setDataset] = useState<BlackjackDataset>('recent');
  const [cursors, setCursors] = useState<(string | null)[]>([null]);
  const [selected, setSelected] = useState<string | null>(null);
  const cursor = cursors.at(-1) ?? null;
  const page = useQuery({
    queryKey: ['admin', session.data?.admin.username, 'blackjack', dataset, 'history', cursor],
    queryFn: ({ signal }) => getBlackjackHistory(dataset, cursor, signal),
    retry: false,
  });
  return (
    <main className="page ops-page">
      <PageHeader
        back={<Link to="/games">{t('返回游戏配置', 'Back to game settings')}</Link>}
        title={t('二十一点历史与导出', 'Blackjack history and exports')}
        description={t(
          '近期保留30天；长期匿名资料不含账号、原局编号、绝对时间和付款来源。',
          'Recent history lasts 30 days. Anonymous records omit accounts, original table IDs, absolute times and payment sources.',
        )}
      />
      <Card>
        <label>
          {t('资料范围', 'Dataset')}{' '}
          <select
            value={dataset}
            onChange={(e) => {
              setDataset(e.target.value as BlackjackDataset);
              setCursors([null]);
              setSelected(null);
            }}
          >
            <option value="recent">{t('近期', 'Recent')}</option>
            <option value="anonymous">{t('匿名留存', 'Anonymous')}</option>
          </select>
        </label>
      </Card>
      {selected ? (
        <Detail dataset={dataset} id={selected} back={() => setSelected(null)} />
      ) : (
        <Card>
          {page.isPending ? (
            <LoadingState />
          ) : page.error ? (
            <ErrorState error={page.error} onRetry={() => void page.refetch()} />
          ) : (
            <>
              <div className="bj-history-list">
                {page.data.items.map((h) => (
                  <button
                    className="nb-btn nb-btn--secondary"
                    key={h.id}
                    onClick={() => setSelected(h.id)}
                  >
                    <span>
                      {h.started_at === null
                        ? text('admin.gameHistory.anonymousMatch')
                        : formatDateTime(h.started_at)}
                    </span>
                    <span>
                      {text('admin.gameHistory.players', { count: h.seats })} ·{' '}
                      {h.phase === 'cancelled'
                        ? t('已取消', 'Cancelled')
                        : `${t('到账', 'Paid')} ${h.net}`}
                    </span>
                  </button>
                ))}
              </div>
              {!page.data.items.length && <p>{t('暂无记录。', 'No records.')}</p>}
            </>
          )}
          <SimplePager
            page={cursors.length}
            hasMore={Boolean(page.data?.next_cursor)}
            onPrev={() => setCursors((v) => v.slice(0, -1))}
            onNext={() => {
              if (page.data?.next_cursor) setCursors((v) => [...v, page.data.next_cursor]);
            }}
            labels={{ previous: t('上一页', 'Previous'), next: t('下一页', 'Next') }}
          />
        </Card>
      )}
      <Card>
        <ExportPanel game="blackjack" dataset={dataset} />
      </Card>
    </main>
  );
}
