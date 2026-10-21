import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { createModel, patchModel } from './api';
import { normalizeModel } from './normalizers';
import type { ModelCreateInput } from './types';

const raw = {
  ...JSON.parse(
    readFileSync(
      resolve(process.cwd(), '..', 'internal/resources/testdata/manual_update.json'),
      'utf8',
    ),
  ).affected_models[0].model,
  cloudflare_proxy_enabled: true,
  cloudflare_proxy_mode: 'legacy',
};
const operation = { idempotencyKey: 'cloudflare-model-example-key', actionId: 'model-save' };
const response = (status: number) =>
  new Response(JSON.stringify(raw), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
afterEach(() => vi.unstubAllGlobals());
describe('personal model Cloudflare API', () => {
  it.each([false, true])(
    'sends a bool on creation and a setting-only patch (%s)',
    async (enabled) => {
      const mock = vi
        .fn<typeof fetch>()
        .mockResolvedValueOnce(response(201))
        .mockResolvedValueOnce(response(200));
      vi.stubGlobal('fetch', mock);
      await createModel(
        { provider: raw.provider, model: raw.model, cloudflare_proxy_enabled: enabled },
        operation,
      );
      await patchModel(
        raw.id,
        { expected_revision: '1', cloudflare_proxy_enabled: enabled },
        operation,
      );
      expect(JSON.parse(String(mock.mock.calls[0][1]!.body))).toEqual({
        provider: raw.provider,
        model: raw.model,
        cloudflare_proxy_enabled: enabled,
      });
      expect(JSON.parse(String(mock.mock.calls[1][1]!.body))).toEqual({
        expected_revision: '1',
        cloudflare_proxy_enabled: enabled,
      });
    },
  );
  it('keeps omitted settings omitted and never sends read-only mode', async () => {
    const mock = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(response(201))
      .mockResolvedValueOnce(response(200));
    vi.stubGlobal('fetch', mock);
    expect(
      await createModel({ provider: raw.provider, model: raw.model }, operation),
    ).toMatchObject({ cloudflare_proxy_enabled: true, cloudflare_proxy_mode: 'legacy' });
    await patchModel(raw.id, { expected_revision: '1', silent_retry: true }, operation);
    for (const call of mock.mock.calls) {
      const body = JSON.parse(String(call[1]!.body));
      expect(body).not.toHaveProperty('cloudflare_proxy_enabled');
      expect(body).not.toHaveProperty('cloudflare_proxy_mode');
    }
  });
  it.each([null, 'true', 1])('rejects a non-boolean setting %s before sending', async (value) => {
    const mock = vi.fn<typeof fetch>();
    vi.stubGlobal('fetch', mock);
    await expect(
      createModel(
        {
          provider: raw.provider,
          model: raw.model,
          cloudflare_proxy_enabled: value,
        } as unknown as ModelCreateInput,
        operation,
      ),
    ).rejects.toThrow();
    expect(mock).not.toHaveBeenCalled();
    expect(() => normalizeModel({ ...raw, cloudflare_proxy_enabled: value })).toThrow();
  });
  it('requires authoritative setting and a known mode value', () => {
    for (const value of [undefined, null, '', 'unknown'])
      expect(() => normalizeModel({ ...raw, cloudflare_proxy_mode: value })).toThrow();
    expect(() => normalizeModel({ ...raw, cloudflare_proxy_enabled: undefined })).toThrow();
  });
});
