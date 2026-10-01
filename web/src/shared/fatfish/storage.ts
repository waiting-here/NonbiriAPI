import { hex, sha256 } from './engine/sha256';
import { MAX_INPUT_BYTES, MAX_INPUTS, type InputTuple } from './engine/types';

const databaseName = 'nonbiri-fatfish-inputs-v1';
const storeName = 'sessions';
const capabilityPrefix = 'nonbiri-fatfish-tab-v1:';
const pendingPrefix = `${capabilityPrefix}pending:`;
const readOnlyPrefix = `${capabilityPrefix}read-only:`;
const maximumLocalSessions = 32;

export interface StoredFishSession {
  id: string;
  capability_hash: string;
  content_hash: string;
  inputs: InputTuple[];
  anchor_wall_ms: number;
  anchor_elapsed_ms: number;
  start_key: string;
  submit_key: string;
  abandon_key: string;
  abandon_revision?: string;
  terminal_tick: number | null;
  accepted: boolean;
  accepted_at_ms: number | null;
  expires_at_ms: number;
}
interface PendingCapability { capability: string; key: string; expires_at_ms: number }

export function requireLocalPlaySupport(): void {
  if (typeof window === 'undefined' || !window.sessionStorage || !window.indexedDB ||
      !navigator.locks?.request || typeof BroadcastChannel === 'undefined' ||
      typeof crypto === 'undefined' || typeof crypto.getRandomValues !== 'function') {
    throw new Error('This browser cannot safely retain and exclusively resume a challenge.');
  }
}

export function localPlaySupportError(): string | null {
  try { requireLocalPlaySupport(); return null; }
  catch (error) { return error instanceof Error ? error.message : 'Local challenge storage is unavailable.'; }
}
export function freshKey(): string {
  if (typeof crypto === 'undefined' || typeof crypto.getRandomValues !== 'function')
    throw new Error('Secure random values are unavailable in this browser.');
  return hex(crypto.getRandomValues(new Uint8Array(24)));
}
export function newCapability(): string { return hex(crypto.getRandomValues(new Uint8Array(32))); }
export function capabilityHash(capability: string): string {
  if (!/^[0-9a-f]{64}$/.test(capability)) throw new Error('Invalid tab capability.');
  const bytes = Uint8Array.from(capability.match(/../g) ?? [], (pair) => Number.parseInt(pair, 16));
  return hex(sha256(bytes));
}
function validPending(value: unknown): value is PendingCapability {
  if (typeof value !== 'object' || value === null) return false;
  const candidate = value as Partial<PendingCapability>;
  return typeof candidate.capability === 'string' && /^[0-9a-f]{64}$/.test(candidate.capability) &&
    typeof candidate.key === 'string' && /^[A-Za-z0-9_-]{22,128}$/.test(candidate.key) &&
    Number.isSafeInteger(candidate.expires_at_ms) && (candidate.expires_at_ms ?? 0) > 0;
}
function pendingStorageKey(scope: string): string {
  if (!scope || scope.length > 256 || !/^[A-Za-z0-9:_-]+$/.test(scope))
    throw new Error('Invalid challenge prepare scope.');
  return `${pendingPrefix}${scope}`;
}
export function pendingCapability(scope: string): PendingCapability {
  requireLocalPlaySupport();
  const pendingKey = pendingStorageKey(scope);
  const existing = sessionStorage.getItem(pendingKey);
  if (existing) {
    try {
      const parsed: unknown = JSON.parse(existing);
      if (validPending(parsed)) {
        if (parsed.expires_at_ms > Date.now()) return parsed;
        sessionStorage.removeItem(pendingKey);
      }
    } catch { /* A damaged pending record cannot be reused. */ }
    if (sessionStorage.getItem(pendingKey)) throw new Error('The pending challenge record is damaged.');
  }
  const value = { capability: newCapability(), key: freshKey(), expires_at_ms: Date.now() + 60000 };
  sessionStorage.setItem(pendingKey, JSON.stringify(value));
  return value;
}
export function bindCapability(id: string, capability: string, scope: string): void {
  if (!/^[0-9a-f]{64}$/.test(capability)) throw new Error('Invalid tab capability.');
  sessionStorage.setItem(`${capabilityPrefix}${id}`, capability);
  sessionStorage.removeItem(pendingStorageKey(scope));
}
export function clearPendingCapability(scope: string): void {
  sessionStorage.removeItem(pendingStorageKey(scope));
}
export function isReadOnlyChallenge(id: string): boolean {
  return sessionStorage.getItem(`${readOnlyPrefix}${id}`) === '1';
}
export function markReadOnlyChallenge(id: string): void {
  sessionStorage.setItem(`${readOnlyPrefix}${id}`, '1');
}
export function clearReadOnlyChallenge(id: string): void {
  sessionStorage.removeItem(`${readOnlyPrefix}${id}`);
}
export function readCapability(id: string): string {
  const value = sessionStorage.getItem(`${capabilityPrefix}${id}`);
  if (!value || !/^[0-9a-f]{64}$/.test(value)) throw new Error('This tab has no valid challenge capability.');
  return value;
}
export function clearLocalCapability(id: string): void {
  sessionStorage.removeItem(`${capabilityPrefix}${id}`);
}

