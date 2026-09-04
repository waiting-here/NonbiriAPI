import { ADMIN_ROUTE_DESCRIPTORS, type RouteDescriptor } from '@shared/routing/routeRegistry';
import type { IconName } from '@shared/components/Icon';

export interface NavItem {
  to: string;
  key: string;
  end?: boolean;
  icon?: IconName;
  labelKey?: string;
  fallbackLabelKey?: string;
  group?: string;
}

export const ADMIN_NAV_GROUPS = ['overview', 'users', 'operations', 'content'] as const;

export const ADMIN_PRIMARY_NAV: NavItem[] = ADMIN_ROUTE_DESCRIPTORS.filter(
  (route): route is RouteDescriptor & { nav: true; labelKey: string; navGroup: string; registered: true } =>
    Boolean(route.nav && route.labelKey && route.navGroup && route.registered),
).map((route) => ({
  to: route.path,
  key: route.id,
  end: route.end,
  icon: route.icon,
  labelKey: route.labelKey,
  fallbackLabelKey: route.fallbackLabelKey,
  group: route.navGroup,
}));
