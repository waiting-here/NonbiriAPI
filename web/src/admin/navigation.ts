import {
  ADMIN_ROUTE_DESCRIPTORS,
  requiredRouteById,
  type RouteDescriptor,
} from '@shared/routing/routeRegistry';
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

/** Sidebar groups, top to bottom. */
export const ADMIN_NAV_GROUPS = [
  'overview',
  'services',
  'users',
  'records',
  'activities',
  'system',
] as const;

export type AdminNavGroupKey = (typeof ADMIN_NAV_GROUPS)[number];

/**
 * Entries per group, most frequently used first. Every navigable admin route must appear
 * exactly once; the registry still owns paths, labels and icons.
 */
const ADMIN_NAV_ORDER: Record<AdminNavGroupKey, readonly string[]> = {
  overview: ['admin-home'],
  services: [
    'admin-charity',
    'admin-mainstream-channels',
    'admin-endpoints',
    'admin-endpoint-policies',
  ],
  users: ['admin-users', 'admin-blacklist', 'admin-reports', 'admin-inactivity-policy'],
  records: [
    'admin-logs',
    'admin-management-operations',
    'admin-risk-audit',
    'admin-economy-audit',
    'admin-alerts',
  ],
  activities: [
    'admin-activities',
    'admin-limited-activities',
    'admin-games',
    'admin-rewards',
    'admin-announcements',
  ],
  system: ['admin-settings', 'admin-agent'],
};

const toNavItem = (route: RouteDescriptor, group: AdminNavGroupKey): NavItem => ({
  to: route.path,
  key: route.id,
  end: route.end,
  icon: route.icon,
  labelKey: route.labelKey,
  fallbackLabelKey: route.fallbackLabelKey,
  group,
});

export const ADMIN_PRIMARY_NAV: NavItem[] = ADMIN_NAV_GROUPS.flatMap((group) =>
  ADMIN_NAV_ORDER[group].map((id) => toNavItem(requiredRouteById('admin', id), group)),
);

/** Routes marked for navigation that the sidebar order does not place (should stay empty). */
export const UNPLACED_ADMIN_NAV_ROUTES = ADMIN_ROUTE_DESCRIPTORS.filter(
  (route) =>
    'nav' in route && route.nav && !ADMIN_PRIMARY_NAV.some((item) => item.key === route.id),
).map((route) => route.id);
