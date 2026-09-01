import { decoded, idempotentOptions } from './api';
import { boolean, decimal, record } from './wire';

export type MaintenanceRole = 'steward' | 'admin';
export type MaintenanceAction = 'enable' | 'disable';

export interface MaintenanceState {
  enabled: boolean;
  revision: string;
}

export const maintenanceKeys = {
  root: (role: MaintenanceRole) => role === 'admin'
    ? ['admin', 'operations'] as const
    : ['user', 'operations', 'steward'] as const,
  state: (role: MaintenanceRole) => [...maintenanceKeys.root(role), 'maintenance'] as const,
};

export function normalizeMaintenanceState(value: unknown): MaintenanceState {
  const root = record(value, ['enabled', 'revision'], 'maintenance state');
  return {
    enabled: boolean(root.enabled, 'maintenance enabled'),
    revision: decimal(root.revision, 'maintenance revision', { positive: true }),
  };
}

export function getMaintenanceState(role: MaintenanceRole): Promise<MaintenanceState> {
  const path = role === 'admin' ? '/admin/api/maintenance' : '/api/steward/maintenance';
  return decoded(path, normalizeMaintenanceState);
}

export function enableMaintenance(
  role: MaintenanceRole,
  expectedRevision: string,
  reason: string,
  key: string,
): Promise<MaintenanceState> {
  const path = role === 'admin' ? '/admin/api/maintenance/enable' : '/api/steward/maintenance/enable';
  return decoded(path, normalizeMaintenanceState, idempotentOptions(key, {
    method: 'POST',
    json: { expected_revision: expectedRevision, reason, confirmation: true },
  }));
}

export function disableAdminMaintenance(
  expectedRevision: string,
  reason: string,
  key: string,
): Promise<MaintenanceState> {
  return decoded('/admin/api/maintenance/disable', normalizeMaintenanceState, idempotentOptions(key, {
    method: 'POST',
    json: { expected_revision: expectedRevision, reason },
  }));
}
