import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react';
import { skipToken, useQuery, useQueryClient } from '@tanstack/react-query';
import { unlockPriceText, useWhaleText } from './copy';
import { Link } from 'react-router';
import { Amount } from '@shared/components/Amount';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { ErrorState, LoadingState, PageHeader } from '@shared/components/States';
import { GameBackLink } from '../common/GameBackLink';
import { UserPageGate } from '../../components/UserPageGate';
import { act, exchange, fetchProfile, fetchNeighborHouse, type Action, type Profile } from './api';
import { soundEngine } from './audio/soundEngine';
import { WhaleNamingModal } from './components/WhaleNamingModal';
import {
  ControlDeck,
  FloatingDock,
  type DeckTabId,
  type MenuPresentationMode,
} from './components/ControlDeck';
import { OceanStage } from './components/OceanStage';
import { Backpack } from './components/Backpack';
import { ActivityPane, DecorPane, WardrobePane } from './components/Panels';
import { SocialPane, DepartureDialog } from './components/SocialPane';
import { ExchangePane } from './components/ExchangePane';
import { DECOR_CATALOG, FEED_CATALOG, OUTFIT_CATALOG } from './game/catalog';
import type { NeighborStation } from './game/types';
import type { RigImpulseEvent } from './rig/WhaleRigActor';
import { DEFAULT_FACE_CALIBRATION } from './rig/calibrator';
import '../games.css';
import './styles/theme.css';
import './styles/deck.css';
import './styles/host.css';

const profileKey = ['user', 'games', 'whale-haven', 'profile'] as const;
const menuPreference = 'nonbiri:user:whale-haven:menu-position';

function itemToUnlock(profile: Profile, command: Action) {
  const { home } = profile.snapshot;
  const { values } = profile.settings;
  if (command.type === 'toggle_decor' && !home.ownedDecor.includes(command.decorId))
    return {
      name: DECOR_CATALOG[command.decorId].name,
      basePrice: values['decor_' + command.decorId + '_price_pearls'],
    };
  if (
    command.type === 'select_outfit' &&
    command.outfitId !== 'maid_classic' &&
    !home.ownedOutfits?.includes(command.outfitId)
  )
    return {
      name: OUTFIT_CATALOG[command.outfitId].name,
      basePrice: values['outfit_' + command.outfitId + '_price_pearls'],
    };
}

