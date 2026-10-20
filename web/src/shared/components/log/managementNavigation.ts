import { listReturnPath } from '@shared/operations/listReturn';
import type { LogRole } from './data';

export function charityManagementPath(
  role: Exclude<LogRole, 'user'>,
  modelID: string,
  key?: { donationID: string; keyID: string },
): string {
  const params = new URLSearchParams(role === 'steward' ? { tab: 'charity' } : {});
  params.set('charity_section', 'models');
  params.set('charity_model', modelID);
  if (key) {
    params.set('managed_donation', key.donationID);
    params.set('managed_key', key.keyID);
  }
  return `${role === 'admin' ? '/charity' : '/steward'}?${params}`;
}

export function logManagementReturnPath(
  state: unknown,
  role: Exclude<LogRole, 'user'>,
): string | undefined {
  if (!state || typeof state !== 'object' || Array.isArray(state)) return undefined;
  const value = (state as Record<string, unknown>).logReturnTo;
  if (typeof value !== 'string') return undefined;
  const path = listReturnPath({ returnTo: value }, role === 'admin' ? '/logs' : '/steward');
  if (path !== value) return undefined;
  if (role === 'steward' && new URLSearchParams(path.split('?')[1]).get('tab') !== 'logs')
    return undefined;
  return path;
}
