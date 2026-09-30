export type BrowserCapability =
  'secureRandom' | 'sessionStorage' | 'indexedDB' | 'locks' | 'broadcastChannel';
export interface BrowserCapabilityResult {
  available: boolean;
  missing: BrowserCapability[];
}

function databaseProbe(name: string, timeout: number): Promise<void> {
  return new Promise((resolve, reject) => {
    let finished = false;
    let database: IDBDatabase | undefined;
    const finish = (error?: Error) => {
      if (finished) return;
      finished = true;
      clearTimeout(timer);
      database?.close();
      try {
        indexedDB.deleteDatabase(name);
      } catch {
        /* The failed probe carries no user data. */
      }
      if (error) reject(error);
      else resolve();
    };
    const timer = setTimeout(() => finish(new Error('Storage check timed out.')), timeout);
    let request: IDBOpenDBRequest;
    try {
      request = indexedDB.open(name, 1);
    } catch {
      finish(new Error('Storage is unavailable.'));
      return;
    }
    request.onupgradeneeded = () => request.result.createObjectStore('probe');
    request.onerror = () => finish(new Error('Storage could not be opened.'));
    request.onblocked = () => finish(new Error('Storage is blocked.'));
    request.onsuccess = () => {
      database = request.result;
      if (finished) {
        database.close();
        indexedDB.deleteDatabase(name);
        return;
      }
      try {
        const transaction = database.transaction('probe', 'readwrite');
        const store = transaction.objectStore('probe');
        store.put('probe', 'probe');
        const read = store.get('probe');
        read.onsuccess = () => {
          if (read.result !== 'probe') transaction.abort();
        };
        transaction.oncomplete = () => {
          finish();
        };
        transaction.onabort = transaction.onerror = () => {
          finish(new Error('Storage could not be written.'));
        };
      } catch {
        finish(new Error('Storage could not be used.'));
      }
    };
  });
}

/** Probe required capabilities before a paid action; browser mode is never inferred. */
export async function checkBrowserCapabilities(
  required: readonly BrowserCapability[],
  timeout = 1500,
): Promise<BrowserCapabilityResult> {
  const missing: BrowserCapability[] = [];
  const name = `nb-capability-probe-${Date.now()}-${++probeSequence}`;
  for (const capability of new Set(required)) {
    try {
      switch (capability) {
        case 'secureRandom': {
          crypto.getRandomValues(new Uint8Array(1));
          break;
        }
        case 'sessionStorage': {
          try {
            sessionStorage.setItem(name, 'probe');
            if (sessionStorage.getItem(name) !== 'probe')
              throw new Error('Storage did not retain the value.');
          } finally {
            sessionStorage.removeItem(name);
          }
          break;
        }
        case 'indexedDB':
          await databaseProbe(name, timeout);
          break;
        case 'broadcastChannel': {
          const channel = new BroadcastChannel(name);
          try {
            channel.postMessage('probe');
          } finally {
            channel.close();
          }
          break;
        }
        case 'locks': {
          const controller = new AbortController();
          const timer = setTimeout(() => controller.abort(), timeout);
          try {
            await navigator.locks.request(name, { signal: controller.signal }, (lock) => {
              if (!lock) throw new Error('A lock could not be acquired.');
            });
          } finally {
            clearTimeout(timer);
            controller.abort();
          }
          break;
        }
      }
    } catch {
      missing.push(capability);
    }
  }
  return { available: missing.length === 0, missing };
}

let probeSequence = 0;
