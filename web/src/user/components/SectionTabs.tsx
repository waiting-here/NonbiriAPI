import { useLayoutEffect, useRef, type ReactNode } from 'react';
import { skipToken, useQuery, useQueryClient } from '@tanstack/react-query';
import { useUserSession } from '../data';
import { resourceNavigation } from '@shared/operations/resourceNavigation';
import { Link, useLocation, useNavigationType } from 'react-router';
import { useTranslation } from 'react-i18next';
import { Icon } from '@shared/components/Icon';
import { USER_SECTION_LINKS, type UserNavGroup } from '../navigation';
import './section-tabs.css';

/**
 * Pages of one top-level entry (for example services and models). Plain links keep each page
 * bookmarkable; the current page is marked with aria-current.
 */
const GROUP_LABEL_KEYS: Record<UserNavGroup, string> = {
  own: 'user.navigation.ownSections',
  records: 'user.records.title',
};

interface SectionVisit {
  search: string;
  hash: string;
  historyKey: string;
  trigger: string;
}

export function SectionTabs({
  group,
  className = '',
  badges = {},
}: {
  group: UserNavGroup;
  className?: string;
  /** Optional trailing content per link path, such as an open-issue count. */
  badges?: Partial<Record<string, ReactNode>>;
}) {
  const { t } = useTranslation();
  const location = useLocation();
  const navigationType = useNavigationType();
  const { pathname } = location;
  const client = useQueryClient();
  const session = useUserSession(false);
  const user = !session.error ? session.data?.user : undefined;
  const scope = user
    ? JSON.stringify([
        user.id,
        user.effective_level,
        user.steward_permissions?.revision,
        resourceNavigation(client).epoch,
      ])
    : undefined;
  const origin = location.state?.sectionTab;
  const belongsToSession = !origin || origin.scope === scope;
  const key = ['user', 'section-navigation', scope, group] as const;
  // Station logout and authority changes clear this user-owned cache. Only
  // explicitly visited URL positions survive across sibling page mounts.
  const { data: visits } = useQuery<Record<string, SectionVisit>>({
    queryKey: key,
    queryFn: skipToken,
    gcTime: Infinity,
  });
  const nav = useRef<HTMLElement>(null);
  const mountedPage = useRef<string | null>(null);
  useLayoutEffect(() => {
    if (!scope) {
      mountedPage.current = null;
      return;
    }
    const page = scope + pathname;
    if (mountedPage.current === page) return;
    mountedPage.current = page;
    const visit = visits?.[pathname];
    const destination =
      navigationType === 'POP' && visit?.historyKey === location.key
        ? visit.trigger
        : (navigationType !== 'POP' || visits !== undefined) &&
            origin?.scope === scope &&
            origin.group === group &&
            origin.path === pathname
          ? pathname
          : undefined;
    if (destination) {
      const links = nav.current?.querySelectorAll<HTMLAnchorElement>('a[data-section]');
      Array.from(links ?? [])
        .find((link) => link.dataset.section === destination)
        ?.focus({ preventScroll: true });
    }
  }, [scope, group, pathname, location.key, origin, navigationType, visits]);
  return (
    <nav
      ref={nav}
      className={`nb-section-tabs${className ? ` ${className}` : ''}`}
      aria-label={t(GROUP_LABEL_KEYS[group])}
    >
      {USER_SECTION_LINKS[group].map((link) => {
        const current = pathname === link.to || pathname.startsWith(link.to + '/');
        const saved =
          pathname === link.to && belongsToSession
            ? location
            : scope
              ? visits?.[link.to]
              : undefined;
        return (
          <Link
            key={link.to}
            data-section={link.to}
            to={{ pathname: link.to, search: saved?.search, hash: saved?.hash }}
            state={scope ? { sectionTab: { scope, group, path: link.to } } : undefined}
            aria-current={current ? 'page' : undefined}
            onClick={(event) => {
              if (
                event.defaultPrevented ||
                event.button !== 0 ||
                event.metaKey ||
                event.ctrlKey ||
                event.shiftKey ||
                event.altKey
              )
                return;
              if (pathname === link.to && belongsToSession) {
                event.preventDefault();
                return;
              }
              if (
                !scope ||
                !belongsToSession ||
                !USER_SECTION_LINKS[group].some((item) => item.to === pathname)
              )
                return;
              client.setQueryData<Record<string, SectionVisit>>(key, (previous) => ({
                ...previous,
                [pathname]: {
                  search: location.search,
                  hash: location.hash,
                  historyKey: location.key,
                  trigger: link.to,
                },
              }));
            }}
          >
            {link.icon ? <Icon name={link.icon} /> : null}
            <span>{t(link.labelKey)}</span>
            {badges[link.to] ?? null}
          </Link>
        );
      })}
    </nav>
  );
}
