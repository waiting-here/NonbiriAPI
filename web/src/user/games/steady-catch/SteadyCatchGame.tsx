import { useCallback, useEffect, useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from 'react-router';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { ErrorState, LoadingState } from '@shared/components/States';
import { GameWallets } from '../common/GameWallets';
import { GameHero } from '../assets/GameHero';
import { createIdempotencyKey, gameRequest } from '../common/request';
import { useGamesSnapshot, gameKeys } from '../common/snapshot';
import { HZ, LAST_TICK, type Phrase } from './engine';
import { CatchSession, type Controls, type Session } from './session';
import { CatchBoard } from './Board';
import { CatchLeaderboard } from './Leaderboard';
import { useCatchText } from './copy';
import '../games.css';
import './catch.css';

const sessionKey = ['user', 'games', 'steadycatch', 'session'] as const;
const send = async (id: string, json: Controls) =>
  (
    await gameRequest<Session>('/api/games/steady-catch/sessions/' + id + '/controls', {
      method: 'POST',
      json,
    })
  ).data;

function Round({
  initial,
  phrases,
  reload,
  refresh,
}: {
  initial: Session;
  phrases: readonly Phrase[];
  reload: () => void;
  refresh: () => void;
}) {
  const t = useCatchText();
  const [session] = useState(() => new CatchSession(initial, phrases, send));
  const [, update] = useState(0);
  const [confirm, setConfirm] = useState(false);
  const field = useRef<HTMLElement>(null);
  useEffect(() => {
    let wasTerminal = session.terminal;
    const unsubscribe = session.subscribe(() => {
      update((v) => v + 1);
      if (!wasTerminal && session.terminal) {
        wasTerminal = true;
        refresh();
      }
    });
    if (session.authority.status === 'playing') void session.pause();
    const hide = () => {
      if (document.hidden) void session.pause();
    };
    document.addEventListener('visibilitychange', hide);
    if (!session.terminal) field.current?.scrollIntoView({ block: 'start' });
    return () => {
      unsubscribe();
      document.removeEventListener('visibilitychange', hide);
      void session.pause();
    };
  }, [session, refresh]);
  const state = session.state,
    result = session.authority;
  const title =
    result.status === 'completed'
      ? t('稳稳地接住了！', 'Well caught!')
      : result.status === 'failed'
        ? t('下次再接再厉', 'Catch you next time')
        : result.status === 'cancelled'
          ? t('对局已中止，门票已退还', 'Game cancelled; entry refunded')
          : t('本局已结束', 'Game ended');
  return (
    <section className="catch-round" ref={field} aria-label={t('当前对局', 'Current game')}>
      <div className="catch-hud">
        <div>
          <span>{t('得分', 'Score')}</span>
          <strong>
            {state.score}
            <small> / 600</small>
          </strong>
        </div>
        <div>
          <span>{t('剩余时间', 'Time left')}</span>
          <strong>{Math.ceil((LAST_TICK - state.tick) / HZ)}s</strong>
        </div>
        <div>
          <span>{t('生命', 'Lives')}</span>
          <strong aria-label={String(state.hp)}>
            {'♥'.repeat(state.hp)}
            <span className="catch-empty-heart">{'♡'.repeat(5 - state.hp)}</span>
          </strong>
        </div>
        <div>
          <span>{t('连击', 'Combo')}</span>
          <strong>{state.combo}</strong>
        </div>
      </div>
      <div className="catch-stage">
        <CatchBoard session={session} phrases={phrases} />
        {(!session.active || !!session.error) && (
          <div className="catch-overlay">
            <div className="catch-overlay__card" role="status">
              <h2>
                {session.terminal
                  ? title
                  : session.error
                    ? t('连接待恢复', 'Connection interrupted')
                    : session.busy
                      ? t('正在保存', 'Saving')
                      : state.cause
                        ? t('正在确认成绩', 'Confirming result')
                        : t('准备好接住了吗？', 'Ready to catch?')}
              </h2>
              {session.terminal ? (
                <>
                  <p>
                    {t('得分', 'Score')} <b>{state.score}</b> · {t('最高连击', 'Best combo')}{' '}
                    {state.max_combo}
                  </p>
                  {result.first_clear && (
                    <p>
                      {t('首次通关！获得游戏积分：', 'First clear! Game credits awarded: ')}
                      {result.reward}
                    </p>
                  )}
                </>
              ) : session.error ? (
                <p>
                  {t(
                    '已保留待确认的操作。恢复后从保存进度继续。',
                    'Your pending controls are retained. Continue from the saved checkpoint after reconnecting.',
                  )}
                </p>
              ) : (
                <p>
                  {t(
                    '接住梗卡，躲开红色故障。坚持 90 秒、保有生命且达到 600 分即通关。',
                    'Catch phrase cards and avoid red hazards. Survive 90 seconds with 600 points to clear.',
                  )}
                </p>
              )}
              <div className="catch-actions">
                {session.error ? (
                  <>
                    <button
                      className="btn btn-primary"
                      disabled={session.busy}
                      onClick={() => void session.retry()}
                    >
                      {t('重试保存', 'Retry save')}
                    </button>
                    <button className="btn btn-secondary" disabled={session.busy} onClick={reload}>
                      {t('读取已存进度', 'Reload saved game')}
                    </button>
                  </>
                ) : (
                  !session.terminal &&
                  !state.cause && (
                    <button
                      className="btn btn-primary"
                      disabled={session.busy}
                      onClick={() => {
                        void session
                          .resume()
                          .then(() =>
                            field.current?.querySelector('canvas')?.focus({ preventScroll: true }),
                          );
                      }}
                    >
                      {t('开始 / 继续', 'Play / resume')}
                    </button>
                  )
                )}
              </div>
            </div>
          </div>
        )}
      </div>
      <div className="catch-controls">
        <button
          className="btn btn-secondary"
          disabled={!session.active}
          onClick={() => void session.pause()}
        >
          {t('暂停', 'Pause')}
        </button>
        <button
          className="btn btn-primary"
          disabled={!session.active || state.charge < 10}
          onClick={() => session.shield()}
        >
          {t('护盾', 'Shield')} {state.charge}/10
        </button>
        {!session.terminal && (
          <button
            className="btn btn-secondary"
            disabled={session.busy || !!session.error}
            onClick={() => {
              void session.pause();
              setConfirm(true);
            }}
          >
            {t('放弃', 'Abandon')}
          </button>
        )}
        <span>{t('方向键 / A D · 拖动 · 空格护盾', 'Arrows / A D · Drag · Space for shield')}</span>
      </div>
      <ConfirmDialog
        open={confirm}
        title={t('放弃本局？', 'Abandon this game?')}
        description={t(
          '门票不退还，本局不进入排行榜。',
          'The entry is not refunded and this game will not enter the leaderboard.',
        )}
        confirmLabel={t('放弃', 'Abandon')}
        busy={session.busy}
        onCancel={() => setConfirm(false)}
        onConfirm={() => {
          setConfirm(false);
          void session.abandon();
        }}
      />
    </section>
  );
}
export function SteadyCatchGame() {
  const t = useCatchText();
  const cache = useQueryClient();
  const snapshot = useGamesSnapshot();
  const catalog = useQuery({
    queryKey: ['user', 'games', 'steadycatch', 'catalog'],
    queryFn: async ({ signal }) =>
      (await gameRequest<Phrase[]>('/api/games/steady-catch/catalog', { signal })).data,
    staleTime: Infinity,
  });
  const current = useQuery({
    queryKey: sessionKey,
    queryFn: async ({ signal }) =>
      (await gameRequest<Session | null>('/api/games/steady-catch/session', { signal })).data,
    retry: false,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    staleTime: Infinity,
  });
  const [error, setError] = useState<unknown>(null),
    [busy, setBusy] = useState(false),
    [generation, setGeneration] = useState(0);
  const startKey = useRef<string | null>(null);
  const refetch = current.refetch;
  const notify = useCallback(() => {
    void cache.invalidateQueries({ queryKey: gameKeys.snapshot });
    void cache.invalidateQueries({ queryKey: ['user', 'games', 'steadycatch', 'leaderboard'] });
    void refetch();
  }, [cache, refetch]);
  const start = async () => {
    if (busy) return;
    setBusy(true);
    setError(null);
    startKey.current ??= createIdempotencyKey();
    try {
      const value = (
        await gameRequest<Session>('/api/games/steady-catch/sessions', {
          method: 'POST',
          json: {},
          idempotencyKey: startKey.current,
        })
      ).data;
      cache.setQueryData(sessionKey, value);
      startKey.current = null;
      setGeneration((v) => v + 1);
      void cache.invalidateQueries({ queryKey: gameKeys.snapshot });
    } catch (error) {
      setError(error);
    } finally {
      setBusy(false);
    }
  };
  const settings = snapshot.data?.steadycatch;
  const enabled = snapshot.data?.gamesEnabled && settings?.enabled && settings.available;
  const active = !!current.data && current.data.terminal_at === null;
  const reload = () => {
    void current.refetch().then((result) => {
      if (result.data) setGeneration((v) => v + 1);
    });
  };
  return (
    <main className="game-page catch-game">
      <header className="catch-page-header">
        <div>
          <Link to="/games">{t('小游戏', 'Minigames')}</Link>
          <h1>{t('稳稳地接住你', 'Catch You Steadily')}</h1>
          <p>{t('接住每一个梗，也接住每一个你。', 'Catch the memes. Catch the moment.')}</p>
        </div>
        {snapshot.data && <GameWallets wallets={snapshot.data} />}
      </header>
      {(catalog.isPending || current.isPending || snapshot.isPending) && <LoadingState />}
      {[catalog.error, current.error, snapshot.error, error].filter(Boolean).map((err, index) => (
        <ErrorState key={index} error={err} />
      ))}
      {current.data && catalog.data && (
        <Round
          key={current.data.id + ':' + generation}
          initial={current.data}
          phrases={catalog.data}
          reload={reload}
          refresh={notify}
        />
      )}
      {!active && settings && (
        <section className="catch-entry">
          {!current.data && <GameHero kind="steadycatch" />}
          <div>
            <h2>{t('今天的梗，由你接住', 'Your daily catch of memes')}</h2>
            <p>
              {t('门票', 'Entry')}: {settings.price} ·{' '}
              {settings.firstCleared
                ? t('已完成首通', 'First clear completed')
                : t('首通游戏积分', 'First-clear game credits') + ': ' + settings.firstClearReward}
            </p>
            <button
              className="btn btn-primary"
              disabled={!enabled || busy || !catalog.data}
              onClick={() => void start()}
            >
              {busy
                ? t('正在开局', 'Starting')
                : enabled
                  ? t('开始新一局', 'Start a new game')
                  : t('暂未开放', 'Currently closed')}
            </button>
          </div>
        </section>
      )}
      <details className="catch-help">
        <summary>{t('怎么玩与积分说明', 'How to play and credits')}</summary>
        <p>
          {t(
            '鼠标、触屏拖动或左右方向键移动。普通梗卡得 10 分，金色梗卡 20 分；连续接住 5 / 10 张后得分变为 2 / 3 倍。错过梗卡或受伤会中断连击。',
            'Move with the mouse, touch, or arrow keys. Phrase cards give 10 points, gold cards 20. At 5 / 10 consecutive catches, scoring becomes 2× / 3×. Missing a card or taking damage resets the combo.',
          )}
        </p>
        <p>
          {t(
            '蓝色道具提供护盾、减速、吸梗、双倍得分或回复生命。接满 10 张可主动释放 4 秒护盾。',
            'Blue power-ups offer a shield, slow falling, a magnet, double points or healing. Every 10 catches charge a four-second shield.',
          )}
        </p>
        <p>
          {t(
            '入场先扣游戏积分，不足部分扣通用积分；仅首次通关发放奖励。正常结束和放弃不退票，系统中止原路退票。暂停与离线期间不推进游戏，本局从创建起保留 30 分钟。',
            'Entry uses game credits first, then general credits. Only the first clear gives a reward. Finished or abandoned games do not refund entry; system cancellation refunds the original payment. Pausing or going offline stops play; a session expires 30 minutes after creation.',
          )}
        </p>
      </details>
      <CatchLeaderboard />
    </main>
  );
}
