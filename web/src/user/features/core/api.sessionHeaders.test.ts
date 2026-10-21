import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { createModel, patchModel } from './api';
import { normalizeModel } from './normalizers';
import { SESSION_HEADER_FORMATS } from '@shared/sessionHeaders';
const raw = JSON.parse(
  readFileSync(
    resolve(process.cwd(), '..', 'internal/resources/testdata/manual_update.json'),
    'utf8',
  ),
).affected_models[0].model;
const identity = { idempotencyKey: 'model-session-headers-example', actionId: 'model-save' };
const response = (model: unknown, status = 200) =>
  new Response(JSON.stringify(model), { status, headers: { 'Content-Type': 'application/json' } });
afterEach(() => vi.unstubAllGlobals());
describe('personal model session header API', () => {
  it('creates and reads all formats including zero, and clears with an empty array', async () => {
    const headers = SESSION_HEADER_FORMATS.map((format, i) => ({
      name: `x-session-${i}`,
      idle_seconds: 0,
      format,
    }));
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(response({ ...raw, session_headers: headers }, 201))
      .mockResolvedValueOnce(response({ ...raw, session_headers: [] }));
    vi.stubGlobal('fetch', fetchMock);
    expect(
      (
        await createModel(
          { provider: raw.provider, model: raw.model, session_headers: headers },
          identity,
        )
      ).session_headers,
    ).toEqual(headers);
    expect(
      (await patchModel(raw.id, { expected_revision: '1', session_headers: [] }, identity))
        .session_headers,
    ).toEqual([]);
    expect(JSON.parse(String(fetchMock.mock.calls[0][1]!.body)).session_headers).toEqual(headers);
    expect(JSON.parse(String(fetchMock.mock.calls[1][1]!.body))).toEqual({
      expected_revision: '1',
      session_headers: [],
    });
  });
  it('preserves omission while defaulting only supplied configuration', async () => {
    const fetchMock = vi.fn<typeof fetch>().mockImplementation(async () => response(raw));
    vi.stubGlobal('fetch', fetchMock);
    await patchModel(raw.id, { expected_revision: '1', silent_retry: true }, identity);
    expect(JSON.parse(String(fetchMock.mock.calls[0][1]!.body))).not.toHaveProperty(
      'session_headers',
    );
    await patchModel(
      raw.id,
      { expected_revision: '1', session_headers: [{ name: 'x-session-id' }] },
      identity,
    );
    expect(JSON.parse(String(fetchMock.mock.calls[1][1]!.body)).session_headers).toEqual([
      { name: 'x-session-id', idle_seconds: 300, format: 'uuid_v4' },
    ]);
    expect(normalizeModel(raw).session_headers).toEqual([]);
  });
  it('rejects explicit null before dispatch instead of silently clearing', async () => {
    const fetchMock = vi.fn<typeof fetch>();
    vi.stubGlobal('fetch', fetchMock);
    expect(() => normalizeModel({ ...raw, session_headers: null })).toThrow();
    await expect(
      patchModel(raw.id, { expected_revision: '1', session_headers: null } as never, identity),
    ).rejects.toMatchObject({ code: 'invalid_request' });
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
