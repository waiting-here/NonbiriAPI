import { logRequestID } from './data';
import { logAttemptSequence } from './managementFailures';
import { listReturnPath } from '@shared/operations/listReturn';
import type { LogRole } from './data';

function safeContext(search: string): URLSearchParams {
  const original = new URLSearchParams(search),
    safe = new URLSearchParams();
  for (const [key, value] of original) {
    if (original.getAll(key).length !== 1) continue;
    const valid =
      key === 'request_id'
        ? logRequestID(value)
        : key === 'attempt_seq'
          ? logAttemptSequence(value)
          : /^(page|attempt_page)$/.test(key)
            ? /^[1-9]\d{0,8}$/.test(value)
            : /^(page_size|attempt_page_size)$/.test(key)
              ? ['10', '20', '50', '100'].includes(value)
              : key === 'range'
                ? ['15m', '1h', '24h', '7d'].includes(value)
                : ['from', 'to'].includes(key)
                  ? /^\d{1,12}$/.test(value) && Number(value) <= 253402300799
                  : false;
    if (valid) safe.set(key, value);
  }
  return safe;
}
export function withLogContext(destination: string, origin: string) {
  const search = safeContext(origin.split('?')[1] ?? '').toString();
  return (
    destination +
    (destination.includes('?') ? '&' : '?') +
    'log_context=' +
    encodeURIComponent(search)
  );
}
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
  destinationSearch = '',
): string | undefined {
  const value =
    state && typeof state === 'object' && !Array.isArray(state)
      ? (state as Record<string, unknown>).logReturnTo
      : undefined;
  const basePath = role === 'admin' ? '/logs' : '/steward';
  if (typeof value !== 'string') {
    const values = new URLSearchParams(destinationSearch).getAll('log_context');
    if (values.length !== 1 || values[0].length > 512) return undefined;
    const params = safeContext(values[0]);
    if (role === 'steward') params.set('tab', 'logs');
    return basePath + (params.size ? '?' + params.toString() : '');
  }
  const path = listReturnPath({ returnTo: value }, basePath);
  if (path !== value) return undefined;
  if (role === 'admin') return path;

  const params = new URLSearchParams(path.slice(basePath.length));
  const tab = params.get('tab');
  if (tab === 'logs') return path;
  if (tab !== null) return undefined;

  // The default steward entry can show logs before a tab is present in the URL.
  params.set('tab', 'logs');
  return `${basePath}?${params}`;
}
