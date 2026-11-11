import type { StewardPermission, StewardPermissions } from './stewardPermissions';

export interface PermissionPair {
  id: string;
  read: StewardPermission;
  manage: StewardPermission;
}
export interface PermissionGroup {
  id: string;
  keys: StewardPermission[];
  pairs?: PermissionPair[];
}

// These are presentation groups, not new authority tiers. Presets only touch
// their two named grants; existing asymmetric policies remain custom.
export const permissionGroups: PermissionGroup[] = [
  { id: 'users', keys: ['users.read', 'users.profile', 'users.limits', 'users.level'] },
  {
    id: 'economy',
    keys: ['users.balance', 'users.game_balance', 'users.resources', 'users.loans.read'],
  },
  {
    id: 'safety',
    keys: [
      'users.ban',
      'users.ban_permanent',
      'users.unban',
      'users.penalties.read',
      'users.caller_keys.read',
      'users.caller_keys.revoke',
      'blacklist.read',
      'blacklist.add',
    ],
    pairs: [
      { id: 'callerKeys', read: 'users.caller_keys.read', manage: 'users.caller_keys.revoke' },
      { id: 'blacklist', read: 'blacklist.read', manage: 'blacklist.add' },
    ],
  },
  {
    id: 'donations',
    keys: [
      'users.donations.read',
      'donations.review',
      'donations.read_all',
      'donations.force_reject',
      'donations.handle',
      'donations.manage_keys',
      'donations.discover',
      'donations.recurring',
      'endpoints.read',
      'endpoints.tags',
    ],
    pairs: [{ id: 'endpoints', read: 'endpoints.read', manage: 'endpoints.tags' }],
  },
  {
    id: 'models',
    keys: [
      'charity.read',
      'charity.manage',
      'charity.non_mainstream',
      'charity.create',
      'charity.delete',
      'charity.model_settings',
      'charity.pricing',
      'charity.request_adaptation',
      'charity.affinity',
      'charity.presets',
      'charity.backoff',
      'charity.price_templates.read',
      'charity.price_templates.manage',
    ],
    pairs: [
      {
        id: 'priceTemplates',
        read: 'charity.price_templates.read',
        manage: 'charity.price_templates.manage',
      },
    ],
  },
  {
    id: 'audit',
    keys: [
      'logs.read',
      'logs.unredacted',
      'diagnostics.read',
      'pressure.read',
      'access_events.read',
      'risk.read',
      'risk.manage',
      'risk.configure',
    ],
    pairs: [{ id: 'risk', read: 'risk.read', manage: 'risk.manage' }],
  },
  {
    id: 'site',
    keys: [
      'announcements.read',
      'announcements.manage',
      'maintenance.read',
      'maintenance.enable',
      'egress.read',
      'automation',
    ],
    pairs: [
      { id: 'announcements', read: 'announcements.read', manage: 'announcements.manage' },
      { id: 'maintenance', read: 'maintenance.read', manage: 'maintenance.enable' },
    ],
  },
  { id: 'assistant', keys: ['ai.chat', 'ai.review.read'] },
];

export type PermissionMode = 'off' | 'read' | 'manage' | 'custom';

export function permissionPairMode(
  grants: StewardPermissions['permissions'],
  pair: PermissionPair,
): PermissionMode {
  return grants[pair.read]
    ? grants[pair.manage]
      ? 'manage'
      : 'read'
    : grants[pair.manage]
      ? 'custom'
      : 'off';
}

export function applyPermissionPair(
  grants: StewardPermissions['permissions'],
  pair: PermissionPair,
  mode: PermissionMode,
): StewardPermissions['permissions'] {
  if (mode === 'custom') return grants;
  return { ...grants, [pair.read]: mode !== 'off', [pair.manage]: mode === 'manage' };
}
