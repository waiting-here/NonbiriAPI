import type { AccountExportAttachment } from './types';
import { clearElevatedCapabilityCookie, clearPendingElevation } from '@shared/operations/elevation';
export {
  clearElevatedCapabilityCookie,
  clearPendingElevation,
  moveElevatedCapabilityFromCookie,
  readPendingElevation,
  writePendingElevation,
} from '@shared/operations/elevation';

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
  link.download = `nonbiriapi-account-export-v${attachment.schemaVersion}.json`;
  link.rel = 'noopener';
  document.body.appendChild(link);
  link.click();
  link.remove();
  URL.revokeObjectURL(url);
}
