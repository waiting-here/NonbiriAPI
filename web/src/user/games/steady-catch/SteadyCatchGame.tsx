import { useCallback, useEffect, useRef, useState, type RefObject } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { ErrorState, LoadingState } from '@shared/components/States';
import { Icon, type IconName } from '@shared/components/Icon';
import { GameWallets } from '../common/GameWallets';
import { GameBackLink } from '../common/GameBackLink';
import { GameToolbar } from '../common/GameToolbar';
import { formatCredits } from '../common/strict';
import { createIdempotencyKey, gameRequest } from '../common/request';
import { useGamesSnapshot, gameKeys } from '../common/snapshot';
import { HZ, LAST_TICK, newGame, type Phrase } from './engine';
import { CatchSession, type Controls, type Session } from './session';
import { CatchBoard, type CatchFeedback } from './Board';
import { CatchAudio } from './audio';
import { CatchResult } from './CatchResult';
import { CatchLeaderboard } from './Leaderboard';
import { CatchCatalog, CatchDialog } from './CatchDialogs';
import { CatchHelp } from './CatchHelp';
import { useCatchText } from './copy';
import '../games.css';
import './catch.css';
import './polish.css';

const sessionKey = ['user', 'games', 'steadycatch', 'session'] as const;
const send = async (id: string, json: Controls) =>
  (
    await gameRequest<Session>('/api/games/steady-catch/sessions/' + id + '/controls', {
      method: 'POST',
      json,
    })
  ).data;
interface Entry {
  price: string;
  firstCleared: boolean;
  firstClearReward: string;
}
const idle = newGame(1);
type AssetStatus = 'loading' | 'ready' | 'error';

function EntryTerms({ entry, playing }: { entry: Entry; playing: boolean }) {
  const t = useCatchText();
  const [expanded, setExpanded] = useState(() => {
    if (!entry.firstCleared && Number(entry.firstClearReward) > 0) return true;
    try {
      return sessionStorage.getItem('nb.catch.reward-notice') !== 'seen';
    } catch {
      return true;
    }
  });
  useEffect(() => {
    try {
      sessionStorage.setItem('nb.catch.reward-notice', 'seen');
    } catch {
      // The disclosure remains usable without browser storage.
    }
  }, []);
  const [wasPlaying, setWasPlaying] = useState(playing);
  if (playing !== wasPlaying) {
    setWasPlaying(playing);
    if (playing) setExpanded(false);
  }
  return (
    <details
      className="entry-terms"
      open={expanded}
      onToggle={(event) => setExpanded(event.currentTarget.open)}
    >
      <summary>
        {t('门票 ', 'Entry ')}
        {formatCredits(entry.price)}
        {t(' 积分 · ', ' credits · ')}
        {entry.firstCleared
          ? t('已首通，本局不发积分', 'Already cleared; no credits this game')
          : Number(entry.firstClearReward) > 0
            ? t('首通可领 ', 'First-clear reward: ') + formatCredits(entry.firstClearReward)
            : t('本局不发积分', 'No credits this game')}
      </summary>
      <p className="reward-notice">
        <strong>{t('没有逐局积分奖励', 'No per-game credit rewards')}</strong>
        <span>
          {entry.firstCleared
            ? t(
                '已领过首通奖励，本局不再发放积分。',
                'First clear already completed; this game awards no credits.',
              )
            : t('仅首次通关可领取游戏积分：', 'Only the first clear awards game credits: ') +
              formatCredits(entry.firstClearReward)}{' '}
          {t('局内分数不等于钱包积分。', 'Game score is not wallet credit.')}
        </span>
      </p>
    </details>
  );
}

function usePresentation() {
  const [asset, setAsset] = useState<AssetStatus>('loading');
  const [coarse] = useState(() => matchMedia('(pointer: coarse)').matches);
  useEffect(() => {
    const image = new Image();
    image.onload = () => setAsset('ready');
    image.onerror = () => setAsset('error');
    image.src = '/assets/steady-catch/player.webp';
    return () => {
      image.onload = image.onerror = null;
    };
  }, []);
  return { asset, coarse };
}

