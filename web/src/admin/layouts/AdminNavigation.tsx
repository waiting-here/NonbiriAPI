import { useQuery } from '@tanstack/react-query';
import { useAdminAlertPage } from '../features/operations/alertPage';
import { reportPageKeys } from '../features/operations/reportPages';
import { getReportBadge } from '../features/operations/reports';
import type { TFunction } from 'i18next';
import { useEffect, useRef, useState } from 'react';
import { NavLink, useLocation } from 'react-router';
import { useTranslation } from 'react-i18next';
import { DonationPendingBadge } from '@shared/components/DonationPendingBadge';
import { Icon } from '@shared/components/Icon';
import {
  ADMIN_NAV_GROUPS,
  ADMIN_PRIMARY_NAV,
  ADMIN_SEARCH_NAV,
  type NavItem,
  type SearchNavItem,
} from '../navigation';
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

function labelFor(item: NavItem, t: TFunction) {
  if (!item.labelKey) return t(`admin.${item.key}.nav`);
  return t(item.labelKey, {
    defaultValue: item.fallbackLabelKey ? t(item.fallbackLabelKey) : item.labelKey,
  });
}

function searchLabels(item: SearchNavItem, t: TFunction) {
  return [
    labelFor(item, t),
    t(groupLabels[item.group]),
    item.parentLabelKey && t(item.parentLabelKey),
    item.fallbackLabelKey && t(item.fallbackLabelKey),
    ...(item.keywordKeys ?? []).map((key) => t(key)),
  ].join(' ');
}

export function AdminNavigation({
  accountId,
  onNavigate,
}: {
  accountId: string;
  onNavigate: () => void;
}) {
  const { t, i18n } = useTranslation(undefined, { bindI18n: 'languageChanged loaded' });
  const { pathname, search } = useLocation();
  const currentGroup = activeAdminNavGroup(pathname);
  const navGroups = useCollapsedAdminNavGroups();
  const input = useRef<HTMLInputElement>(null);
  const nav = useRef<HTMLElement>(null);
  const [query, setQuery] = useState('');
  const alerts = useAdminAlertPage(
    accountId ? 'admin:' + accountId : undefined,
    'false',
    '1',
    20,
    Boolean(accountId),
  );
  const reports = useQuery({
    queryKey: reportPageKeys.badge(accountId || 'anonymous'),
    queryFn: ({ signal }) => getReportBadge(signal),
    enabled: Boolean(accountId),
    retry: false,
    refetchInterval: 30_000,
  });
  useEffect(() => {
    const focusSearch = (event: KeyboardEvent) => {
      if (
        event.defaultPrevented ||
        event.isComposing ||
        event.altKey ||
        !(event.metaKey || event.ctrlKey) ||
        event.key.toLowerCase() !== 'k' ||
        !input.current ||
        input.current.closest('[inert]')
      )
        return;
      event.preventDefault();
      input.current.focus();
      input.current.select();
    };
    document.addEventListener('keydown', focusSearch);
    return () => document.removeEventListener('keydown', focusSearch);
  }, []);
  const normalizedQuery = searchable(query).trim();
  const searching = normalizedQuery.length > 0;
  const otherLanguage = i18n.resolvedLanguage === 'zh' ? 'en' : 'zh';
  const hasOtherCatalog = i18n.hasResourceBundle(otherLanguage, 'translation');
  let matches = ADMIN_PRIMARY_NAV;
  if (searching) {
    const exact = ADMIN_SEARCH_NAV.find((item) => searchable(item.to) === normalizedQuery);
    const words = normalizedQuery.split(/\s+/);
    const otherT = hasOtherCatalog ? i18n.getFixedT(otherLanguage) : undefined;
    matches = exact
      ? [exact]
      : ADMIN_SEARCH_NAV.filter((item) => {
          const text = searchable(
            [item.to, searchLabels(item, t), otherT && searchLabels(item, otherT)].join(' '),
          );
          return words.every((word) => text.includes(word));
        });
  }
  function clear() {
    setQuery('');
    input.current?.focus();
  }
  function follow() {
    setQuery('');
    onNavigate();
  }

  return (
    <nav
      ref={nav}
      onKeyDown={(event) => {
        if (
          !searching ||
          event.nativeEvent.isComposing ||
          !['ArrowUp', 'ArrowDown'].includes(event.key)
        )
          return;
        const anchors = Array.from(
          nav.current?.querySelectorAll<HTMLAnchorElement>('a.nb-admin-sidebar__link') ?? [],
        );
        if (!anchors.length) return;
        event.preventDefault();
        const index = anchors.indexOf(document.activeElement as HTMLAnchorElement);
        const next =
          index < 0
            ? event.key === 'ArrowDown'
              ? 0
              : anchors.length - 1
            : (index + (event.key === 'ArrowDown' ? 1 : -1) + anchors.length) % anchors.length;
        anchors[next].focus();
      }}
      className="nb-admin-sidebar__nav"
      aria-label={t('admin.shell.navLabel')}
    >
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
            onFocus={() => {
              if (!hasOtherCatalog) void i18n.loadLanguages(otherLanguage);
            }}
            onChange={(event) => setQuery(event.currentTarget.value)}
            onKeyDown={(event) => {
              if (event.nativeEvent.isComposing || !query) return;
              if (event.key === 'Escape') {
                event.stopPropagation();
                event.preventDefault();
                clear();
              }
              if (event.key === 'Enter' && searching && matches.length > 0) {
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
                const count =
                  item.key === 'admin-alerts' && !alerts.error
                    ? alerts.data?.pagination.total_items
                    : item.key === 'admin-reports' && !reports.error
                      ? reports.data?.total
                      : undefined;
                const link = (
                  <NavLink
                    key={item.key}
                    className="nb-admin-sidebar__link"
                    to={item.to}
                    end={item.end}
                    onClick={follow}
                    aria-current={searching && pathname + search !== item.to ? false : 'page'}
                  >
                    {item.icon && <Icon name={item.icon} />}
                    <span className="nb-admin-sidebar__label">{labelFor(item, t)}</span>
                    {count && count !== '0' && (
                      <span
                        className="nb-badge"
                        aria-label={t('admin.reports.badge.exactCount', { count })}
                      >
                        {BigInt(count) > 99n ? '99+' : count}
                      </span>
                    )}
                  </NavLink>
                );
                return item.key === 'admin-charity' ? (
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
