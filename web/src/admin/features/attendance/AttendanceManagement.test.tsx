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
import { campaignCoverChoices, defaultCover, resolveCover } from '@shared/attendance/coverChoices';
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

it('lets started activities change featured milestones and the cover while preserving their rules', async () => {
  const url = 'https://images.example.test/autumn.webp';
  const period = attendancePeriod({
    cover: { kind: 'external', value: url, fit: 'contain' },
  });
  api.list.mockResolvedValue(attendancePage([period]));
  const view = await renderWithProviders(<AttendanceManagement account="operator" enabled />, {
    station: 'admin',
  });
  view.queryClient.setQueryData(['admin', 'session'], attendanceAdmin);
  await view.user.click(await screen.findByRole('button', { name: 'Edit activity' }));
  const dialog = await screen.findByRole('dialog');
  expect(within(dialog).getByLabelText('Start date')).toBeDisabled();
  expect(within(dialog).queryByRole('button', { name: 'Add milestone' })).not.toBeInTheDocument();
  const featured = within(dialog).getByRole('group', { name: 'Featured rewards' });
  const first = within(featured).getByRole('checkbox', { name: 'Day 7' });
  const second = within(featured).getByRole('checkbox', { name: 'Day 14' });
  expect(first).toHaveAccessibleDescription(/General credits.*100/);
  expect(first).not.toBeChecked();
  expect(second).not.toBeChecked();
  await view.user.click(first);
  expect(first).toBeChecked();
  expect(second).not.toBeChecked();
  await view.user.click(second);
  expect(first).toBeChecked();
  expect(second).toBeChecked();

  expect(within(dialog).getByLabelText('HTTPS image address')).toHaveValue(url);
  expect(within(dialog).getByLabelText('Image fit')).toHaveValue('contain');
  await view.user.selectOptions(within(dialog).getByLabelText('Image source'), 'default');
  const title = within(dialog).getByRole('textbox', { name: 'Activity title' });
  await view.user.clear(title);
  await view.user.type(title, 'Updated title');
  await view.user.click(within(dialog).getByRole('button', { name: 'Save activity' }));
  await waitFor(() => expect(api.save).toHaveBeenCalledTimes(1));
  expect(api.save.mock.calls[0][0]).toMatchObject({
    title: 'Updated title',
    start_date: period.start_date,
    end_date: period.end_date,
    mode: period.mode,
    published: period.published,
    description: period.description,
    tiers: period.tiers.map(({ days, rewards }) => ({ days, rewards, featured: true })),
  });
  expect(api.save.mock.calls[0][0].cover).toBeUndefined();
  expect(api.save.mock.calls[0][2]).toEqual({ id: 'autumn', revision: 1 });
});

it('previews pool, collection and external covers and recovers from a failed image', async () => {
  const { view, dialog } = await openNew();
  const form = within(dialog);
  const source = form.getByLabelText('Image source');
  const preview = form.getByText('Cover preview').parentElement!;
  const image = () => preview.querySelector('img')!;
  expect(source).toHaveValue('default');
  expect(image()).toHaveAttribute('src', defaultCover);

  for (const kind of ['pool', 'collection'] as const) {
    const item = campaignCoverChoices(kind, true).find(({ label }) => !label.endsWith('R-18'))!;
    await view.user.selectOptions(source, kind);
    await view.user.selectOptions(form.getByLabelText('Image'), item.value);
    await view.user.selectOptions(form.getByLabelText('Image fit'), 'contain');
    expect(image()).toHaveAttribute(
      'src',
      resolveCover({ kind, value: item.value, fit: 'contain' })?.src,
    );
    expect(image().parentElement).toHaveAttribute('data-fit', 'contain');
  }

  await view.user.selectOptions(source, 'external');
  const url = form.getByLabelText('HTTPS image address');
  fireEvent.change(url, { target: { value: 'http://images.example.test/cover.webp' } });
  expect(form.getByText(/Enter a public HTTPS image address/)).toBeVisible();
  expect(image()).toHaveAttribute('src', defaultCover);
  await view.user.click(form.getByRole('button', { name: 'Save activity' }));
  expect(api.save).not.toHaveBeenCalled();

  const first = 'https://images.example.test/cover.webp';
  fireEvent.change(url, { target: { value: first } });
  expect(image()).toHaveAttribute('src', first);
  fireEvent.error(image());
  expect(image()).toHaveAttribute('src', defaultCover);
  expect(form.getByText(/The default cover is shown/)).toBeVisible();
  expect(url).toHaveValue(first);

  const replacement = 'https://images.example.test/new-cover.webp';
  fireEvent.change(url, { target: { value: replacement } });
  expect(image()).toHaveAttribute('src', replacement);
  expect(form.queryByText(/The default cover is shown/)).not.toBeInTheDocument();
  await view.user.selectOptions(form.getByLabelText('Image fit'), 'cover');
  await view.user.click(form.getByRole('button', { name: 'Save activity' }));
  await waitFor(() => expect(api.save).toHaveBeenCalledTimes(1));
  expect(api.save.mock.calls[0][0].cover).toEqual({
    kind: 'external',
    value: replacement,
    fit: 'cover',
  });
});

