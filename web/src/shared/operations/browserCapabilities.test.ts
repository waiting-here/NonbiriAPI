import { describe, expect, it, vi } from 'vitest';
import { checkBrowserCapabilities } from './browserCapabilities';

describe('browser capability probes', () => {
  it('writes and reads real tab storage, cleans its probe and does not infer browser mode', async () => {
    const result = await checkBrowserCapabilities(['sessionStorage', 'secureRandom']);
    expect(result).toEqual({ available: true, missing: [] });
    expect(sessionStorage.length).toBe(0);
  });
  it('reports a storage write failure even when the API exists', async () => {
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new DOMException('Denied', 'SecurityError');
    });
    expect(await checkBrowserCapabilities(['sessionStorage'])).toEqual({
      available: false,
      missing: ['sessionStorage'],
    });
  });
  it('reports only required missing features', async () => {
    vi.stubGlobal('indexedDB', undefined);
    expect(await checkBrowserCapabilities(['indexedDB'])).toEqual({
      available: false,
      missing: ['indexedDB'],
    });
    expect(await checkBrowserCapabilities([])).toEqual({ available: true, missing: [] });
  });
  it('checks communication and lock use and closes the temporary channel', async () => {
    const postMessage = vi.fn();
    const close = vi.fn();
    vi.stubGlobal(
      'BroadcastChannel',
      class {
        postMessage = postMessage;
        close = close;
      },
    );
    Object.defineProperty(navigator, 'locks', {
      configurable: true,
      value: {
        request: vi.fn(
          async (_name: string, _options: unknown, callback: (lock: unknown) => void) =>
            callback({ name: 'probe' }),
        ),
      },
    });
    try {
      expect(await checkBrowserCapabilities(['locks', 'broadcastChannel'])).toEqual({
        available: true,
        missing: [],
      });
      expect(postMessage).toHaveBeenCalledOnce();
      expect(close).toHaveBeenCalledOnce();
    } finally {
      Reflect.deleteProperty(navigator, 'locks');
    }
  });
});
