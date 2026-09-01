import { encodeBase64URL } from '../common/strict';
import { secureBytes } from '../common/request';

const DEVICE_KEY = 'nonbiri.game.rps.device.v1';
const TOKEN = /^[A-Za-z0-9_-]{43}$/;

/** Browser-scoped, deliberately not keyed by account or copied into query state. */
export function rpsDeviceToken(
  storage: Pick<Storage, 'getItem' | 'setItem'> = localStorage,
): string {
  try {
    const existing = storage.getItem(DEVICE_KEY);
    if (existing && TOKEN.test(existing)) return existing;
    const created = encodeBase64URL(secureBytes(32));
    if (!TOKEN.test(created)) throw new Error('Invalid device token encoding.');
    storage.setItem(DEVICE_KEY, created);
    return created;
  } catch {
    const ephemeral = encodeBase64URL(secureBytes(32));
    if (!TOKEN.test(ephemeral)) throw new Error('Secure device identity is unavailable.');
    return ephemeral;
  }
}
