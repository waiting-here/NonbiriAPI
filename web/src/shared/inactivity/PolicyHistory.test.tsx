import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, useLocation, useNavigate } from 'react-router';
import { beforeEach, expect, it, vi } from 'vitest';
import { PolicyHistory } from './PolicyHistory';
import type { PageSize } from '@shared/operations/pageNumbers';

vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
beforeEach(() => localStorage.clear());
const result = (page = '1', size: PageSize = 20, total = 181) => ({
  data: [`row-${page}`],
  pagination: {
    page,
    page_size: size,
    total_items: String(total),
    total_pages: String(Math.ceil(total / size)),
  },
});
function Location() {
  const location = useLocation(),
    navigate = useNavigate();
  return (
    <>
      <output data-testid="location">{location.search}</output>
      <button onClick={() => navigate('?runs_page=2')}>URL second</button>
      <button onClick={() => navigate('?runs_page=3')}>URL third</button>
    </>
  );
}
function mount(
  load = vi.fn().mockImplementation(async (page: string, size: PageSize) => result(page, size)),
) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <Location />
        <PolicyHistory kind="runs" title="Runs" zh={false} load={load}>
          {(rows) => <p>{rows.join(',')}</p>}
        </PolicyHistory>
        <PolicyHistory
          kind="audits"
          title="Audits"
          zh={false}
          load={async (page, size) => result(page, size)}
        >
          {(rows) => <p>{rows.join(',')}</p>}
        </PolicyHistory>
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return load;
}
it('keeps independent URL pages, enables navigation after failure, retries and retains a useful single-page size selector', async () => {
  let fail = true;
  const load = vi.fn().mockImplementation(async (page: string, size: PageSize) => {
    if (page === '2' && fail) throw new Error('unavailable');
    return result(page, size, size === 100 ? 12 : 181);
  });
  mount(load);
  fireEvent.click(screen.getByText('Runs'));
  const runs = within(screen.getByLabelText('Runs'));
  await runs.findByText('row-1');
  fireEvent.click(screen.getByText('Audits'));
  const audits = within(screen.getByLabelText('Audits'));
  await audits.findByText('row-1');
  fireEvent.click(audits.getByRole('button', { name: 'common.next' }));
  await audits.findByText('row-2');
  fireEvent.click(runs.getByRole('button', { name: 'common.next' }));
  await runs.findByRole('button', { name: 'common.retry' });
  expect(runs.queryByText('row-1')).not.toBeInTheDocument();
  expect(runs.getByRole('button', { name: 'common.next' })).toBeEnabled();
  expect(screen.getByTestId('location')).toHaveTextContent('audits_page=2');
  fail = false;
  fireEvent.click(runs.getByRole('button', { name: 'common.retry' }));
  await runs.findByText('row-2');
  const jump = runs.getByLabelText('common.pageControls.jump');
  fireEvent.change(jump, { target: { value: '8' } });
  fireEvent.keyDown(jump, { key: 'Enter' });
  await runs.findByText('row-8');
  fireEvent.change(runs.getByLabelText('common.pageControls.size'), { target: { value: '100' } });
  await runs.findByText('row-1');
  expect(runs.getByLabelText('common.pageControls.size')).toHaveValue('100');
  expect(audits.getByText('row-2')).toBeInTheDocument();
  expect(screen.getByTestId('location')).toHaveTextContent('runs_page=1');
});
it('aborts a superseded page and ignores its late response', async () => {
  let finish!: (value: ReturnType<typeof result>) => void;
  let signal!: AbortSignal;
  const load = vi
    .fn()
    .mockImplementation((page: string, size: PageSize, requestSignal: AbortSignal) => {
      if (page === '2') {
        signal = requestSignal;
        return new Promise((resolve) => {
          finish = resolve;
        });
      }
      return Promise.resolve(result(page, size));
    });
  mount(load);
  fireEvent.click(screen.getByText('Runs'));
  await screen.findByText('row-1');
  fireEvent.click(screen.getByText('URL second'));
  await waitFor(() => expect(signal).toBeDefined());
  fireEvent.click(screen.getByText('URL third'));
  await screen.findByText('row-3');
  expect(signal.aborted).toBe(true);
  await act(async () => finish(result('2')));
  expect(screen.queryByText('row-2')).not.toBeInTheDocument();
  expect(screen.getByText('row-3')).toBeInTheDocument();
});
