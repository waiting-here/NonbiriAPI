import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import {
  attendanceAdmin,
  attendanceAssets,
  attendancePage,
  attendancePeriod,
} from '../../../../test/unit/attendance';
import { ApiError } from '@shared/query/http';
import { AttendanceManagement } from './AttendanceManagement';

const api = vi.hoisted(() => ({ list: vi.fn(), save: vi.fn() }));
vi.mock('@shared/attendance/api', async (load) => ({
  ...(await load<typeof import('@shared/attendance/api')>()),
  listAttendance: api.list,
  saveAttendance: api.save,
}));
vi.mock('@shared/rewards/api', async (load) => ({
  ...(await load<typeof import('@shared/rewards/api')>()),
  getAssets: async () => attendanceAssets,
}));
beforeEach(() => {
  api.list.mockReset().mockResolvedValue(attendancePage([]));
  api.save.mockReset().mockResolvedValue(attendancePeriod());
});
async function openNew() {
  const view = await renderWithProviders(<AttendanceManagement account="operator" enabled />, {
    station: 'admin',
  });
  view.queryClient.setQueryData(['admin', 'session'], attendanceAdmin);
  await view.user.click(await screen.findByRole('button', { name: 'Create activity' }));
  const dialog = await screen.findByRole('dialog');
  await view.user.type(
    within(dialog).getByRole('textbox', { name: 'Activity title' }),
    'Autumn rewards',
  );
  return { view, dialog };
}
it('preserves configuration after a server refusal and sends the corrected draft', async () => {
  api.save.mockRejectedValueOnce(
    new ApiError('invalid_request', 'The date range must cover the largest milestone.', 400),
  );
  const { view, dialog } = await openNew();
  await view.user.click(within(dialog).getByRole('button', { name: 'Save activity' }));
  expect(
    await within(dialog).findByText('The date range must cover the largest milestone.'),
  ).toBeVisible();
  expect(within(dialog).getByRole('textbox', { name: 'Activity title' })).toHaveValue(
    'Autumn rewards',
  );
  fireEvent.change(within(dialog).getByLabelText('End date'), { target: { value: '2026-11-30' } });
  await view.user.click(within(dialog).getByRole('button', { name: 'Save activity' }));
  await waitFor(() => expect(api.save).toHaveBeenCalledTimes(2));
  expect(api.save.mock.calls[1][0].end_date).toBe('2026-11-30');
  expect(api.save.mock.calls[0][1]).not.toBe(api.save.mock.calls[1][1]);
});

it('retains an uncertain creation and its operation key when the sheet is closed and reopened', async () => {
  api.save.mockRejectedValueOnce(new ApiError('network_error', 'Response lost', 0));
  const { view, dialog } = await openNew();
  await view.user.click(within(dialog).getByRole('button', { name: 'Save activity' }));
  await within(dialog).findByRole('button', { name: 'Retry' });
  await view.user.click(within(dialog).getByRole('button', { name: 'Close' }));
  await view.user.click(screen.getByRole('button', { name: 'Create activity' }));
  const reopened = await screen.findByRole('dialog');
  expect(within(reopened).getByRole('textbox', { name: 'Activity title' })).toHaveValue(
    'Autumn rewards',
  );
  await view.user.click(within(reopened).getByRole('button', { name: 'Retry' }));
  await waitFor(() => expect(api.save).toHaveBeenCalledTimes(2));
  expect(api.save.mock.calls[0].slice(0, 3)).toEqual(api.save.mock.calls[1].slice(0, 3));
});

it('keeps started rules fixed while allowing title and description maintenance', async () => {
  api.list.mockResolvedValue(attendancePage());
  const view = await renderWithProviders(<AttendanceManagement account="operator" enabled />, {
    station: 'admin',
  });
  view.queryClient.setQueryData(['admin', 'session'], attendanceAdmin);
  await view.user.click(await screen.findByRole('button', { name: 'Edit activity' }));
  const dialog = await screen.findByRole('dialog');
  expect(within(dialog).getByLabelText('Start date')).toBeDisabled();
  expect(within(dialog).getByRole('button', { name: 'Add milestone' })).toBeDisabled();
  const title = within(dialog).getByRole('textbox', { name: 'Activity title' });
  await view.user.clear(title);
  await view.user.type(title, 'Updated title');
  await view.user.click(within(dialog).getByRole('button', { name: 'Save activity' }));
  await waitFor(() => expect(api.save).toHaveBeenCalledTimes(1));
  expect(api.save.mock.calls[0][0]).toMatchObject({
    title: 'Updated title',
    start_date: '2026-11-01',
    mode: 'cumulative',
  });
  expect(api.save.mock.calls[0][2]).toEqual({ id: 'autumn', revision: 1 });
});