it('displays the cover and labels only the featured milestones on each list card', async () => {
  const url = 'https://images.example.test/one.webp';
  const [first, second] = attendancePeriod().tiers;
  api.list.mockResolvedValue(
    attendancePage([
      attendancePeriod({ id: 'zero', title: 'No highlights' }),
      attendancePeriod({
        id: 'one',
        title: 'One highlight',
        cover: { kind: 'external', value: url, fit: 'contain' },
        tiers: [first, { ...second, featured: true }],
      }),
      attendancePeriod({
        id: 'many',
        title: 'Two highlights',
        tiers: [
          { ...first, featured: true },
          { ...second, featured: true },
        ],
      }),
    ]),
  );
  await renderWithProviders(<AttendanceManagement account="operator" enabled />, {
    station: 'admin',
  });
  const none = await screen.findByRole('region', { name: 'No highlights' });
  const one = screen.getByRole('region', { name: 'One highlight' });
  const many = screen.getByRole('region', { name: 'Two highlights' });
  expect(within(none).queryByText('Featured reward')).not.toBeInTheDocument();
  const highlighted = within(one).getByText('Featured reward').closest('li');
  expect(highlighted).toContainElement(within(one).getByText('Day 14'));
  expect(highlighted).toHaveClass('attendance-milestone--featured');
  expect(within(many).getAllByText('Featured reward')).toHaveLength(2);
  expect(none.querySelector('.attendance-management__cover img')).toHaveAttribute(
    'src',
    defaultCover,
  );
  expect(one.querySelector('.attendance-management__cover img')).toHaveAttribute('src', url);
  expect(one.querySelector('.attendance-management__cover')).toHaveAttribute('data-fit', 'contain');
});

it('keeps an existing restricted cover and its featured milestone when saving another edit', async () => {
  const kind = 'collection';
  const rated = campaignCoverChoices(kind, true).find(({ label }) => label.endsWith('R-18'))!;
  const cover = { kind, value: rated.value, fit: 'contain' } as const;
  const [first, second] = attendancePeriod().tiers;
  api.list.mockResolvedValue(
    attendancePage([attendancePeriod({ cover, tiers: [{ ...first, featured: true }, second] })]),
  );
  const view = await renderWithProviders(<AttendanceManagement account="operator" enabled />, {
    station: 'admin',
  });
  view.queryClient.setQueryData(['admin', 'session'], attendanceAdmin);
  await view.user.click(await screen.findByRole('button', { name: 'Create activity' }));
  const create = within(await screen.findByRole('dialog'));
  await view.user.selectOptions(create.getByLabelText('Image source'), kind);
  expect(create.getByRole('option', { name: rated.label })).toBeDisabled();
  await view.user.click(create.getByRole('button', { name: 'Close' }));
  await view.user.click(screen.getByRole('button', { name: 'Edit activity' }));
  const edit = within(await screen.findByRole('dialog'));
  expect(edit.getByLabelText('Image')).toHaveValue(rated.value);
  expect(edit.getByRole('option', { name: rated.label })).toBeEnabled();
  expect(edit.getByRole('checkbox', { name: 'Day 7' })).toBeChecked();
  await view.user.clear(edit.getByLabelText('Description'));
  await view.user.type(edit.getByLabelText('Description'), 'Updated description.');
  await view.user.click(edit.getByRole('button', { name: 'Save activity' }));
  await waitFor(() => expect(api.save).toHaveBeenCalledTimes(1));
  expect(api.save.mock.calls[0][0]).toMatchObject({
    description: 'Updated description.',
    cover,
    tiers: [
      { days: first.days, rewards: first.rewards, featured: true },
      { days: second.days, rewards: second.rewards },
    ],
  });
});
