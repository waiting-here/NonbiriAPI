import { screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { Route, Routes, useLocation } from 'react-router';
import { renderWithProviders } from '../../../test/unit/support';
import { ReportDetailPage } from './ReportDetailPage';
import { ApiError } from '@shared/query/http';
import type { NumberedReportDetail, NumberedReportPage } from '../features/operations/reportPages';
import type {
  ReportCaseSummary,
  ReportDonationMatch,
  ReportTarget,
} from '../features/operations/reports';

const mocks = vi.hoisted(() => ({
  useAdminSession: vi.fn(),
  useReportDetailPage: vi.fn(),
  useReportTargetsPage: vi.fn(),
  useReportTargetDonationsPage: vi.fn(),
}));

vi.mock('../data', async (loadOriginal) => ({
  ...(await loadOriginal<typeof import('../data')>()),
  useAdminSession: mocks.useAdminSession,
}));

vi.mock('../features/operations/reportPages', async (loadOriginal) => ({
  ...(await loadOriginal<typeof import('../features/operations/reportPages')>()),
  useReportDetailPage: mocks.useReportDetailPage,
  useReportTargetsPage: mocks.useReportTargetsPage,
  useReportTargetDonationsPage: mocks.useReportTargetDonationsPage,
}));

const caseId = `rpc_${'A'.repeat(22)}`;
const targetId = `rpt_${'A'.repeat(22)}`;
const summary: ReportCaseSummary = {
  id: caseId,
  status: 'approved',
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
    processed: '1',
    deleted: '0',
    released: '0',
  },
  retry: null,
  created_at: 1,
  terminal_at: 2,
};
const target: ReportTarget = {
  id: targetId,
  target_seq: '7',
  state: 'released',
  endpoint_key_id: null,
  key_ref: 'A'.repeat(43),
  owner: null,
  endpoint: {
    connector_type: 'openai-compatible',
    canonical_base_url: 'https://api.example.test/v1',
    display_head: 'head',
    display_tail: 'tail',
  },
  discovered_version: '1',
  decided_version: '2',
  donation_match_count: '1',
  created_at: 1,
  updated_at: 2,
};
const donation: ReportDonationMatch = {
  donation_id: '7',
  donation_key_id: '8',
  donation_status: 'deleted',
  key_state: 'ended',
  expires_at: 10,
  ended_reason: 'account_deleted',
  ended_at: 11,
};
const detail: NumberedReportDetail = {
  ...summary,
  materials: { data: [], next_cursor: null },
  materials_pagination: { page: '2', page_size: 50, total_items: '51', total_pages: '2' },
  decision: null,
};
const targets: NumberedReportPage<ReportTarget> = {
  data: [target],
  next_cursor: null,
  pagination: { page: '3', page_size: 10, total_items: '21', total_pages: '3' },
};
const donations: NumberedReportPage<ReportDonationMatch> = {
  data: [donation],
  next_cursor: null,
  pagination: { page: '4', page_size: 100, total_items: '301', total_pages: '4' },
};

function LocationProbe() {
  const location = useLocation();
  return <output data-testid="location">{`${location.pathname}${location.search}`}</output>;
}

beforeEach(() => {
  mocks.useAdminSession.mockReturnValue({
    data: { admin: { username: 'operator' } },
    error: null,
    isPending: false,
  });
  mocks.useReportDetailPage.mockReturnValue({
    data: detail,
    error: null,
    isPending: false,
    isFetching: false,
    refetch: vi.fn(),
  });
  mocks.useReportTargetsPage.mockReturnValue({
    data: targets,
    error: null,
    isPending: false,
    isFetching: false,
    refetch: vi.fn(),
  });
  mocks.useReportTargetDonationsPage.mockReturnValue({
    data: donations,
    error: null,
    isPending: false,
    isFetching: false,
    refetch: vi.fn(),
  });
});

afterEach(() => {
  vi.clearAllMocks();
});

