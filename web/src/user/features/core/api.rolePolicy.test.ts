import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { createModel, patchModel } from './api';
import { normalizeModel } from './normalizers';

const raw = JSON.parse(
  readFileSync(
    resolve(process.cwd(), '..', 'internal/resources/testdata/manual_update.json'),
    'utf8',
  ),
).affected_models[0].model;
const operation = { idempotencyKey: 'personal-role-policy-example-key', actionId: 'role-save' };
function response(model: unknown, status: number) {
  return new Response(JSON.stringify(model), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

afterEach(() => vi.unstubAllGlobals());

describe('personal model role policy API', () => {
  it('sends role policy on create and role-only PATCH with the original revision and identity', async () => {
    const policy = {
      default_action: 'reject' as const,
      rules: { developer: 'system' as const, critic: 'user' as const },
    };
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(response({ ...raw, role_policy: policy }, 201))
      .mockResolvedValueOnce(response({ ...raw, revision: '2', role_policy: policy }, 200));
    vi.stubGlobal('fetch', fetchMock);
    await createModel({ provider: raw.provider, model: raw.model, role_policy: policy }, operation);
    await patchModel(raw.id, { expected_revision: '1', role_policy: policy }, operation);
    const create = fetchMock.mock.calls[0][1]!;
    const patch = fetchMock.mock.calls[1][1]!;
    expect(JSON.parse(String(create.body)).role_policy).toEqual(policy);
    expect(JSON.parse(String(patch.body))).toEqual({ expected_revision: '1', role_policy: policy });
    expect(patch.method).toBe('PATCH');
    expect(new Headers(patch.headers).get('Idempotency-Key')).toBe(operation.idempotencyKey);
  });

  it('keeps omitted PATCH policy omitted and defaults missing legacy reads to native', async () => {
    const legacy = { ...raw };
    delete legacy.role_policy;
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(response(legacy, 200));
    vi.stubGlobal('fetch', fetchMock);
    expect(normalizeModel(legacy).role_policy).toEqual({ default_action: 'native', rules: {} });
    await patchModel(raw.id, { expected_revision: '1', silent_retry: true }, operation);
    expect(JSON.parse(String(fetchMock.mock.calls[0][1]!.body))).not.toHaveProperty('role_policy');
  });

  it('rejects explicit null rather than converting it to the native default', async () => {
    expect(() => normalizeModel({ ...raw, role_policy: null })).toThrow();
    const fetchMock = vi.fn<typeof fetch>();
    vi.stubGlobal('fetch', fetchMock);
    await expect(
      patchModel(raw.id, { expected_revision: '1', role_policy: null } as never, operation),
    ).rejects.toMatchObject({ code: 'invalid_request' });
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
