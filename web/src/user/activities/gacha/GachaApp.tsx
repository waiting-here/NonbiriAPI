import { artSourceSet } from './art-resolution';
import { useEffect, useRef, useState, type ReactNode } from 'react';
import { Button } from '@shared/components/ui/Button';
import { Tabs } from '@shared/components/ui/Tabs';
import { Field } from '@shared/components/ui/Field';
import { Fold } from '@shared/components/ui/Fold';
import { Toggle } from '@shared/components/ui/Toggle';
import { Note } from '@shared/components/ui/Note';
import { ApiError } from '@shared/query/http';
import { responseOutcomeUnknown } from '@shared/operations/api';
import { Icon } from '@shared/components/Icon';
import { PageHeader } from '@shared/components/States';
import { formatDateTime } from '@shared/utils/datetime';
import { formatCredits, creditsToMilli } from '../../games/common/strict';
import { cards, cardById, themes, themeById, rarities, isRare } from './catalog';
import { Artwork } from './Artwork';
import { CardTile } from './CardTile';
import { Collection } from './Collection';
import { Shop, TicketIcon } from './Shop';
import { DrawReveal } from './DrawReveal';
import { voiceLines } from './voice';
import { GachaDialog } from './GachaDialog';
import { GachaAudio } from './audio';
import { freshPity, poolOpen } from './rules';
import type {
  Action,
  Card,
  DrawReceipt,
  GachaPort,
  Preferences,
  Snapshot,
  Theme,
  Ticket,
} from './types';
import './gacha.css';
import { useOptionalAppearance } from '../../appearance/context';

