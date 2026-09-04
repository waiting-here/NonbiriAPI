import type { AccountExportAttachment, LifecycleIntent } from './types';

const ELEVATION_COOKIE = 'nb_elevated';
const PENDING_INTENT_KEY = 'nb.pending.elevation';
const PENDING_ACCOUNT_KEY = 'nb.pending.elevation.account';
const TOKEN_PATTERN = /^[A-Za-z0-9._-]{8,512}$/;

export function clearElevatedCapabilityCookie(): void {
  if (typeof document === 'undefined') return;
  document.cookie = `${ELEVATION_COOKIE}=; Path=/; Max-Age=0; SameSite=Lax`;
}

/** Moves the one-shot capability from the cookie into the current page flow. */
export function moveElevatedCapabilityFromCookie(): string | undefined {
  if (typeof document === 'undefined') return undefined;
  const matches = document.cookie
    .split(';')
    .map((part) => part.trim())
    .filter((part) => part.startsWith(`${ELEVATION_COOKIE}=`));
  if (matches.length !== 1) {
    if (matches.length > 0) clearElevatedCapabilityCookie();
    return undefined;
  }
  const encoded = matches[0]?.slice(ELEVATION_COOKIE.length + 1) ?? '';
  let token: string;
  try {
    token = decodeURIComponent(encoded);
  } catch {
    clearElevatedCapabilityCookie();
    return undefined;
  }
  clearElevatedCapabilityCookie();
  return TOKEN_PATTERN.test(token) ? token : undefined;
}

export function writePendingElevation(intent: LifecycleIntent, accountId: string): void {
  try {
    window.sessionStorage.setItem(PENDING_INTENT_KEY, intent);
    window.sessionStorage.setItem(PENDING_ACCOUNT_KEY, accountId);
  } catch {
    // Storage is only for the non-secret intent. A blocked store leaves the
    // user on the manual retry path and never changes authorization.
  }
}

export function readPendingElevation(accountId: string): LifecycleIntent | null {
  try {
    const intent = window.sessionStorage.getItem(PENDING_INTENT_KEY);
    const owner = window.sessionStorage.getItem(PENDING_ACCOUNT_KEY);
    if (owner !== accountId || (intent !== 'export' && intent !== 'delete')) return null;
    return intent;
  } catch {
    return null;
  }
}

export function clearPendingElevation(): void {
  try {
    window.sessionStorage.removeItem(PENDING_INTENT_KEY);
    window.sessionStorage.removeItem(PENDING_ACCOUNT_KEY);
  } catch {
    // Best effort cleanup for non-secret continuation state.
  }
}

function removeNamespace(storage: Storage, prefix: string): void {
  const keys: string[] = [];
  for (let index = 0; index < storage.length; index += 1) {
    const key = storage.key(index);
    if (key?.startsWith(prefix)) keys.push(key);
  }
  for (const key of keys) storage.removeItem(key);
}

export function clearAccountLocalNamespace(accountId: string): void {
  const prefix = `nb.account.${accountId}.`;
  try {
    removeNamespace(window.localStorage, prefix);
  } catch {
    // Best effort after authoritative deletion.
  }
  try {
    removeNamespace(window.sessionStorage, prefix);
  } catch {
    // Best effort after authoritative deletion.
  }
  clearPendingElevation();
  clearElevatedCapabilityCookie();
}

export function downloadAccountExport(attachment: AccountExportAttachment): void {
  const url = URL.createObjectURL(attachment.blob);
  const link = document.createElement('a');
  link.href = url;
  link.download = 'nonbiriapi-account-export-v4.json';
  link.rel = 'noopener';
  document.body.appendChild(link);
  link.click();
  link.remove();
  URL.revokeObjectURL(url);
}
