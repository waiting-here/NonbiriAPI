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
  it('shows the site offset and preserves original epochs until the schedule is edited', async () => {
    const epoch = Date.parse('2030-01-01T00:00:00Z') / 1000;
    const detail = limitedActivity({ starts_at: epoch, ends_at: epoch + 3600 });
    const writes: Record<string, unknown>[] = [];
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path.endsWith('/session')) return reply({ admin: { username: 'fixture-admin' } });
      if (path.endsWith('/time-context')) return reply({ mode: 'site', offset_minutes: 330 });
      if (init?.method === 'PUT') {
        writes.push(JSON.parse(String(init.body)) as Record<string, unknown>);
        return reply(detail);
      }
      return reply(detail);
    }));
    const view = await renderWithProviders(<LimitedActivitiesPage />, {
      station: 'admin', role: 'admin',
    });
    const opening = await screen.findByLabelText('Opening time');
    await waitFor(() => expect(opening).toHaveValue('2030-01-01T05:30'));
    expect(screen.getByLabelText('Closing time (exclusive)')).toHaveValue('2030-01-01T06:30');
    await view.user.click(screen.getByRole('button', { name: 'Save settings' }));
    await waitFor(() => expect(writes).toHaveLength(1));
    expect(writes[0]).toMatchObject({ starts_at: epoch, ends_at: epoch + 3600 });

    fireEvent.change(opening, { target: { value: '2030-01-01T06:00' } });
    await view.user.click(screen.getByRole('button', { name: 'Save settings' }));
    await waitFor(() => expect(writes).toHaveLength(2));
    expect(writes[1]).toMatchObject({ starts_at: epoch + 1800, ends_at: epoch + 3600 });
  });

  it('retains the original version and key after an uncertain save', async () => {
    const writes: { key: string | null; body: string }[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const path = String(input);
        if (path.endsWith('/session')) return reply({ admin: { username: 'fixture-admin' } });
        if (path.endsWith('/time-context')) return reply({ mode: 'site', offset_minutes: 0 });
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
        if (String(input).endsWith('/time-context'))
          return reply({ mode: 'site', offset_minutes: 0 });
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