type Page = 'pools' | 'collection' | 'shop';
type PendingAction = { input: Action; key: string; message: string };
function storedAction(scope: string): PendingAction | null {
  try {
    return JSON.parse(sessionStorage.getItem('nb.gacha.pending:' + scope) ?? 'null');
  } catch {
    return null;
  }
}
type Dialog = 'rules' | 'settings' | 'buy' | 'history' | null;
const errorCopy: Record<string, [string, string]> = {
  maintenance: [
    '站点正在维护，请稍后再来。',
    'The site is under maintenance. Please return later.',
  ],
  feature_disabled: [
    '活动暂未就绪，请稍后再来。',
    'The activity is not ready yet. Please return later.',
  ],
  'theme-incomplete': ['集齐这个主题后即可领取奖励。', 'Complete this theme to claim its reward.'],
  'tickets-insufficient': [
    '抽奖券不足，请先兑换所需票券。',
    'You need more tickets. Exchange credits first.',
  ],
  'credits-insufficient': [
    '可用积分不足，请调整兑换数量。',
    'Not enough credits for this quantity.',
  ],
  'fragments-insufficient': ['卡牌碎片不足。', 'Not enough card shards.'],
  'monthly-limit': ['本月剩余额度不足。', 'This exceeds your remaining monthly limit.'],
  'already-owned': ['这张卡已经在你的收藏中了。', 'This card is already in your collection.'],
  'already-claimed': ['这份奖励已经领取。', 'This reward has already been claimed.'],
  'pool-closed': [
    '奖池当前未开放，请选择其他奖池。',
    'This pool is currently closed. Please choose another.',
  ],
  'price-changed': [
    '兑换价格已更新，请确认当前价格后重试。',
    'Prices changed. Review the current price and try again.',
  ],
};
export function GachaApp({
  port,
  english = false,
  back,
}: {
  port: GachaPort;
  english?: boolean;
  back?: ReactNode;
}) {
  const appearance = useOptionalAppearance();
  const t = (zh: string, en: string) => (english ? en : zh);
  const [state, setState] = useState<Snapshot | null>(null),
    [page, setPage] = useState<Page>('pools');
  const [poolId, setPoolId] = useState('permanent'),
    [heroIndex, setHeroIndex] = useState(0);
  const [dialog, setDialog] = useState<Dialog>(null),
    [card, setCard] = useState<Card | null>(null),
    [group, setGroup] = useState<Theme | null>(null);
  const [draw, setDraw] = useState<DrawReceipt | null>(null),
    [history, setHistory] = useState<DrawReceipt[]>([]);
  const [working, setBusy] = useState(false),
    [error, setError] = useState(''),
    [notice, setNotice] = useState('');
  const [ticket, setTicket] = useState<Ticket>('permanent'),
    [quantity, setQuantity] = useState('10');
  const [preferences, setPreferences] = useState<Preferences>(() => {
    try {
      const saved = JSON.parse(localStorage.getItem('nb.gacha.preferences') ?? 'null');
      if (saved) return { cardBackTell: true, ...saved };
    } catch {
      /* Use accessible defaults. */
    }
    return {
      music: false,
      sound: true,
      vibration: true,
      cardBackTell: true,
      reduced: matchMedia('(prefers-reduced-motion: reduce)').matches,
    };
  });
  const [motion, setMotion] = useState(true);
  const [audio] = useState(() => new GachaAudio());
  const enterKey = useRef(crypto.randomUUID());
  const inFlight = useRef(false);
  const [pendingState, setPendingState] = useState<PendingAction | null>(() =>
    storedAction(port.scope),
  );
  const pending = useRef(pendingState);
  const mounted = useRef(true);
  const busy = working || pendingState !== null;
  const [historyCursor, setHistoryCursor] = useState<string | undefined>();
  const [historyLoading, setHistoryLoading] = useState(false);
  const [historyError, setHistoryError] = useState('');
  const [clock, setClock] = useState(() => Date.now());
  const [receivedAt, setReceivedAt] = useState(() => Date.now());
  function accept(snapshot: Snapshot) {
    setReceivedAt(Date.now());
    setState(snapshot);
  }
  function remember(value: PendingAction | null) {
    pending.current = value;
    setPendingState(value);
    try {
      if (value) sessionStorage.setItem('nb.gacha.pending:' + port.scope, JSON.stringify(value));
      else sessionStorage.removeItem('nb.gacha.pending:' + port.scope);
    } catch {
      /* The same request remains recoverable in this visit. */
    }
  }
  useEffect(() => {
    mounted.current = true;
    const timer = window.setInterval(() => setClock(Date.now()), 1000);
    return () => {
      mounted.current = false;
      window.clearInterval(timer);
    };
  }, []);
  useEffect(() => {
    let cancelled = false;
    void (
      pending.current
        ? port.load()
        : port.act({ action: 'enter' }, enterKey.current).then((value) => value.snapshot)
    )
      .then((value) => {
        if (!cancelled) {
          setReceivedAt(Date.now());
          setState(value);
        }
      })
      .catch(() => {
        if (!cancelled)
          setError(
            english ? 'Could not open the activity. Please retry.' : '暂时无法打开活动，请重试。',
          );
      });
    return () => {
      cancelled = true;
    };
  }, [port, english]);
  useEffect(() => {
    try {
      localStorage.setItem('nb.gacha.preferences', JSON.stringify(preferences));
    } catch {
      /* Preferences still apply for this visit. */
    }
    if (!preferences.music) void audio.playMusic(false);
    const visibility = () => {
      if (document.hidden) audio.pause();
      else if (preferences.music) void audio.playMusic(true);
    };
    document.addEventListener('visibilitychange', visibility);
    return () => document.removeEventListener('visibilitychange', visibility);
  }, [audio, preferences]);
  useEffect(() => () => audio.dispose(), [audio]);
  useEffect(() => {
    if (!notice) return;
    const timer = window.setTimeout(() => setNotice(''), 4500);
    return () => window.clearTimeout(timer);
  }, [notice]);
  async function act(input: Action, message = '') {
    if (inFlight.current) return;
    if (pending.current && JSON.stringify(pending.current.input) !== JSON.stringify(input)) return;
    const operation = pending.current ?? { input, key: crypto.randomUUID(), message };
    remember(operation);
    inFlight.current = true;
    setBusy(true);
    setError('');
    try {
      void audio.playMusic(preferences.music);
      const result = await port.act(operation.input, operation.key);
      if (!mounted.current) return;
      remember(null);
      accept(result.snapshot);
      appearance?.refresh();
      if (result.draw) setDraw(result.draw);
      else {
        setNotice(
          result.message === 'receipt-expired'
            ? t(
                '原操作已完成，明细已超过 180 天保留期；没有再次扣券。',
                'The original action completed. Its detail has expired; no tickets were charged again.',
              )
            : operation.message || t('已完成', 'Done'),
        );
        if (input.action === 'claim') audio.cue('theme', preferences.sound);
        if (input.action === 'buy') setDialog(null);
      }
    } catch (cause) {
      if (!mounted.current) return;
      const code =
        cause instanceof ApiError ? cause.code : cause instanceof Error ? cause.message : '';
      const pair = errorCopy[code];
      const known = !!pair || (cause instanceof ApiError && !responseOutcomeUnknown(cause));
      if (known) {
        remember(null);
        setError(
          pair
            ? t(...pair)
            : t(
                '本次操作未成功，请刷新后重试。',
                'This action was not completed. Refresh and try again.',
              ),
        );
        try {
          const fresh = await port.load();
          if (mounted.current) accept(fresh);
        } catch {
          /* Retain the last visible snapshot. */
        }
      } else {
        setError(
          t(
            '还未收到操作结果，请重试原操作。',
            'The result has not arrived. Retry the original action.',
          ),
        );
      }
    } finally {
      inFlight.current = false;
      if (mounted.current) setBusy(false);
    }
  }
  async function showHistory(more = false) {
    setDialog('history');
    setHistoryError('');
    setHistoryLoading(true);
    try {
      const result = await port.history(more ? historyCursor : undefined);
      if (!mounted.current) return;
      setHistory((previous) => (more ? [...previous, ...result.items] : result.items));
      setHistoryCursor(result.next_cursor);
    } catch {
      if (mounted.current) setHistoryError(t('暂时无法读取记录。', 'Could not load history.'));
    } finally {
      if (mounted.current) setHistoryLoading(false);
    }
  }
  const inspect = (value: Card) => {
    setCard(value);
    setMotion(!preferences.reduced);
  };
  if (!state)
    return (
      <div className="gacha-loading" role="status">
        <Icon name="spark" />
        <p>{error || t('正在打开星光收藏册…', 'Opening your book of starlight…')}</p>
        {error && (
          <Button
            onClick={() =>
              void act(pendingState?.input ?? { action: 'enter' }, pendingState?.message)
            }
          >
            {t('重试', 'Retry')}
          </Button>
        )}
      </div>
    );
  const pool = state.pools.find((p) => p.id === poolId) ?? state.pools[0];
  const pity = state.pity[pool.group] ?? freshPity(pool.rules);
  const highlighted =
    pool.kind === 'limited'
      ? pool.featured
          .map((id) => cardById[id])
          .sort((a, b) => Number(isRare(b.rarity)) - Number(isRare(a.rarity)))
      : cards.filter((c) => c.theme === 'g');
  const hero =
    highlighted[heroIndex % highlighted.length] ?? cards.find((c) => c.id === 'g-chatgpt')!;
  const completeCount = themes.filter(
    (theme) =>
      !state.claimed.includes(theme.id) &&
      cards.filter((c) => c.theme === theme.id).every((c) => !!state.owned[c.id]),
  ).length;
  const quantityValue = Math.max(1, Math.min(100, Number.parseInt(quantity) || 1));
  const unitPrice = state.shop[ticket === 'permanent' ? 'permanent_price' : 'limited_price'];
  const totalMilli = creditsToMilli(unitPrice) * BigInt(quantityValue);
  const pointText = (milli: bigint) => {
    const integer = milli / 1000n,
      fraction = (milli % 1000n).toString().padStart(3, '0').replace(/0+$/, '');
    return formatCredits(integer + (fraction ? '.' + fraction : ''));
  };
  const availableGame =
    creditsToMilli(state.credits.game) > 0n ? creditsToMilli(state.credits.game) : 0n;
  const availableGeneral =
    creditsToMilli(state.credits.general) > 0n ? creditsToMilli(state.credits.general) : 0n;
  const currentTime = state.server_time + Math.max(0, Math.floor((clock - receivedAt) / 1000));
  const currentState = { ...state, server_time: currentTime };
  const repeatPool = draw && state.pools.find((value) => value.id === draw.pool_id);
  const repeatQuantity = draw?.items.length === 10 ? 10 : 1;
  const canRepeat =
    !busy &&
    repeatPool &&
    poolOpen(repeatPool, currentTime) &&
    state.tickets[repeatPool.kind] >= repeatQuantity;
  const recovery =
    pendingState && !working ? (
      <Note tone="warn">
        <p>
          {t(
            '上一笔操作的结果尚未确认，完成重试后即可继续。',
            'The previous action is awaiting confirmation. Retry it to continue.',
          )}
        </p>
        <Button onClick={() => void act(pendingState.input, pendingState.message)}>
          {t('重试原操作', 'Retry original action')}
        </Button>
      </Note>
    ) : null;
  const gameSpend =
    ticket === 'permanent' ? (availableGame < totalMilli ? availableGame : totalMilli) : 0n;
  const generalSpend = totalMilli - gameSpend;
  return (
    <div className={'gacha-app' + (preferences.reduced ? ' gacha-static' : '')}>
      <PageHeader
        title={t('真·抽卡模拟器', 'The Gacha Simulator')}
        eyebrow="NONBIRI · COLLECTION"
        back={back}
        className="gacha-page-header"
        actions={
          <div className="gacha-header-actions">
            <Button
              variant="ghost"
              aria-label={t('规则', 'Rules')}
              onClick={() => setDialog('rules')}
            >
              <Icon name="help" />
              <span>{t('规则', 'Rules')}</span>
            </Button>
            <Button
              variant="ghost"
              aria-label={t('声音与动效', 'Sound & motion')}
              onClick={() => setDialog('settings')}
            >
              <Icon name="sound" />
              <span>{t('声音与动效', 'Sound & motion')}</span>
            </Button>
            <Button
              variant="ghost"
              aria-label={t('抽卡记录', 'History')}
              onClick={() => void showHistory()}
            >
              <Icon name="history" />
              <span>{t('抽卡记录', 'History')}</span>
            </Button>
          </div>
        }
      />
      <div className="gacha-shell">
        <div className="gacha-topbar">
          <Tabs
            label={t('活动导航', 'Activity navigation')}
            value={page}
            onChange={setPage}
            tabs={[
              {
                value: 'pools',
                label: (
                  <>
                    <Icon name="spark" />
                    {t('星光召唤', 'Summon')}
                  </>
                ),
              },
              {
                value: 'collection',
                label: (
                  <>
                    <Icon name="book" />
                    {t('我的图鉴', 'Collection')}
                    {completeCount > 0 && <span className="gacha-reward-dot">{completeCount}</span>}
                  </>
                ),
              },
              {
                value: 'shop',
                label: (
                  <>
                    <Icon name="resources" />
                    {t('神秘商店', 'Mystery shop')}
                  </>
                ),
              },
            ]}
          />
          <div className="gacha-wallet">
            <Button
              variant="ghost"
              onClick={() => {
                setTicket('permanent');
                setDialog('buy');
              }}
              title={t('兑换常驻抽奖券', 'Buy standard tickets')}
            >
              <TicketIcon />
              <span>
                <small>{t('常驻券', 'Standard')}</small>
                <strong>{state.tickets.permanent.toLocaleString()}</strong>
              </span>
              <b>{t('兑换', 'Get')}</b>
            </Button>
            <Button
              variant="ghost"
              onClick={() => {
                setTicket('limited');
                setDialog('buy');
              }}
              title={t('兑换限定抽奖券', 'Buy limited tickets')}
            >
              <TicketIcon limited />
              <span>
                <small>{t('限定券', 'Limited')}</small>
                <strong>{state.tickets.limited.toLocaleString()}</strong>
              </span>
              <b>{t('兑换', 'Get')}</b>
            </Button>
          </div>
        </div>
        {error && <Note tone="bad">{error}</Note>}
        {recovery}
        {page === 'pools' && (
          <>
            <div className="gacha-pool-nav">
              <Tabs
                label={t('选择奖池', 'Choose a pool')}
                value={pool.id}
                onChange={(id) => {
                  setPoolId(id);
                  setHeroIndex(0);
                }}
                tabs={state.pools.map((p) => ({
                  value: p.id,
                  label: english && p.name_en ? p.name_en : p.name,
                }))}
              />
              <Button
                size="sm"
                onClick={() =>
                  void act(
                    { action: 'check-in' },
                    t('签到成功，常驻抽奖券 +1', 'Checked in · +1 standard ticket'),
                  )
                }
                disabled={busy || state.checked_in}
              >
                <Icon name={state.checked_in ? 'check' : 'credits'} />
                {state.checked_in
                  ? t('今日已签到', 'Checked in')
                  : t('每日签到 · 领 1 券', 'Daily check-in · +1 ticket')}
              </Button>
            </div>
            <section
              className={'gacha-banner gacha-banner--' + pool.cover}
              aria-labelledby="gacha-pool-title"
            >
              <img
                className="gacha-banner__back"
                src={'/assets/gacha/covers/' + pool.cover + '.webp'}
                srcSet={artSourceSet(
                  '/assets/gacha/covers/' + pool.cover + '.webp',
                  ['l', 'm'].includes(pool.cover) ? 1600 : 1672,
                )}
                sizes="(width < 80rem) 100vw, 80rem"
                alt=""
              />
              <div className="gacha-banner__haze" />
              <div className="gacha-banner__copy">
                <span className="gacha-kicker">
                  {pool.kind === 'permanent'
                    ? t('常驻召唤 · 永久开放', 'STANDARD · ALWAYS OPEN')
                    : t('限定召唤 · 期间开放', 'LIMITED · FOR A MOMENT')}
                </span>
                <h2 id="gacha-pool-title">{english && pool.name_en ? pool.name_en : pool.name}</h2>
                <p className="gacha-banner__tagline">
                  {pool.kind === 'permanent'
                    ? t('让每一次相遇，都闪闪发光。', 'Let every encounter shine.')
                    : t(themeById[hero.theme].tagline, themeById[hero.theme].tagline_en)}
                </p>
                <div className="gacha-banner__divider">
                  <Icon name="spark" />
                </div>
                <p className="gacha-banner__description">
                  {pool.kind === 'permanent'
                    ? t(
                        '五位少女，万千日常。收藏属于你的悠哉时光。',
                        'Five companions. Countless little moments. Start a collection of your own.',
                      )
                    : t(
                        '本期限定角色登场，与常驻卡牌共同回应你的期待。',
                        'Meet the featured companions alongside the permanent collection.',
                      )}
                </p>
                <span className="gacha-banner__tag">
                  {pool.kind === 'permanent'
                    ? t('全部常驻卡牌', 'All permanent cards')
                    : t('同规则奖池共享保底', 'Shared pity within this rule group')}
                </span>
                {pool.ends_at && (
                  <p className="gacha-banner__time">
                    {t('开放至 ', 'Until ')}
                    {formatDateTime(pool.ends_at)}
                  </p>
                )}
                {state.newcomer && pool.kind === 'permanent' && (
                  <p className="gacha-newcomer">
                    <Icon name="spark" />
                    {t(
                      '见面礼 10 券已到账 · 首次十连必得 SSR',
                      '10 welcome tickets · your first ten-pull guarantees SSR',
                    )}
                  </p>
                )}
              </div>
              <div className="gacha-banner__art">
                <div className="gacha-halo" />
                <button
                  className={'gacha-hero-card gacha-card--' + hero.rarity}
                  onClick={() => inspect(hero)}
                  aria-label={t('查看 ', 'Inspect ') + (english ? hero.name_en : hero.name)}
                >
                  <Artwork key={hero.id} card={hero} full />
                  <span className="gacha-hero-card__rank">{hero.rarity}</span>
                  <span className="gacha-hero-card__name">
                    <small>{t(themeById[hero.theme].name, themeById[hero.theme].name_en)}</small>
                    <strong>{hero.character}</strong>
                  </span>
                </button>
                <div
                  className="gacha-character-picker"
                  aria-label={t('展示角色', 'Featured characters')}
                >
                  {highlighted.map((c, index) => (
                    <button
                      key={c.id}
                      onClick={() => setHeroIndex(index)}
                      aria-pressed={hero.id === c.id}
                      aria-label={c.character}
                      title={c.character}
                    >
                      <img src={c.thumb} alt="" />
                    </button>
                  ))}
                </div>
              </div>
            </section>
            <div className="gacha-drawbar">
              <div className="gacha-pity">
                <div>
                  <span>{t('距离必得 SR 或以上', 'SR or better within')}</span>
                  <strong>
                    {Math.max(1, Math.min(pity.sr_limit, pool.rules.sr_pity) - pity.sr)}{' '}
                    <small>{t('抽', 'pulls')}</small>
                  </strong>
                </div>
                <div>
                  <span>{t('距离必得 SSR', 'SSR within')}</span>
                  <strong>
                    {Math.max(1, Math.min(pity.ssr_limit, pool.rules.ssr_pity) - pity.ssr)}{' '}
                    <small>{t('抽', 'pulls')}</small>
                  </strong>
                </div>
                {pool.kind === 'limited' && (pool.rules.ssr_guarantee_misses ?? 1) > 1 && (
                  <p className="gacha-special-progress">
                    {t('SP 大保底：连续未中 ', 'SP guarantee: consecutive misses ')}
                    <strong>
                      {pity.ssr_misses ?? 0} / {pool.rules.ssr_guarantee_misses}
                    </strong>
                    {t(' · 与普通限定池独立', ' · independent of regular limited pools')}
                  </p>
                )}
                {pool.kind === 'limited' && (pity.sr_featured || pity.ssr_featured) && (
                  <p>
                    {[pity.sr_featured && 'SR', pity.ssr_featured && 'SSR']
                      .filter(Boolean)
                      .join(' / ')}{' '}
                    {t('下次同档必为限定', 'next hit guaranteed featured')}
                  </p>
                )}
              </div>
              <div className="gacha-draw-actions">
                <Button
                  disabled={busy || !poolOpen(pool, currentTime)}
                  onClick={() =>
                    state.tickets[pool.kind] < 1
                      ? (setTicket(pool.kind), setQuantity('1'), setDialog('buy'))
                      : void act({ action: 'draw', pool_id: pool.id, quantity: 1 })
                  }
                >
                  <Icon name="spark" />
                  <span>
                    {t('召唤一次', 'Summon once')}
                    <small>
                      1{' '}
                      {pool.kind === 'permanent'
                        ? t('常驻券', 'standard ticket')
                        : t('限定券', 'limited ticket')}
                    </small>
                  </span>
                </Button>
                <Button
                  variant="primary"
                  disabled={busy || !poolOpen(pool, currentTime)}
                  onClick={() =>
                    state.tickets[pool.kind] < 10
                      ? (setTicket(pool.kind),
                        setQuantity(String(10 - state.tickets[pool.kind])),
                        setDialog('buy'))
                      : void act({ action: 'draw', pool_id: pool.id, quantity: 10 })
                  }
                >
                  <Icon name="spark" />
                  <span>
                    {working ? t('召唤中…', 'Summoning…') : t('十次召唤', 'Summon ten')}
                    <small>
                      10{' '}
                      {pool.kind === 'permanent'
                        ? t('常驻券', 'standard tickets')
                        : t('限定券', 'limited tickets')}
                    </small>
                  </span>
                </Button>
              </div>
            </div>
            <div className="gacha-underbar">
              <p>
                {t(
                  '每抽必得 1 张卡牌 · 重复卡自动转为同稀有度碎片',
                  'One card every pull · duplicates become matching shards',
                )}
              </p>
              <Button variant="link" size="sm" onClick={() => setDialog('rules')}>
                {t('查看概率与保底规则', 'Rates & guarantees')}
              </Button>
            </div>
            <div className="gacha-home-bottom">
              <div>
                <span className="gacha-eyebrow">YOUR NEXT CHAPTER</span>
                <h3>{t('有些美好，要一起才完整', 'Some memories need everyone')}</h3>
                <p>
                  {t(
                    '集齐一个主题，解锁主题插画与特别回馈。',
                    'Complete a theme to unlock its illustration and rewards.',
                  )}
                </p>
              </div>
              <Button onClick={() => setPage('collection')}>
                <Icon name="book" />
                {t('翻开收藏册', 'Open your collection')}
                <span>
                  {Object.keys(state.owned).length} / {cards.length}
                </span>
              </Button>
            </div>
          </>
        )}
        {page === 'collection' && (
          <Collection
            state={state}
            english={english}
            busy={busy}
            act={(a, m) => void act(a, m)}
            inspect={inspect}
            viewGroup={setGroup}
          />
        )}
        {page === 'shop' && (
          <Shop
            state={currentState}
            english={english}
            busy={busy}
            act={(a, m) => void act(a, m)}
            inspect={inspect}
          />
        )}
        <footer className="gacha-footer">
          <p>
            {t(
              '卡牌仅供收藏，没有常规积分收益，不能交易或兑换积分。',
              'Cards are for collection only. No regular credit rewards, trading or cash-out.',
            )}
          </p>
          <span>NONBIRI · COLLECT A LITTLE JOY</span>
        </footer>
      </div>
      {notice && (
        <div className="gacha-toast" role="status">
          <Icon name="check" />
          {notice}
        </div>
      )}
      {draw && (
        <DrawReveal
          key={draw.id}
          receipt={draw}
          preferences={preferences}
          audio={audio}
          english={english}
          onClose={() => setDraw(null)}
          onAgain={
            canRepeat
              ? () => {
                  setDraw(null);
                  void act({ action: 'draw', pool_id: draw.pool_id, quantity: repeatQuantity });
                }
              : undefined
          }
        />
      )}
      {card && (
        <GachaDialog
          title={t(card.name, card.name_en)}
          closeLabel={t('关闭', 'Close')}
          onClose={() => setCard(null)}
          className={state.owned[card.id] ? 'gacha-spotlight' : 'gacha-card-info'}
        >
          {state.owned[card.id] ? (
            <div className="gacha-spotlight__stage">
              <Artwork key={card.id} card={card} full animated={motion && !preferences.reduced} />
              <div className="gacha-spotlight__caption">
                <span>{card.rarity}</span>
                <h3>{t(card.name, card.name_en)}</h3>
              </div>
            </div>
          ) : (
            <>
              <CardTile card={card} english={english} onClick={() => {}} />
              <h3>{t('等待与你相遇', 'Waiting to meet you')}</h3>
              <p>
                {t('获得后解锁完整卡面欣赏。', 'Collect this card to view the full illustration.')}
              </p>
            </>
          )}
          <p>
            {card.source === 'permanent'
              ? t('常驻卡牌 · 可从全部奖池获得', 'Permanent · available in every pool')
              : card.source === 'shop'
                ? t('神秘商店专属 · 不参与抽取', 'Mystery shop exclusive · not in any pool')
                : state.pools.some((p) => poolOpen(p, currentTime) && p.featured.includes(card.id))
                  ? t(
                      '限定奖池与期间神秘商店可得',
                      'Available in an open limited pool and its shop',
                    )
                  : t('限定卡牌 · 当前暂无获取途径', 'Limited card · not currently available')}
          </p>
          {state.owned[card.id] && (
            <p className="gacha-owned-voice">
              {voiceLines(card.id, english).map((line, i) => (
                <span key={i}>{line}</span>
              ))}
            </p>
          )}
          {state.owned[card.id] && (
            <p>
              {t('初次相遇：', 'First collected: ')}
              {formatDateTime(state.owned[card.id].at)}
            </p>
          )}
          {state.owned[card.id] && isRare(card.rarity) && !preferences.reduced && (
            <Button onClick={() => setMotion(!motion)}>
              <Icon name={motion ? 'pause' : 'play'} />
              {motion ? t('暂停动态', 'Pause animation') : t('播放动态', 'Play animation')}
            </Button>
          )}
        </GachaDialog>
      )}
      {group && (
        <GachaDialog
          title={t(group.name, group.name_en)}
          closeLabel={t('关闭', 'Close')}
          onClose={() => setGroup(null)}
          className="gacha-group-view"
        >
          <img
            src={group.art}
            srcSet={artSourceSet(group.art, ['l', 'm'].includes(group.id) ? 1600 : 1672)}
            sizes="90vw"
            alt={t(group.name + '主题插画', group.name_en + ' group memory')}
          />
          <p>{t(group.tagline, group.tagline_en)}</p>
        </GachaDialog>
      )}
      {dialog === 'buy' && (
        <GachaDialog
          title={t('兑换抽奖券', 'Exchange credits for tickets')}
          onClose={() => !busy && setDialog(null)}
          closeLabel={t('关闭', 'Close')}
        >
          <Tabs
            label={t('票券类型', 'Ticket type')}
            value={ticket}
            onChange={setTicket}
            tabs={[
              { value: 'permanent', label: t('常驻抽奖券', 'Standard tickets') },
              { value: 'limited', label: t('限定抽奖券', 'Limited tickets') },
            ]}
          />
          <div className="gacha-buy-icon">
            <TicketIcon limited={ticket === 'limited'} />
          </div>
          <p>
            {formatCredits(unitPrice)} {t('积分 / 张', 'credits / ticket')}
          </p>
          <Field
            label={t('兑换数量', 'Quantity')}
            help={t('每次 1–100 张', '1–100 tickets per exchange')}
          >
            {(props) => (
              <input
                {...props}
                type="number"
                min="1"
                max="100"
                value={quantity}
                onChange={(e) => setQuantity(e.target.value)}
                onBlur={() => setQuantity(String(quantityValue))}
              />
            )}
          </Field>
          <dl className="gacha-buy-summary">
            <dt>{t('游戏积分余额', 'Game credits')}</dt>
            <dd>
              {formatCredits(state.credits.game)} {t('积分', 'credits')}
            </dd>
            <dt>{t('通用积分余额', 'General credits')}</dt>
            <dd>
              {formatCredits(state.credits.general)} {t('积分', 'credits')}
            </dd>
            <dt>{t('本次花费', 'Total cost')}</dt>
            <dd>
              {pointText(totalMilli)} {t('积分', 'credits')}
            </dd>
          </dl>
          <p>
            {ticket === 'permanent'
              ? t('优先消耗游戏积分：', 'Game credits first: ') +
                pointText(gameSpend) +
                t(' 游戏积分 + ', ' game + ') +
                pointText(generalSpend) +
                t(' 通用积分', ' general')
              : t('仅消耗通用积分。', 'Uses general credits only.')}
          </p>
          <p>
            {t(
              '票券永久有效，兑换后不能换回积分。',
              'Tickets never expire and cannot be exchanged back for credits.',
            )}
          </p>
          {error && <Note tone="bad">{error}</Note>}
          {recovery}
          <Button
            variant="primary"
            block
            disabled={busy || !quantity || generalSpend > availableGeneral}
            onClick={() =>
              void act(
                { action: 'buy', ticket, quantity: quantityValue, revision: state.shop.revision },
                t('抽奖券已到账', 'Tickets added'),
              )
            }
          >
            {generalSpend > availableGeneral
              ? t('积分不足', 'Not enough credits')
              : t('兑换 ', 'Exchange for ') + quantityValue + t(' 张', ' tickets')}
          </Button>
        </GachaDialog>
      )}
      {dialog === 'settings' && (
        <GachaDialog
          title={t('声音与动效', 'Sound & motion')}
          onClose={() => setDialog(null)}
          closeLabel={t('关闭', 'Close')}
        >
          <Toggle
            label={t('背景音乐', 'Background music')}
            description={t('Elevate · 大厅', 'Elevate · Lobby')}
            checked={preferences.music}
            onChange={(music) => {
              setPreferences((p) => ({ ...p, music }));
              void audio.playMusic(music);
            }}
          />
          <Toggle
            label={t('演出音效', 'Reveal sounds')}
            checked={preferences.sound}
            onChange={(sound) => {
              setPreferences((p) => ({ ...p, sound }));
              audio.cue('SR', sound);
            }}
          />
          <Toggle
            label={t('卡背预示', 'Card-back hints')}
            description={t(
              'SR 及以上卡背泛起稀有度光芒。关闭后，揭晓前所有卡背相同。',
              'SR and higher card backs glow with their rarity. Turn off to keep every back the same until revealed.',
            )}
            checked={preferences.cardBackTell !== false}
            onChange={(cardBackTell) => setPreferences((p) => ({ ...p, cardBackTell }))}
          />
          <Toggle
            label={t('减弱动效', 'Reduce motion')}
            description={t(
              '直接展示抽卡结果，SSR／SP 使用静态卡面。',
              'Show results immediately with static SSR/SP artwork.',
            )}
            checked={preferences.reduced}
            onChange={(reduced) => setPreferences((p) => ({ ...p, reduced }))}
          />
          <Toggle
            label={t('设备振动', 'Device vibration')}
            checked={preferences.vibration !== false}
            onChange={(vibration) => setPreferences((p) => ({ ...p, vibration }))}
          />
          <p>
            {t(
              '在支持振动的设备和浏览器上播放短提示。',
              'Short feedback on supported devices and browsers.',
            )}
          </p>
          <Fold title={t('试听专用音效', 'Listen to reveal sounds')}>
            <div className="gacha-sound-grid">
              {(
                ['gather', 'spread', 'N', 'R', 'SR', 'SSR', 'spotlight', 'theme', 'SP'] as const
              ).map((cue, i) => (
                <Button key={cue} onClick={() => audio.cue(cue, true)}>
                  {
                    [
                      t('星光汇聚', 'Gather'),
                      t('卡阵展开', 'Spread'),
                      'N',
                      'R',
                      'SR',
                      'SSR',
                      t('SSR 特写', 'Spotlight'),
                      t('主题解锁', 'Theme unlock'),
                      'SP',
                    ][i]
                  }
                </Button>
              ))}
            </div>
          </Fold>
        </GachaDialog>
      )}
      {dialog === 'rules' && (
        <GachaDialog
          title={t('概率与活动规则', 'Rates and activity rules')}
          onClose={() => setDialog(null)}
          className="gacha-rules"
          closeLabel={t('关闭', 'Close')}
        >
          <h3>{english && pool.name_en ? pool.name_en : pool.name}</h3>
          <div className="gacha-rates">
            {rarities.map((r, i) => (
              <div key={r}>
                <span>{r}</span>
                <strong>{(pool.rules.weights[i] / 100).toFixed(2)}%</strong>
                <small>{t('基础概率', 'Base rate')}</small>
              </div>
            ))}
          </div>
          <p>
            {t(
              '每 ' +
                pool.rules.sr_pity +
                ' 抽内至少一张 SR 或 SSR；每 ' +
                pool.rules.ssr_pity +
                ' 抽内至少一张 SSR。抽中 SSR 同时重置两档计数。普通十连与单抽规则相同。',
              'At least SR within ' +
                pool.rules.sr_pity +
                ' pulls and SSR within ' +
                pool.rules.ssr_pity +
                '. SSR resets both counters. Ten-pulls follow the same rules as single pulls.',
            )}
          </p>
          <p>
            {t('含保底的长期综合概率：', 'Long-run rates including pity: ') +
              rarities
                .map(
                  (rarity, index) =>
                    rarity + ' ' + Number((pool.rates[index] * 100).toFixed(2)) + '%',
                )
                .join(' · ')}
          </p>
          {pool.kind === 'limited' && (
            <>
              <p>
                {t(
                  '规则完全相同的限定奖池共享进度并跨期继承；SR 与 SSR 的限定保证分别计算。',
                  'Limited pools with identical rules share progress across periods. SR and SSR featured guarantees are independent.',
                )}
              </p>
              {(['SR', 'SSR'] as const).map((rarity) => {
                const probability =
                  rarity === 'SR' ? pool.rules.sr_featured : pool.rules.ssr_featured;
                const guarantee =
                  rarity === 'SR' ? pool.rules.sr_guarantee : pool.rules.ssr_guarantee;
                return (
                  <p key={rarity}>
                    {rarity} · {t('抽中该档后，', 'After a hit of this rarity, ')}
                    {probability / 100}% {t('为本池限定。', 'is featured. ')}
                    {guarantee
                      ? rarity === 'SSR' && (pool.rules.ssr_guarantee_misses ?? 1) > 1
                        ? t(
                            '连续两次未中限定后，下次同档必为限定。',
                            'After two consecutive misses, the next hit is featured.',
                          )
                        : t(
                            '未中限定后，下次同档必为限定。',
                            'After a miss, the next hit is featured.',
                          )
                      : t('未开启下次限定保证。', 'No next-hit featured guarantee.')}
                  </p>
                );
              })}
              <p>
                {t(
                  '本池没有某档限定时，该档保证进度保持不变。SP 与 SSR 共用初始出率、小保底及 SSR 碎片，限定占比、未中次数与兑换价以当前配置为准。',
                  'A pool without a featured rarity preserves that guarantee. SP uses the SSR rarity roll, hard pity and shards; featured share, misses and exchange price follow this configuration.',
                )}
              </p>
            </>
          )}
          <Fold title={t('新手、签到与碎片', 'Welcome gifts, check-in and shards')}>
            <p>
              {t(
                '首次进入赠送 10 张常驻抽奖券，首次常驻十连至少获得一张 SSR；先单抽不消耗资格。每日手动签到获得 1 张常驻券，按站点时区零点刷新。每张重复卡自动转为 1 枚同稀有度卡牌碎片。',
                'Receive 10 standard tickets on your first visit. Your first standard ten-pull includes at least one SSR; single pulls do not use this guarantee. Check in manually once per site day for one standard ticket. Each duplicate becomes one matching rarity shard.',
              )}
            </p>
          </Fold>
          <Fold title={t('收集与记录', 'Collection and records')}>
            <p>
              {t(
                '卡牌仅供收藏，无积分返还或交易功能。集齐主题自动解锁合照，票券与 10 捐赠回馈手动领取一次。抽卡逐条明细保留 180 天，收藏、保底和领取状态在账号存在期间长期保留。',
                'Cards are collectibles with no credit redemption or trading. A complete theme unlocks its illustration immediately; claim its ticket and 10 donation credit once. Draw details are kept for 180 days. Collection, pity and claim status remain while your account exists.',
              )}
            </p>
          </Fold>
          <Fold title={t('本期卡牌', 'Cards in this pool')}>
            <div className="gacha-pool-list">
              {cards
                .filter((c) => c.source === 'permanent' || pool.featured.includes(c.id))
                .map((c) => (
                  <p key={c.id}>
                    <b>{c.rarity}</b> {t(c.name, c.name_en)}
                  </p>
                ))}
            </div>
          </Fold>
        </GachaDialog>
      )}
      {dialog === 'history' && (
        <GachaDialog
          title={t('抽卡记录', 'Draw history')}
          onClose={() => setDialog(null)}
          className="gacha-history"
          closeLabel={t('关闭', 'Close')}
        >
          <p>
            {t(
              '展示最近 180 天记录，关闭或跳过演出不影响结果。',
              'Your last 180 days of draws. Skipping the animation never changes a result.',
            )}
          </p>
          {historyError && (
            <Note tone="bad">
              {historyError}
              <Button onClick={() => void showHistory()}>{t('重试', 'Retry')}</Button>
            </Note>
          )}
          {historyLoading && <p role="status">{t('正在读取…', 'Loading…')}</p>}
          {!history.length && !historyLoading && !historyError ? (
            <div className="gacha-empty">
              <Icon name="history" />
              <h3>{t('故事还没有开始', 'Your story is just beginning')}</h3>
              <p>
                {t(
                  '完成第一次召唤，这里就会留下相遇的记录。',
                  'Your first summon will appear here.',
                )}
              </p>
            </div>
          ) : (
            history.map((receipt) => (
              <article key={receipt.id}>
                <header>
                  <h3>{receipt.pool_name}</h3>
                  <time>{formatDateTime(receipt.created_at)}</time>
                </header>
                <div>
                  {receipt.items.map((item, i) => (
                    <button key={i} onClick={() => inspect(cardById[item.card_id])}>
                      <span
                        className={'gacha-rarity gacha-rarity--' + cardById[item.card_id].rarity}
                      >
                        {cardById[item.card_id].rarity}
                      </span>
                      <span>{t(cardById[item.card_id].name, cardById[item.card_id].name_en)}</span>
                      <small>
                        {item.duplicate ? t('碎片 +1', '+1 shard') : t('初次相遇', 'NEW')}
                      </small>
                    </button>
                  ))}
                </div>
              </article>
            ))
          )}
          {historyCursor && (
            <Button disabled={historyLoading} onClick={() => void showHistory(true)}>
              {t('加载更多', 'Load more')}
            </Button>
          )}
        </GachaDialog>
      )}
    </div>
  );
}