function Round({
  initial,
  phrases,
  autoStart,
  controllerRef,
  collectionRef,
  audio,
  reload,
  refresh,
  start,
  enabled,
  busy,
  entry,
  openCatalog,
  openHelp,
  asset,
  coarse,
}: {
  initial: Session | null;
  phrases: readonly Phrase[];
  autoStart: boolean;
  controllerRef: RefObject<CatchSession | null>;
  collectionRef: RefObject<CatchFeedback[]>;
  audio: CatchAudio;
  reload: () => void;
  refresh: () => void;
  start: () => void;
  enabled: boolean;
  busy: boolean;
  entry: Entry | undefined;
  openCatalog: () => void;
  openHelp: () => void;
  asset: AssetStatus;
  coarse: boolean;
}) {
  const t = useCatchText();
  const [session] = useState(() => (initial ? new CatchSession(initial, phrases, send) : null));
  const [, update] = useState(0),
    [confirm, setConfirm] = useState(false);
  const [recent, setRecent] = useState<CatchFeedback[]>([]);
  const field = useRef<HTMLElement>(null);
  const onCatch = useCallback(
    (item: CatchFeedback) => {
      collectionRef.current = [item, ...collectionRef.current];
      setRecent(collectionRef.current);
    },
    [collectionRef],
  );
  useEffect(() => {
    collectionRef.current = [];
    controllerRef.current = session;
    if (!session) return;
    let terminal = session.terminal;
    const unsubscribe = session.subscribe(() => {
      update((v) => v + 1);
      if (!terminal && session.terminal) {
        terminal = true;
        refresh();
      }
    });
    let mounted = true;
    queueMicrotask(() => {
      if (!mounted) return;
      if (session.authority.status === 'playing') void session.pause();
      else if (autoStart && !session.terminal)
        void session.beginCountdown().then(() => {
          if (session.active)
            field.current?.querySelector('canvas')?.focus({ preventScroll: true });
        });
    });
    const hide = () => {
      if (document.hidden) void session.pause();
    };
    const blur = () => void session.pause();
    document.addEventListener('visibilitychange', hide);
    window.addEventListener('blur', blur);
    return () => {
      mounted = false;
      unsubscribe();
      controllerRef.current = null;
      document.removeEventListener('visibilitychange', hide);
      window.removeEventListener('blur', blur);
      void session.pause();
    };
  }, [session, refresh, autoStart, controllerRef, collectionRef]);
  const state = session?.state ?? idle,
    result = session?.authority;
  const terminal = !!session?.terminal,
    active = !!session?.active,
    counting = session?.countdown != null;
  const title =
    result?.status === 'completed'
      ? t('这次，真的接住了', 'This time, you caught it')
      : result?.status === 'cancelled'
        ? t('本局已中止，门票已退还', 'Game cancelled; entry refunded')
        : result?.status === 'abandoned'
          ? t('本局已放弃', 'Game abandoned')
          : state.cause === 'hp'
            ? t('耐心暂时用完了', 'Patience ran out')
            : t('再接几句就过关了', 'A few more catches next time');
  const resume = () =>
    asset === 'ready' &&
    void session?.beginCountdown(2).then(() => {
      if (session.active) field.current?.querySelector('canvas')?.focus({ preventScroll: true });
    });
  const best = useQuery({
    queryKey: ['user', 'games', 'steadycatch', 'leaderboard', '30d'],
    queryFn: async ({ signal }) =>
      (
        await gameRequest<{
          rows: { score: number; is_me: boolean }[];
          me: { score: number } | null;
        }>('/api/games/steady-catch/leaderboard?window=30d', { signal })
      ).data,
    staleTime: 30000,
  });
  const bestScore = best.data?.me?.score ?? best.data?.rows.find((row) => row.is_me)?.score;
  const [previousBest, setPreviousBest] = useState<number | null>(null);
  if (!terminal && previousBest === null && best.data) setPreviousBest(bestScore ?? 0);
  return (
    <>
      {entry && <EntryTerms entry={entry} playing={active || counting} />}
      <div className="layout">
        <section className="game-column" ref={field} aria-label={t('接物游戏', 'Catch game')}>
          <div className="hud">
            <div className="score-stat" aria-label={t('本局分数', 'Game score')}>
              <span className="stat-label">{t('本局分数', 'Game score')}</span>
              <strong>{state.score}</strong>
              <span className="goal">/ 600</span>
            </div>
            <div className="time-stat" aria-label={t('剩余时间', 'Time left')}>
              <span className="stat-label">{t('剩余时间', 'Time left')}</span>
              <strong>
                {Math.ceil((LAST_TICK - state.tick) / HZ)}
                <span>s</span>
              </strong>
            </div>
            <div className="life-stat">
              <span className="stat-label">{t('耐心值', 'Patience')}</span>
              <span
                className="catch-lives"
                aria-label={t('剩余耐心：', 'Health remaining: ') + state.hp}
              >
                {Array.from({ length: 5 }, (_, i) => (
                  <span className={i >= state.hp ? 'lost' : undefined} key={i}>
                    {i ? ' ♥' : '♥'}
                  </span>
                ))}
              </span>
            </div>
            <button
              className="icon-button pause-button"
              disabled={
                !session ||
                terminal ||
                (!active && session.busy) ||
                !!session.error ||
                asset !== 'ready'
              }
              aria-label={
                active || counting ? t('暂停游戏', 'Pause game') : t('继续游戏', 'Resume game')
              }
              onClick={() => (active || counting ? void session?.pause() : resume())}
            >
              <Icon name={!session || active || counting ? 'pause' : 'play'} />
            </button>
          </div>
          <div className="stage">
            <CatchBoard
              session={asset === 'ready' ? session : null}
              phrases={phrases}
              audio={audio}
              onCatch={onCatch}
            />
            <div className="stage-top">
              <span>
                {
                  [
                    t('第一幕 · 温柔的开场', 'Act I · A gentle opening'),
                    t('第二幕 · 不是雨，是八股', 'Act II · Raining phrases'),
                    t('第三幕 · 极其极其极其', 'Act III · Extremely extreme'),
                  ][Math.min(2, Math.floor(state.tick / HZ / 30))]
                }
              </span>
              <span className="combo">
                {!session
                  ? t('接物机待命', 'Ready to catch')
                  : state.combo
                    ? state.combo +
                      t(' 连击 · ×', ' combo · ×') +
                      (1 + Math.min(2, Math.floor(state.combo / 5)))
                    : t('连击从下一句开始', 'A new combo starts next')}
              </span>
            </div>
            <div className="effect-bar" aria-label={t('生效中的道具', 'Active power-ups')}>
              {(['shield', 'slow', 'magnet', 'double'] as const)
                .filter((key) => state.effects[key] > state.tick)
                .map((key, index) => (
                  <span className="effect-pill" key={key}>
                    {
                      [
                        t('护盾', 'Shield'),
                        t('降速', 'Slow'),
                        t('磁吸', 'Magnet'),
                        t('双倍', 'Double'),
                      ][(['shield', 'slow', 'magnet', 'double'] as const).indexOf(key)]
                    }{' '}
                    {Math.ceil((state.effects[key] - state.tick) / HZ)}s
                    <span className="sr-only">{index + 1}</span>
                  </span>
                ))}
            </div>
            {counting && (
              <div key={session?.countdown} className="catch-countdown" aria-live="assertive">
                {session?.countdown === 0 ? t('接！', 'Go!') : session?.countdown}
              </div>
            )}
            {!counting && (!active || !!session?.error) && (
              <div className={'overlay' + (session ? ' centered' : '')}>
                {!session ? (
                  <div className="start-panel">
                    <span className="eyebrow">
                      {t('今天的八股，真的会掉下来。', 'Today, the phrases really are falling.')}
                    </span>
                    <h1>
                      {t('稳稳地', 'Catch you')}
                      <br />
                      <span>{t('接住你', 'steadily')}</span>
                      <i aria-hidden="true">
                        <Icon name="spark" />
                      </i>
                    </h1>
                    <p className="intro">
                      {t('接住那些熟悉的句子。', 'Catch those familiar phrases.')}
                      <br />
                      {t('红色错误卡，请让它落地。', 'Let the red error cards fall.')}
                    </p>
                    <div className="start-rules">
                      <span>
                        <b>{t('90 秒', '90 seconds')}</b>
                        {t('接物挑战', 'Catch challenge')}
                      </span>
                      <span>
                        <b>{t('600 分', '600 points')}</b>
                        {t('过关目标', 'Clear target')}
                      </span>
                    </div>
                    <button
                      className="primary"
                      disabled={!enabled || busy || asset !== 'ready'}
                      onClick={start}
                    >
                      {asset === 'loading'
                        ? t('接物机准备中…', 'Preparing the catcher…')
                        : asset === 'error'
                          ? t('角色未能载入', 'Character could not load')
                          : busy
                            ? t('正在开局', 'Starting')
                            : enabled
                              ? t('开始接住', 'Start catching')
                              : t('暂未开放', 'Currently closed')}
                    </button>
                    <p className="input-hint">
                      {asset === 'error'
                        ? t('请刷新页面后再试。', 'Refresh the page and try again.')
                        : coarse
                          ? t(
                              '按住场内左右滑动 · 或长按方向按钮',
                              'Drag across the field · Or hold a direction button',
                            )
                          : t('鼠标移动 · 左右方向键 / A D', 'Mouse · Arrow keys / A D')}
                    </p>
                  </div>
                ) : (
                  <div className="end-panel" role="status">
                    <span className="end-symbol">
                      <Icon
                        name={
                          terminal ? (result?.status === 'completed' ? 'spark' : 'shield') : 'pause'
                        }
                      />
                    </span>
                    <h2>
                      {terminal
                        ? title
                        : session.error
                          ? t('连接待恢复', 'Connection interrupted')
                          : state.cause
                            ? t('正在确认成绩', 'Confirming result')
                            : t('先缓一缓', 'Take a breath')}
                    </h2>
                    {terminal ? (
                      <CatchResult
                        result={result!}
                        previousBest={previousBest}
                        caught={recent}
                        disabled={!enabled || busy || asset !== 'ready'}
                        busy={busy}
                        start={start}
                        openCatalog={openCatalog}
                      />
                    ) : session.error ? (
                      <>
                        <p>
                          {t(
                            '已保留待确认的操作。恢复后从保存进度继续。',
                            'Pending controls are retained. Continue from the saved checkpoint after reconnecting.',
                          )}
                        </p>
                        <ErrorState error={session.error} />
                        <button
                          className="primary"
                          disabled={session.busy}
                          onClick={() => void session.retry()}
                        >
                          {t('重试保存', 'Retry save')}
                        </button>
                        <button className="secondary" disabled={session.busy} onClick={reload}>
                          {t('读取已存进度', 'Reload saved game')}
                        </button>
                      </>
                    ) : (
                      <>
                        <p>{t('八股文已在半空待命。', 'The phrases are waiting mid-air.')}</p>
                        <p className="paused-detail">
                          {session.busy
                            ? t('正在保存，请稍候。', 'Saving, please wait.')
                            : state.cause
                              ? t('正在保存最终成绩。', 'Saving the final result.')
                              : t(
                                  '时间、道具和掉落物都已暂停。',
                                  'Time, power-ups and cards are paused.',
                                )}
                        </p>
                        {!state.cause && (
                          <>
                            <button
                              className="primary"
                              disabled={session.busy || asset !== 'ready'}
                              onClick={resume}
                            >
                              {t('继续接住', 'Keep catching')}
                            </button>
                            <button
                              className="secondary"
                              disabled={session.busy}
                              onClick={() => setConfirm(true)}
                            >
                              {t('重新开一局', 'Start a new game')}
                            </button>
                          </>
                        )}
                      </>
                    )}
                  </div>
                )}
              </div>
            )}
          </div>
          <div className="control-strip">
            <div className="move-controls">
              {([-1, 1] as const).map((dir) => (
                <button
                  className="move-button"
                  key={dir}
                  disabled={!active && !counting}
                  aria-label={dir < 0 ? t('向左移动', 'Move left') : t('向右移动', 'Move right')}
                  onPointerDown={(e) => {
                    e.currentTarget.setPointerCapture(e.pointerId);
                    session?.move(dir);
                  }}
                  onPointerUp={() => session?.move(0)}
                  onPointerCancel={() => session?.move(0)}
                  onLostPointerCapture={() => session?.move(0)}
                >
                  <Icon name="arrow-left" className={dir > 0 ? 'move-right' : undefined} />
                </button>
              ))}
            </div>
            <div className="control-hint">
              <b>
                {coarse
                  ? t('按住场内，左右滑动', 'Hold and drag across the field')
                  : t('鼠标移动，稳稳接住', 'Move the mouse, catch steadily')}
              </b>
              <span>
                {coarse
                  ? t('也可长按下方的方向按钮', 'Or hold a direction button below')
                  : t('也可使用 ← → / A D · P 暂停', 'Or ← → / A D · P to pause')}
              </span>
            </div>
            <button
              className={'ability' + (active && state.charge >= 10 ? ' ready' : '')}
              disabled={!active || state.charge < 10}
              onClick={() => session?.shield()}
            >
              <span className="ability-symbol">
                <Icon name="shield" />
              </span>
              <span>
                <b>{t('稳稳护场', 'Steady shield')}</b>
                <small>
                  {state.charge >= 10
                    ? coarse
                      ? t('已充满 · 点按释放', 'Ready · Tap to release')
                      : t('已充满 · 空格释放', 'Ready · Press Space')
                    : state.charge +
                      (coarse
                        ? t(' / 10 句 · 充满后点按', ' / 10 catches · Tap when charged')
                        : t(' / 10 句 · 空格释放', ' / 10 catches · Space'))}
                </small>
              </span>
              <span
                className="charge-meter"
                style={{ width: Math.min(100, state.charge * 10) + '%' }}
              />
            </button>
          </div>
          <p className="below-note">
            <Icon name="spark" />
            {t(
              '漏接不扣耐心，只中断连击。坚持 90 秒并达到 600 分就过关。',
              'Misses break combos without costing health. Last 90 seconds and reach 600 points to clear.',
            )}
          </p>
        </section>
        <aside className="side-column">
          <section className="quick-guide">
            <div className="side-title">
              <h2>{t('接物速查', 'Quick guide')}</h2>
              <span>{t('看图标，也看颜色', 'Icons and colors')}</span>
            </div>
            {[
              [
                'normal',
                'book',
                t('白色八股卡', 'White phrase'),
                t('接住 +10 分，连击越高越赚', 'Catch for +10; combos multiply'),
              ],
              [
                'gold',
                'spark',
                t('金色名场面', 'Golden moment'),
                t('接住 +20 分，同样累计连击', 'Catch for +20; keeps the combo'),
              ],
              [
                'danger',
                'warning',
                t('红色错误卡', 'Red error'),
                t('躲开！接到扣 1 点耐心', 'Avoid! Costs 1 health'),
              ],
              [
                'power',
                'shield',
                t('绿色道具卡', 'Green power-up'),
                t('护盾、降速、磁吸、加分、回血', 'Shield, slow, magnet, score, heal'),
              ],
            ].map(([style, icon, title, description]) => (
              <div className="guide-row" key={style}>
                <span className={'mini-card ' + style}>
                  <Icon name={icon as IconName} />
                </span>
                <div>
                  <b>{title}</b>
                  <p>{description}</p>
                </div>
              </div>
            ))}
            <p className="prop-note">
              {t('报错与道具的名称均为游戏梗。', 'Error and power-up names are game jokes.')}
            </p>
          </section>
          <section className="recent-box">
            <div className="side-title">
              <h2>{t('刚刚接住', 'Just caught')}</h2>
              <span>
                {state.caught} {t('句', 'phrases')}
              </span>
            </div>
            <ul>
              {recent.length ? (
                recent.slice(0, 3).map((item, i) => (
                  <li key={i}>
                    <span>{item.text}</span>
                    <b>+{item.points}</b>
                  </li>
                ))
              ) : (
                <li className="empty-recent">
                  {t('“极其”正在来路上。', '“Extremely” is on its way.')}
                </li>
              )}
            </ul>
          </section>
          <section className="record-box">
            <span>{t('近 30 天最佳', 'Best in 30 days')}</span>
            <strong>{bestScore ?? '—'}</strong>
            <span>{t('分', 'points')}</span>
            <p>
              {t(
                '从一句八股开始，接出自己的名场面。',
                'Start with one phrase. Catch your own golden moment.',
              )}
            </p>
          </section>
          <button className="help-button" onClick={openHelp}>
            {t('玩法与道具说明', 'How to play and power-ups')} <Icon name="help" />
          </button>
        </aside>
        <CatchDialog
          open={confirm}
          onClose={() => setConfirm(false)}
          className="help-dialog"
          title={t('重新开一局？', 'Start a new game?')}
        >
          <p>
            {t(
              '结束当前局，旧门票不退，本局不进入排行榜。新局门票：',
              'End this game without refund or leaderboard entry. New game entry: ',
            )}
            {entry?.price ?? '—'}
          </p>
          <button
            className="primary"
            disabled={session?.busy || busy || !enabled || asset !== 'ready'}
            onClick={async () => {
              setConfirm(false);
              await session?.abandon();
              if (session?.terminal) start();
            }}
          >
            {t('重新开一局', 'Start a new game')}
          </button>
          <button className="secondary" onClick={() => setConfirm(false)}>
            {t('继续本局', 'Keep this game')}
          </button>
        </CatchDialog>
      </div>
    </>
  );
}

