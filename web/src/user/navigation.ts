import {
  USER_ROUTE_DESCRIPTORS,
  requiredRouteById,
  type RouteDescriptor,
} from '@shared/routing/routeRegistry';
import { matchPath } from 'react-router';
import type { IconName } from '@shared/components/Icon';

/** Destinations that share one top-level entry and a row of section tabs. */
export type UserNavGroup = 'own' | 'records';

export interface NavItem {
  to: string;
  key: string;
  end?: boolean;
  icon?: IconName;
  labelKey?: string;
  fallbackLabelKey?: string;
  /** Set for an entry that stands for several pages; it stays active on all of them. */
  group?: UserNavGroup;
}

export interface SectionLink {
  to: string;
  labelKey: string;
  icon?: IconName;
}

const toNavItem = (route: RouteDescriptor): NavItem => ({
  to: route.path,
  key: route.id,
  end: route.end,
  icon: route.icon,
  labelKey: route.labelKey,
  fallbackLabelKey: route.fallbackLabelKey,
});

// The co-management entry appears only while the session reports effective
// level 5. This is display state only: every steward route re-resolves the
// current level server-side before exposing its bounded management surface.
export const STEWARD_NAV_ITEM: NavItem = toNavItem(requiredRouteById('user', 'steward'));

/**
 * Top bar order: everyday tasks first, then records and the optional extras. "Own services"
 * covers services and models; "Records" covers calls, credits, issues and debugging.
 */
export const USER_PRIMARY_NAV: NavItem[] = [
  toNavItem(requiredRouteById('user', 'home')),
  toNavItem(requiredRouteById('user', 'caller-key')),
  toNavItem(requiredRouteById('user', 'charity')),
  {
    ...toNavItem(requiredRouteById('user', 'endpoints')),
    key: 'own',
    labelKey: 'user.navigation.own',
    fallbackLabelKey: 'user.resources.nav',
    group: 'own',
  },
  { ...toNavItem(requiredRouteById('user', 'logs')), group: 'records' },
  toNavItem(requiredRouteById('user', 'activities')),
  toNavItem(requiredRouteById('user', 'games')),
  toNavItem(requiredRouteById('user', 'rewards')),
];

/** Section tabs for grouped entries, in display order. */
export const USER_SECTION_LINKS: Record<UserNavGroup, readonly SectionLink[]> = {
  own: [
    { to: '/endpoints', labelKey: 'user.navigation.services', icon: 'resources' },
    { to: '/models', labelKey: 'user.navigation.models', icon: 'models' },
  ],
  records: [
    { to: '/logs', labelKey: 'user.records.calls', icon: 'logs' },
    { to: '/credits', labelKey: 'user.records.credits', icon: 'credits' },
    { to: '/issues', labelKey: 'user.records.issues', icon: 'issues' },
    { to: '/debug', labelKey: 'user.debug.nav', icon: 'debug' },
  ],
};

function routeForPath(pathname: string): RouteDescriptor | undefined {
  return USER_ROUTE_DESCRIPTORS.find((entry) =>
    matchPath({ path: entry.path, end: true }, pathname),
  );
}

/** The grouped entry that owns the current page, if any. */
export function activeUserNavGroup(pathname: string): UserNavGroup | undefined {
  const group = routeForPath(pathname)?.navGroup;
  return group === 'own' || group === 'records' ? group : undefined;
}

/** Layout hint for the current path; unknown paths fall back to the workspace layout. */
export function userRouteLayout(pathname: string): RouteDescriptor['layout'] {
  return routeForPath(pathname)?.layout ?? 'wide';
}
