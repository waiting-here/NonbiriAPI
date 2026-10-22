import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../../test/unit/support';
import { getEndpoint } from '../api';
import { ConnectionOperationNotice } from './OperationNotice';
import { useMetadataOperation } from './useMetadataOperation';
function Edit({ onSaved }: { onSaved: () => void }) {
  const operation = useMetadataOperation(
    '1',
    '/api/endpoints/11',
    'PATCH',
    (signal) => getEndpoint('11', signal),
    onSaved,
  );
  return (
    <>
      <button
        type="button"
        onClick={() =>
          void operation
            .mutateAsync({
              native_services: ['responses', 'chat_completions'],
              expected_revision: '1',
            })
            .catch(() => undefined)
        }
      >
        Save
      </button>
      <ConnectionOperationNotice operation={operation} />
    </>
  );
}
describe('metadata unknown-result recovery', () => {
  it('replays the original canonical body and key, then rereads authorized metadata with canonical service order', async () => {
    const writes: { body: unknown; key: string | null }[] = [];
    const reads: string[] = [];
    const endpoint = JSON.parse(
      readFileSync(resolve(process.cwd(), '../internal/resources/testdata/endpoint.json'), 'utf8'),
    );
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        if (init?.method === 'PATCH') {
          writes.push({
            body: JSON.parse(String(init.body)),
            key: new Headers(init.headers).get('Idempotency-Key'),
          });
          if (writes.length < 3) throw new TypeError('Failed to fetch');
          return new Response(JSON.stringify({ ok: true }), {
            headers: { 'Content-Type': 'application/json' },
          });
        }
        reads.push(String(input));
        return new Response(
          JSON.stringify({
            ...endpoint,
            native_services: ['chat_completions', 'responses'],
            revision: '2',
          }),
          { headers: { 'Content-Type': 'application/json' } },
        );
      }),
    );
    const onSaved = vi.fn();
    const view = await renderWithProviders(<Edit onSaved={onSaved} />, {
      station: 'user',
      role: 'level4',
    });
    view.queryClient.setQueryData(['user', 'session'], {
      user: { id: '1', username: 'reader', effective_level: 4 },
    });
    await view.user.click(screen.getByRole('button', { name: 'Save' }));
    await view.user.click(await screen.findByRole('button', { name: 'Check saved result' }));
    await waitFor(() => expect(onSaved).toHaveBeenCalledOnce());
    expect(writes).toHaveLength(3);
    expect(
      writes.every(
        (row) =>
          row.key === writes[0]!.key &&
          JSON.stringify(row.body) === JSON.stringify(writes[0]!.body),
      ),
    ).toBe(true);
    expect(writes[0]?.key).toBeTruthy();
    expect(reads).toHaveLength(1);
    expect(reads[0]).toContain('/api/endpoints/11');
  });
});
