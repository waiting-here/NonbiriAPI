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
    const endpoint = JSON.parse(
      readFileSync(resolve(process.cwd(), '../internal/resources/testdata/endpoint.json'), 'utf8'),
    );
    const key = JSON.parse(
      readFileSync(
        resolve(process.cwd(), '../internal/resources/testdata/endpoint_key.json'),
        'utf8',
      ),
    );
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(async (input) => {
        requests.push(String(input));
        const path = new URL(String(input), 'https://app.example').pathname;
        const rows =
          path === '/api/connections'
            ? []
            : path === '/api/endpoints'
              ? [{ ...endpoint, id: '11', key_count: '1' }]
              : path === '/api/endpoints/11/keys'
                ? [{ ...key, id: '13', endpoint_id: '11' }]
                : path === '/api/models/31/binding-candidates'
                  ? [
                      {
                        ...fixture.data[0],
                        endpoint_key_id: '13',
                        upstream_model_id: 'vendor/c',
                        endpoint_note: 'Entry C',
                      },
                    ]
                  : undefined;
        if (!rows) throw new Error('Unexpected request: ' + path);
        return new Response(
          JSON.stringify({
            data: rows,
            next_cursor: null,
            pagination: {
              page: '1',
              page_size: 20,
              total_items: String(rows.length),
              total_pages: '1',
            },
          }),
          { headers: { 'Content-Type': 'application/json' } },
        );
      }),
    );
    const view = await renderWithProviders(<Harness />, environment);
    await view.user.click(screen.getByText('Add model sources', { exact: true }));
    await view.user.click(await screen.findByRole('checkbox', { name: /vendor\/c/ }));
    const candidateRequest = requests.find((path) =>
      path.startsWith('/api/models/31/binding-candidates?'),
    );
    expect(candidateRequest).toBeDefined();
    const filters = new URL(candidateRequest!, 'https://app.example').searchParams;
    expect(Object.fromEntries(filters)).toMatchObject({
      endpoint_id: '11',
      key_id: '13',
      page: '1',
      page_size: '20',
    });
    const added = screen.getByRole('group', { name: '3. Entry C' });
    const chat = within(added).getByRole('checkbox', { name: 'Chat completions' });
    expect(chat).toBeChecked();
    for (const option of within(added).getAllByRole('checkbox')) expect(option).toBeChecked();
    const rows = JSON.parse(screen.getByTestId('targets').textContent!) as MatrixTarget[];
    expect(rows[2]).toMatchObject({
      endpoint_key_id: '13',
      upstream_model_id: 'vendor/c',
      native_services: fixture.data[0].native_services,
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