function useSmallViewport() {
  const [small, setSmall] = useState(() => window.matchMedia('(max-width:860px)').matches);
  useEffect(() => {
    const media = window.matchMedia('(max-width:860px)');
    const change = () => setSmall(media.matches);
    media.addEventListener('change', change);
    return () => media.removeEventListener('change', change);
  }, []);
  return small;
}
function WhaleGame() {
  const t = useWhaleText();
  const client = useQueryClient();
  const [busy, setBusy] = useState(false);
  const lock = useRef(false);
  const [feedback, setFeedback] = useState<{ message: string; error: boolean } | null>(null);
  const [tab, setTab] = useState<DeckTabId | null>(null);
  const [guestFocus, setGuestFocus] = useState<string>();
  const [settings, setSettings] = useState(false);
  const [menu, setMenu] = useState<MenuPresentationMode>(() => {
    try {
      return localStorage.getItem(menuPreference) === 'sheet' ? 'sheet' : 'drawer';
    } catch {
      return 'drawer';
    }
  });
  const lastTab = useRef<DeckTabId>('feed');
  const [spokenReply, setSpokenReply] = useState('');
  const [dark, setDark] = useState(() => document.documentElement.dataset.theme === 'dark');
  const [pure, setPure] = useState(false);
  const [bg, setBg] = useState<string | null>(null);
  const [view, setView] = useState<string | null>(null);
  const [departure, setDeparture] = useState<{ peer: NeighborStation; preset?: string } | null>(
    null,
  );
  const [confirmation, setConfirmation] = useState<{
    command: Action;
    title: string;
    description: ReactNode;
    confirmLabel: string;
    revision: string;
    profileRevision: number;
    error?: string;
  } | null>(null);
  const [impulse, setImpulse] = useState<RigImpulseEvent | null>(null);
  const nextImpulse = useRef(0);
  const small = useSmallViewport();
  const data = useQuery({
    queryKey: profileKey,
    queryFn: ({ signal }) => fetchProfile(signal),
    refetchInterval: busy ? false : 15000,
    retry: false,
  });
  const profile = data.data;
  const effectiveMenu = small ? 'sheet' : menu;
  const door = profile?.social.open ? view : null;
  const visit = useQuery({
    queryKey: ['user', 'games', 'whale-haven', 'house', door],
    queryFn: door ? ({ signal }) => fetchNeighborHouse(door, signal) : skipToken,
    refetchInterval: busy ? false : 15000,
    gcTime: 0,
    retry: false,
  });
  const viewing = visit.data?.house;
  function deck(id: DeckTabId) {
    lastTab.current = id;
    setGuestFocus(undefined);
    setTab(id);
    setSettings(false);
  }
  function closeDeck() {
    setTab(null);
    document.getElementById('whale-dock-' + lastTab.current)?.focus({ preventScroll: true });
  }
  useEffect(() => soundEngine.mount(), []);
  useEffect(() => {
    const close = (e: KeyboardEvent) => {
      if (
        e.key !== 'Escape' ||
        e.defaultPrevented ||
        document.querySelector('[role="alertdialog"]')
      )
        return;
      if (settings) setSettings(false);
      else if (tab) closeDeck();
      else if (pure) setPure(false);
    };
    window.addEventListener('keydown', close);
    return () => window.removeEventListener('keydown', close);
  }, [tab, settings, pure]);
  useEffect(() => {
    if (!feedback || feedback.error) return;
    const timer = window.setTimeout(() => setFeedback(null), 4500);
    return () => window.clearTimeout(timer);
  }, [feedback]);
  async function commit(
    perform: () => Promise<Profile>,
    message?: string,
    onError?: (reason: string) => void,
  ) {
    if (lock.current) return null;
    lock.current = true;
    setBusy(true);
    setFeedback(null);
    try {
      await client.cancelQueries({ queryKey: profileKey });
      const updated = await perform();
      client.setQueryData(profileKey, updated);
      void client.invalidateQueries({ queryKey: ['user', 'games', 'whale-haven', 'neighbors'] });
      if (door) void visit.refetch();
      void client.invalidateQueries({ queryKey: ['user', 'games', 'snapshot'] });
      if (message) setFeedback({ message, error: false });
      return updated;
    } catch (err) {
      const reason =
        err instanceof Error
          ? err.message
          : t('操作未完成，请刷新重试。', 'The action could not be completed. Refresh and retry.');
      if (onError) onError(reason);
      else setFeedback({ message: reason, error: true });
      void data.refetch();
      return null;
    } finally {
      lock.current = false;
      setBusy(false);
    }
  }
  function receipt(command: Action, previous: Profile) {
    const item = itemToUnlock(previous, command);
    if (item) {
      const unlocked =
        command.type === 'toggle_decor'
          ? t(`已解锁并布置${item.name}。`, `Unlocked and placed ${item.name}.`)
          : t(`已解锁并换上${item.name}。`, `Unlocked and equipped ${item.name}.`);
      return `${unlocked} ${unlockPriceText(previous, item.basePrice, t)}`;
    }
    switch (command.type) {
      case 'buy_feed': {
        const count = command.quantity ?? 1;
        const name = FEED_CATALOG[command.feedId].name;
        return t(`已购入 ${name} ×${count}。`, `Purchased ${name} ×${count}.`);
      }
      case 'harvest_pearls':
        return t('本次收益已入账。', 'Earnings collected.');
      case 'set_open':
        if (!command.open)
          return t('小筑已关闭，本次来往已结算。', 'Haven closed and visits settled.');
        break;
      case 'send_visit':
        return t('鲸鱼娘已出发。', 'Your whale has departed.');
      case 'recall_home':
        return t(
          '鲸鱼娘已回家，本次来往已结算。',
          'Your whale is home and the visit has been settled.',
        );
      case 'catch_guest':
        if (command.action === 'hire') return t('客鲸开始帮工了。', 'Your guest has started work.');
        if (command.action === 'treat')
          return t('已招待客鲸用餐。', 'You shared a meal with your guest.');
        return t(
          '客鲸已回家，本次来往已结算。',
          'Your guest is home and the visit has been settled.',
        );
      case 'spin_conch_gacha':
        return t(
          '海螺扭蛋已开启，本次奖励已领取。',
          'Conch draw complete. Your reward has been granted.',
        );
    }
  }
  async function dispatch(command: Action, onError?: (reason: string) => void) {
    if (!profile || lock.current) return false;
    if (command.type === 'interact') {
      const kind = command.zone === 'head' ? 'headpat' : 'poke';
      setImpulse({ seq: ++nextImpulse.current, kind });
      soundEngine.play(kind);
    }
    const prev = profile;
    const message = receipt(command, prev);
    const updated = await commit(() => act(prev, command), message, onError);
    if (updated) {
      if (
        prev.snapshot.interactionAffectionRemaining > 0 &&
        updated.snapshot.interactionAffectionRemaining === 0
      ) {
        const limit = t(
          '今天的三次互动羁绊奖励已领完，仍可继续互动。',
          'You earned all three interaction Bond XP awards today. You can keep playing.',
        );
        setFeedback({ message: message ? `${message} ${limit}` : limit, error: false });
      }
      if (command.type === 'chat' || command.type === 'interact')
        setSpokenReply(updated.snapshot.whale.tempEmotion?.line ?? '');
      const anim =
        command.type === 'use_feed'
          ? command.feedId === 'berry_teacake'
            ? 'teacake'
            : 'feed'
          : command.type === 'harvest_pearls'
            ? 'celebrate'
            : null;
      if (anim) setImpulse({ seq: ++nextImpulse.current, kind: anim });
      if (command.type !== 'interact')
        soundEngine.play(
          command.type === 'use_feed'
            ? 'feed_rice'
            : command.type === 'harvest_pearls'
              ? 'coin_harvest'
              : command.type === 'catch_guest'
                ? (('catch_' + command.action) as 'catch_gentle')
                : 'ui_click',
        );
      if (command.type === 'set_open' && !command.open) setView(null);
    }
    return !!updated;
  }
  function choose(command: Action) {
    if (!profile) return;
    const {
      settings: { values },
      snapshot,
    } = profile;
    const ask = (action: Action, title: string, description: ReactNode, confirmLabel: string) =>
      setConfirmation({
        command: action,
        title,
        description,
        confirmLabel,
        revision: profile.settings_revision,
        profileRevision: snapshot.revision,
      });
    if (command.type === 'use_feed' && command.feedId !== 'berry_teacake') {
      const { troughGrams, troughFeedType } = snapshot.home;
      const room = values.trough_grams - troughGrams;
      const serving = values['feed_' + command.feedId + '_grams'];
      const spill = Math.ceil(serving - room);
      // An occupied bowl rejects mixed staples and leaves its last 10 g of space alone.
      const canFill = troughGrams === 0 || (troughFeedType === command.feedId && room > 10);
      if (canFill && spill > 0 && snapshot.inventory[command.feedId] > 0) {
        ask(
          { ...command, maxSpillGrams: spill },
          t('饭碗装不下整份口粮', 'The whole serving won’t fit'),
          t(
            `这份口粮有 ${serving} 克，预计最多会浪费 ${spill} 克。继续添饭会用掉整份。`,
            `This serving weighs ${serving} g. Up to ${spill} g may spill, and the whole serving will be used.`,
          ),
          t('仍然添饭', 'Fill anyway'),
        );
      } else {
        void dispatch({ ...command, maxSpillGrams: 0 });
      }
      return;
    }
    const item = itemToUnlock(profile, command);
    if (item) {
      ask(
        command,
        t('解锁', 'Unlock ') + item.name,
        unlockPriceText(profile, item.basePrice, t),
        t('确认', 'Confirm'),
      );
      return;
    }
    let title: string, price: number, unit: string;
    if (command.type === 'buy_feed') {
      title =
        t('购入', 'Buy ') + FEED_CATALOG[command.feedId].name + ' ×' + (command.quantity ?? 1);
      price = values['feed_' + command.feedId + '_price_milli'];
      unit = t('游戏积分', 'game credits');
    } else if (command.type === 'spin_conch_gacha') {
      title = t('转动海螺扭蛋', 'Draw a conch reward');
      price = values.conch_cost_shards;
      unit = t('海螺碎片', 'conch shards');
    } else {
      void dispatch(command);
      return;
    }
    ask(
      command,
      title,
      <>
        {t('本次花费', 'Cost')}{' '}
        {command.type === 'buy_feed' ? (
          <Amount value={String(BigInt(price) * BigInt(command.quantity ?? 1))} unit={unit} />
        ) : (
          price + ' ' + unit
        )}
      </>,
      t('确认', 'Confirm'),
    );
  }
  const stageHost = useRef<HTMLDivElement>(null);
  const adopted = profile?.snapshot.whale.hasCompletedNaming;
  useLayoutEffect(() => {
    const host = stageHost.current;
    if (!host || !adopted) return;
    const resize = () => {
      const top = host.getBoundingClientRect().top + window.scrollY;
      host.style.setProperty(
        '--whale-stage-height',
        Math.min(860, Math.max(440, window.innerHeight - top - 12)) + 'px',
      );
    };
    resize();
    const observer = new ResizeObserver(resize);
    if (host.previousElementSibling) observer.observe(host.previousElementSibling);
    window.addEventListener('resize', resize);
    return () => {
      observer.disconnect();
      window.removeEventListener('resize', resize);
    };
  }, [adopted]);
  const snap = profile?.snapshot;

  const header = (
    <PageHeader
      back={<GameBackLink />}
      title={t('鲸鱼湾小筑', 'Whale Haven')}
      description={
        t('', 'The original character dialogue and artwork are in Chinese.') || undefined
      }
      actions={
        snap ? (
          <dl className="game-wallets">
            <div>
              <dt>{t('游戏积分', 'Game credits')}</dt>
              <dd>
                <Amount value={String(snap.wallet.gameMilli)} unit="" />
              </dd>
            </div>
            <div>
              <dt>{t('通用积分', 'General credits')}</dt>
              <dd>
                <Amount value={String(snap.wallet.generalMilli)} unit="" />
              </dd>
            </div>
          </dl>
        ) : undefined
      }
    />
  );
  if (!profile || !snap)
    return (
      <main className="game-page">
        {header}
        {data.error ? (
          <ErrorState error={data.error} onRetry={() => void data.refetch()} />
        ) : (
          <LoadingState />
        )}
      </main>
    );
  if (!profile.settings.enabled)
    return (
      <main className="game-page">
        {header}
        <p>{t('鲸鱼湾暂未开放。', 'Whale Haven is closed.')}</p>
      </main>
    );
  const values = profile.settings.values;
  function toggleMenu() {
    const next = menu === 'drawer' ? 'sheet' : 'drawer';
    setMenu(next);
    try {
      localStorage.setItem(menuPreference, next);
    } catch {
      // The selected position still applies for this visit.
    }
    deck(tab ?? lastTab.current);
  }
  function preview(peer: NeighborStation) {
    setView(peer.id);
    setTab(null);
  }
  return (
    <main className="game-page whale-page">
      {header}
      <div ref={stageHost} className="whale-haven" data-whale-theme={dark ? 'dark' : 'light'}>
        {!snap.whale.hasCompletedNaming ? (
          <WhaleNamingModal
            initialName={snap.whale.name}
            busy={busy}
            onConfirmName={(name, open) => void dispatch({ type: 'adopt', name, open })}
          />
        ) : door && (visit.isError || !visit.data) ? (
          <section className="card">
            {visit.isError ? (
              <ErrorState error={visit.error} onRetry={() => void visit.refetch()} />
            ) : (
              <LoadingState />
            )}
            <button
              type="button"
              className="nb-btn"
              onClick={() => {
                setView(null);
                setTab(null);
              }}
            >
              {t('回到我的小筑', 'Return to my haven')}
            </button>
          </section>
        ) : (
          <OceanStage
            snapshot={snap}
            social={profile.social}
            values={values}
            neighborHouse={door ? visit.data : undefined}
            rigImpulse={impulse}
            faceCalibration={DEFAULT_FACE_CALIBRATION}
            showBonesDebug={false}
            rigReloadToken={0}
            theme={dark ? 'dark' : 'light'}
            pureView={pure}
            isMobileViewport={small}
            customBgUrl={bg}
            menuMode={effectiveMenu}
            isDeckOpen={!!tab && !settings}
            settingsOpen={settings}
            onToggleTheme={() => setDark(!dark)}
            onTogglePureView={() => setPure(!pure)}
            onChangeCustomBg={setBg}
            onToggleMenuMode={toggleMenu}
            onCloseDeck={closeDeck}
            onCloseSettings={() => setSettings(false)}
            onOpenWardrobe={() => deck('wardrobe')}
            onOpenFeed={() => deck('feed')}
            onReturnHome={() => {
              setView(null);
              setTab(null);
            }}
            onViewNeighbor={(id) => {
              const p = snap.neighbors.find((n) => n.id === id);
              if (p) preview(p);
              else deck('social');
            }}
            onTogglePinNeighbor={(neighborId) =>
              void dispatch({ type: 'toggle_pin_neighbor', neighborId })
            }
            onOpenNeighborSearch={() => deck('social')}
            onRandomVisitView={() => deck('social')}
            onInteractWhale={(zone, streak) => {
              void dispatch({ type: 'interact', zone, streak });
              return zone === 'head' ? 'cheerful' : 'pout';
            }}
            onChatWithWhale={() => void dispatch({ type: 'chat' })}
            onHarvestPearls={() => void dispatch({ type: 'harvest_pearls' })}
            onQuickRefill={() => {
              const id =
                snap.home.troughGrams > 0
                  ? snap.home.troughFeedType
                  : snap.inventory.silver_fish > 0
                    ? 'silver_fish'
                    : 'sweet_shrimp';
              if (snap.inventory[id] > 0) choose({ type: 'use_feed', feedId: id });
              else deck('feed');
            }}
            onToggleSleep={(forceSleep) => void dispatch({ type: 'toggle_sleep', forceSleep })}
            onOpenGuestCatch={(id) => {
              deck('social');
              setGuestFocus(id);
            }}
            onSendWhaleHere={(id, preset) => {
              if (viewing?.id === id) setDeparture({ peer: viewing, preset });
            }}
            onRecallWhale={() => void dispatch({ type: 'recall_home' })}
            onRenameWhale={(name) => void dispatch({ type: 'rename', name })}
            dockSlot={
              <FloatingDock
                profile={profile}
                activeTab={settings ? null : tab}
                isHome={!viewing}
                settingsOpen={settings}
                onHome={() => {
                  setView(null);
                  setTab(null);
                  setSettings(false);
                }}
                onToggleSettings={() => setSettings(!settings)}
                onSelectTab={(id) => {
                  if (id === tab && !settings) closeDeck();
                  else deck(id);
                }}
              />
            }
            drawerSlot={
              tab && (
                <ControlDeck
                  activeTab={tab}
                  focusTarget={
                    tab === 'social' && guestFocus ? 'whale-guest-' + guestFocus : undefined
                  }
                  menuMode={effectiveMenu}
                  hidden={settings}
                  onClose={closeDeck}
                  onToggleMenuMode={toggleMenu}
                >
                  {tab === 'feed' && (
                    <Backpack
                      snapshot={snap}
                      values={values}
                      onBuyFeed={(feedId, quantity) =>
                        choose({ type: 'buy_feed', feedId, quantity })
                      }
                      onUseFeed={(feedId) => choose({ type: 'use_feed', feedId })}
                      onToggleDecor={(decorId) => choose({ type: 'toggle_decor', decorId })}
                      onToggleAutoRefill={() => choose({ type: 'toggle_auto_refill' })}
                      onActivateAroma={() => choose({ type: 'activate_aroma' })}
                      onSpinConchGacha={() => choose({ type: 'spin_conch_gacha' })}
                    />
                  )}
                  {tab === 'wardrobe' && <WardrobePane profile={profile} dispatch={choose} />}
                  {tab === 'social' && (
                    <SocialPane
                      profile={profile}
                      busy={busy}
                      dispatch={dispatch}
                      onActivity={() => deck('logs')}
                      onWallet={() => deck('exchange')}
                      onRefresh={() => {
                        void data.refetch();
                        if (door) void visit.refetch();
                      }}
                      refreshing={data.isFetching || visit.isFetching}
                      onPreview={preview}
                      onDepart={(peer) => setDeparture({ peer })}
                    />
                  )}
                  {tab === 'exchange' && (
                    <>
                      <ExchangePane
                        profile={profile}
                        busy={busy}
                        onRefresh={() => void data.refetch()}
                        onCollect={() => void dispatch({ type: 'harvest_pearls' })}
                        onExchange={async (q, onError) =>
                          !!(await commit(
                            () => exchange(q),
                            t(
                              '兑换成功，通用积分已到账。',
                              'Exchange complete. General credits added.',
                            ),
                            onError,
                          ))
                        }
                      />
                      <DecorPane profile={profile} dispatch={choose} />
                    </>
                  )}
                  {tab === 'logs' && (
                    <>
                      <ActivityPane snapshot={snap} />
                      <Link to="/credits?category=whale_haven">
                        {t('查看小筑积分明细', 'View Whale Haven credit history')}
                      </Link>
                    </>
                  )}
                </ControlDeck>
              )
            }
          />
        )}
        <span className="sr-only" aria-live="polite" aria-atomic="true">
          {spokenReply}
        </span>
        {feedback && (
          <div
            className={'wh-feedback' + (feedback.error ? ' is-error' : '')}
            role={feedback.error ? 'alert' : 'status'}
          >
            <span>{feedback.message}</span>
            <button onClick={() => setFeedback(null)} aria-label={t('关闭提示', 'Dismiss message')}>
              ×
            </button>
          </div>
        )}
      </div>
      {departure && (
        <DepartureDialog
          neighbor={departure.peer}
          initialMessage={departure.preset}
          dailyWageLimitReached={
            values.guest_wage_bp > 0 &&
            snap.wallet.dailySocialPearlsGained >= values.daily_social_pearls
          }
          busy={busy}
          dispatch={dispatch}
          onClose={() => setDeparture(null)}
        />
      )}
      {confirmation && (
        <ConfirmDialog
          open
          title={confirmation.title}
          description={confirmation.description}
          confirmLabel={confirmation.confirmLabel}
          busy={busy}
          confirmDisabled={
            confirmation.revision !== profile.settings_revision ||
            confirmation.profileRevision !== snap.revision
          }
          onCancel={() => setConfirmation(null)}
          onConfirm={async () => {
            if (
              await dispatch(confirmation.command, (error) =>
                setConfirmation({ ...confirmation, error }),
              )
            )
              setConfirmation(null);
          }}
        >
          {confirmation.error && !busy && <p role="alert">{confirmation.error}</p>}
          {(confirmation.revision !== profile.settings_revision ||
            confirmation.profileRevision !== snap.revision) && (
            <p>
              {t(
                '小筑规则或存档已变动，请取消后重新选择。',
                'The haven rules or your saved game changed. Cancel and choose again.',
              )}
            </p>
          )}
        </ConfirmDialog>
      )}
    </main>
  );
}
export function WhaleHavenPage() {
  return (
    <UserPageGate>
      <WhaleGame />
    </UserPageGate>
  );
}
