import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router';
import { Amount } from '@shared/components/Amount';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { ErrorState, LoadingState, PageHeader } from '@shared/components/States';
import { GameBackLink } from '../common/GameBackLink';
import { UserPageGate } from '../../components/UserPageGate';
import { act, exchange, fetchProfile, costWithCoupon, type Action, type Profile } from './api';
import { soundEngine } from './audio/soundEngine';
import { WhaleNamingModal } from './components/WhaleNamingModal';
import { ControlDeck, FloatingDock, type DeckTabId } from './components/ControlDeck';
import { OceanStage, type MenuPresentationMode } from './components/OceanStage';
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
  const { i18n } = useTranslation();
  const client = useQueryClient();
  const [busy, setBusy] = useState(false);
  const lock = useRef(false);
  const [feedback, setFeedback] = useState<{ message: string; error: boolean } | null>(null);
  const [tab, setTab] = useState<DeckTabId | null>(null);
  const [settings, setSettings] = useState(false);
  const [menu, setMenu] = useState<MenuPresentationMode>('drawer');
  const [dark, setDark] = useState(() => document.documentElement.dataset.theme === 'dark');
  const [pure, setPure] = useState(false);
  const [bg, setBg] = useState<string | null>(null);
  const [view, setView] = useState<NeighborStation | null>(null);
  const [departure, setDeparture] = useState<{ peer: NeighborStation; preset?: string } | null>(
    null,
  );
  const [purchase, setPurchase] = useState<{
    command: Action;
    title: string;
    price: string;
    unit: string;
    revision: string;
    profileRevision: number;
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
  const viewing = profile?.social.open ? view : null;
  function deck(t: DeckTabId) {
    setTab(t);
    setSettings(false);
  }
  const closeDeck = () => setTab(null);
  useEffect(() => soundEngine.mount(), []);
  useEffect(() => {
    const close = (e: KeyboardEvent) => {
      if (
        e.key !== 'Escape' ||
        e.defaultPrevented ||
        document.querySelector('[role="alertdialog"]')
      )
        return;
      if (tab) setTab(null);
      else if (settings) setSettings(false);
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
  async function commit(perform: () => Promise<Profile>, message: string) {
    if (lock.current) return false;
    lock.current = true;
    setBusy(true);
    try {
      await client.cancelQueries({ queryKey: profileKey });
      const updated = await perform();
      client.setQueryData(profileKey, updated);
      void client.invalidateQueries({ queryKey: ['user', 'games', 'whale-haven', 'neighbors'] });
      void client.invalidateQueries({ queryKey: ['user', 'games', 'snapshot'] });
      setFeedback({ message, error: false });
      return true;
    } catch (err) {
      setFeedback({
        message: err instanceof Error ? err.message : '操作未完成，请刷新重试。',
        error: true,
      });
      void data.refetch();
      return false;
    } finally {
      lock.current = false;
      setBusy(false);
    }
  }
  async function dispatch(command: Action) {
    if (!profile) return false;
    const prev = profile;
    const success = await commit(
      () => act(prev, command),
      (
        {
          adopt: '鲸鱼娘到家啦！',
          buy_feed: '已购入口粮。',
          use_feed: '已投喂。',
          harvest_pearls: '小鲸币已收取。',
          set_open: '小筑开放设置已保存。',
          send_visit: '鲸鱼娘已出发。',
          recall_home: '鲸鱼娘已回家，本次来往已结算。',
          catch_guest: '客鲸状态已更新。',
          spin_conch_gacha: '扭蛋已开启，奖励已放入背包。',
          rename: '新名字已保存。',
        } as Partial<Record<Action['type'], string>>
      )[command.type] ?? '已更新。',
    );
    if (success) {
      const anim =
        command.type === 'interact'
          ? command.zone === 'head'
            ? 'headpat'
            : 'poke'
          : command.type === 'use_feed'
            ? command.feedId === 'berry_teacake'
              ? 'teacake'
              : 'feed'
            : command.type === 'harvest_pearls'
              ? 'celebrate'
              : null;
      if (anim) setImpulse({ seq: ++nextImpulse.current, kind: anim });
      soundEngine.play(
        command.type === 'use_feed'
          ? 'feed_rice'
          : command.type === 'harvest_pearls'
            ? 'coin_harvest'
            : command.type === 'catch_guest'
              ? (('catch_' + command.action) as 'catch_gentle')
              : command.type === 'interact'
                ? command.zone === 'head'
                  ? 'headpat'
                  : 'poke'
                : 'ui_click',
      );
      if (command.type === 'set_open' && !command.open) setView(null);
    }
    return success;
  }
  function choose(command: Action) {
    if (!profile) return;
    const {
      settings: { values },
      snapshot,
    } = profile;
    let title: string,
      price: number,
      unit = '小鲸币';
    if (command.type === 'buy_feed') {
      title = '购入' + FEED_CATALOG[command.feedId].name;
      price = values['feed_' + command.feedId + '_price_milli'];
      unit = '游戏积分';
    } else if (
      command.type === 'select_outfit' &&
      command.outfitId !== 'maid_classic' &&
      !snapshot.home.ownedOutfits?.includes(command.outfitId)
    ) {
      title = '解锁' + OUTFIT_CATALOG[command.outfitId].name;
      price = costWithCoupon(profile, values['outfit_' + command.outfitId + '_price_pearls']);
    } else if (
      command.type === 'toggle_decor' &&
      !snapshot.home.ownedDecor.includes(command.decorId)
    ) {
      title = '解锁' + DECOR_CATALOG[command.decorId].name;
      price = costWithCoupon(profile, values['decor_' + command.decorId + '_price_pearls']);
    } else if (command.type === 'spin_conch_gacha') {
      title = '转动海螺扭蛋';
      price = values.conch_cost_shards;
      unit = '海螺碎片';
    } else {
      void dispatch(command);
      return;
    }
    setPurchase({
      command,
      title,
      price: String(price),
      unit,
      revision: profile.settings_revision,
      profileRevision: snapshot.revision,
    });
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
  const showIntro = i18n.language.startsWith('en');
  const header = (
    <PageHeader
      back={<GameBackLink />}
      title={showIntro ? 'Whale Haven' : '鲸鱼湾小筑'}
      description={showIntro ? 'Original game in Chinese.' : undefined}
      actions={
        snap ? (
          <dl className="game-wallets">
            <div>
              <dt>游戏积分</dt>
              <dd>
                <Amount value={String(snap.wallet.gameMilli)} unit="" />
              </dd>
            </div>
            <div>
              <dt>通用积分</dt>
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
        <p>鲸鱼湾暂未开放。</p>
      </main>
    );
  const values = profile.settings.values;
  const toggleMenu = () => setMenu((prev) => (prev === 'drawer' ? 'sheet' : 'drawer'));
  function preview(peer: NeighborStation) {
    setView(peer);
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
        ) : (
          <OceanStage
            snapshot={snap}
            values={values}
            viewingNeighbor={viewing}
            rigImpulse={impulse}
            faceCalibration={DEFAULT_FACE_CALIBRATION}
            showBonesDebug={false}
            rigReloadToken={0}
            theme={dark ? 'dark' : 'light'}
            pureView={pure}
            isMobileViewport={small}
            customBgUrl={bg}
            menuMode={effectiveMenu}
            isDeckOpen={!!tab}
            settingsOpen={settings}
            onToggleTheme={() => setDark(!dark)}
            onTogglePureView={() => setPure(!pure)}
            onChangeCustomBg={setBg}
            onChangeMenuMode={setMenu}
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
              if (snap.inventory[id] > 0) void dispatch({ type: 'use_feed', feedId: id });
              else deck('feed');
            }}
            onToggleSleep={(forceSleep) => void dispatch({ type: 'toggle_sleep', forceSleep })}
            onOpenGuestCatch={() => deck('social')}
            onSendWhaleHere={(id, preset) => {
              if (viewing?.id === id) setDeparture({ peer: viewing, preset });
            }}
            onRecallWhale={() => void dispatch({ type: 'recall_home' })}
            onRenameWhale={(name) => void dispatch({ type: 'rename', name })}
            dockSlot={
              <FloatingDock
                profile={profile}
                activeTab={tab}
                isHome={!viewing}
                settingsOpen={settings}
                onHome={() => {
                  setView(null);
                  setTab(null);
                  setSettings(false);
                }}
                onToggleSettings={() => {
                  setTab(null);
                  setSettings(!settings);
                }}
                onSelectTab={(id) => {
                  setTab(id === tab ? null : id);
                  setSettings(false);
                }}
              />
            }
            drawerSlot={
              tab && (
                <ControlDeck
                  activeTab={tab}
                  menuMode={effectiveMenu}
                  onClose={closeDeck}
                  onToggleMenuMode={toggleMenu}
                >
                  {tab === 'feed' && (
                    <Backpack
                      snapshot={snap}
                      values={values}
                      onBuyFeed={(feedId) => choose({ type: 'buy_feed', feedId })}
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
                      onPreview={preview}
                      onDepart={(peer) => setDeparture({ peer })}
                    />
                  )}
                  {tab === 'exchange' && (
                    <>
                      <ExchangePane
                        profile={profile}
                        busy={busy}
                        onExchange={(q) => commit(() => exchange(q), '兑换成功，通用积分已到账。')}
                      />
                      <DecorPane profile={profile} dispatch={choose} />
                    </>
                  )}
                  {tab === 'logs' && (
                    <>
                      <ActivityPane snapshot={snap} />
                      <Link to="/credits">查看积分明细</Link>
                    </>
                  )}
                </ControlDeck>
              )
            }
          />
        )}
        {feedback && (
          <div
            className={'wh-feedback' + (feedback.error ? ' is-error' : '')}
            role={feedback.error ? 'alert' : 'status'}
          >
            <span>{feedback.message}</span>
            <button onClick={() => setFeedback(null)} aria-label="关闭提示">
              ×
            </button>
          </div>
        )}
      </div>
      {departure && (
        <DepartureDialog
          neighbor={departure.peer}
          initialMessage={departure.preset}
          busy={busy}
          dispatch={dispatch}
          onClose={() => setDeparture(null)}
        />
      )}
      {purchase && (
        <ConfirmDialog
          open
          title={purchase.title}
          description={
            <>
              本次花费{' '}
              {purchase.unit === '游戏积分' ? (
                <Amount value={purchase.price} unit="游戏积分" />
              ) : (
                purchase.price + ' ' + purchase.unit
              )}
              。
            </>
          }
          confirmLabel="确认"
          busy={busy}
          confirmDisabled={
            purchase.revision !== profile.settings_revision ||
            purchase.profileRevision !== snap.revision
          }
          onCancel={() => setPurchase(null)}
          onConfirm={async () => {
            await dispatch(purchase.command);
            setPurchase(null);
          }}
        >
          {(purchase.revision !== profile.settings_revision ||
            purchase.profileRevision !== snap.revision) && (
            <p>价格或存档已变动，请取消后重新选择。</p>
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
