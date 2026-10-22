import { screen, waitFor } from '@testing-library/react';
import { it, expect, vi } from 'vitest';
import { renderWithProviders } from '../../../../../test/unit/support';
import { CredentialVersions } from './CredentialVersions';
it('shows direct-enable unknown recovery in its row and replays the same intent without another confirmation', async () => {
  let enabled = false;
  const writes: { body: unknown; key: string | null }[] = [];
  const json = (value: unknown) =>
    new Response(JSON.stringify(value), { headers: { 'content-type': 'application/json' } });
  vi.stubGlobal(
    'fetch',
    vi.fn<typeof fetch>(async (input, init) => {
      const url = new URL(String(input), 'https://app.example');
      if (init?.method === 'PATCH') {
        writes.push({
          body: JSON.parse(String(init.body)),
          key: new Headers(init.headers).get('Idempotency-Key'),
        });
        if (writes.length < 3) throw new TypeError('Failed to fetch');
        enabled = true;
        return json({ ok: true });
      }
      if (url.pathname === '/api/credentials/8/versions')
        return json({
          data: [
            {
              credential_id: '8',
              credential_version: '1',
              enabled,
              revision: enabled ? '2' : '1',
              display_head: 'sk-a',
              display_tail: 'tail',
              created_at: 1700000000,
            },
          ],
          next_cursor: null,
          pagination: { page: '1', page_size: 20, total_items: '1', total_pages: '1' },
        });
      if (url.pathname === '/api/time-context') return json({ mode: 'site', offset_minutes: 0 });
      throw Error('Unexpected ' + url.pathname);
    }),
  );
  const view = await renderWithProviders(<CredentialVersions accountId="1" credentialId="8" />, {
    station: 'user',
    role: 'user',
  });
  view.queryClient.setQueryData(['user', 'session'], {
    user: { id: '1', username: 'owner', effective_level: 1 },
  });
  await view.user.click(await screen.findByRole('button', { name: 'Enable' }));
  const check = await screen.findByRole('button', { name: 'Check saved result' });
  expect(screen.queryByRole('alertdialog')).toBeNull();
  expect(screen.getByRole('button', { name: 'Enable' })).toBeDisabled();
  await view.user.click(check);
  await screen.findByRole('button', { name: 'Disable' });
  await waitFor(() => expect(writes).toHaveLength(3));
  expect(writes.every((row) => JSON.stringify(row) === JSON.stringify(writes[0]))).toBe(true);
  expect(writes[0]?.body).toEqual({ enabled: true, expected_revision: '1' });
});
