export type ElevationIntent = 'export' | 'delete';
const ELEVATION_COOKIE = 'nb_elevated';
const PENDING_INTENT_KEY = 'nb.pending.elevation';
const PENDING_ACCOUNT_KEY = 'nb.pending.elevation.account';
const TOKEN_PATTERN = /^[A-Za-z0-9._-]{8,4096}$/;

export function clearElevatedCapabilityCookie(): void {
  if (typeof document !== 'undefined')
    document.cookie = `${ELEVATION_COOKIE}=; Path=/; Max-Age=0; SameSite=Lax`;
}
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
  let token: string;
  try {
    token = decodeURIComponent(matches[0]?.slice(ELEVATION_COOKIE.length + 1) ?? '');
  } catch {
    clearElevatedCapabilityCookie();
    return undefined;
  }
  clearElevatedCapabilityCookie();
  return TOKEN_PATTERN.test(token) ? token : undefined;
}
export function writePendingElevation(intent: ElevationIntent, accountId: string): void {
  try {
    window.sessionStorage.setItem(PENDING_INTENT_KEY, intent);
    window.sessionStorage.setItem(PENDING_ACCOUNT_KEY, accountId);
  } catch {
    /* Blocked storage leaves a manual retry path, without changing authorization. */
  }
}
export function readPendingElevation(accountId: string): ElevationIntent | null {
  try {
    const intent = window.sessionStorage.getItem(PENDING_INTENT_KEY);
    const owner = window.sessionStorage.getItem(PENDING_ACCOUNT_KEY);
    return owner === accountId && (intent === 'export' || intent === 'delete') ? intent : null;
  } catch {
    return null;
  }
}
export function clearPendingElevation(): void {
  try {
    window.sessionStorage.removeItem(PENDING_INTENT_KEY);
    window.sessionStorage.removeItem(PENDING_ACCOUNT_KEY);
  } catch {
    /* Best effort cleanup of non-secret continuation state. */
  }
}

/** Consume once only. A missing/cancelled/wrong-account return never authorizes a download. */
export function consumeElevationReturn(
  accountId: string,
): { intent: ElevationIntent; token: string } | null {
  const intent = readPendingElevation(accountId);
  const token = moveElevatedCapabilityFromCookie();
  clearPendingElevation();
  return intent && token ? { intent, token } : null;
}
