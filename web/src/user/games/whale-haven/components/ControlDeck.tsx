import { useWhaleText } from '../copy';
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
  label: [string, string];
  title: [string, string];
}[] = [
  { id: 'wardrobe', label: ['角色', 'Whale'], title: ['角色与衣橱', 'Whale and wardrobe'] },
  { id: 'social', label: ['串门', 'Visits'], title: ['串门', 'Visits'] },
  { id: 'feed', label: ['背包', 'Bag'], title: ['背包与投喂', 'Inventory and feeding'] },
  {
    id: 'exchange',
    label: ['鲸币', 'Coins'],
    title: ['鲸币兑换与小屋', 'Exchange and furnishings'],
  },
  { id: 'logs', label: ['动态', 'Activity'], title: ['小筑动态', 'Haven activity'] },
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
  const t = useWhaleText();
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
    <nav
      className="floating-dock"
      aria-label={t('海湾功能菜单栏', 'Haven navigation')}
      onClick={(e) => e.stopPropagation()}
    >
      <button
        type="button"
        className={`dock-item ${isHome && !activeTab && !settingsOpen ? 'is-active' : ''}`}
        aria-current={isHome && !activeTab && !settingsOpen ? 'page' : undefined}
        onClick={onHome}
      >
        <span className="dock-item__icon">
          <Home size={20} />
        </span>
        <span className="dock-item__label">{t('首页', 'Home')}</span>
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
            <span className="dock-item__label">{t(...tab.label)}</span>
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
        <span className="dock-item__label">{t('设置', 'Settings')}</span>
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
  const t = useWhaleText();
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
              {t(...active.title)}
            </h2>
          </div>
          <div className="deck-header__actions">
            <button
              type="button"
              className="deck-icon-btn hide-on-mobile"
              onClick={onToggleMenuMode}
              aria-label={
                menuMode === 'drawer'
                  ? t('切换底部模式', 'Switch to bottom panel')
                  : t('切换侧栏模式', 'Switch to sidebar')
              }
              title={
                menuMode === 'drawer' ? t('底部模式', 'Bottom panel') : t('侧栏模式', 'Sidebar')
              }
            >
              {menuMode === 'drawer' ? <PanelBottom size={17} /> : <PanelRight size={17} />}
            </button>
            <button
              type="button"
              className="deck-icon-btn"
              onClick={onClose}
              aria-label={t('收起菜单面板', 'Close panel')}
              title={t('收起面板 (Esc)', 'Close panel (Esc)')}
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
