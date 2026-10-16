import { afterEach, expect, it, vi } from 'vitest';
import { apiFetch, ApiError } from './http';
import { coreRawRequest, callerSafeError } from '../../user/features/core/request';
import { gameRequest } from '../../user/games/common/request';

const originalLanguage = document.documentElement.lang;
afterEach(() => {
  document.documentElement.lang = originalLanguage;
  vi.unstubAllGlobals();
});

it.each([
  ['shared', () => apiFetch('/api/test')],
  ['core', () => coreRawRequest('/api/test')],
  ['game', () => gameRequest('/api/test')],
] as const)('sends the current UI language on each %s request', async (_, request) => {
  const fetcher = vi.fn(async () => new Response('{}', { status: 200 }));
  vi.stubGlobal('fetch', fetcher);
  for (const [language, expected] of [
    ['zh-CN', 'zh-CN'],
    ['en', 'en'],
    ['zh', 'zh-CN'],
  ]) {
    document.documentElement.lang = language;
    await request();
    const options = (fetcher.mock.calls.at(-1) as unknown as [string, RequestInit])[1];
    expect(new Headers(options.headers).get('Accept-Language')).toBe(expected);
  }
});

it.each([
  ['shared', () => apiFetch('/api/test')],
  ['core', () => coreRawRequest('/api/test')],
  ['game', () => gameRequest('/api/test')],
] as const)('keeps localized %s errors and normalizes text once', async (_, request) => {
  vi.stubGlobal(
    'fetch',
    vi.fn(
      async () =>
        new Response(
          JSON.stringify({
            error: { code: 'conflict', message: '[NonbiriAPI] 当前状态已变化。\u0000' },
          }),
          { status: 409 },
        ),
    ),
  );
  await expect(request()).rejects.toMatchObject({
    code: 'conflict',
    status: 409,
    message: '[NonbiriAPI] 当前状态已变化。',
  });
  const error = new ApiError('conflict', '当前状态已变化。', 409);
  expect(callerSafeError(error)).toBe(error.message);
});
