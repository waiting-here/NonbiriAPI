import { decoded, idempotentOptions } from './api';
import { boolean, decimal, decimalID, record } from './wire';
import type { ManagementRole } from './managedUsers';

export const STEWARD_PERMISSION_DEFAULTS = {
  'users.read': true,
  'users.profile': false,
  'users.limits': true,
  'users.level': false,
  'users.balance': false,
  'users.game_balance': false,
  'users.ban': true,
  'users.unban': false,
  'users.ban_permanent': true,
  'users.caller_keys.read': true,
  'users.caller_keys.revoke': true,
  'users.loans.read': true,
  'users.penalties.read': true,
  'users.donations.read': true,
  'blacklist.read': false,
  'blacklist.add': false,
  'donations.review': true,
  'donations.read_all': false,
  'donations.force_reject': false,
  'donations.handle': true,
  'donations.manage_keys': true,
  'donations.discover': true,
  'donations.recurring': true,
  'charity.read': true,
  'charity.create': false,
  'charity.delete': false,
  'charity.model_settings': false,
  'charity.pricing': false,
  'charity.manage': true,
  'charity.non_mainstream': false,
  'charity.presets': false,
  'charity.backoff': false,
  'charity.price_templates.read': false,
  'logs.read': true,
  'logs.unredacted': false,
  'diagnostics.read': true,
  'pressure.read': true,
  'access_events.read': false,
  'risk.read': false,
  'risk.manage': false,
  'announcements.read': false,
  'announcements.manage': false,
  'maintenance.read': false,
  'maintenance.enable': false,
  'egress.read': false,
  automation: false,
} as const;

export const STEWARD_LIMIT_MAXIMUMS = {
  'users.endpoint_limit_max': '10000',
  'users.rpm_limit_max': '4096',
  'users.concurrency_limit_max': '100000',
  'users.balance_adjustment_max_milli': '9000000000000000',
  'users.game_balance_adjustment_max_milli': '9000000000000000',
  'users.ban_duration_max_seconds': '316224000',
} as const;

export type StewardPermission = keyof typeof STEWARD_PERMISSION_DEFAULTS;
export type StewardLimit = keyof typeof STEWARD_LIMIT_MAXIMUMS;
export interface StewardPermissions {
  revision: string;
  permissions: Record<StewardPermission, boolean>;
  limits: Record<StewardLimit, string>;
}
export const stewardPermissionKeys = Object.keys(
  STEWARD_PERMISSION_DEFAULTS,
) as StewardPermission[];
export const stewardLimitKeys = Object.keys(STEWARD_LIMIT_MAXIMUMS) as StewardLimit[];

export function defaultStewardPermissions(level: number): StewardPermissions {
  return {
    revision: '0',
    permissions: Object.fromEntries(
      stewardPermissionKeys.map((key) => [
        key,
        level === 6 || (level === 5 && STEWARD_PERMISSION_DEFAULTS[key]),
      ]),
    ) as StewardPermissions['permissions'],
    limits: Object.fromEntries(
      stewardLimitKeys.map((key) => [key, level >= 5 ? STEWARD_LIMIT_MAXIMUMS[key] : '0']),
    ) as StewardPermissions['limits'],
  };
}

export function normalizeStewardPermissions(value: unknown): StewardPermissions {
  const root = record(value, ['revision', 'permissions', 'limits'], 'steward permissions');
  const permissions = record(root.permissions, stewardPermissionKeys, 'steward grants');
  const limits = record(root.limits, stewardLimitKeys, 'steward limits');
  return {
    revision: decimal(root.revision, 'permission revision'),
    permissions: Object.fromEntries(
      stewardPermissionKeys.map((key) => [key, boolean(permissions[key], key)]),
    ) as StewardPermissions['permissions'],
    limits: Object.fromEntries(
      stewardLimitKeys.map((key) => [key, decimal(limits[key], key)]),
    ) as StewardPermissions['limits'],
  };
}

export function hasStewardPermission(
  user: { effective_level: number; steward_permissions?: StewardPermissions } | null | undefined,
  key: StewardPermission,
): boolean {
  if (!user || user.effective_level < 5) return false;
  if (user.effective_level === 6) return true;
  return (user.steward_permissions ?? defaultStewardPermissions(5)).permissions[key];
}

function policyPath(role: ManagementRole, userId: string): string {
  return `${role === 'admin' ? '/admin/api' : '/api/steward'}/users/${decimalID(userId, 'user id')}/steward-permissions`;
}

export function getStewardPermissions(role: ManagementRole, userId: string, signal?: AbortSignal) {
  return decoded(policyPath(role, userId), normalizeStewardPermissions, { signal });
}

export function saveStewardPermissions(
  role: ManagementRole,
  userId: string,
  policy: StewardPermissions,
  key: string,
  signal?: AbortSignal,
) {
  return decoded(
    policyPath(role, userId),
    normalizeStewardPermissions,
    idempotentOptions(key, {
      method: 'PUT',
      signal,
      json: {
        expected_revision: policy.revision,
        permissions: policy.permissions,
        limits: policy.limits,
      },
    }),
  );
}
