import { screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useLocation } from 'react-router';
import { installJsonFetchFixtures, renderWithProviders } from '../../../test/unit/support';
import { ReportsPage } from './ReportsPage';
import type { NumberedReportPage } from '../features/operations/reportPages';
import type { ReportCaseSummary } from '../features/operations/reports';

const mocks = vi.hoisted(() => ({
  useAdminSession: vi.fn(),
  useReportsPage: vi.fn(),
}));

vi.mock('../data', async (loadOriginal) => ({
  ...(await loadOriginal<typeof import('../data')>()),
  useAdminSession: mocks.useAdminSession,
}));

vi.mock('../features/operations/reportPages', async (loadOriginal) => ({
  ...(await loadOriginal<typeof import('../features/operations/reportPages')>()),
  useReportsPage: mocks.useReportsPage,
}));

const summary: ReportCaseSummary = {
  id: `rpc_${'A'.repeat(22)}`,
  status: 'pending_review',
  progress_state: 'complete',
  connector_type: 'openai-compatible',
  canonical_base_url: 'https://api.example.test/v1',
  material_version: '1',
  target_version: '1',
  deadline: 2,
  counts: {
    materials: '1',
    targets: '1',
    distinct_owners: '1',
    processed: '0',
    deleted: '0',
    released: '0',
  },
  retry: null,
  created_at: 1,
  terminal_at: null,
};

function LocationProbe() {
  const location = useLocation();
  return (
    <output data-testid="location">{`${location.pathname}${location.search}|${JSON.stringify(location.state)}`}</output>
  );
}

function page(): NumberedReportPage<ReportCaseSummary> {
  return {
    data: [summary],
    next_cursor: null,
    pagination: { page: '3', page_size: 50, total_items: '101', total_pages: '3' },
  };
}

beforeEach(() => {
  mocks.useAdminSession.mockReturnValue({
    data: { admin: { username: 'operator' } },
    error: null,
    isPending: false,
  });
  mocks.useReportsPage.mockReturnValue({
    data: page(),
    error: null,
    isPending: false,
    isFetching: false,
    refetch: vi.fn(),
  });
  installJsonFetchFixtures([
    {
      method: 'GET',
      path: '/admin/api/reports/badge',
      body: {
        total: '1',
        by_status: { pending_indexing: '0', pending_review: '1', approved_processing: '0' },
      },
    },
  ]);
});

afterEach(() => {
  vi.clearAllMocks();
});

describe('ReportsPage numbered pagination', () => {
  it('restores URL page/filter and sends the complete page identity to the query', async () => {
    const view = await renderWithProviders(
      <>
        <ReportsPage />
        <LocationProbe />
      </>,
      {
        station: 'admin',
        role: 'admin',
        route: '/reports?status=pending_review&page=3&page_size=50',
      },
    );
    expect(mocks.useReportsPage).toHaveBeenCalledWith('operator', 'pending_review', '3', 50, true);
    expect(screen.getByTestId('location')).toHaveTextContent(
      '/reports?status=pending_review&page=3&page_size=50',
    );

    const jump = screen.getByRole('textbox');
    await view.user.clear(jump);
    await view.user.type(jump, '2');
    await view.user.click(screen.getByRole('button', { name: 'Go' }));
    expect(screen.getByTestId('location')).toHaveTextContent(
      'status=pending_review&page=2&page_size=50',
    );

    const sizeSelects = screen.getAllByRole('combobox');
    await view.user.selectOptions(sizeSelects[sizeSelects.length - 1], '100');
    expect(screen.getByTestId('location')).toHaveTextContent(
      'status=pending_review&page=1&page_size=100',
    );
  });

  it('resets page when the status changes and keeps all four page-size choices', async () => {
    const view = await renderWithProviders(
      <>
        <ReportsPage />
        <LocationProbe />
      </>,
      {
        station: 'admin',
        role: 'admin',
        route: '/reports?page=3&page_size=20',
      },
    );
    const selects = screen.getAllByRole('combobox');
    expect(selects.at(-1)).toHaveDisplayValue('50');
    expect(screen.getAllByRole('option', { name: '10' })).toHaveLength(1);
    expect(screen.getAllByRole('option', { name: '20' })).toHaveLength(1);
    expect(screen.getAllByRole('option', { name: '50' })).toHaveLength(1);
    expect(screen.getAllByRole('option', { name: '100' })).toHaveLength(1);
    await view.user.selectOptions(selects[0], 'approved');
    expect(screen.getByTestId('location')).toHaveTextContent('status=approved&page=1&page_size=20');
  });

  it('carries the clamped, actual list window into the detail return state', async () => {
    const view = await renderWithProviders(
      <>
        <ReportsPage />
        <LocationProbe />
      </>,
      {
        station: 'admin',
        role: 'admin',
        route: '/reports?status=pending_review&page=99&page_size=50',
      },
    );
    await view.user.click(screen.getByRole('link', { name: /review/i }));
    expect(screen.getByTestId('location')).toHaveTextContent(
      `/reports/${summary.id}|{"returnTo":"/reports?status=pending_review&page=3&page_size=50"}`,
    );
  });
});
