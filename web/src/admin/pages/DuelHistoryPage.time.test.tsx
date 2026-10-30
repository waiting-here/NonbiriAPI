import { fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { TimeContextProvider } from '@shared/components/TimeContext';
import { DuelHistoryPage } from './DuelHistoryPage';

afterEach(() => vi.unstubAllGlobals());

it('parses match-history filters in the fixed site offset', async () => {
  const requested: URL[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = new URL(String(input), window.location.origin);
      if (url.pathname === '/admin/api/time-context')
        return new Response(JSON.stringify({ mode: 'site', offset_minutes: 330 }));
      if (url.pathname.endsWith('/history')) {
        requested.push(url);
        return new Response(JSON.stringify({ dataset: 'recent', items: [], next_cursor: null }));
      }
      throw new Error(`Unexpected request: ${url.pathname}`);
    }),
  );
  const view = await renderWithProviders(
    <TimeContextProvider station="admin">
      <DuelHistoryPage />
    </TimeContextProvider>,
    { station: 'admin' },
  );
  const from = screen.getByLabelText('Ended from');
  await waitFor(() => expect(from).toBeEnabled());
  expect(screen.getByText(/UTC\+05:30/)).toBeVisible();
  const version = screen.getByLabelText('Rules version (blank for all)');
  fireEvent.change(version, { target: { value: '7' } });
  fireEvent.change(screen.getByLabelText('Outcome'), { target: { value: 'draw' } });
  await waitFor(() =>
    expect(requested.some((url) => url.searchParams.get('outcome') === 'draw')).toBe(true),
  );
  expect(requested.every((url) => !url.searchParams.has('rules_version'))).toBe(true);
  fireEvent.change(from, { target: { value: '2030-01-01T05:30' } });
  fireEvent.change(screen.getByLabelText('Ended through'), {
    target: { value: '2030-01-01T06:30' },
  });
  await view.user.click(screen.getByRole('button', { name: 'Search' }));
  await waitFor(() => expect(requested.some((url) => url.searchParams.has('from'))).toBe(true));
  const withRange = requested.find((url) => url.searchParams.has('from'))!;
  expect(withRange.searchParams.get('rules_version')).toBe('7');
  expect(withRange.searchParams.get('from')).toBe(
    String(Date.parse('2030-01-01T00:00:00Z') / 1000),
  );
  expect(withRange.searchParams.get('to')).toBe(String(Date.parse('2030-01-01T01:00:00Z') / 1000));
  await view.user.click(screen.getByRole('button', { name: 'Reset' }));
  expect(version).toHaveValue('');
  expect(screen.getByLabelText('Outcome')).toHaveValue('');
  await waitFor(() => expect(requested.at(-1)?.search).not.toContain('outcome'));
});
