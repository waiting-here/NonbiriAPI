import { useRef, useState } from 'react';
import { NavLink, useLocation } from 'react-router';
import { useTranslation } from 'react-i18next';
import { DonationPendingBadge } from '@shared/components/DonationPendingBadge';
import { Icon } from '@shared/components/Icon';
import { ADMIN_NAV_GROUPS, ADMIN_PRIMARY_NAV } from '../navigation';
import { activeAdminNavGroup, useCollapsedAdminNavGroups } from '../navigationGroups';

const groupLabels = {
  overview: 'admin.navigation.overview',
  services: 'admin.navigation.services',
  users: 'admin.navigation.users',
  records: 'admin.navigation.records',
  activities: 'admin.navigation.activities',
  system: 'admin.navigation.system',
} as const;

function searchable(value: string) {
  return value.normalize('NFKC').toLowerCase();
}

export function AdminNavigation({
  accountId,
  onNavigate,
}: {
  accountId: string;
  onNavigate: () => void;
}) {
  const { t } = useTranslation();
  const { pathname } = useLocation();
  const currentGroup = activeAdminNavGroup(pathname);
  const navGroups = useCollapsedAdminNavGroups();
  const input = useRef<HTMLInputElement>(null);
  const nav = useRef<HTMLElement>(null);
  const [query, setQuery] = useState('');
  const words = searchable(query).trim().split(/\s+/).filter(Boolean);
  const searching = words.length > 0;
  const entries = ADMIN_PRIMARY_NAV.map((item) => {
    const label = item.labelKey
      ? t(item.labelKey, {
          defaultValue: item.fallbackLabelKey ? t(item.fallbackLabelKey) : item.labelKey,
        })
      : t(`admin.${item.key}.nav`);
    return { ...item, label };
  });
  const matches = entries.filter((item) => {
    const group = t(groupLabels[item.group]);
    const text = searchable(
      [item.label, group, item.to, item.fallbackLabelKey && t(item.fallbackLabelKey)]
        .filter(Boolean)
        .join(' '),
    );
    return words.every((word) => text.includes(word));
  });
  function clear() {
    setQuery('');
    input.current?.focus();
  }
  function follow() {
    setQuery('');
    onNavigate();
  }

  return (
    <nav ref={nav} className="nb-admin-sidebar__nav" aria-label={t('admin.shell.navLabel')}>
      <div className="nb-nav-lookup" role="search" aria-label={t('admin.navigation.findPage')}>
        <label htmlFor="admin-page-search">{t('admin.navigation.findPage')}</label>
        <div className="nb-nav-lookup__input">
          <Icon name="search" />
          <input
            ref={input}
            id="admin-page-search"
            type="text"
            inputMode="search"
            autoComplete="off"
            maxLength={120}
            value={query}
            placeholder={t('admin.navigation.findHint')}
            onChange={(event) => setQuery(event.currentTarget.value)}
            onKeyDown={(event) => {
              if (event.nativeEvent.isComposing || !query) return;
              if (event.key === 'Escape') {
                event.stopPropagation();
                event.preventDefault();
                clear();
              }
              if (event.key === 'Enter' && searching && matches.length === 1) {
                event.preventDefault();
                nav.current?.querySelector<HTMLAnchorElement>('a.nb-admin-sidebar__link')?.click();
              }
            }}
          />
          {query && (
            <button type="button" onClick={clear} aria-label={t('admin.navigation.clearSearch')}>
              <Icon name="close" />
            </button>
          )}
        </div>
        <span
          className={searching && matches.length === 0 ? 'nb-nav-lookup__empty' : 'sr-only'}
          role="status"
        >
          {searching &&
            (matches.length
              ? t('admin.navigation.matchCount', { count: matches.length })
              : t('admin.navigation.noPages'))}
        </span>
      </div>
      {ADMIN_NAV_GROUPS.map((group) => {
        const items = matches.filter((item) => item.group === group);
        if (!items.length) return null;
        const title = t(groupLabels[group]);
        const current = group === currentGroup;
        const open = searching || current || !navGroups.collapsed.includes(group);
        const titled = searching || items.length > 1;
        const listId = `admin-nav-${group}`;
        return (
          <section className="nb-admin-sidebar__group" key={group}>
            {titled && (
              <h2 className="nb-admin-sidebar__group-title">
                {current || searching ? (
                  <span>{title}</span>
                ) : (
                  <button
                    type="button"
                    aria-expanded={open}
                    aria-controls={listId}
                    onClick={() => navGroups.toggle(group)}
                  >
                    <span>{title}</span>
                    <Icon name="chevron-down" />
                  </button>
                )}
              </h2>
            )}
            <div className="nb-admin-sidebar__items" id={listId} hidden={titled && !open}>
              {items.map((item) => {
                const link = (
                  <NavLink
                    key={item.key}
                    className="nb-admin-sidebar__link"
                    to={item.to}
                    end={item.end}
                    onClick={follow}
                  >
                    {item.icon && <Icon name={item.icon} />}
                    <span className="nb-admin-sidebar__label">{item.label}</span>
                  </NavLink>
                );
                return item.to === '/charity' ? (
                  <div className="nb-admin-sidebar__row" key={item.key}>
                    {link}
                    <DonationPendingBadge role="admin" accountID={accountId} onNavigate={follow} />
                  </div>
                ) : (
                  link
                );
              })}
            </div>
          </section>
        );
      })}
    </nav>
  );
}
