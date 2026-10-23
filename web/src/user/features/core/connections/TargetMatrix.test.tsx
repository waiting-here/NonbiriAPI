import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
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
  it('loads candidates through the normal API adapter and adds an explicit native target', async () => {
    const fixture = JSON.parse(
      readFileSync(
        resolve(process.cwd(), '../internal/resources/testdata/binding_candidates.json'),
        'utf8',
      ),
    );
    const requests: string[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(async (input) => {
        requests.push(String(input));
        return new Response(
          JSON.stringify({
            data: [
              {
                ...fixture.data[0],
                endpoint_key_id: '13',
                upstream_model_id: 'vendor/c',
                endpoint_note: 'Entry C',
              },
            ],
            next_cursor: null,
            pagination: { page: '1', page_size: 20, total_items: '1', total_pages: '1' },
          }),
          { headers: { 'Content-Type': 'application/json' } },
        );
      }),
    );
    const view = await renderWithProviders(<Harness />, environment);
    await view.user.click(screen.getByText('Add target', { exact: true }));
    const table = await screen.findByRole('table', { name: 'Available targets' });
    expect(requests).toEqual(['/api/models/31/binding-candidates?page=1&page_size=20']);
    await view.user.click(within(table).getByRole('button', { name: 'Add target' }));
    const added = screen.getByRole('group', { name: '3. Entry C' });
    const chat = within(added).getByRole('checkbox', { name: 'Chat completions' });
    expect(chat).not.toBeChecked();
    await view.user.click(chat);
    const rows = JSON.parse(screen.getByTestId('targets').textContent!) as MatrixTarget[];
    expect(rows[2]).toMatchObject({
      endpoint_key_id: '13',
      upstream_model_id: 'vendor/c',
      native_services: ['chat_completions'],
    });
  });
  it('applies an explicit bulk selection only to empty targets, preserving an exception and target order', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>().mockResolvedValue(
        new Response(
          JSON.stringify({
            data: [],
            next_cursor: null,
            pagination: { page: '1', page_size: 20, total_items: '0', total_pages: '1' },
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
