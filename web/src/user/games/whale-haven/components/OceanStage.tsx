import { useEffect, useRef, useState, type ReactNode } from 'react';
import {
  BellRing,
  Camera,
  ChevronDown,
  Compass,
  Dices,
  Edit3,
  Gem,
  HelpCircle,
  Home,
  Image as ImageIcon,
  MessageCircle,
  Moon,
  Plus,
  Send,
  ShieldAlert,
  Shirt,
  Soup,
  Star,
  Sun,
  Trash2,
  Upload,
  Volume2,
  VolumeX,
  Waves,
} from './Icons';
import { soundEngine, type AudioSettings } from '../audio/soundEngine';
import {
  DECOR_CATALOG,
  FEED_CATALOG,
  OUTFIT_CATALOG,
  PRESET_MESSAGES,
  displayNeighborName,
  formatNeighborHandles,
  getAffectionLevel,
  getSceneAssetUrl,
  pickRandomCuteWhaleName,
} from '../game/catalog';
import { getActiveGuests, resolveStageConversations, resolveWhaleExpression } from '../game/scene';
import type { GameSnapshot, NeighborStation, WhaleExpression } from '../game/types';
import { SleepingBedCanvas } from '../pet/SleepingBedCanvas';
import { WhalePetActor } from '../pet/WhalePetActor';

import type { FaceCalibrationConfig } from '../rig/calibrator';
import { WhaleRigActor, type RigImpulseEvent } from '../rig/WhaleRigActor';
import { formatMilliCredits, type Profile } from '../api';
import { UiSwitch } from './UiMotifs';
import { MenuPositionButton, type MenuPresentationMode } from './ControlDeck';

import { guestVisitLabel, useWhaleText } from '../copy';

const CABIN_FURNITURE = ['shell_cabinet', 'pearl_clock', 'sea_planter'] as const;

interface OceanStageProps {
  values: Record<string, number>;
  snapshot: GameSnapshot;
  social: Profile['social'];
  viewingNeighbor: NeighborStation | null;
  rigImpulse: RigImpulseEvent | null;
  faceCalibration: FaceCalibrationConfig;
  showBonesDebug: boolean;
  rigReloadToken: number;
  theme: 'light' | 'dark';
  pureView: boolean;
  isMobileViewport: boolean;
  customBgUrl: string | null;
  menuMode: MenuPresentationMode;
  isDeckOpen: boolean;
  settingsOpen: boolean;
  dockSlot?: ReactNode;
  drawerSlot?: ReactNode;
  onToggleTheme: () => void;

  onTogglePureView: () => void;
  onChangeCustomBg: (dataUrl: string | null) => void;
  onToggleMenuMode: () => void;
  onCloseDeck: () => void;
  onCloseSettings: () => void;

  onOpenWardrobe: () => void;
  onOpenFeed: () => void;
  onReturnHome: () => void;
  onViewNeighbor: (neighborId: string) => void;
  onTogglePinNeighbor: (neighborId: string) => void;
  onOpenNeighborSearch: () => void;
  onRandomVisitView: () => void;
  onInteractWhale: (zone: 'head' | 'body', pokeStreak: number) => WhaleExpression;
  onChatWithWhale?: () => void;
  onHarvestPearls: () => void;
  onQuickRefill: () => void;
  onToggleSleep?: (forceSleep?: boolean) => void;
  onOpenGuestCatch: (neighborId?: string) => void;
  onSendWhaleHere: (neighborId: string, presetId?: string) => void;
  onRecallWhale: () => void;
  onRenameWhale: (newName: string) => void;
  // Sandbox fast-test callbacks
}

const BUBBLES = [
  { left: '8%', size: 10, dur: '8.2s', delay: '0s' },
  { left: '19%', size: 15, dur: '9.8s', delay: '1.6s' },
  { left: '31%', size: 8, dur: '7.1s', delay: '3.2s' },
  { left: '46%', size: 13, dur: '8.8s', delay: '0.7s' },
  { left: '61%', size: 11, dur: '7.9s', delay: '2.4s' },
  { left: '76%', size: 16, dur: '10.2s', delay: '1.1s' },
  { left: '89%', size: 9, dur: '7.4s', delay: '3.9s' },
];

