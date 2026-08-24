export interface NavItem {
  to: string;
  key: string;
  end?: boolean;
}

export const NAV_ITEMS: NavItem[] = [
  { to: '/', end: true, key: 'home' },
  { to: '/endpoints', key: 'endpoints' },
  { to: '/models', key: 'models' },
  { to: '/games', key: 'games' },
  { to: '/charity', key: 'charity' },
  { to: '/keys', key: 'keys' },
  { to: '/debug', key: 'debug' },
  { to: '/logs', key: 'logs' },
  { to: '/issues', key: 'issues' },
  { to: '/account', key: 'account' },
];

// The co-management entry appears only while the session reports effective
// level 5. This is display state only: every steward route re-resolves the
// current level server-side before exposing its bounded management surface.
export const STEWARD_NAV_ITEM: NavItem = { to: '/steward', key: 'steward' };