describe('ReportDetailPage numbered nested pagination', () => {
  it('keeps a deep-linked nested page while the first session read is pending', async () => {
    mocks.useAdminSession.mockReturnValue({ data: undefined, error: null, isPending: true });
    const content = () => (
      <Routes>
        <Route
          path="/reports/:caseId"
          element={
            <>
              <ReportDetailPage />
              <LocationProbe />
            </>
          }
        />
      </Routes>
    );
    const view = await renderWithProviders(content(), {
      station: 'admin',
      role: 'admin',
      route: `/reports/${caseId}?materials_page=2&materials_page_size=50&lineage_target=${targetId}&lineage_page=4&lineage_page_size=100`,
    });
    expect(mocks.useReportDetailPage).not.toHaveBeenCalled();
    mocks.useAdminSession.mockReturnValue({
      data: { admin: { username: 'operator' } },
      error: null,
      isPending: false,
    });
    view.rerender(content());
    await waitFor(() =>
      expect(mocks.useReportDetailPage).toHaveBeenCalledWith('operator', caseId, '2', 50, true),
    );
    expect(mocks.useReportTargetDonationsPage).toHaveBeenCalledWith(
      'operator',
      caseId,
      targetId,
      '4',
      100,
      true,
    );
    expect(screen.getByTestId('location')).toHaveTextContent('materials_page=2');
    await view.user.click(screen.getByRole('button', { name: 'Close' }));
    await waitFor(() =>
      expect(screen.getByTestId('location').textContent).not.toContain('lineage_'),
    );
    expect(screen.getByTestId('location')).toHaveTextContent('materials_page=2');
  });

  it('immediately hides cached materials and review controls when a nested query loses authority', async () => {
    mocks.useReportTargetsPage.mockReturnValue({
      data: targets,
      error: new ApiError('forbidden', 'Denied', 403),
      isPending: false,
      isFetching: false,
      refetch: vi.fn(),
    });
    await renderWithProviders(
      <Routes>
        <Route path="/reports/:caseId" element={<ReportDetailPage />} />
      </Routes>,
      { station: 'admin', role: 'admin', route: `/reports/${caseId}` },
    );
    expect(screen.queryByText(summary.canonical_base_url)).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /approve deletion/i })).not.toBeInTheDocument();
  });

  it('keeps materials, targets, and lineage query identities separate', async () => {
    const view = await renderWithProviders(
      <Routes>
        <Route
          path="/reports/:caseId"
          element={
            <>
              <ReportDetailPage />
              <LocationProbe />
            </>
          }
        />
      </Routes>,
      {
        station: 'admin',
        role: 'admin',
        route: `/reports/${caseId}?materials_page=2&materials_page_size=50&targets_page=3&targets_page_size=10&lineage_target=${targetId}&lineage_page=4&lineage_page_size=100`,
      },
    );
    expect(mocks.useReportDetailPage).toHaveBeenCalledWith('operator', caseId, '2', 50, true);
    expect(mocks.useReportTargetsPage).toHaveBeenCalledWith('operator', caseId, '3', 10, true);
    expect(mocks.useReportTargetDonationsPage).toHaveBeenCalledWith(
      'operator',
      caseId,
      targetId,
      '4',
      100,
      true,
    );
    expect(screen.getAllByRole('combobox')).toHaveLength(3);
    expect(document.querySelectorAll('[aria-busy="false"]')).toHaveLength(3);
    expect(screen.getByRole('button', { name: /open lineage/i })).toBeInTheDocument();

    await view.user.click(screen.getByRole('button', { name: /open lineage/i }));
    expect(screen.getByTestId('location')).toHaveTextContent(
      `materials_page=2&materials_page_size=50&targets_page=3&targets_page_size=10&lineage_target=${targetId}&lineage_page=1&lineage_page_size=100`,
    );
  });

  it('restores a lineage target from the URL even when the target page has no matching row', async () => {
    mocks.useReportTargetsPage.mockReturnValue({
      data: { ...targets, data: [] },
      error: null,
      isPending: false,
      isFetching: false,
      refetch: vi.fn(),
    });
    await renderWithProviders(
      <Routes>
        <Route
          path="/reports/:caseId"
          element={
            <>
              <ReportDetailPage />
              <LocationProbe />
            </>
          }
        />
      </Routes>,
      {
        station: 'admin',
        role: 'admin',
        route: `/reports/${caseId}?lineage_target=${targetId}&lineage_page=2&lineage_page_size=20`,
      },
    );
    expect(screen.getAllByText(new RegExp(targetId)).length).toBeGreaterThan(0);
    expect(mocks.useReportTargetDonationsPage).toHaveBeenCalledWith(
      'operator',
      caseId,
      targetId,
      '2',
      20,
      true,
    );
  });
});
