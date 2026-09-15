import { useEffect, useRef, useState } from 'react';
import { Link } from 'react-router';
import { useQuery } from '@tanstack/react-query';
import { Card, ErrorState, LoadingState, PageHeader } from '@shared/components/States';
import { useAdminSession } from '../data';
import { useGameAdminText } from '../features/games/copy';
import {
  exportBlackjack,
  getBlackjackDetail,
  getBlackjackHistory,
  type BlackjackDataset,
} from '../features/games/blackjack';
import { downloadPart } from '../features/games/export';
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
      <button className="btn btn-secondary" onClick={back}>
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
function Export({ dataset }: { readonly dataset: BlackjackDataset }) {
  const t = useGameAdminText();
  const [position, setPosition] = useState<{
    cursor: string | null;
    part: number;
    records: number;
    done: boolean;
  }>({ cursor: null, part: 1, records: 0, done: false });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const controller = useRef<AbortController | null>(null);
  useEffect(() => () => controller.current?.abort(), []);
  const run = async () => {
    if (controller.current || position.done) return;
    const abort = new AbortController();
    controller.current = abort;
    setBusy(true);
    setError(null);
    try {
      const page = await exportBlackjack(dataset, position.cursor, abort.signal);
      abort.signal.throwIfAborted();
      if (page.items.length)
        downloadPart(
          new TextEncoder().encode(
            page.items.map((item) => JSON.stringify(item)).join('\n') + '\n',
          ),
          `blackjack-${dataset}-${position.part}.ndjson`,
        );
      setPosition({
        cursor: page.next_cursor,
        part: position.part + 1,
        records: position.records + page.items.length,
        done: page.next_cursor === null,
      });
    } catch (failure) {
      if (!abort.signal.aborted) setError(failure);
    } finally {
      controller.current = null;
      setBusy(false);
    }
  };
  return (
    <Card>
      <h2>{t('分段导出', 'Export in parts')}</h2>
      <p>
        {t(
          '每次下载最多10局的UTF-8 NDJSON。后续页固定使用首请求的范围，失败时保留当前页，可继续重试。',
          'Each download contains up to ten tables in UTF-8 NDJSON. Later pages keep the first request’s range. A failed download keeps its page for retry.',
        )}
      </p>
      <button
        className="btn btn-secondary"
        disabled={busy || position.done}
        onClick={() => void run()}
      >
        {position.done
          ? t('导出完成', 'Export complete')
          : busy
            ? t('正在读取…', 'Reading…')
            : position.part === 1
              ? t('开始导出', 'Start export')
              : t('下载下一部分', 'Download next part')}
      </button>
      {busy && (
        <button className="btn btn-secondary" onClick={() => controller.current?.abort()}>
          {t('取消', 'Cancel')}
        </button>
      )}
      <p aria-live="polite">
        {t('已导出', 'Exported')} {position.records} {t('局', 'tables')}
      </p>
      {!!error && <ErrorState error={error} />}
    </Card>
  );
}
export function BlackjackHistoryPage() {
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
        title={t('二十一点历史与导出', 'Blackjack history and exports')}
        description={t(
          '近期保留30天；长期匿名资料不含账号、原局编号、绝对时间和付款来源。',
          'Recent history lasts 30 days. Anonymous records omit accounts, original table IDs, absolute times and payment sources.',
        )}
      />
      <Link className="btn btn-secondary" to="/games">
        {t('返回游戏管理', 'Back to game settings')}
      </Link>
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
                    className="btn btn-secondary"
                    key={h.id}
                    onClick={() => setSelected(h.id)}
                  >
                    <span>
                      {h.started_at === null
                        ? h.id
                        : new Date(h.started_at * 1000).toLocaleString()}
                    </span>
                    <span>
                      {h.seats} {t('席', 'seats')} ·{' '}
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
          <div className="ops-actions">
            <button
              className="btn btn-secondary"
              disabled={cursors.length === 1}
              onClick={() => setCursors((v) => v.slice(0, -1))}
            >
              {t('上一页', 'Previous')}
            </button>
            <button
              className="btn btn-secondary"
              disabled={!page.data?.next_cursor}
              onClick={() => {
                if (page.data?.next_cursor) setCursors((v) => [...v, page.data.next_cursor]);
              }}
            >
              {t('下一页', 'Next')}
            </button>
          </div>
        </Card>
      )}
      <Export key={dataset} dataset={dataset} />
    </main>
  );
}
