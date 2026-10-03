import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { createModel, patchModel } from './api';
import { normalizeModel } from './normalizers';
import { transportRules } from '@shared/transportRule';
const raw = JSON.parse(
  readFileSync(
    resolve(process.cwd(), '..', 'internal/resources/testdata/manual_update.json'),
    'utf8',
  ),
).affected_models[0].model;
const operation = { idempotencyKey: 'transport-policy-example-key', actionId: 'model-save' };
function response(model: unknown, status: number) {
  return new Response(JSON.stringify(model), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}
afterEach(() => vi.unstubAllGlobals());
describe('model transport API', () => {
  it.each(transportRules)('round-trips %s on create and rule-only patch', async (rule) => {
    const mock = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(response({ ...raw, transport_rule: rule }, 201))
      .mockResolvedValueOnce(response({ ...raw, transport_rule: rule, revision: '2' }, 200));
    vi.stubGlobal('fetch', mock);
    expect(
      (
        await createModel(
          { provider: raw.provider, model: raw.model, transport_rule: rule },
          operation,
        )
      ).transport_rule,
    ).toBe(rule);
    expect(
      (await patchModel(raw.id, { expected_revision: '1', transport_rule: rule }, operation))
        .transport_rule,
    ).toBe(rule);
    expect(JSON.parse(String(mock.mock.calls[1][1]!.body))).toEqual({
      expected_revision: '1',
      transport_rule: rule,
    });
  });
  it('keeps omitted patch transport omitted and rejects invalid read values', async () => {
    const mock = vi
      .fn<typeof fetch>()
      .mockResolvedValue(response({ ...raw, transport_rule: 'force_stream' }, 200));
    vi.stubGlobal('fetch', mock);
    expect(
      (await patchModel(raw.id, { expected_revision: '1', silent_retry: true }, operation))
        .transport_rule,
    ).toBe('force_stream');
    expect(JSON.parse(String(mock.mock.calls[0][1]!.body))).not.toHaveProperty('transport_rule');
    for (const value of [null, '', 'unknown'])
      expect(() => normalizeModel({ ...raw, transport_rule: value })).toThrow();
  });
});
