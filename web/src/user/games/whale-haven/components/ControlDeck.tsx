import { useLayoutEffect, useRef, type ReactNode } from 'react';
import type { Profile } from '../api';
import {
  ArrowRightLeft,
  Footprints,
  Home,
  PanelBottom,
  PanelRight,
  ScrollText,
  Settings,
  Shirt,
  ShoppingBag,
  X,
} from './Icons';
import type { MenuPresentationMode } from './OceanStage';
import { StarWave } from './UiMotifs';

export type DeckTabId = 'feed' | 'wardrobe' | 'social' | 'exchange' | 'logs';

const DECK_TABS_META: readonly {
  id: DeckTabId;
  label: string;
  title: string;
}[] = [
  { id: 'wardrobe', label: '角色', title: '角色与衣橱' },
  { id: 'social', label: '串门', title: '串门' },
  { id: 'feed', label: '背包', title: '背包与投喂' },
  { id: 'exchange', label: '鲸币', title: '鲸币兑换与小屋' },
  { id: 'logs', label: '动态', title: '小筑动态' },
];

const TAB_ICONS = {
  feed: ShoppingBag,
  wardrobe: Shirt,
  social: Footprints,
  exchange: ArrowRightLeft,
  logs: ScrollText,
} satisfies Record<DeckTabId, typeof ShoppingBag>;

interface FloatingDockProps {
  profile: Profile;
  activeTab: DeckTabId | null;
  isHome: boolean;
  settingsOpen: boolean;
  onHome: () => void;
  onToggleSettings: () => void;
  onSelectTab: (tab: DeckTabId) => void;
}

export function FloatingDock({
  profile,
  activeTab,
  isHome,
  settingsOpen,
  onHome,
  onToggleSettings,
  onSelectTab,
}: FloatingDockProps) {
  const snapshot = profile.snapshot;
  const values = profile.settings.values;
  const hasConchGachaBadge = (snapshot.wallet.conchShards ?? 0) >= values.conch_cost_shards;
  const hasSocialBadge =
    snapshot.whale.location.kind === 'visiting' ||
    (snapshot.guestVisit !== null && snapshot.guestVisit.stage === 'eating');
  const hasExchangeBadge =
    snapshot.wallet.pearls >= values.lot_pearls &&
    snapshot.wallet.dailyPearlExchanged < values.daily_exchange_pearls &&
    BigInt(snapshot.wallet.generalMilli) < BigInt(values.wallet_cap_milli);

  const hasBadge = (id: DeckTabId) => {
    if (id === 'feed') return hasConchGachaBadge;
    if (id === 'social') return hasSocialBadge;
    if (id === 'exchange') return profile.settings.exchange_enabled && hasExchangeBadge;
    return false;
  };

  return (
    <nav className="floating-dock" aria-label="海湾功能菜单栏" onClick={(e) => e.stopPropagation()}>
      <button
        type="button"
        className={`dock-item ${isHome && !activeTab && !settingsOpen ? 'is-active' : ''}`}
        aria-current={isHome && !activeTab && !settingsOpen ? 'page' : undefined}
        onClick={onHome}
      >
        <span className="dock-item__icon">
          <Home size={20} />
        </span>
        <span className="dock-item__label">首页</span>
      </button>
      {DECK_TABS_META.map((tab) => {
        const isActive =
          activeTab === tab.id || (tab.id === 'social' && !isHome && !activeTab && !settingsOpen);
        const Icon = TAB_ICONS[tab.id];
        return (
          <button
            key={tab.id}
            type="button"
            className={`dock-item ${isActive ? 'is-active' : ''}`}
            onClick={() => onSelectTab(tab.id)}
            aria-expanded={activeTab === tab.id}
            aria-controls="game-control-deck"
          >
            <span className="dock-item__icon">
              <Icon size={20} />
              {hasBadge(tab.id) && <i className="dock-item__dot" />}
            </span>
            <span className="dock-item__label">{tab.label}</span>
          </button>
        );
      })}
      <button
        type="button"
        className={`dock-item ${settingsOpen ? 'is-active' : ''}`}
        aria-expanded={settingsOpen}
        aria-controls="stage-settings"
        onClick={onToggleSettings}
      >
        <span className="dock-item__icon">
          <Settings size={20} />
        </span>
        <span className="dock-item__label">设置</span>
      </button>
    </nav>
  );
}

interface ControlDeckProps {
  activeTab: DeckTabId;
  menuMode: MenuPresentationMode;
  onClose: () => void;
  onToggleMenuMode: () => void;
  children: ReactNode;
}
export function ControlDeck({
  activeTab,
  menuMode,
  onClose,
  onToggleMenuMode,
  children,
}: ControlDeckProps) {
  const body = useRef<HTMLDivElement>(null);

  useLayoutEffect(() => {
    if (body.current) body.current.scrollTop = 0;
  }, [activeTab]);

  const active = DECK_TABS_META.find((tab) => tab.id === activeTab) ?? DECK_TABS_META[0];
  const Icon = TAB_ICONS[active.id];

  return (
    <aside
      id="game-control-deck"
      className={`deck-panel deck-panel--${menuMode} is-open`}

      aria-labelledby="deck-title"
      onClick={(event) => event.stopPropagation()}
    >
      <div className="deck-sheet-grabber" aria-hidden="true" onClick={onClose}>
        <span />
      </div>
      <header className="deck-header">
        <div className="deck-header__row">
          <div className="deck-header__identity">
            <span className="deck-header__emblem" aria-hidden="true">
              <Icon size={23} />
            </span>
            <h2 className="deck-header__title" id="deck-title">
              {active.title}
            </h2>
          </div>
          <div className="deck-header__actions">
            <button
              type="button"
              className="deck-icon-btn hide-on-mobile"
              onClick={onToggleMenuMode}
              aria-label={menuMode === 'drawer' ? '切换底部模式' : '切换侧栏模式'}
              title={menuMode === 'drawer' ? '底部模式' : '侧栏模式'}
            >
              {menuMode === 'drawer' ? <PanelBottom size={17} /> : <PanelRight size={17} />}
            </button>
            <button
              type="button"
              className="deck-icon-btn"
              onClick={onClose}
              aria-label="收起菜单面板"
              title="收起面板 (Esc)"
            >
              <X size={19} />
            </button>
          </div>
        </div>
        <StarWave className="deck-header__wave" />
      </header>

      <div className={`deck-body deck-body--${activeTab}`} ref={body}>
        {children}
      </div>
    </aside>
  );
}
