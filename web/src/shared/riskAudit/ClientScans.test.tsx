import { screen, waitFor } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { useLocation, useNavigate } from 'react-router';
import { renderWithProviders } from '../../../test/unit/support';
import { ClientScans } from './ClientScans';
import { type ClientScan, type ScanResults, type Request } from './api';

const api = vi.hoisted(() => ({
  recentScans: vi.fn(),
  scanResults: vi.fn(),
  createScan: vi.fn(),
  cancelScan: vi.fn(),
}));
vi.mock('./api', async (load) => ({
  ...(await load<typeof import('./api')>()),
  riskAPI: () => api,
}));
const scan: ClientScan = {
  id: 'scn_AAAAAAAAAAAAAAAAAAAAAA',
  state: 'completed',
  reason: '',
  from: 1800000000,
  to: 1800086400,
  kind: 'total',
  model: '',
  candidates: '257',
  scanned: '257',
  matched: 119,
  rule_count: 2,
  created_at: 1800086400,
  updated_at: 1800086410,
  expires_at: 1800172800,
};
function pageResult(page: string, size: 10 | 20 | 50 | 100): ScanResults {
  const actual = Math.min(Number(page), Math.ceil(119 / size));
  const count = Math.min(size, 119 - (actual - 1) * size);
  return {
    scan,
    page: String(actual),
    page_size: size,
    total_items: '119',
    total_pages: String(Math.ceil(119 / size)),
    items: Array.from(
      { length: count },
      (_, i) => ({ log_id: String((actual - 1) * size + i + 1) }) as Request,
    ),
  };
}
function View() {
  const location = useLocation();
  const navigate = useNavigate();
  return (
    <>
      <output data-testid="location">{location.search}</output>
      <button onClick={() => navigate(-1)}>Browser back</button>
      <ClientScans
        role="admin"
        scopeKey="synthetic-operator"
        filters={{ lookback_hours: 24, kind: 'total' }}
        renderItem={(item) => <div key={item.log_id}>Result {item.log_id}</div>}
      />
    </>
  );
}
beforeEach(() => {
  vi.resetAllMocks();
  api.recentScans.mockResolvedValue([scan]);
  api.scanResults.mockImplementation((_id: string, page: string, size: 10 | 20 | 50 | 100) =>
    Promise.resolve(pageResult(page, size)),
  );
  api.createScan.mockResolvedValue(scan);
  api.cancelScan.mockResolvedValue({ ...scan, state: 'cancelled' });
});
it('restores the selected scan and supports previous, next, sizes, jumps and browser back', async () => {
  const view = await renderWithProviders(<View />, {
    station: 'admin',
    role: 'admin',
    route: '/?audit_tab=clients&audit_scan=' + scan.id + '&audit_page=2&audit_size=20',
  });
  expect(await screen.findByText('Page 2 of 6 · Total: 119')).toBeVisible();
  expect(screen.getByText('Result 21')).toBeVisible();
  await view.user.click(screen.getByRole('button', { name: 'Previous' }));
  expect(await screen.findByText('Page 1 of 6 · Total: 119')).toBeVisible();
  expect(screen.getByRole('button', { name: 'Previous' })).toBeDisabled();
  await view.user.click(screen.getByRole('button', { name: 'Next' }));
  await screen.findByText('Result 21');
  await view.user.clear(screen.getByLabelText('Go to page'));
  await view.user.type(screen.getByLabelText('Go to page'), '6{Enter}');
  expect(await screen.findByText('Page 6 of 6 · Total: 119')).toBeVisible();
  expect(screen.getByText('Result 119')).toBeVisible();
  expect(screen.getByRole('button', { name: 'Next' })).toBeDisabled();
  await view.user.selectOptions(screen.getByLabelText('Items per page'), '50');
  expect(await screen.findByText('Page 1 of 3 · Total: 119')).toBeVisible();
  expect(screen.getByTestId('location')).toHaveTextContent('audit_size=50');
  await view.user.click(screen.getByRole('button', { name: 'Browser back' }));
  expect(await screen.findByText('Page 6 of 6 · Total: 119')).toBeVisible();
  expect(api.createScan).not.toHaveBeenCalled();
});
it('retains committed results while stopping a provisional scan', async () => {
  const active = { ...scan, state: 'running' as const };
  api.recentScans.mockResolvedValue([active]);
  api.scanResults.mockImplementation((_id: string, page: string, size: 10 | 20 | 50 | 100) =>
    Promise.resolve({ ...pageResult(page, size), scan: active }),
  );
  const view = await renderWithProviders(<View />, {
    station: 'admin',
    route: '/?audit_scan=' + scan.id,
  });
  expect(await screen.findByText(/provisional until the scan completes/)).toBeVisible();
  expect(screen.getByRole('button', { name: 'Start new scan' })).toBeDisabled();
  await view.user.click(screen.getByRole('button', { name: 'Stop scan' }));
  await waitFor(() => expect(api.cancelScan).toHaveBeenCalledWith(scan.id));
  expect(screen.getByText('Result 1')).toBeVisible();
});
it('retries the same creation token after an uncertain failure', async () => {
  api.recentScans.mockResolvedValue([]);
  api.createScan.mockRejectedValueOnce(new Error('Connection interrupted'));
  const view = await renderWithProviders(<View />, { station: 'admin' });
  await waitFor(() => expect(screen.getByRole('button', { name: 'Start new scan' })).toBeEnabled());
  await view.user.click(screen.getByRole('button', { name: 'Start new scan' }));
  await view.user.click(await screen.findByRole('button', { name: 'Retry' }));
  await waitFor(() => expect(api.createScan).toHaveBeenCalledTimes(2));
  expect(api.createScan.mock.calls[0][0]).toEqual(api.createScan.mock.calls[1][0]);
  expect(await screen.findByText('Result 1')).toBeVisible();
  expect(screen.getByTestId('location')).toHaveTextContent('audit_scan=' + scan.id);
});
