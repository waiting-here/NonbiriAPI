import { fireEvent, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { limitedActivity } from '../../../test/fixtures/limitedActivities';
import { LimitedActivitiesPage } from './LimitedActivitiesPage';
function reply(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}
describe('limited activity configuration', () => {
  it('retains the original version and key after an uncertain save', async () => {
    const writes: { key: string | null; body: string }[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const path = String(input);
        if (path.endsWith('/session')) return reply({ admin: { username: 'fixture-admin' } });
        if (init?.method === 'PUT') {
          writes.push({
            key: new Headers(init.headers).get('Idempotency-Key'),
            body: String(init.body),
          });
          if (writes.length === 1) throw new TypeError('connection lost');
          return reply(limitedActivity({ visible: true, revision: '2' }));
        }
        return reply(limitedActivity());
      }),
    );
    const rendered = await renderWithProviders(<LimitedActivitiesPage />, {
      station: 'admin',
      role: 'admin',
    });
    const visibility = await screen.findByRole('checkbox', { name: 'Show in activity directory' });
    fireEvent.click(visibility);
    await rendered.user.click(screen.getByRole('button', { name: 'Save settings' }));
    await screen.findByText(/save result is unconfirmed/i);
    expect(visibility).toBeDisabled();
    await rendered.user.click(screen.getByRole('button', { name: 'Retry save' }));
    await screen.findByText('Settings saved.');
    expect(writes).toHaveLength(2);
    expect(writes[0]).toEqual(writes[1]);
    expect(JSON.parse(writes[1].body)).toMatchObject({
      expected_revision: '1',
      visible: true,
      module_config: { brush_cap: '10' },
    });
  });
  it('keeps invalid quantities editable and does not send a mutation', async () => {
    let writes = 0;
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        if (String(input).endsWith('/session'))
          return reply({ admin: { username: 'fixture-admin' } });
        if (init?.method === 'PUT') writes++;
        return reply(limitedActivity());
      }),
    );
    const rendered = await renderWithProviders(<LimitedActivitiesPage />, {
      station: 'admin',
      role: 'admin',
    });
    const cap = await screen.findByLabelText('Total cumulative brush exchange cap');
    fireEvent.change(cap, { target: { value: '1.5' } });
    await rendered.user.click(screen.getByRole('button', { name: 'Save settings' }));
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Save settings' })).toBeEnabled(),
    );
    expect(cap).toBeEnabled();
    expect(writes).toBe(0);
  });
});
