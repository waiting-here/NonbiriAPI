import { describe, expect, it, vi } from 'vitest';
import { executeConnection } from './resourceOperation';

const input = {
  base_url: 'https://catalog.example/v1',
  list_format: 'openai' as const,
  credential_id: '8',
  enabled: true,
};
const credential = {
  id: '8',
  connection_id: '9',
  note: '',
  enabled: true,
  revision: '1',
  credential_version: '2',
  display_head: '',
  display_tail: '',
  created_at: 1,
  updated_at: 1,
};
const source = {
  id: '17',
  connection_id: '9',
  name: '',
  ...input,
  credential_version: '2',
  revision: '1',
  discovery_source_revision: '1',
  models_url: 'https://catalog.example/v1/models',
  state: 'unknown',
  checked_at: null,
  safe_class: 'none',
  safe_diag: '',
  created_at: 1,
  updated_at: 1,
};
const json = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } });
const page = (data: unknown[], number = '1', pages = '1') =>
  json({
    data,
    next_cursor: null,
    pagination: {
      page: number,
      page_size: 100,
      total_items: String(data.length),
      total_pages: pages,
    },
  });
const run = () =>
  executeConnection(
    { kind: 'discovery_source', connectionId: '9', input, suggestedCredentialVersion: '2' },
    undefined,
    'ABCDEFGHIJKLMNOPQRSTUV',
    new AbortController().signal,
  );

describe('suggested discovery catalogs', () => {
  it('finds an exact available version across pages without changing existing catalogs', async () => {
    const calls: string[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(async (url, init) => {
        expect(init?.method ?? 'GET').toBe('GET');
        const parsed = new URL(String(url), 'https://app.example');
        calls.push(parsed.pathname);
        if (parsed.pathname === '/api/credentials/8') return json(credential);
        expect(parsed.searchParams.get('q')).toBe(input.base_url);
        return parsed.searchParams.get('page') === '1'
          ? page(
              [
                { ...source, credential_version: '1' },
                { ...source, enabled: false },
                { ...source, list_format: 'anthropic' },
                { ...source, credential_available: false },
              ],
              '1',
              '2',
            )
          : page([source], '2', '2');
      }),
    );
    await expect(run()).resolves.toEqual({ kind: 'discovery_source', source });
    expect(calls).toHaveLength(3);
  });
  it('creates once and reuses the exact catalog for another entry', async () => {
    let created = false;
    const writes: { body: unknown; key: string | null }[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(async (url, init) => {
        if (String(url).startsWith('/api/credentials/')) return json(credential);
        if (init?.method === 'POST') {
          writes.push({
            body: JSON.parse(String(init.body)),
            key: new Headers(init.headers).get('Idempotency-Key'),
          });
          created = true;
          return json(source, 201);
        }
        return page(created ? [source] : []);
      }),
    );
    await expect(run()).resolves.toMatchObject({ source: { id: '17' } });
    await expect(run()).resolves.toMatchObject({ source: { id: '17' } });
    expect(writes).toEqual([{ body: input, key: 'ABCDEFGHIJKLMNOPQRSTUV' }]);
  });
  it('rejects a selected credential that rotated before saving without creating a catalog', async () => {
    const fetch = vi.fn<typeof fetch>(async () => json({ ...credential, credential_version: '3' }));
    vi.stubGlobal('fetch', fetch);
    await expect(run()).rejects.toMatchObject({ code: 'conflict', status: 409 });
    expect(fetch).toHaveBeenCalledTimes(1);
  });
  it('does not return a catalog created with a different version during rotation', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(async (url, init) => {
        if (String(url).startsWith('/api/credentials/')) return json(credential);
        if (init?.method === 'POST') return json({ ...source, credential_version: '3' }, 201);
        return page([]);
      }),
    );
    await expect(run()).rejects.toMatchObject({ code: 'conflict', status: 409 });
  });
});