export function SteadyCatchGame() {
  const t = useCatchText(),
    cache = useQueryClient();
  const snapshot = useGamesSnapshot();
  const { asset, coarse } = usePresentation();
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
    [generation, setGeneration] = useState(0),
    [sound, setSound] = useState(false),
    [dialog, setDialog] = useState<'catalog' | 'help' | null>(null);
  const controllerRef = useRef<CatchSession | null>(null),
    collectionRef = useRef<CatchFeedback[]>([]),
    startKey = useRef<string | null>(null),
    modalResume = useRef<Promise<void> | null>(null),
    modalSequence = useRef(0);
  const [audio] = useState(() => new CatchAudio());
  const [catalogScope, setCatalogScope] = useState<'all' | 'round'>('all');
  const [catalogCaught, setCatalogCaught] = useState<string[]>([]);
  useEffect(() => () => audio.dispose(), [audio]);
  const [created, setCreated] = useState<string | null>(null);
  const refetch = current.refetch;
  const notify = useCallback(() => {
    void cache.invalidateQueries({ queryKey: gameKeys.snapshot });
    void cache.invalidateQueries({ queryKey: ['user', 'games', 'steadycatch', 'leaderboard'] });
    void refetch();
  }, [cache, refetch]);
  const start = async () => {
    if (busy || asset !== 'ready') return;
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
      setCreated(value.id);
      cache.setQueryData(sessionKey, value);
      startKey.current = null;
      setGeneration((v) => v + 1);
      void cache.invalidateQueries({ queryKey: gameKeys.snapshot });
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  };
  const openDialog = (value: 'catalog' | 'help', round = false) => {
    setCatalogCaught(collectionRef.current.map((item) => item.id));
    modalSequence.current++;
    const session = controllerRef.current;
    modalResume.current =
      session?.active || session?.countdown != null ? (session?.pause() ?? null) : null;
    setCatalogScope(round ? 'round' : 'all');
    setDialog(value);
  };
  const closeDialog = () => {
    setDialog(null);
    const paused = modalResume.current,
      sequence = modalSequence.current,
      session = controllerRef.current;
    modalResume.current = null;
    if (paused)
      void paused.then(() => {
        if (
          sequence === modalSequence.current &&
          session === controllerRef.current &&
          !document.hidden
        )
          return session?.beginCountdown(2);
      });
  };
  const settings = snapshot.data?.steadycatch;
  const enabled = !!(snapshot.data?.gamesEnabled && settings?.enabled && settings.available);
  return (
    <main className="game-page catch-game">
      <div className="app">
        <div className="catch-navigation">
          <GameBackLink className="catch-back" />
          <GameToolbar
            items={[
              {
                id: 'rules',
                label: t('玩法说明', 'How to play'),
                icon: 'help',
                onClick: () => openDialog('help'),
              },
              {
                id: 'credits',
                label: t('积分记录', 'Credit history'),
                icon: 'credits',
                to: '/credits',
              },
              {
                id: 'rankings',
                label: t('排行榜', 'Rankings'),
                icon: 'trophy',
                href: '#game-rankings',
              },
              {
                id: 'catalog',
                label: t('梗图鉴', 'Phrases'),
                icon: 'book',
                onClick: () => openDialog('catalog'),
              },
            ]}
            sound={{
              enabled: sound,
              toggle: () => {
                audio.enable(!sound);
                setSound(!sound);
              },
              labelOn: t('音效已开启', 'Sound effects on'),
              labelOff: t('音效已关闭', 'Sound effects off'),
            }}
          />
        </div>
        <header className="topbar">
          <div className="brand">
            <span className="brand-mark" aria-hidden="true">
              ⌑
            </span>
            <span>
              {t('稳稳地接住你', 'Catch You Steadily')}
              <small>{t('AI 八股接物机', 'AI PHRASE CATCHER')}</small>
            </span>
          </div>
          <div className="version">{snapshot.data && <GameWallets wallets={snapshot.data} />}</div>
        </header>
        {asset === 'error' && (
          <p role="alert">
            {t(
              '角色图片未能载入，请刷新页面后再试。',
              'The character image could not load. Refresh the page and try again.',
            )}
          </p>
        )}
        {(catalog.isPending || current.isPending || snapshot.isPending) && <LoadingState />}
        {[catalog.error, current.error, snapshot.error, error].filter(Boolean).map((err, i) => (
          <ErrorState key={i} error={err} />
        ))}
        {catalog.data && !current.isPending && (
          <Round
            key={(current.data?.id ?? 'lobby') + ':' + generation}
            initial={current.data ?? null}
            phrases={catalog.data}
            autoStart={created === current.data?.id}
            controllerRef={controllerRef}
            collectionRef={collectionRef}
            audio={audio}
            reload={() =>
              void current.refetch().then((r) => {
                if (r.data) {
                  setCreated(null);
                  setGeneration((v) => v + 1);
                }
              })
            }
            refresh={notify}
            start={() => void start()}
            enabled={enabled}
            busy={busy}
            entry={settings}
            openCatalog={() => openDialog('catalog', true)}
            openHelp={() => openDialog('help')}
            asset={asset}
            coarse={coarse}
          />
        )}
        <CatchLeaderboard />
        <footer>
          <span>
            {t(
              '每一份过量的关心，都有了落点。',
              'Every bit of excessive care has a place to land.',
            )}
          </span>
          <span>
            {t('社区梗与游戏改写见图鉴', 'Community phrases and adaptations in the catalog')}
          </span>
        </footer>
        {catalog.data && (
          <CatchCatalog
            phrases={catalog.data}
            caught={catalogCaught}
            initialScope={catalogScope}
            open={dialog === 'catalog'}
            onClose={closeDialog}
          />
        )}
        <CatchHelp open={dialog === 'help'} onClose={closeDialog} />
      </div>
    </main>
  );
}
