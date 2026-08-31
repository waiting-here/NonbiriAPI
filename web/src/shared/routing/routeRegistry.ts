import type { IconName } from '@shared/components/Icon';

export type Station = 'user' | 'admin';
export type RouteAccess = 'public' | 'user' | 'admin';
export type RouteLayout = 'wide' | 'readable' | 'game' | 'notice';

export interface RouteDescriptor {
  id: string;
  path: string;
  access: RouteAccess;
  station: Station;
  layout: RouteLayout;
  end?: boolean;
  nav?: boolean;
  navGroup?: string;
  icon?: IconName;
  labelKey?: string;
  fallbackLabelKey?: string;
  /** Set only when the station router has an actual element for this route. */
  registered?: boolean;
}

const user = (descriptor: Omit<RouteDescriptor, 'station'>): RouteDescriptor => ({ ...descriptor, station: 'user' });
const admin = (descriptor: Omit<RouteDescriptor, 'station'>): RouteDescriptor => ({ ...descriptor, station: 'admin' });

/**
 * Route identity is deliberately data-only. Feature modules attach their
 * loaders/elements in the station router; this registry owns canonical slugs,
 * access intent, shell navigation and layout hints without inventing DTOs.
 */
export const USER_ROUTE_DESCRIPTORS = [
  user({ id: 'home', path: '/', access: 'public', layout: 'wide', end: true, nav: true, registered: true, icon: 'home', labelKey: 'user.home.nav' }),
  user({ id: 'endpoints', path: '/endpoints', access: 'user', layout: 'wide', nav: true, registered: true, icon: 'resources', labelKey: 'user.resources.nav', fallbackLabelKey: 'user.endpoints.nav' }),
  user({ id: 'endpoint-detail', path: '/endpoints/:endpointId', access: 'user', layout: 'readable', registered: true }),
  user({ id: 'models', path: '/models', access: 'user', layout: 'wide', nav: true, registered: true, icon: 'models', labelKey: 'user.models.nav' }),
  user({ id: 'charity', path: '/charity', access: 'user', layout: 'wide', nav: true, registered: true, icon: 'charity', labelKey: 'user.charity.nav' }),
  user({ id: 'donation-detail', path: '/charity/donations/:donationId', access: 'user', layout: 'readable' }),
  user({ id: 'activities', path: '/activities', access: 'user', layout: 'wide', nav: true, icon: 'activities', labelKey: 'user.activities.nav', fallbackLabelKey: 'user.games.nav' }),
  user({ id: 'games', path: '/games', access: 'user', layout: 'wide', nav: true, registered: true, icon: 'games', labelKey: 'user.games.nav' }),
  user({ id: 'game-fishing', path: '/games/fishing', access: 'user', layout: 'game' }),
  user({ id: 'game-linklink', path: '/games/linklink', access: 'user', layout: 'game' }),
  user({ id: 'game-rps', path: '/games/rps', access: 'user', layout: 'game' }),
  user({ id: 'debug', path: '/debug', access: 'user', layout: 'wide', nav: true, registered: true, icon: 'diagnostics', labelKey: 'user.diagnostics.nav', fallbackLabelKey: 'user.debug.nav' }),
  user({ id: 'logs', path: '/logs', access: 'user', layout: 'wide', registered: true }),
  user({ id: 'issues', path: '/issues', access: 'user', layout: 'readable', registered: true }),
  user({ id: 'credential-report', path: '/report', access: 'public', layout: 'readable' }),
  user({ id: 'announcements', path: '/announcements', access: 'user', layout: 'wide' }),
  user({ id: 'announcement-detail', path: '/announcements/:announcementId', access: 'user', layout: 'readable' }),
  user({ id: 'caller-key', path: '/keys', access: 'user', layout: 'readable', registered: true, icon: 'keys' }),
  user({ id: 'account', path: '/account', access: 'user', layout: 'readable', registered: true, icon: 'account', labelKey: 'user.account.nav' }),
  user({ id: 'steward', path: '/steward', access: 'user', layout: 'wide', registered: true, icon: 'steward', labelKey: 'user.steward.nav' }),
  user({ id: 'privacy', path: '/privacy', access: 'public', layout: 'readable', registered: true }),
  user({ id: 'terms', path: '/terms', access: 'public', layout: 'readable', registered: true }),
  user({ id: 'maintenance', path: '/maintenance', access: 'public', layout: 'notice', registered: true }),
  user({ id: 'registration-closed', path: '/registration-closed', access: 'public', layout: 'notice', registered: true }),
] as const satisfies readonly RouteDescriptor[];

