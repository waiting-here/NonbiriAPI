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
import { StarWave } from './UiMotifs';

export type MenuPresentationMode = 'drawer' | 'sheet';
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
  const hasSocialBadge = !!profile.social.away || profile.social.guests.length > 0;
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
            id={`whale-dock-${tab.id}`}
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

export function MenuPositionButton({
  mode,
  onClick,
}: {
  mode: MenuPresentationMode;
  onClick: () => void;
}) {
  const t = useWhaleText();
  const bottom = mode === 'drawer';
  const label = bottom
    ? t('将菜单移到底部', 'Move menu to bottom')
    : t('将菜单移到右侧', 'Move menu to the right');
  return (
    <button
      type="button"
      className="wh-menu-position hide-on-mobile"
      onClick={onClick}
      aria-label={label}
      title={label}
      aria-controls="game-control-deck"
    >
      {bottom ? <PanelBottom size={18} /> : <PanelRight size={18} />}
      {bottom ? t('移到底部', 'Dock below') : t('移到右侧', 'Dock right')}
    </button>
  );
}

interface ControlDeckProps {
  activeTab: DeckTabId;
  focusTarget?: string;
  menuMode: MenuPresentationMode;
  hidden: boolean;
  onClose: () => void;
  onToggleMenuMode: () => void;
  children: ReactNode;
}
export function ControlDeck({
  activeTab,
  focusTarget,
  menuMode,
  hidden,
  onClose,
  onToggleMenuMode,
  children,
}: ControlDeckProps) {
  const t = useWhaleText();
  const body = useRef<HTMLDivElement>(null);
  const heading = useRef<HTMLHeadingElement>(null);

  useLayoutEffect(() => {
    if (body.current) body.current.scrollTop = 0;
  }, [activeTab]);
  useLayoutEffect(() => {
    if (hidden) return;
    const guest = focusTarget && document.getElementById(focusTarget);
    if (guest && body.current?.contains(guest)) {
      guest.focus({ preventScroll: true });
      guest.scrollIntoView({ block: 'nearest' });
    } else {
      heading.current?.focus({ preventScroll: true });
    }
  }, [activeTab, hidden, focusTarget]);

  const active = DECK_TABS_META.find((tab) => tab.id === activeTab) ?? DECK_TABS_META[0];
  const Icon = TAB_ICONS[active.id];

  return (
    <aside
      id="game-control-deck"
      className={`deck-panel deck-panel--${menuMode} is-open`}
      hidden={hidden}
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
            <h2 className="deck-header__title" id="deck-title" ref={heading} tabIndex={-1}>
              {t(...active.title)}
            </h2>
          </div>
          <div className="deck-header__actions">
            <MenuPositionButton mode={menuMode} onClick={onToggleMenuMode} />
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
