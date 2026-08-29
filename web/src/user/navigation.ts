import { USER_ROUTE_DESCRIPTORS, requiredRouteById, type RouteDescriptor } from '@shared/routing/routeRegistry';
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