export const ADMIN_ROUTE_DESCRIPTORS = [
  admin({ id: 'admin-home', path: '/', access: 'admin', layout: 'wide', end: true, nav: true, registered: true, navGroup: 'overview', icon: 'dashboard', labelKey: 'admin.navigation.overview', fallbackLabelKey: 'admin.dashboard.nav' }),
  admin({ id: 'admin-users', path: '/users', access: 'admin', layout: 'wide', nav: true, registered: true, navGroup: 'users', icon: 'users', labelKey: 'admin.navigation.users', fallbackLabelKey: 'admin.users.nav' }),
  admin({ id: 'admin-logs', path: '/logs', access: 'admin', layout: 'wide', nav: true, registered: true, navGroup: 'operations', icon: 'logs', labelKey: 'admin.logs.nav' }),
  admin({ id: 'admin-endpoints', path: '/endpoints', access: 'admin', layout: 'wide', nav: true, registered: true, navGroup: 'operations', icon: 'endpoints', labelKey: 'admin.endpoints.nav' }),
  admin({ id: 'admin-alerts', path: '/alerts', access: 'admin', layout: 'wide', nav: true, registered: true, navGroup: 'operations', icon: 'alerts', labelKey: 'admin.alerts.nav' }),
  admin({ id: 'admin-settings', path: '/settings', access: 'admin', layout: 'wide', nav: true, registered: true, navGroup: 'operations', icon: 'settings', labelKey: 'admin.settings.nav' }),
  admin({ id: 'admin-charity', path: '/charity', access: 'admin', layout: 'wide', nav: true, registered: true, navGroup: 'content', icon: 'charity', labelKey: 'admin.charity.nav' }),
  admin({ id: 'admin-activities', path: '/activities', access: 'admin', layout: 'wide', nav: true, navGroup: 'content', icon: 'activities', labelKey: 'admin.activities.nav', fallbackLabelKey: 'admin.games.nav' }),
  admin({ id: 'admin-games', path: '/games', access: 'admin', layout: 'wide', nav: true, registered: true, navGroup: 'content', icon: 'games', labelKey: 'admin.games.nav' }),
  admin({ id: 'admin-reports', path: '/reports', access: 'admin', layout: 'wide', nav: true, navGroup: 'content', icon: 'reports', labelKey: 'admin.reports.nav', fallbackLabelKey: 'admin.alerts.nav' }),
  admin({ id: 'admin-report-detail', path: '/reports/:caseId', access: 'admin', layout: 'readable' }),
  admin({ id: 'admin-announcements', path: '/announcements', access: 'admin', layout: 'wide', nav: true, navGroup: 'content', icon: 'announcements', labelKey: 'admin.announcements.nav', fallbackLabelKey: 'admin.settings.nav' }),
  admin({ id: 'admin-announcement-detail', path: '/announcements/:announcementId', access: 'admin', layout: 'readable' }),
] as const satisfies readonly RouteDescriptor[];

export const ROUTE_REGISTRY = {
  user: USER_ROUTE_DESCRIPTORS,
  admin: ADMIN_ROUTE_DESCRIPTORS,
} as const;

export function routeById(station: Station, id: string): RouteDescriptor | undefined {
  return ROUTE_REGISTRY[station].find((route) => route.id === id);
}

export function requiredRouteById(station: Station, id: string): RouteDescriptor {
  const route = routeById(station, id);
  if (!route) throw new Error(`Unknown ${station} route descriptor: ${id}`);
  return route;
}

export function relativeRoutePath(station: Station, id: string): string {
  const path = requiredRouteById(station, id).path;
  if (path === '/') return '';
  return path.slice(1);
}

export function routePath(station: Station, id: string, params: Record<string, string> = {}): string {
  const descriptor = requiredRouteById(station, id);
  return descriptor.path.replace(/:([A-Za-z][A-Za-z0-9_]*)/g, (_, key: string) => {
    const value = params[key];
    if (!value) throw new Error(`Missing route parameter: ${key}`);
    return encodeURIComponent(value);
  });
}
