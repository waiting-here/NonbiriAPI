import { useState } from 'react';
import { screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../../test/unit/support';
import { TargetMatrix, type MatrixTarget } from './TargetMatrix';
const environment = { station: 'user', role: 'user', locale: 'en' } as const;
function Harness() {
  const [rows, setRows] = useState<MatrixTarget[]>([
    {
      endpoint_key_id: '11',
      upstream_model_id: 'vendor/a',
      native_services: ['responses'],
      options: ['chat_completions', 'responses'],
      label: 'Entry A',
    },
    {
      endpoint_key_id: '12',
      upstream_model_id: 'vendor/b',
      native_services: [],
      options: ['chat_completions', 'responses'],
      label: 'Entry B',
    },
  ]);
  return (
    <>
      <TargetMatrix accountId="1" modelId="31" value={rows} onChange={setRows} />
      <output data-testid="targets">{JSON.stringify(rows)}</output>
    </>
  );
}
describe('ordered native target matrix', () => {
  it('applies an explicit bulk selection only to empty targets, preserving an exception and target order', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>().mockResolvedValue(
        new Response(
          JSON.stringify({
            data: [],
            next_cursor: null,
            pagination: { page: '1', page_size: 10, total_items: '0', total_pages: '1' },
          }),
          { headers: { 'Content-Type': 'application/json' } },
        ),
      ),
    );
    const view = await renderWithProviders(<Harness />, environment);
    const bulk = screen.getByText('Bulk native service selection').closest('details')!;
    await view.user.click(within(bulk).getByText('Bulk native service selection'));
    await view.user.click(within(bulk).getByRole('checkbox', { name: 'Chat completions' }));
    await view.user.click(
      within(bulk).getByRole('button', { name: 'Apply to targets without a service selection' }),
    );
    let rows = JSON.parse(screen.getByTestId('targets').textContent!) as MatrixTarget[];
    expect(rows.map((row) => row.native_services)).toEqual([['responses'], ['chat_completions']]);
    const second = screen.getByRole('group', { name: '2. Entry B' });
    await view.user.click(within(second).getByRole('button', { name: 'Move up' }));
    rows = JSON.parse(screen.getByTestId('targets').textContent!) as MatrixTarget[];
    expect(rows.map((row) => row.upstream_model_id)).toEqual(['vendor/b', 'vendor/a']);
    expect(rows[1]?.native_services).toEqual(['responses']);
  });
});
