export interface NavItem {
  to: string;
  key: string;
  end?: boolean;
}

export const NAV_ITEMS: NavItem[] = [
  { to: '/', end: true, key: 'dashboard' },
  { to: '/users', key: 'users' },
  { to: '/logs', key: 'logs' },
  { to: '/endpoints', key: 'endpoints' },
  { to: '/alerts', key: 'alerts' },
  { to: '/settings', key: 'settings' },
  { to: '/games', key: 'games' },
  { to: '/charity', key: 'charity' },
];