export function OceanStage({
  values,
  snapshot,
  social,
  viewingNeighbor,
  rigImpulse,
  faceCalibration,
  showBonesDebug,
  rigReloadToken,
  theme,
  pureView,
  isMobileViewport,
  customBgUrl,
  menuMode,
  isDeckOpen,
  settingsOpen,
  dockSlot,
  drawerSlot,
  onToggleTheme,

  onTogglePureView,
  onChangeCustomBg,
  onToggleMenuMode,
  onCloseDeck,
  onCloseSettings,

  onOpenWardrobe,
  onOpenFeed,
  onReturnHome,
  onViewNeighbor,
  onTogglePinNeighbor,
  onOpenNeighborSearch,
  onRandomVisitView,
  onInteractWhale,
  onChatWithWhale,
  onHarvestPearls,
  onQuickRefill,
  onToggleSleep,
  onOpenGuestCatch,
  onSendWhaleHere,
  onRecallWhale,
  onRenameWhale,
}: OceanStageProps) {
  const t = useWhaleText();
  const { whale, home } = snapshot;
  const activeGuests = getActiveGuests(snapshot);
  const sneakingGuests = activeGuests.filter((g) => g.stage === 'eating');
  const workingGuests = activeGuests.filter((g) => g.stage === 'working');
  const [animClass, setAnimClass] = useState<'is-headpat' | 'is-poked' | ''>('');
  const [fxSymbol, setFxSymbol] = useState<{ key: number; icon: string } | null>(null);
  const [statusPopoverOpen, setStatusPopoverOpen] = useState(false);

  const [bgPopoverOpen, setBgPopoverOpen] = useState(false);
  const [audioPopoverOpen, setAudioPopoverOpen] = useState(false);
  const [audioSettings, setAudioSettings] = useState<AudioSettings>(() =>
    soundEngine.getSettings(),
  );
  const [helpOpen, setHelpOpen] = useState(false);
  const [lastSettings, setLastSettings] = useState(settingsOpen);
  if (lastSettings !== settingsOpen) {
    setLastSettings(settingsOpen);
    if (settingsOpen) {
      setStatusPopoverOpen(false);
    } else {
      setBgPopoverOpen(false);
      setAudioPopoverOpen(false);
      setHelpOpen(false);
    }
  }
  const [editingName, setEditingName] = useState(false);
  const [draftName, setDraftName] = useState(whale.name);
  const [stageWidth, setStageWidth] = useState(() =>
    typeof window !== 'undefined' ? Math.min(window.innerWidth, 1120) : 1120,
  );
  const [banterState, setBanterState] = useState<{ seed: number; until: number }>({
    seed: 0,
    until: 0,
  });
  const [sleepCanvasReady, setSleepCanvasReady] = useState(false);
  const [sleepWalkPhase, setSleepWalkPhase] = useState<
    'none' | 'walking_to_bed' | 'spawning_at_bed' | 'walking_to_rug'
  >('none');
  const sleepTimerRef = useRef<number | null>(null);

  const stageRef = useRef<HTMLDivElement | null>(null);
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const pokeStreakRef = useRef<{ count: number; lastAt: number }>({ count: 0, lastAt: 0 });

  const visitPresets = PRESET_MESSAGES.filter((m) => m.category === 'visit');
  const [visitPresetId, setVisitPresetId] = useState<string>(visitPresets[0]?.id ?? '');

  useEffect(() => {
    const el = stageRef.current;
    if (!el || typeof ResizeObserver === 'undefined') return;
    const ro = new ResizeObserver((entries) => {
      const w = entries[0]?.contentRect.width;
      if (w && w > 0) setStageWidth(w);
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  useEffect(() => {
    return () => {
      if (sleepTimerRef.current) window.clearTimeout(sleepTimerRef.current);
    };
  }, []);

  const isSleepingInBed =
    !viewingNeighbor &&
    whale.location.kind === 'home' &&
    Boolean(whale.isSleeping) &&
    sleepWalkPhase === 'none';

  const triggerSleepOrWake = () => {
    if (viewingNeighbor || whale.location.kind !== 'home') {
      onToggleSleep?.();
      return;
    }
    if (sleepWalkPhase !== 'none') return;

    if (whale.isSleeping) {
      // Wake up: swap bed back to empty, spawn whale beside bed and waddle back to center rug!
      onToggleSleep?.(false);
      setSleepWalkPhase('spawning_at_bed');
      requestAnimationFrame(() => {
        requestAnimationFrame(() => {
          setSleepWalkPhase('walking_to_rug');
          if (sleepTimerRef.current) window.clearTimeout(sleepTimerRef.current);
          sleepTimerRef.current = window.setTimeout(() => {
            setSleepWalkPhase('none');
          }, 880);
        });
      });
    } else {
      if (whale.satiety <= 15 && home.troughGrams <= 0) {
        onToggleSleep?.(true);
        return;
      }
      // Walk from center rug over to the bed on tiny stubby feet, then hop into 24fps bed sleep!
      setSleepWalkPhase('walking_to_bed');
      if (sleepTimerRef.current) window.clearTimeout(sleepTimerRef.current);
      sleepTimerRef.current = window.setTimeout(() => {
        setSleepWalkPhase('none');
        onToggleSleep?.(true);
      }, 920);
    }
  };

  const [nowMs, setNowMs] = useState(() => Date.now());
  useEffect(() => {
    const timer = window.setInterval(() => {
      if (!document.hidden) setNowMs(Date.now());
    }, 1000);
    return () => window.clearInterval(timer);
  }, []);
  const workingAway = !!social.away?.working;
  const resolved = resolveWhaleExpression(snapshot, nowMs, values, workingAway);
  const stageTalk = resolveStageConversations(
    snapshot,
    resolved.speech,
    nowMs,
    banterState.seed,
    banterState.until,
  );
  const triggerManualBanter = () => {
    soundEngine.play('chat_bubble');
    setBanterState((prev) => ({ seed: prev.seed + 1, until: Date.now() + 12_000 }));
  };
  const affectionInfo = getAffectionLevel(whale.affection, values.affection_per_level);

  const maxPearls = home.equippedDecor.includes('golden_clam')
    ? values.bank_upgraded_pearls
    : values.bank_base_pearls;

  const activeDecor = viewingNeighbor ? viewingNeighbor.equippedDecor : home.equippedDecor;
  const currentOutfitMeta = OUTFIT_CATALOG[whale.outfit ?? 'maid_classic'];
  const renderMode = whale.renderMode ?? 'pet';
  const isProceduralSea = customBgUrl === '__procedural__';
  const hasCustomUploadBg = Boolean(customBgUrl && !isProceduralSea);
  const effectiveBgUrl = isProceduralSea
    ? null
    : customBgUrl ||
      getSceneAssetUrl(
        isMobileViewport ? 'backgrounds/cabin-mobile.webp' : 'backgrounds/cabin-desktop.webp',
      );
  const isIllustratedCabin = Boolean(effectiveBgUrl);

  const whaleCoinIconUrl = getSceneAssetUrl('props/whale-coin.webp');
  const piggyBankImgUrl = getSceneAssetUrl(
    home.equippedDecor.includes('golden_clam')
      ? DECOR_CATALOG.golden_clam.artwork
      : home.unclaimedPearls > 0
        ? 'props/piggybank-ready.webp'
        : 'props/piggybank-normal.webp',
  );
  const homeBowlImgUrl = getSceneAssetUrl(
    home.troughGrams <= 0
      ? 'props/rice-bowl-empty.webp'
      : home.troughFeedType === 'sweet_shrimp'
        ? 'props/rice-bowl-token.webp'
        : 'props/rice-bowl-full.webp',
  );
  const neighborBowlImgUrl = viewingNeighbor
    ? getSceneAssetUrl(
        viewingNeighbor.troughGrams <= 0
          ? 'props/rice-bowl-empty.webp'
          : viewingNeighbor.troughFeedType === 'sweet_shrimp'
            ? 'props/rice-bowl-token.webp'
            : 'props/rice-bowl-full.webp',
      )
    : homeBowlImgUrl;

  // Dynamically scale actor & roam distance with the stage width (including narrow 360~430px phones)
  const isNarrowPhone = stageWidth < 400;
  const isPhoneStage = stageWidth < 480;
  const isCompactStage = stageWidth < 680;
  const mainWhaleWidth = isNarrowPhone
    ? 156
    : isPhoneStage
      ? 172
      : isCompactStage
        ? 210
        : stageWidth >= 1180
          ? 286
          : 262;
  const guestWhaleWidth = isNarrowPhone ? 114 : isPhoneStage ? 126 : isCompactStage ? 152 : 196;
  const roamRangePx =
    activeGuests.length > 0
      ? isPhoneStage
        ? 14
        : 24
      : isPhoneStage
        ? Math.max(20, Math.min(48, Math.round(stageWidth * 0.1)))
        : Math.max(36, Math.min(180, Math.round(stageWidth * 0.15)));

  const handleZoneClick = (zone: 'head' | 'body') => {
    const now = Date.now();
    let streak = 1;
    if (zone === 'body') {
      if (now - pokeStreakRef.current.lastAt < 3800) {
        streak = pokeStreakRef.current.count + 1;
      }
      pokeStreakRef.current = { count: streak, lastAt: now };
    } else {
      pokeStreakRef.current = { count: 0, lastAt: now };
    }

    const reaction = onInteractWhale(zone, streak);
    if (renderMode === 'sprite') {
      setAnimClass('');
      requestAnimationFrame(() => {
        setAnimClass(zone === 'head' ? 'is-headpat' : 'is-poked');
      });
    }
    const icon =
      zone === 'head' ? '💖' : reaction === 'pout' ? '💢' : reaction === 'teary' ? '💧' : '💦';
    setFxSymbol({ key: now, icon });
  };

  const handleBgUpload = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (!file) return;
    const reader = new FileReader();
    reader.onload = () => {
      if (typeof reader.result === 'string') {
        onChangeCustomBg(reader.result);
        setBgPopoverOpen(false);
      }
    };
    reader.readAsDataURL(file);
    e.target.value = '';
  };

  const closeFloatingPopovers = () => {
    if (statusPopoverOpen) setStatusPopoverOpen(false);

    if (bgPopoverOpen) setBgPopoverOpen(false);
    if (audioPopoverOpen) setAudioPopoverOpen(false);
    if (helpOpen) setHelpOpen(false);
  };

  const isOurWhaleAtThisNeighbor =
    viewingNeighbor !== null &&
    whale.location.kind === 'visiting' &&
    whale.location.neighborId === viewingNeighbor.id;
  const awayStatus = t(resolved.statusLabel, workingAway ? 'Working away' : 'Visiting a neighbor');
  const awayDetail =
    whale.location.kind === 'visiting' &&
    (workingAway
      ? t(
          `工资按帮工产出的 ${values.guest_wage_bp / 100}% 结算`,
          `Wages are ${values.guest_wage_bp / 100}% of your whale’s production`,
        )
      : t(
          `已吃 ${Math.floor(whale.location.eatenGrams ?? 0)} 克 · 餐食奖励预计 ${whale.location.pendingPearls ?? 0} 枚小鲸币`,
          `${Math.floor(whale.location.eatenGrams ?? 0)} g eaten · Estimated meal reward: ${whale.location.pendingPearls ?? 0} whale coins`,
        ));

  const satietyColor =
    whale.satiety >= 60 ? '#44d492' : whale.satiety >= 30 ? '#f6b84c' : '#ff6b7a';

  const isGoldenClam = home.equippedDecor.includes('golden_clam');
  const troughRatio = Math.max(0, Math.min(1, home.troughGrams / values.trough_grams));

  return (
    <section
      ref={stageRef}
      className={`ocean-frame ocean-stage ${isIllustratedCabin ? 'has-custom-bg is-cabin-scene' : ''} ${
        pureView ? 'is-pure-view' : ''
      } ${isDeckOpen && menuMode === 'drawer' ? 'is-drawer-open' : ''}`}
      onClick={() => {
        closeFloatingPopovers();
        if (settingsOpen) onCloseSettings();
      }}
    >
      {/* =======================================================
          LAYER 0: FULL-BLEED CABIN ARTWORK (PC 16:9 / MOBILE 9:16)
         ======================================================= */}
      <div
        className="stage-bg-layer"
        style={effectiveBgUrl ? { backgroundImage: `url("${effectiveBgUrl}")` } : undefined}
      />
      {isIllustratedCabin && <div className="stage-bg-tint" />}

      {/* =======================================================
          LAYER 1: CAUSTIC SUNBEAMS, VIGNETTE & WINDOW BUBBLES
         ======================================================= */}
      <div className="ocean-caustics" />
      <div className="stage-vignette" />

      {BUBBLES.map((b, idx) => (
        <span
          key={idx}
          className="ocean-bubble"
          style={{
            left: b.left,
            width: b.size,
            height: b.size,
            animationDuration: b.dur,
            animationDelay: b.delay,
          }}
        />
      ))}

      {/* Procedural seabed & kelp (only shown if user switches to classic gradient mode) */}
      {!isIllustratedCabin && (
        <>
          <svg
            className="ocean-kelp"
            style={{ left: '3.5%' }}
            width="96"
            height="190"
            viewBox="0 0 88 170"
          >
            <path
              d="M28 170 C12 130, 44 95, 22 45 C14 26, 26 10, 30 4 C40 28, 28 65, 44 102 C56 130, 38 152, 42 170 Z"
              fill="rgba(46, 158, 133, 0.52)"
            />
            <path
              d="M58 170 C44 138, 72 108, 56 68 C48 48, 58 32, 62 24 C70 46, 60 80, 72 112 C80 136, 66 155, 68 170 Z"
              fill="rgba(68, 182, 154, 0.42)"
            />
          </svg>

          <svg
            className="ocean-kelp ocean-kelp--right"
            style={{ right: '4%' }}
            width="102"
            height="185"
            viewBox="0 0 92 165"
          >
            <path
              d="M60 165 C76 128, 42 96, 62 48 C68 30, 58 14, 54 8 C46 32, 58 68, 42 104 C30 132, 48 150, 46 165 Z"
              fill="rgba(46, 158, 133, 0.5)"
            />
            <circle cx="28" cy="148" r="14" fill="rgba(235, 132, 150, 0.5)" />
            <circle cx="42" cy="154" r="10" fill="rgba(242, 178, 124, 0.52)" />
          </svg>

          <div className="ocean-floor" />
        </>
      )}

      {/* =======================================================
          LAYER 2: COZY SANDWICH WHALE BED & CABIN DECORATIONS
         ======================================================= */}
      <button
        type="button"
        className={`cabin-bed-anchor ${isSleepingInBed ? 'is-sleeping' : ''}`}
        onClick={(e) => {
          e.stopPropagation();
          triggerSleepOrWake();
        }}
        title={
          viewingNeighbor
            ? `「${viewingNeighbor.whaleName}」的深海蓝鲸软垫小窝`
            : isSleepingInBed
              ? `「${whale.name}」正在软垫窝里呼呼大睡（点击轻拍唤醒她）`
              : `深海蓝鲸软垫小窝（点击哄「${whale.name}」走过去趴着睡午觉！）`
        }
      >
        {isSleepingInBed && !pureView && Boolean(stageTalk.hostSpeech) && (
          <div className="whale-speech whale-speech--dream" key={stageTalk.hostSpeech}>
            <span className="whale-speech__speaker">{whale.name}</span>
            💭 {stageTalk.hostSpeech}
          </div>
        )}

        {isSleepingInBed && (
          <div className="cabin-bed__zzz" aria-hidden="true">
            <span>z</span>
            <span>Z</span>
            <span>💤</span>
          </div>
        )}

        <img
          src={getSceneAssetUrl('props/whale-bed-base.webp')}
          alt="深海蓝鲸软垫小床"
          className="cabin-bed__base"
          style={isSleepingInBed && sleepCanvasReady ? { opacity: 0 } : undefined}
          draggable={false}
        />

        {isSleepingInBed ? (
          <SleepingBedCanvas
            outfit={whale.outfit}
            reloadToken={rigReloadToken}
            onReadyChange={setSleepCanvasReady}
          />
        ) : (
          <div className="cabin-bed__sleeper-slot" aria-hidden="true" />
        )}

        <img
          src={getSceneAssetUrl('props/whale-bed-blanket.webp')}
          alt=""
          className="cabin-bed__blanket"
          style={isSleepingInBed && sleepCanvasReady ? { opacity: 0 } : undefined}
          draggable={false}
        />

        {!pureView && !viewingNeighbor && (
          <span className={`cabin-bed__pill ${isSleepingInBed ? 'is-active' : ''}`}>
            {isSleepingInBed ? '🔔 点击唤醒' : '💤 哄她趴睡'}
          </span>
        )}
      </button>

      {activeDecor.includes('jelly_lamp') && (
        <>
          <img
            src={getSceneAssetUrl(DECOR_CATALOG.jelly_lamp.artwork)}
            alt="幽蓝数据水母夜灯"
            className="decor-jelly"
            draggable={false}
          />
          {!isMobileViewport && (
            <img
              src={getSceneAssetUrl(DECOR_CATALOG.jelly_lamp.artwork)}
              alt=""
              className="decor-jelly decor-jelly--two"
              draggable={false}
            />
          )}
        </>
      )}

      {activeDecor.includes('coral_tea') && (
        <img
          src={getSceneAssetUrl(DECOR_CATALOG.coral_tea.artwork)}
          alt="珊瑚恒温电饭煲茶几"
          className="decor-tea"
          draggable={false}
        />
      )}

      {CABIN_FURNITURE.some((id) => activeDecor.includes(id)) && (
        <div className="cabin-furniture-layer">
          {CABIN_FURNITURE.filter((id) => activeDecor.includes(id)).map((id) => (
            <img
              key={id}
              className={`cabin-furniture cabin-furniture--${id}`}
              src={getSceneAssetUrl(DECOR_CATALOG[id].artwork)}
              alt={DECOR_CATALOG[id].name}
              draggable={false}
            />
          ))}
        </div>
      )}

      {/* =======================================================
          LAYER 3: INTERACTIVE CHARACTERS & DIEGETIC FIXTURES
         ======================================================= */}
      {viewingNeighbor ? (
        /* ---------- MODE B: VISITING NEIGHBOR'S SANCTUARY ---------- */
        <>
          {/* Neighbor's Whale */}
          {viewingNeighbor.whaleAtHome ? (
            <div
              className={`whale-actor ${isOurWhaleAtThisNeighbor ? 'whale-actor--shifted' : ''}`}
              style={{ width: mainWhaleWidth, height: Math.round(mainWhaleWidth * 1.08) }}
            >
              <div className="whale-speech">
                <span className="whale-speech__speaker">{viewingNeighbor.whaleName}</span>
                {isOurWhaleAtThisNeighbor && workingAway
                  ? t(`「${whale.name}」正在我家帮工！`, `${whale.name} is helping in my haven!`)
                  : isOurWhaleAtThisNeighbor
                    ? `欢迎「${whale.name}」来我家一起吃${FEED_CATALOG[viewingNeighbor.troughFeedType].name}呀～`
                    : viewingNeighbor.troughGrams > 0
                      ? `我是「${viewingNeighbor.whaleName}」，我家饭盆还有 ${Math.round(viewingNeighbor.troughGrams)}g 口粮哦！`
                      : `呜……我是「${viewingNeighbor.whaleName}」，我家主人也忘记添饭了……`}
              </div>
              <div
                className={
                  renderMode === 'sprite'
                    ? `whale-sprite-wrap ${viewingNeighbor.troughGrams > 0 ? 'is-eating' : ''}`
                    : 'whale-rig-stage-wrap'
                }
              >
                {renderMode === 'pet' ? (
                  <WhalePetActor
                    alt={viewingNeighbor.whaleName}
                    outfit={viewingNeighbor.whaleOutfit ?? 'maid_classic'}
                    expression={
                      viewingNeighbor.whaleSatiety < 30
                        ? 'teary'
                        : viewingNeighbor.troughGrams > 0
                          ? 'cheerful'
                          : 'calm'
                    }
                    activity={viewingNeighbor.troughGrams > 0 ? 'eating' : 'idle'}
                    lookTrackingEnabled
                    reloadToken={rigReloadToken}
                    displayWidth={mainWhaleWidth - 10}
                  />
                ) : (
                  <WhaleRigActor
                    alt={viewingNeighbor.whaleName}
                    outfit={viewingNeighbor.whaleOutfit ?? 'maid_classic'}
                    expression={
                      viewingNeighbor.whaleSatiety < 30
                        ? 'teary'
                        : viewingNeighbor.troughGrams > 0
                          ? 'cheerful'
                          : 'calm'
                    }
                    renderMode={renderMode}
                    activity={viewingNeighbor.troughGrams > 0 ? 'eating' : 'idle'}
                    showBonesDebug={showBonesDebug}
                    reloadToken={rigReloadToken}
                    displayWidth={mainWhaleWidth - 10}
                  />
                )}
              </div>
              <span
                className="guest-tag"
                style={{ background: '#e0f4f1', color: '#0e2b27', borderColor: '#7fcfc1' }}
              >
                屋主鲸 · {viewingNeighbor.whaleName}
              </span>
            </div>
          ) : (
            <div className="away-sign">
              <div style={{ fontSize: '1.06rem', fontWeight: 700, marginBottom: '0.3rem' }}>
                {t(
                  `「${viewingNeighbor.whaleName}」出门了`,
                  `${viewingNeighbor.whaleName} is away`,
                )}
              </div>
              <div style={{ fontSize: '0.82rem', opacity: 0.88 }}>
                {t('屋主鲸暂时不在家。', 'The resident whale is away from home.')}
              </div>
            </div>
          )}

          {/* Our Whale eating at this Neighbor's sanctuary */}
          {isOurWhaleAtThisNeighbor && whale.location.kind === 'visiting' && (
            <div
              className="guest-actor"
              style={{
                cursor: 'default',
                width: guestWhaleWidth,
                height: Math.round(guestWhaleWidth * 1.08),
              }}
            >
              <div className="whale-speech whale-speech--guest">
                <span className="whale-speech__speaker">{whale.name}</span>
                {awayDetail}
              </div>
              <div
                className={
                  renderMode === 'sprite' ? 'whale-sprite-wrap is-eating' : 'whale-rig-stage-wrap'
                }
              >
                {renderMode === 'pet' ? (
                  <WhalePetActor
                    alt={whale.name}
                    outfit={whale.outfit}
                    expression="cheerful"
                    isWorkerUniform={workingAway}
                    activity="eating"
                    alwaysEatingWhenActive
                    lookTrackingEnabled={false}
                    reloadToken={rigReloadToken}
                    displayWidth={guestWhaleWidth - 8}
                  />
                ) : (
                  <WhaleRigActor
                    alt={whale.name}
                    outfit={whale.outfit ?? 'maid_classic'}
                    expression="cheerful"
                    renderMode={renderMode}
                    activity="eating"
                    heldProp="silver_fish"
                    showBonesDebug={showBonesDebug}
                    reloadToken={rigReloadToken}
                    displayWidth={guestWhaleWidth - 8}
                  />
                )}
              </div>
              <span className={`guest-tag${workingAway ? ' guest-tag--worker' : ''}`}>
                {whale.name} ·{' '}
                {workingAway ? t('正在帮工', 'Working') : t('串门用餐', 'Visiting for a meal')}
              </span>
            </div>
          )}

          {/* Diegetic Neighbor Porcelain Whale Rice Basin */}
          <div className="diegetic-prop diegetic-prop--right" aria-label="邻家青花鲸鱼饭碗">
            <div className="diegetic-prop__badge">
              <span>{viewingNeighbor.troughGrams > 0 ? '🍚' : '🥣'}</span>
              <strong>{Math.round(viewingNeighbor.troughGrams)}g</strong>
              <span className="diegetic-prop__sub">
                {viewingNeighbor.troughGrams > 0 ? '余粮充足' : '已吃空'}
              </span>
            </div>
            <img
              src={neighborBowlImgUrl}
              alt="邻家白米饭碗"
              className="diegetic-prop__img"
              draggable={false}
            />
          </div>
        </>
      ) : (
        /* ---------- MODE A: PLAYER'S OWN WHALE SANCTUARY ---------- */
        <>
          {/* Left Diegetic Prop: DeepSeek Whale Ceramic Piggy Bank */}
          <button
            type="button"
            className={`diegetic-prop diegetic-prop--left ${
              home.unclaimedPearls > 0 ? 'is-ready' : ''
            } ${isGoldenClam ? 'is-golden' : ''}`}
            onClick={(e) => {
              e.stopPropagation();
              if (home.unclaimedPearls > 0) onHarvestPearls();
            }}
            title={
              home.unclaimedPearls > 0
                ? t(
                    `从存钱罐收取 ${home.unclaimedPearls} 枚小鲸币`,
                    `Collect ${home.unclaimedPearls} whale coins from the bank`,
                  )
                : `小鲸币积攒进度 ${Math.floor(home.pearlProgress)}%${
                    workingGuests.length > 0 ? `（${workingGuests.length} 只打工客鲸加速中）` : ''
                  }（存钱罐上限 ${maxPearls} 枚）`
            }
          >
            <div className="diegetic-prop__badge">
              <img src={whaleCoinIconUrl} alt="" className="inline-coin-icon" />
              <strong>
                {home.unclaimedPearls}
                <small>/{maxPearls}</small>
              </strong>
              <span className="diegetic-prop__sub">
                {home.unclaimedPearls > 0 ? '点击收币' : `${Math.floor(home.pearlProgress)}%`}
              </span>
            </div>

            <img
              src={piggyBankImgUrl}
              alt={isGoldenClam ? '鎏金皇冠鲸鱼存钱罐' : '蓝鲸陶瓷存钱罐'}
              className="diegetic-prop__img diegetic-prop__img--piggy"
              draggable={false}
            />

            <div className="diegetic-prop__bar">
              <span style={{ width: `${home.pearlProgress}%` }} />
            </div>
          </button>

          {/* Center: Player's Whale Maid (hidden when tucked into bed!) OR Away Signboard */}
          {whale.location.kind === 'home'
            ? !isSleepingInBed && (
                <div
                  className={`whale-actor ${activeGuests.length > 0 ? 'whale-actor--shifted' : ''} ${
                    activeGuests.length >= 2 ? 'whale-actor--shifted-two' : ''
                  } ${
                    sleepWalkPhase === 'walking_to_bed' || sleepWalkPhase === 'spawning_at_bed'
                      ? 'is-at-bed'
                      : ''
                  } ${sleepWalkPhase === 'spawning_at_bed' ? 'no-transition' : ''} ${
                    sleepWalkPhase === 'walking_to_bed' || sleepWalkPhase === 'walking_to_rug'
                      ? 'is-bed-walking'
                      : ''
                  }`}
                  style={{ width: mainWhaleWidth, height: Math.round(mainWhaleWidth * 1.08) }}
                >
                  {!pureView &&
                    (sleepWalkPhase === 'walking_to_bed' || Boolean(stageTalk.hostSpeech)) && (
                      <div
                        className={`whale-speech whale-speech--interactive ${
                          stageTalk.banterMode === 'trio_party' ? 'whale-speech--party' : ''
                        }`}
                        key={sleepWalkPhase !== 'none' ? sleepWalkPhase : stageTalk.hostSpeech}
                        role="button"
                        tabIndex={0}
                        title={
                          activeGuests.length > 0
                            ? '点击换一幕小鲸鱼们的对话 / 听她讲冷笑话与冷知识！'
                            : '点击气泡听她讲冷笑话、深海冷知识或程序员梗！'
                        }
                        onClick={(e) => {
                          e.stopPropagation();
                          if (activeGuests.length > 0) {
                            triggerManualBanter();
                          } else {
                            onChatWithWhale?.();
                          }
                        }}
                        onKeyDown={(event) => {
                          if (event.key !== 'Enter' && event.key !== ' ') return;
                          event.preventDefault();
                          event.currentTarget.click();
                        }}
                      >
                        <span className="whale-speech__speaker">
                          {whale.name} <Volume2 size={13} aria-hidden="true" />
                        </span>
                        {sleepWalkPhase === 'walking_to_bed'
                          ? `哈啊～迈着小短腿去蓝鲸软窝趴着睡午觉咯……`
                          : stageTalk.hostSpeech}
                      </div>
                    )}

                  {fxSymbol && (
                    <span key={fxSymbol.key} className="whale-fx">
                      {fxSymbol.icon}
                    </span>
                  )}

                  <div
                    className={
                      renderMode === 'sprite'
                        ? `whale-sprite-wrap ${animClass || (home.troughGrams > 0 ? 'is-eating' : '')}`
                        : 'whale-rig-stage-wrap'
                    }
                    style={{ position: 'relative' }}
                    onAnimationEnd={() => {
                      if (animClass) setAnimClass('');
                    }}
                  >
                    {renderMode === 'pet' ? (
                      <WhalePetActor
                        alt={whale.name}
                        outfit={whale.outfit}
                        expression={resolved.expression}
                        pinnedPetState={
                          sleepWalkPhase === 'walking_to_bed'
                            ? 'running-left'
                            : sleepWalkPhase === 'walking_to_rug' ||
                                sleepWalkPhase === 'spawning_at_bed'
                              ? 'running-right'
                              : null
                        }
                        lookTrackingEnabled={
                          sleepWalkPhase === 'none' && (whale.petLookEnabled ?? true)
                        }
                        roamEnabled={sleepWalkPhase === 'none' && (whale.petRoamEnabled ?? true)}
                        roamMaxOffsetPx={roamRangePx}
                        activity={home.troughGrams > 0 ? 'eating' : 'idle'}
                        impulse={rigImpulse}
                        reloadToken={rigReloadToken}
                        displayWidth={mainWhaleWidth - 6}
                      >
                        <button
                          type="button"
                          className="whale-zone-head"
                          title="摸摸头（触发挥手打招呼互动！）"
                          onClick={(e) => {
                            e.stopPropagation();
                            handleZoneClick('head');
                          }}
                        />
                        <button
                          type="button"
                          className="whale-zone-body"
                          title="戳一戳（触发受惊鼓腮反应，连戳会生气哦！）"
                          onClick={(e) => {
                            e.stopPropagation();
                            handleZoneClick('body');
                          }}
                        />
                      </WhalePetActor>
                    ) : (
                      <>
                        <WhaleRigActor
                          alt={whale.name}
                          outfit={whale.outfit ?? 'maid_classic'}
                          expression={resolved.expression}

                          renderMode={renderMode}
                          activity={home.troughGrams > 0 ? 'eating' : 'idle'}
                          heldProp={
                            home.unclaimedPearls >= maxPearls
                              ? 'pearl'
                              : home.troughGrams > 0
                                ? 'silver_fish'
                                : 'none'
                          }
                          impulse={rigImpulse}
                          calibration={faceCalibration}
                          showBonesDebug={showBonesDebug}
                          reloadToken={rigReloadToken}
                          displayWidth={mainWhaleWidth - 8}
                        />

                        <button
                          type="button"
                          className="whale-zone-head"
                          title="摸摸头（触发享受闭眼微笑与呆毛物理弹跳！）"
                          onClick={(e) => {
                            e.stopPropagation();
                            handleZoneClick('head');
                          }}
                        />

                        <button
                          type="button"
                          className="whale-zone-body"
                          title="戳一戳（触发受惊物理震颤，连戳会鼓腮生气哦！）"
                          onClick={(e) => {
                            e.stopPropagation();
                            handleZoneClick('body');
                          }}
                        />
                      </>
                    )}
                  </div>
                </div>
              )
            : (() => {
                const targetNb = snapshot.neighbors.find(
                  (n) => whale.location.kind === 'visiting' && n.id === whale.location.neighborId,
                );
                const targetName = targetNb ? displayNeighborName(targetNb) : '邻家海湾';
                return (
                  <div className="away-sign" onClick={(e) => e.stopPropagation()}>
                    <div className="nb-badge nb-badge--warm" style={{ marginBottom: '0.45rem' }}>
                      {awayStatus}
                    </div>
                    <div style={{ fontSize: '0.98rem', fontWeight: 700, marginBottom: '0.35rem' }}>
                      {t(
                        `「${whale.name}」在${targetName}的小筑`,
                        `${whale.name} is at ${targetName}’s haven`,
                      )}
                    </div>
                    <div style={{ fontSize: '0.8rem', opacity: 0.88, marginBottom: '0.75rem' }}>
                      {awayDetail}
                    </div>
                    <div
                      style={{
                        display: 'flex',
                        justifyContent: 'center',
                        gap: '0.5rem',
                        flexWrap: 'wrap',
                      }}
                    >
                      {targetNb && (
                        <button
                          type="button"
                          className="nb-btn nb-btn--sm"
                          onClick={() => onViewNeighbor(targetNb.id)}
                        >
                          <Compass size={14} /> {t('去邻居家看她', 'View host haven')}
                        </button>
                      )}
                      <button
                        type="button"
                        className="nb-btn nb-btn--sm nb-btn--primary"
                        onClick={onRecallWhale}
                      >
                        <BellRing size={14} /> {t('摇铃喊她回家', 'Call my whale home')}
                      </button>
                    </div>
                  </div>
                );
              })()}

          {/* Sneaking / Hired Working Guest Whales (Up to 2 Slots!) */}
          {activeGuests.map((guest, idx) => {
            const slot = (guest.slotIndex ?? (idx === 0 ? 0 : 1)) as 0 | 1;
            const guestNeighbor = snapshot.neighbors.find((n) => n.id === guest.neighborId);
            const visitor = social.guests.find((g) => g.neighbor_id === guest.neighborId);
            const guestName = guestNeighbor?.whaleName ?? t('小鲸', 'Whale');
            const isWorking = guest.stage === 'working';
            const visitStatus = guestVisitLabel(
              visitor ?? { working: isWorking, allow_hire: false },
              t,
            );
            const isInteractive = guest.stage === 'eating' || isWorking;
            const isLeaving = !isInteractive;
            const isActivelyEating =
              guest.stage === 'eating' || (isWorking && home.troughGrams > 0);
            const guestExpr: WhaleExpression =
              guest.stage === 'eating' || isWorking || guest.stage === 'treated_happy'
                ? 'cheerful'
                : guest.stage === 'panicked'
                  ? 'panicked'
                  : guest.stage === 'leaving_pout'
                    ? 'pout'
                    : 'teary';

            return (
              <button
                type="button"
                disabled={!isInteractive || pureView}
                key={guest.neighborId}
                aria-label={t(
                  `查看客鲸${guestName}，${visitStatus}`,
                  `View guest ${guestName}, ${visitStatus}`,
                )}
                className={`guest-actor guest-actor--slot-${slot} ${isWorking ? 'is-working' : ''} ${
                  isLeaving ? 'is-leaving' : ''
                }`}
                style={{ width: guestWhaleWidth, height: Math.round(guestWhaleWidth * 1.08) }}
                onClick={(e) => {
                  e.stopPropagation();
                  if (isInteractive) onOpenGuestCatch(guest.neighborId);
                }}
                title={visitStatus}
              >
                {!pureView && Boolean(stageTalk.guestSpeeches[guest.neighborId]) && (
                  <div
                    className={`whale-speech whale-speech--guest ${
                      slot === 1 ? 'whale-speech--guest-back' : ''
                    } ${stageTalk.banterMode === 'trio_party' ? 'whale-speech--party' : ''}`}
                    key={stageTalk.guestSpeeches[guest.neighborId]}
                  >
                    <span className="whale-speech__speaker">
                      {guestNeighbor?.whaleName ?? '客鲸'}
                    </span>
                    {stageTalk.guestSpeeches[guest.neighborId]}
                  </div>
                )}
                <div
                  className={
                    renderMode === 'sprite'
                      ? `whale-sprite-wrap ${isActivelyEating ? 'is-eating' : ''}`
                      : 'whale-rig-stage-wrap'
                  }
                >
                  {renderMode === 'pet' ? (
                    <WhalePetActor
                      alt={guestNeighbor?.whaleName ?? '客鲸'}
                      outfit={guestNeighbor?.whaleOutfit ?? 'maid_classic'}
                      expression={guestExpr}
                      isWorkerUniform={isWorking}
                      activity={isActivelyEating ? 'eating' : isLeaving ? 'swimming' : 'idle'}
                      alwaysEatingWhenActive={isActivelyEating}
                      pinnedPetState={guest.stage === 'treated_happy' ? 'jumping' : null}
                      lookTrackingEnabled={false}
                      reloadToken={rigReloadToken}
                      displayWidth={guestWhaleWidth - 8}
                    />
                  ) : (
                    <WhaleRigActor
                      alt={guestNeighbor?.whaleName ?? '客鲸'}
                      outfit={guestNeighbor?.whaleOutfit ?? 'maid_classic'}
                      expression={guestExpr}
                      renderMode={renderMode}
                      activity={isActivelyEating ? 'eating' : 'swimming'}
                      heldProp={
                        guest.stage === 'treated_happy'
                          ? 'pearl'
                          : isActivelyEating
                            ? 'silver_fish'
                            : 'none'
                      }
                      showBonesDebug={showBonesDebug}
                      reloadToken={rigReloadToken}
                      displayWidth={guestWhaleWidth - 8}
                    />
                  )}
                </div>
                <span className={`guest-tag ${isWorking ? 'guest-tag--worker' : ''}`}>
                  {guestName} · {visitStatus}
                </span>
              </button>
            );
          })}

          {/* Right Diegetic Prop: DeepSeek Whale Porcelain Rice Bowl */}
          <button
            type="button"
            className={`diegetic-prop diegetic-prop--right ${
              home.troughGrams <= 0 ? 'is-empty' : ''
            }`}
            onClick={(e) => {
              e.stopPropagation();
              onQuickRefill();
            }}
            title={`青花金鲸恒温饭碗：${Math.round(home.troughGrams)}g / ${values.trough_grams}g（点击一键添饭）`}
          >
            <div className="diegetic-prop__badge">
              <span>{home.troughGrams > 0 ? '🍚' : '🥣'}</span>
              <strong>{Math.round(home.troughGrams)}g</strong>
              <span className="diegetic-prop__sub">
                {home.troughGrams < values.trough_grams ? '+ 添饭' : '已满碗'}
              </span>
            </div>

            <img
              src={homeBowlImgUrl}
              alt={home.troughGrams > 0 ? '热腾腾白米饭碗' : '空青花鲸鱼饭碗'}
              className="diegetic-prop__img diegetic-prop__img--bowl"
              draggable={false}
            />

            <div className="diegetic-prop__bar diegetic-prop__bar--warm">
              <span style={{ width: `${troughRatio * 100}%` }} />
            </div>
          </button>
        </>
      )}

      {/* =======================================================
          LAYER 4: FLOATING GLASS HUD, QUICK CHIPS, DOCK & DRAWER
         ======================================================= */}
      {pureView ? (
        <button
          type="button"
          className="pure-exit-pill"
          onClick={(e) => {
            e.stopPropagation();
            onTogglePureView();
          }}
          title="退出纯享观赏模式，恢复悬浮菜单"
        >
          <Camera size={15} /> 退出纯享画面
        </button>
      ) : (
        <>
          {/* ---------- TOP-LEFT: COMPACT CHARACTER STATUS CAPSULE ---------- */}
          <div className="hud-top-left" onClick={(e) => e.stopPropagation()}>
            <div
              className="hud-capsule hud-status-trigger"
              role="button"
              tabIndex={0}
              aria-expanded={statusPopoverOpen}
              onClick={() => {
                setStatusPopoverOpen((v) => !v);

                setBgPopoverOpen(false);
                setAudioPopoverOpen(false);
                setHelpOpen(false);
              }}
              onKeyDown={(e) => {
                if (e.key !== 'Enter' && e.key !== ' ') return;
                e.preventDefault();
                e.currentTarget.click();
              }}
              title="点击展开鲸鱼娘详细状态、改名与试衣间快捷入口"
            >
              <span className="hud-avatar-dot">
                <img src={getSceneAssetUrl(currentOutfitMeta.previewSprite)} alt="" />
              </span>
              <div className="hud-identity">
                <div className="hud-identity__row">
                  <strong className="hud-whale-name">{whale.name}</strong>
                  <span className="hud-lv-pill">Lv.{affectionInfo.level}</span>
                </div>
                <div className="hud-mini-bars">
                  <span className="hud-mini-meter" title={`饱食度 ${Math.round(whale.satiety)}%`}>
                    <i style={{ width: `${whale.satiety}%`, background: satietyColor }} />
                  </span>
                  <span className="hud-mini-meter" title={`心情值 ${Math.round(whale.mood)}%`}>
                    <i style={{ width: `${whale.mood}%`, background: '#5cc8ff' }} />
                  </span>
                </div>
              </div>
              <ChevronDown
                size={14}
                className={`hud-chevron ${statusPopoverOpen ? 'is-open' : ''}`}
              />
            </div>

            {whale.satiety < 30 && home.troughGrams <= 0 && !viewingNeighbor && (
              <span className="hud-alert-chip hud-alert-chip--danger">🥺 断粮饥饿</span>
            )}

            {/* Expandable Character Detail Card */}
            {statusPopoverOpen && (
              <div className="hud-popover hud-popover--left">
                <div className="hud-popover__head">
                  {editingName ? (
                    <form
                      onSubmit={(e) => {
                        e.preventDefault();
                        if (draftName.trim()) onRenameWhale(draftName.trim().slice(0, 10));
                        setEditingName(false);
                      }}
                      style={{ display: 'flex', gap: '0.35rem', width: '100%' }}
                    >
                      <input
                        value={draftName}
                        onChange={(e) => setDraftName(e.target.value)}
                        maxLength={10}
                        autoFocus
                        aria-label="鲸鱼昵称"
                        className="hud-inline-input"
                      />
                      <button
                        type="button"
                        className="nb-btn nb-btn--sm"
                        title="随机可爱名字"
                        onClick={() => {
                          soundEngine.play('ui_click');
                          setDraftName(pickRandomCuteWhaleName(draftName));
                        }}
                      >
                        <Dices size={13} />
                      </button>
                      <button type="submit" className="nb-btn nb-btn--sm nb-btn--primary">
                        保存
                      </button>
                    </form>
                  ) : (
                    <>
                      <div>
                        <div style={{ fontWeight: 800, fontSize: '0.96rem' }}>
                          🐋 {whale.name}{' '}
                          <span className="nb-badge nb-badge--teal">
                            Lv.{affectionInfo.level} {affectionInfo.title}
                          </span>
                        </div>
                        <div style={{ fontSize: '0.75rem', opacity: 0.85, marginTop: 2 }}>
                          {whale.location.kind === 'visiting' ? awayStatus : resolved.statusLabel}
                        </div>
                        <div
                          style={{
                            fontSize: '0.73rem',
                            color: 'var(--nb-color-accent)',
                            marginTop: 2,
                            fontWeight: 650,
                          }}
                        >
                          {whale.energizedUntil && whale.energizedUntil > nowMs
                            ? '午睡元气生效中'
                            : ''}
                        </div>
                      </div>
                      <button
                        type="button"
                        className="nb-btn nb-btn--sm"
                        onClick={() => {
                          setDraftName(whale.name);
                          setEditingName(true);
                        }}
                        title="修改昵称"
                      >
                        <Edit3 size={13} /> 改名
                      </button>
                    </>
                  )}
                </div>

                <div className="hud-popover__meters">
                  <div>
                    <div className="hud-meter-label">
                      <span>🍚 饱食度</span>
                      <strong>{Math.round(whale.satiety)}%</strong>
                    </div>
                    <div className="stat-meter__bar">
                      <div
                        className="stat-meter__fill"
                        style={{ width: `${whale.satiety}%`, background: satietyColor }}
                      />
                    </div>
                  </div>
                  <div>
                    <div className="hud-meter-label">
                      <span>🎵 心情值</span>
                      <strong>{Math.round(whale.mood)}%</strong>
                    </div>
                    <div className="stat-meter__bar">
                      <div
                        className="stat-meter__fill"
                        style={{ width: `${whale.mood}%`, background: '#3da5d9' }}
                      />
                    </div>
                  </div>
                  <div>
                    <div className="hud-meter-label">
                      <span>💖 羁绊经验</span>
                      <strong>
                        {affectionInfo.currentExp} / {affectionInfo.nextExp}
                      </strong>
                    </div>
                    <div className="stat-meter__bar">
                      <div
                        className="stat-meter__fill"
                        style={{ width: `${affectionInfo.currentExp}%`, background: '#f472b6' }}
                      />
                    </div>
                  </div>
                </div>

                <div className="hud-popover__actions">
                  <button
                    type="button"
                    className="nb-btn nb-btn--sm"
                    onClick={() => {
                      setStatusPopoverOpen(false);
                      onOpenWardrobe();
                    }}
                  >
                    <Shirt size={13} /> {currentOutfitMeta.emoji} {currentOutfitMeta.name}
                  </button>
                </div>
              </div>
            )}
          </div>

          {/* ---------- TOP-RIGHT: WALLETS & COMPACT SYSTEM TOOLS ---------- */}
          <div className="hud-top-right" onClick={(e) => e.stopPropagation()}>
            <div
              className="hud-wallets"
              role="group"
              aria-label={t('资源栏', 'Wallet and supplies')}
            >
              <div className="hud-resource" title={t('游戏积分', 'Game credits')}>
                <Gem className="hud-resource__icon" size={20} aria-hidden="true" />
                <strong aria-label={t('游戏积分', 'Game credits')}>
                  {formatMilliCredits(snapshot.wallet.gameMilli)}
                </strong>
                <button
                  type="button"
                  className="hud-resource__add"
                  onClick={onOpenFeed}
                  aria-label={t('查看背包与购粮', 'Open inventory and buy feed')}
                >
                  <Plus size={14} />
                </button>
              </div>
              <div className="hud-resource" title={t('小鲸币', 'Whale coins')}>
                <img src={whaleCoinIconUrl} alt="" className="hud-coin-img" />
                <strong aria-label={t('小鲸币', 'Whale coins')}>{snapshot.wallet.pearls}</strong>
                <button
                  type="button"
                  className="hud-resource__add"
                  onClick={onHarvestPearls}
                  aria-label={t('从存钱罐收取小鲸币', 'Collect whale coins and settle earnings')}
                  disabled={
                    home.unclaimedPearls <= 0 &&
                    !snapshot.wallet.pendingSocialPearls &&
                    !snapshot.wallet.pendingConchShards
                  }
                >
                  <Plus size={14} />
                </button>
              </div>
              <div className="hud-resource" title={t('自家饭盆余粮', 'Feed remaining at home')}>
                <Soup
                  className="hud-resource__icon hud-resource__icon--food"
                  size={19}
                  aria-hidden="true"
                />
                <strong aria-label={t('余粮', 'Feed remaining')}>
                  {Math.round(home.troughGrams)}g
                </strong>
                <button
                  type="button"
                  className="hud-resource__add"
                  onClick={onOpenFeed}
                  aria-label={t('打开背包添饭', 'Open bag to refill the bowl')}
                >
                  <Plus size={14} />
                </button>
              </div>
            </div>

            <div
              className="hud-capsule hud-icon-bar"
              id="stage-settings"
              role="group"
              aria-label={t('小屋设置', 'Haven settings')}
              hidden={!settingsOpen}
            >
              <MenuPositionButton mode={menuMode} onClick={onToggleMenuMode} />

              {/* Sound Effects & Deep Sea Ambient Popover */}
              <button
                type="button"
                className={`hud-icon-btn ${
                  audioPopoverOpen || audioSettings.ambientEnabled ? 'is-active' : ''
                }`}
                onClick={() => {
                  setAudioPopoverOpen((v) => !v);
                  setBgPopoverOpen(false);

                  setStatusPopoverOpen(false);
                  setHelpOpen(false);
                  soundEngine.play('ui_click');
                }}
                title={
                  !audioSettings.sfxEnabled && !audioSettings.ambientEnabled
                    ? '音效已静音（点击开启软萌互动音效与深海海浪声）'
                    : audioSettings.ambientEnabled
                      ? '声效与深海海浪白噪音已开启（点击调节音量）'
                      : '软萌互动音效已开启（点击调节音量或开启深海海浪声）'
                }
              >
                {!audioSettings.sfxEnabled && !audioSettings.ambientEnabled ? (
                  <VolumeX size={15} />
                ) : (
                  <Volume2 size={15} />
                )}
              </button>

              {/* Custom Background Image Preview / Upload */}
              <button
                type="button"
                className={`hud-icon-btn ${bgPopoverOpen || hasCustomUploadBg ? 'is-active' : ''}`}
                onClick={() => {
                  setBgPopoverOpen((v) => !v);
                  setAudioPopoverOpen(false);

                  setStatusPopoverOpen(false);
                  setHelpOpen(false);
                }}
                title="切换深海小筑背景图或上传自定义壁纸"
              >
                <ImageIcon size={15} />
              </button>

              {/* Day / Night UI Theme */}
              <button
                type="button"
                className="hud-icon-btn"
                onClick={onToggleTheme}
                title={theme === 'light' ? '切换深色界面主题' : '切换浅色界面主题'}
              >
                {theme === 'light' ? <Moon size={15} /> : <Sun size={15} />}
              </button>

              {/* Pure Wallpaper View */}
              <button
                type="button"
                className="hud-icon-btn"
                onClick={onTogglePureView}
                title="隐藏所有菜单栏（纯享桌宠背景模式）"
              >
                <Camera size={15} />
              </button>

              {/* Help Popover */}
              <button
                type="button"
                className={`hud-icon-btn ${helpOpen ? 'is-active' : ''}`}
                onClick={() => {
                  setHelpOpen((v) => !v);
                  setAudioPopoverOpen(false);

                  setBgPopoverOpen(false);
                  setStatusPopoverOpen(false);
                }}
                title="操作指南"
              >
                <HelpCircle size={15} />
              </button>
            </div>

            {/* Audio & Deep-Sea Ambient Settings Popover */}
            {settingsOpen && audioPopoverOpen && (
              <div className="hud-popover hud-popover--right">
                <div
                  style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}
                >
                  <span style={{ fontWeight: 800, fontSize: '0.88rem' }}>🎵 小筑音效与海浪声</span>
                  <span className="nb-badge nb-badge--teal">
                    音量 {Math.round(audioSettings.volume * 100)}%
                  </span>
                </div>

                <div className="ui-switch-group">
                  <UiSwitch
                    checked={audioSettings.sfxEnabled}
                    onChange={() => {
                      const next = soundEngine.updateSettings({
                        sfxEnabled: !audioSettings.sfxEnabled,
                      });
                      setAudioSettings(next);
                      if (next.sfxEnabled) soundEngine.play('headpat');
                    }}
                  >
                    交互音效
                  </UiSwitch>

                  <UiSwitch
                    checked={audioSettings.ambientEnabled}
                    onChange={() => {
                      const next = soundEngine.updateSettings({
                        ambientEnabled: !audioSettings.ambientEnabled,
                      });
                      setAudioSettings(next);
                    }}
                  >
                    <Waves size={13} /> 深海潮汐白噪音
                  </UiSwitch>
                </div>

                <div style={{ display: 'grid', gap: '0.25rem' }}>
                  <div className="hud-meter-label">
                    <span>🔉 主音量调节</span>
                    <strong>{Math.round(audioSettings.volume * 100)}%</strong>
                  </div>
                  <input
                    type="range"
                    aria-label="主音量"
                    min={0}
                    max={100}
                    step={5}
                    value={Math.round(audioSettings.volume * 100)}
                    onChange={(e) => {
                      const vol = Number(e.target.value) / 100;
                      const next = soundEngine.updateSettings({ volume: vol });
                      setAudioSettings(next);
                    }}
                    onMouseUp={() => soundEngine.play('chat_bubble')}
                    onTouchEnd={() => soundEngine.play('chat_bubble')}
                    style={{ width: '100%', accentColor: '#2596be', cursor: 'pointer' }}
                  />
                </div>

                <div style={{ display: 'flex', gap: '0.35rem', flexWrap: 'wrap', paddingTop: 2 }}>
                  <button
                    type="button"
                    className="nb-btn nb-btn--sm"
                    onClick={() => soundEngine.play('headpat')}
                  >
                    🫧 试听摸头
                  </button>
                  <button
                    type="button"
                    className="nb-btn nb-btn--sm"
                    onClick={() => soundEngine.play('poke')}
                  >
                    🍮 试听戳肚
                  </button>
                  <button
                    type="button"
                    className="nb-btn nb-btn--sm"
                    onClick={() => soundEngine.play('coin_harvest', { coinCount: 4 })}
                  >
                    🪙 试听收币
                  </button>
                </div>
              </div>
            )}

            {/* Custom Background Picker Popover */}
            {settingsOpen && bgPopoverOpen && (
              <div className="hud-popover hud-popover--right">
                <div style={{ fontWeight: 800, fontSize: '0.88rem' }}>🖼️ 深海小筑背景设置</div>
                <div style={{ fontSize: '0.76rem', opacity: 0.82, lineHeight: 1.45 }}>
                  默认自动适配桌面横版与手机竖版温馨木屋插画，也支持上传任意自定义壁纸。
                </div>
                <input
                  ref={fileInputRef}
                  type="file"
                  accept="image/*"
                  style={{ display: 'none' }}
                  onChange={handleBgUpload}
                />
                <div style={{ display: 'flex', gap: '0.45rem', flexWrap: 'wrap' }}>
                  <button
                    type="button"
                    className={`nb-btn nb-btn--sm ${!customBgUrl ? 'nb-btn--primary' : ''}`}
                    onClick={() => {
                      onChangeCustomBg(null);
                      setBgPopoverOpen(false);
                    }}
                  >
                    🏡 深海温馨木屋（默认）
                  </button>
                  <button
                    type="button"
                    className={`nb-btn nb-btn--sm ${isProceduralSea ? 'nb-btn--primary' : ''}`}
                    onClick={() => {
                      onChangeCustomBg('__procedural__');
                      setBgPopoverOpen(false);
                    }}
                  >
                    🌊 极简蔚蓝浅湾
                  </button>
                  <button
                    type="button"
                    className="nb-btn nb-btn--sm"
                    onClick={() => fileInputRef.current?.click()}
                  >
                    <Upload size={13} /> 上传自定义图
                  </button>
                  {hasCustomUploadBg && (
                    <button
                      type="button"
                      className="nb-btn nb-btn--sm nb-btn--danger"
                      onClick={() => {
                        onChangeCustomBg(null);
                        setBgPopoverOpen(false);
                      }}
                    >
                      <Trash2 size={13} /> 清除上传
                    </button>
                  )}
                </div>
              </div>
            )}

            {/* Quick Help Popover */}
            {settingsOpen && helpOpen && (
              <div className="hud-popover hud-popover--right">
                <div style={{ fontWeight: 800, fontSize: '0.88rem' }}>💡 深海小筑微交互指南</div>
                <div style={{ fontSize: '0.77rem', opacity: 0.88, lineHeight: 1.55 }}>
                  • <strong>视线与漫步</strong>
                  ：待机时瞳孔会平滑跟随鼠标，鲸鱼娘也会在木屋贝壳地毯上自由散步。
                  <br />• <strong>摸头与戳肚子</strong>
                  ：点击头顶顺毛挥手加心情；连戳肚子会触发受惊鼓腮抗议！
                  <br />• <strong>场景家具</strong>
                  ：点击左下【蓝鲸存钱罐】收小鲸币，点击右下【青花鲸鱼碗】添白米饭，点击后方【蓝鲸软窝】互动。
                  <br />• <strong>双端自适应</strong>：桌面端宽屏横版小屋 +
                  手机端竖版错层榻榻米自动无缝切换。
                </div>
              </div>
            )}
          </div>

          {/* ---------- VISITING NEIGHBOR FLOATING BAR ---------- */}
          {viewingNeighbor &&
            (() => {
              const viewingHandles = formatNeighborHandles(viewingNeighbor);
              const isViewingPinned = (snapshot.pinnedNeighborIds ?? []).includes(
                viewingNeighbor.id,
              );
              return (
                <div className="ocean-visit-banner" onClick={(e) => e.stopPropagation()}>
                  <div
                    style={{
                      display: 'flex',
                      alignItems: 'center',
                      gap: '0.42rem',
                      flexWrap: 'wrap',
                    }}
                  >
                    <span className="nb-badge nb-badge--teal">
                      {t('串门视角', 'Neighbor view')}
                    </span>
                    <strong>
                      {viewingNeighbor.oceanTitle} ·{' '}
                      {displayNeighborName(viewingNeighbor, t('匿名饲养员', 'Anonymous keeper'))}
                    </strong>
                    {viewingHandles.uidBadge && (
                      <span
                        className="nb-badge"
                        style={{ background: 'rgba(255,255,255,0.16)', color: '#e6f5ff' }}
                      >
                        {viewingHandles.uidBadge}
                      </span>
                    )}
                    {viewingHandles.discordBadge && (
                      <span
                        className="nb-badge"
                        style={{ background: 'rgba(114,137,218,0.28)', color: '#eef2ff' }}
                      >
                        {viewingHandles.discordBadge}
                      </span>
                    )}
                    <span
                      className="nb-badge"
                      style={{ background: 'rgba(255,255,255,0.14)', color: '#d9f2ec' }}
                    >
                      {viewingHandles.bayCodeBadge}
                    </span>
                    {!viewingNeighbor.gameProfilePublic && (
                      <span
                        className="nb-badge"
                        style={{ background: 'rgba(255,255,255,0.16)', color: '#d9f2ec' }}
                      >
                        {t('匿名保护', 'Anonymous keeper')}
                      </span>
                    )}
                    <button
                      type="button"
                      className="nb-btn nb-btn--sm"
                      style={{ padding: '0.18rem 0.48rem', minHeight: 26 }}
                      onClick={() => onTogglePinNeighbor(viewingNeighbor.id)}
                      title={
                        isViewingPinned
                          ? t('已收藏常访（点击取消）', 'Unpin this haven')
                          : t('收藏这家海湾，下次一键直达', 'Pin this haven for your next visit')
                      }
                    >
                      <Star
                        size={13}
                        fill={isViewingPinned ? '#fbbf24' : 'none'}
                        color={isViewingPinned ? '#fbbf24' : 'currentColor'}
                      />
                      {isViewingPinned ? t('已收藏', 'Pinned') : t('收藏', 'Pin')}
                    </button>
                  </div>

                  <div
                    style={{
                      display: 'flex',
                      alignItems: 'center',
                      gap: '0.45rem',
                      flexWrap: 'wrap',
                    }}
                  >
                    {isOurWhaleAtThisNeighbor ? (
                      <button
                        type="button"
                        className="nb-btn nb-btn--sm nb-btn--primary"
                        onClick={onRecallWhale}
                      >
                        <BellRing size={13} />{' '}
                        {t(`喊「${whale.name}」回家`, `Recall ${whale.name}`)}
                      </button>
                    ) : (
                      <>
                        <select
                          value={visitPresetId}
                          aria-label={t('串门口信', 'Message for the host')}
                          onChange={(e) => setVisitPresetId(e.target.value)}
                          className="visit-banner-select"
                        >
                          {visitPresets.map((m) => (
                            <option key={m.id} value={m.id}>
                              {t('口信', 'Message')} · {m.text}
                            </option>
                          ))}
                        </select>
                        <button
                          type="button"
                          className="nb-btn nb-btn--sm nb-btn--primary"
                          disabled={whale.location.kind === 'visiting'}
                          onClick={() => onSendWhaleHere(viewingNeighbor.id, visitPresetId)}
                        >
                          <Send size={13} /> {t('留在这蹭饭', 'Send my whale here')}
                        </button>
                      </>
                    )}
                    <button
                      type="button"
                      className="nb-btn nb-btn--sm"
                      onClick={onOpenNeighborSearch}
                    >
                      {t('🔍 查找邻居', '🔍 Find neighbors')}
                    </button>
                    <button type="button" className="nb-btn nb-btn--sm" onClick={onRandomVisitView}>
                      <Compass size={13} /> {t('换一家', 'Switch haven')}
                    </button>
                    <button type="button" className="nb-btn nb-btn--sm" onClick={onReturnHome}>
                      <Home size={13} /> {t('回自家', 'Return home')}
                    </button>
                  </div>
                </div>
              );
            })()}

          {/* ---------- FLOATING CONTEXTUAL ACTION PILLS (ABOVE DOCK) ---------- */}
          {!viewingNeighbor && (
            <div className="stage-quick-pills" onClick={(e) => e.stopPropagation()}>
              {sneakingGuests.length > 0 && (
                <button
                  type="button"
                  className="floating-action-pill floating-action-pill--warm"
                  onClick={() => onOpenGuestCatch(sneakingGuests[0]?.neighborId)}
                >
                  <ShieldAlert size={14} />{' '}
                  {isPhoneStage
                    ? t(`客鲸 ×${sneakingGuests.length}`, `Guests ×${sneakingGuests.length}`)
                    : t(
                        `来访客鲸 ×${sneakingGuests.length}`,
                        `Manage guests ×${sneakingGuests.length}`,
                      )}
                </button>
              )}
              {workingGuests.length > 0 && (
                <button
                  type="button"
                  className="floating-action-pill"
                  onClick={() => onOpenGuestCatch(workingGuests[0]?.neighborId)}
                >
                  💼{' '}
                  {isPhoneStage
                    ? t(`打工 ×${workingGuests.length}`, `Helpers ×${workingGuests.length}`)
                    : t(
                        `打工帮吃中 ×${workingGuests.length}`,
                        `Working guests ×${workingGuests.length}`,
                      )}
                </button>
              )}
              {home.unclaimedPearls > 0 && (
                <button
                  type="button"
                  className="floating-action-pill floating-action-pill--pearl"
                  onClick={onHarvestPearls}
                >
                  <img src={whaleCoinIconUrl} alt="" className="inline-coin-icon" />{' '}
                  {isPhoneStage
                    ? t(`收币 ×${home.unclaimedPearls}`, `Collect ×${home.unclaimedPearls}`)
                    : t(
                        `收取小鲸币 ×${home.unclaimedPearls}`,
                        `Collect whale coins ×${home.unclaimedPearls}`,
                      )}
                </button>
              )}
              {whale.location.kind === 'home' && (
                <button
                  type="button"
                  className="floating-action-pill"
                  onClick={() => {
                    if (activeGuests.length > 0 && !whale.isSleeping) {
                      triggerManualBanter();
                    } else {
                      onChatWithWhale?.();
                    }
                  }}
                  title={
                    activeGuests.length >= 2 && !whale.isSleeping
                      ? '三鲸同堂热闹开聊！点击触发一幕三鲸群像对话（展示 12 秒）'
                      : activeGuests.length === 1 && !whale.isSleeping
                        ? '点击触发屋主鲸与客鲸斗嘴/巡视对话（展示 12 秒）'
                        : '听小鲸鱼讲深海冷知识、谐音冷笑话或程序员梗'
                  }
                >
                  <MessageCircle size={14} />{' '}
                  {activeGuests.length >= 2 && !whale.isSleeping
                    ? '三鲸群聊'
                    : activeGuests.length === 1 && !whale.isSleeping
                      ? '双鲸斗嘴'
                      : isPhoneStage
                        ? '唠嗑'
                        : '唠嗑/冷笑话'}
                </button>
              )}
              {whale.location.kind === 'home' && (
                <button
                  type="button"
                  className="floating-action-pill"
                  onClick={triggerSleepOrWake}
                  title={
                    whale.isSleeping
                      ? '轻拍唤醒软垫窝里呼呼大睡的小鲸鱼'
                      : '哄小鲸鱼走去蓝鲸软垫窝趴着睡午觉（省粮模式 + 恢复心情）'
                  }
                >
                  {whale.isSleeping
                    ? isPhoneStage
                      ? '🔔 唤醒'
                      : '🔔 轻拍唤醒'
                    : isPhoneStage
                      ? '💤 趴睡'
                      : '💤 哄她趴睡'}
                </button>
              )}
              <button
                type="button"
                className="floating-action-pill"
                onClick={onRandomVisitView}
                title="查找其他开放的海湾"
              >
                <Compass size={14} /> {isPhoneStage ? '逛海湾' : '查找海湾'}
              </button>
            </div>
          )}

          {/* ---------- BOTTOM FLOATING GLASS DOCK ---------- */}
          {dockSlot}

          {/* ---------- COLLAPSIBLE OVERLAY SCRIM + DRAWER / SHEET ---------- */}
          {isDeckOpen && (
            <div
              className={`stage-deck-backdrop stage-deck-backdrop--${menuMode}`}
              onClick={(e) => {
                e.stopPropagation();
                onCloseDeck();
              }}
            />
          )}
          {drawerSlot}
        </>
      )}
    </section>
  );
}