export function openFishDatabase(): Promise<IDBDatabase> {
  requireLocalPlaySupport();
  return new Promise((resolve, reject) => {
    const request = indexedDB.open(databaseName, 1);
    request.onupgradeneeded = () => { request.result.createObjectStore(storeName, { keyPath: 'id' }); };
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error ?? new Error('Local challenge storage is unavailable.'));
    request.onblocked = () => reject(new Error('Local challenge storage is blocked.'));
  });
}
export function pruneExpiredFishSessions(db: IDBDatabase, nowMS = Date.now()): Promise<void> {
  return new Promise((resolve, reject) => {
    const tx = db.transaction(storeName, 'readwrite');
    const live = new Set<string>();
    let count = 0;
    const cursor = tx.objectStore(storeName).openCursor();
    cursor.onsuccess = () => {
      const current = cursor.result;
      if (!current) return;
      const record = current.value as Partial<StoredFishSession>;
      if (!Number.isSafeInteger(record.expires_at_ms) || (record.expires_at_ms ?? 0) <= nowMS)
        current.delete();
      else {
        count++;
        if (typeof record.id === 'string') live.add(record.id);
      }
      current.continue();
    };
    tx.oncomplete = () => {
      for (let index = sessionStorage.length - 1; index >= 0; index--) {
        const key = sessionStorage.key(index);
        if (!key?.startsWith(capabilityPrefix)) continue;
        if (key.startsWith(pendingPrefix)) {
          try {
            const pending: unknown = JSON.parse(sessionStorage.getItem(key) ?? '');
            if (!validPending(pending) || pending.expires_at_ms <= nowMS)
              sessionStorage.removeItem(key);
          } catch { sessionStorage.removeItem(key); }
        } else if (key.startsWith(readOnlyPrefix)) {
          if (!live.has(key.slice(readOnlyPrefix.length))) sessionStorage.removeItem(key);
        } else if (!live.has(key.slice(capabilityPrefix.length))) sessionStorage.removeItem(key);
      }
      if (count > maximumLocalSessions) reject(new Error('Too many local challenge records are retained.'));
      else resolve();
    };
    tx.onerror = () => reject(tx.error ?? new Error('Local challenge cleanup failed.'));
    tx.onabort = () => reject(tx.error ?? new Error('Local challenge cleanup was interrupted.'));
  });
}
export async function cleanupLocalFishSessions(): Promise<void> {
  requireLocalPlaySupport();
  const db = await openFishDatabase();
  try { await pruneExpiredFishSessions(db); }
  finally { db.close(); }
}
function transactionResult<T>(db: IDBDatabase, mode: IDBTransactionMode, requestFactory: (store: IDBObjectStore) => IDBRequest<T>): Promise<T> {
  return new Promise((resolve, reject) => {
    const tx = db.transaction(storeName, mode);
    let result: T;
    const request = requestFactory(tx.objectStore(storeName));
    request.onsuccess = () => { result = request.result; };
    tx.oncomplete = () => resolve(result);
    tx.onerror = () => reject(tx.error ?? new Error('Local challenge storage failed.'));
    tx.onabort = () => reject(tx.error ?? new Error('Local challenge storage was interrupted.'));
  });
}
export function validateStoredSession(value: unknown, id: string): StoredFishSession {
  if (typeof value !== 'object' || value === null) throw new Error('The local challenge record is missing.');
  const record = value as Partial<StoredFishSession>;
  if (record.id !== id || !/^[0-9a-f]{64}$/.test(record.capability_hash ?? '') ||
      !/^[0-9a-f]{64}$/.test(record.content_hash ?? '') || !Array.isArray(record.inputs) ||
      record.inputs.length > MAX_INPUTS || !Number.isSafeInteger(record.anchor_wall_ms) ||
      !Number.isSafeInteger(record.anchor_elapsed_ms) || (record.anchor_elapsed_ms ?? -1) < 0 ||
      typeof record.start_key !== 'string' || typeof record.submit_key !== 'string' ||
      typeof record.abandon_key !== 'string' ||
      (record.terminal_tick !== null && (!Number.isSafeInteger(record.terminal_tick) || (record.terminal_tick ?? -1) < 0)) ||
      typeof record.accepted !== 'boolean' ||
      (record.accepted_at_ms !== null && !Number.isSafeInteger(record.accepted_at_ms)) ||
      !Number.isSafeInteger(record.expires_at_ms) || (record.expires_at_ms ?? 0) <= 0 ||
      JSON.stringify(record.inputs).length > MAX_INPUT_BYTES) {
    throw new Error('The local challenge record is damaged.');
  }
  return record as StoredFishSession;
}
export async function readFishSession(db: IDBDatabase, id: string): Promise<StoredFishSession> {
  return validateStoredSession(await transactionResult(db, 'readonly', (store) => store.get(id)), id);
}
export async function writeFishSession(db: IDBDatabase, record: StoredFishSession): Promise<void> {
  validateStoredSession(record, record.id);
  await new Promise<void>((resolve, reject) => {
    const tx = db.transaction(storeName, 'readwrite');
    const store = tx.objectStore(storeName);
    const existing = store.get(record.id);
    existing.onsuccess = () => {
      if (existing.result) { store.put(record); return; }
      const count = store.count();
      count.onsuccess = () => {
        if (count.result >= maximumLocalSessions) { tx.abort(); return; }
        store.put(record);
      };
    };
    tx.oncomplete = () => resolve();
    tx.onerror = () => reject(tx.error ?? new Error('Local challenge storage failed.'));
    tx.onabort = () => reject(new Error('The local challenge record limit or storage transaction failed.'));
  });
}
export async function deleteFishSession(db: IDBDatabase, id: string): Promise<void> {
  await transactionResult(db, 'readwrite', (store) => store.delete(id));
}
