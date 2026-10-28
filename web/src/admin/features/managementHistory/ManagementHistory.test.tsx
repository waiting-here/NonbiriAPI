import { screen, waitFor, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import { ApiError } from '@shared/query/http';
import { ManagementHistory } from './ManagementHistory';

const mocks = vi.hoisted(() => ({
  list: vi.fn(),
  detail: vi.fn(),
  items: vi.fn(),
  prepare: vi.fn(),
  start: vi.fn(),
  progress: vi.fn(),
  resume: vi.fn(),
}));
vi.mock('./api', async (original) => ({
  ...(await original<typeof import('./api')>()),
  getHistory: mocks.list,
  getOperation: mocks.detail,
  getOperationItems: mocks.items,
  prepareCleanup: mocks.prepare,
  startCleanup: mocks.start,
  getCleanup: mocks.progress,
  resumeCleanup: mocks.resume,
}));
const person = { id: '7', name: 'Example operator', role: 'level5' };
const counts = { targets: '123', succeeded: '120', skipped: '2', failed: '1' };
const group = {
  ...counts,
  key: 'selected-group',
  bucket: 1700000000000,
  source: 'ai_manual',
  domain: 'donations',
  action: 'approve',
  actor: person,
  authorizer: person,
  outcome: 'partial',
  operations: '123',
};
const operation = {
  ...counts,
  id: '9007199254740993',
  event_id: 'event-1',
  created_at: 1700000000000,
  source: 'ai_manual',
  executor_kind: 'ai',
  actor: person,
  authorizer: person,
  domain: 'donations',
  action: 'approve',
  outcome: 'partial',
  code: '',
  summary: 'Approve donations',
};
const job = {
  id: '9',
  state: 'completed',
  created_at: 1700000000000,
  updated_at: 1700000000001,
  actor: person,
  filter: {},
  cutoff_max_id: '9007199254740993',
  deleted_operations: '120',
  deleted_items: '120',
  skipped_active: '3',
  error_code: '',
};
function history(view = 'groups') {
  return {
    view,
    groups: view === 'groups' ? [group] : [],
    operations: view === 'operations' ? [operation] : [],
    total: '1',
    page: 1,
    page_size: 20 as const,
    snapshot_max_id: '9007199254740993',
  };
}
beforeEach(() => {
  vi.resetAllMocks();
  mocks.list.mockImplementation(async (_filter, view) => history(view));
  mocks.detail.mockResolvedValue(operation);
  mocks.items.mockResolvedValue({ items: [], total: '0', page: 1, page_size: 20 });
  mocks.prepare.mockResolvedValue({ filter: {}, all: true, cutoff_max_id: '9007199254740993' });
  mocks.start.mockResolvedValue(job);
  mocks.progress.mockResolvedValue(job);
});

describe('management history workflows', () => {
  it('opens a grouped operation with its human authorization without loading every target', async () => {
    mocks.list.mockImplementation(async (_filter, view, page) => ({
      ...history(view),
      total: view === 'groups' ? '60' : '1',
      page: Number(page),
    }));
    const view = await renderWithProviders(<ManagementHistory account="synthetic" />, {
      station: 'admin',
      role: 'admin',
      route: '/management-operations?mh_page=3',
    });
    expect(
      await screen.findByText('AI Agent (authorized by Example operator)'),
    ).toBeInTheDocument();
    expect(mocks.items).not.toHaveBeenCalled();
    await view.user.click(screen.getByRole('button', { name: 'Open 123' }));
    await waitFor(() =>
      expect(mocks.list).toHaveBeenCalledWith(
        expect.objectContaining({ group_key: 'selected-group' }),
        'operations',
        '1',
        20,
        expect.any(String),
        expect.any(AbortSignal),
      ),
    );
    await view.user.click(
      await screen.findByRole('button', { name: 'Details · 9007199254740993' }),
    );
    await screen.findByText('Operation 9007199254740993');
    expect(mocks.items).toHaveBeenCalledWith('9007199254740993', '1', 20, expect.any(AbortSignal));
    await view.user.click(screen.getByRole('button', { name: 'Back to overview' }));
    await screen.findByRole('button', { name: 'Open 123' });
    await waitFor(() =>
      expect(mocks.list).toHaveBeenLastCalledWith(
        {},
        'groups',
        '3',
        20,
        expect.any(String),
        expect.any(AbortSignal),
      ),
    );
  });
  it('freezes the cleanup scope and recovers an uncertain submission with the same request', async () => {
    mocks.start
      .mockRejectedValueOnce(new ApiError('network_error', 'Response lost', 0))
      .mockResolvedValueOnce(job);
    const view = await renderWithProviders(<ManagementHistory account="synthetic" />, {
      station: 'admin',
      role: 'admin',
    });
    view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'synthetic' } });
    await screen.findByRole('button', { name: 'Open 123' });
    await view.user.click(screen.getByRole('button', { name: 'Clear all retained records' }));
    const dialog = await screen.findByRole('alertdialog');
    expect(
      within(dialog).getByText('This scope contains 1 operations through record 9007199254740993.'),
    ).toBeInTheDocument();
    await view.user.click(within(dialog).getByRole('button', { name: 'Start cleanup' }));
    await within(dialog).findByText(
      'The result is not yet confirmed. Check the same request to recover its progress without starting another cleanup.',
    );
    await view.user.click(within(dialog).getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument();
    await view.user.click(screen.getByRole('button', { name: 'Check saved result' }));
    await screen.findByText('Cleanup 9');
    expect(mocks.prepare).toHaveBeenCalledTimes(1);
    expect(mocks.start).toHaveBeenCalledTimes(2);
    expect(mocks.start.mock.calls[1]?.slice(0, 2)).toEqual(mocks.start.mock.calls[0]?.slice(0, 2));
    expect(mocks.start.mock.calls[0]?.[0].cutoff_max_id).toBe('9007199254740993');
    await screen.findByText('3 active operations retained');
  });
  it('keeps draft filters off the network until the operator applies them', async () => {
    const view = await renderWithProviders(<ManagementHistory account="synthetic" />, {
      station: 'admin',
      role: 'admin',
    });
    await screen.findByRole('button', { name: 'Open 123' });
    const initial = mocks.list.mock.calls.length;
    await view.user.type(
      screen.getByRole('searchbox', { name: 'Find records' }),
      'Example operator',
    );
    expect(mocks.list).toHaveBeenCalledTimes(initial);
    await view.user.click(screen.getByRole('button', { name: /^Search$/ }));
    await waitFor(() =>
      expect(mocks.list).toHaveBeenLastCalledWith(
        { search: 'Example operator' },
        'groups',
        '1',
        20,
        '',
        expect.any(AbortSignal),
      ),
    );
  });
  it('checks an uncertain resume without repeating the write after the connection returns', async () => {
    mocks.progress
      .mockResolvedValueOnce({ ...job, state: 'failed', error_code: 'storage_unavailable' })
      .mockRejectedValueOnce(new ApiError('network_error', 'Connection unavailable', 0))
      .mockResolvedValue(job);
    mocks.resume.mockRejectedValueOnce(new ApiError('network_error', 'Response lost', 0));
    const view = await renderWithProviders(<ManagementHistory account="synthetic" />, {
      station: 'admin',
      role: 'admin',
      route: '/management-operations?mh_job=9',
    });
    view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'synthetic' } });
    await view.user.click(await screen.findByRole('button', { name: 'Resume cleanup' }));
    await waitFor(() => expect(mocks.progress).toHaveBeenCalledTimes(2));
    await view.user.click(screen.getByRole('button', { name: 'Check saved result' }));
    await screen.findByText(/^Completed/);
    expect(mocks.resume).toHaveBeenCalledTimes(1);
    expect(mocks.progress).toHaveBeenCalledTimes(3);
    expect(screen.queryByRole('button', { name: 'Resume cleanup' })).not.toBeInTheDocument();
  });
  it('marks the actual view when a search requires individual operations', async () => {
    mocks.list.mockResolvedValue(history('operations'));
    await renderWithProviders(<ManagementHistory account="synthetic" />, {
      station: 'admin',
      role: 'admin',
      route: '/management-operations?mh_search=event-1',
    });
    await screen.findByRole('button', { name: 'Details · 9007199254740993' });
    expect(screen.getByRole('tab', { name: 'Individual operations' })).toHaveAttribute(
      'aria-selected',
      'true',
    );
  });
});
