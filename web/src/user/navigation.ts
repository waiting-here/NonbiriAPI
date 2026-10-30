import {
  USER_ROUTE_DESCRIPTORS,
  requiredRouteById,
  type RouteDescriptor,
} from '@shared/routing/routeRegistry';
import { matchPath } from 'react-router';
import type { IconName } from '@shared/components/Icon';

export interface NavItem {
  to: string;
  key: string;
  end?: boolean;
  icon?: IconName;
  labelKey?: string;
  fallbackLabelKey?: string;
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

export const USER_PRIMARY_NAV: NavItem[] = USER_ROUTE_DESCRIPTORS.filter(
  (route): route is RouteDescriptor & { nav: true; labelKey: string; registered: true } =>
    Boolean(route.nav && route.labelKey && route.registered),
).map((route) => ({
  to: route.path,
  key: route.id,
  end: route.end,
  icon: route.icon,
  labelKey: route.labelKey,
  fallbackLabelKey: route.fallbackLabelKey,
}));

/** Layout hint for the current path; unknown paths fall back to the workspace layout. */
export function userRouteLayout(pathname: string): RouteDescriptor['layout'] {
  const route = USER_ROUTE_DESCRIPTORS.find((entry) =>
    matchPath({ path: entry.path, end: true }, pathname),
  );
  return route?.layout ?? 'wide';
}
