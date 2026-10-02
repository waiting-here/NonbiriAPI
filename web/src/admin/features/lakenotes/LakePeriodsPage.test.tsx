import { act, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '@shared/query/http';
import { directions, type LakeDirectory, type Period } from '@shared/lakenotes/api';
import { renderWithProviders } from '../../../../test/unit/support';
import en from '../../i18n/en.json';
import { LakePeriodsPage } from './LakePeriodsPage';

const api = vi.hoisted(() => ({
  getLakeConfig: vi.fn(),
  getPeriods: vi.fn(),
  updateLakeConfig: vi.fn(),
  savePeriod: vi.fn(),
}));
vi.mock('@shared/lakenotes/api', async (original) => ({
  ...(await original<typeof import('@shared/lakenotes/api')>()),
  ...api,
}));
vi.mock('../../data', async (original) => ({
  ...(await original<typeof import('../../data')>()),
  useAdminSession: () => ({
    data: { admin: { username: 'admin-a' } },
    isPending: false,
    isFetching: false,
    error: null,
  }),
}));
const copy = en.admin.lakeNotes;
const config: LakeDirectory = {
  key: 'lake-notes',
  name: 'Lake',
  cover_key: 'lake-notes',
  visible: false,
  paused: false,
  starts_at: null,
  ends_at: null,
  revision: '1',
  status: 'unconfigured',
  module_config: { periods: [] },
};
const period: Period = {
  id: 'lnp_AAAAAAAAAAAAAAAAAAAAAA',
  revision: '1',
  name: 'Morning lake',
  status: 'draft',
  starts_at: 1800000000,
  ends_at: 1800003600,
  entry_fee_milli: null,
  exchanges: Object.fromEntries(
    directions.map((key) => [key, { enabled: false, source_amount: '', target_amount: '' }]),
  ) as Period['exchanges'],
};
const periodList = { items: [period], page: 1, page_size: 20, has_more: false };
async function renderPage() {
  const view = await renderWithProviders(<LakePeriodsPage />, {
    station: 'admin',
    role: 'admin',
    locale: 'en',
  });
  view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'admin-a' } });
  await screen.findByRole('button', { name: copy.saveAvailability });
  await waitFor(() =>
    expect(screen.getByRole('button', { name: copy.saveAvailability })).toBeEnabled(),
  );
  return view;
}
describe('lake period operations', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        if (String(input).endsWith('/time-context')) {
          return new Response(JSON.stringify({ mode: 'site', offset_minutes: 0 }), {
            headers: { 'content-type': 'application/json' },
          });
        }
        throw new Error('Unexpected request: ' + String(input));
      }),
    );
    api.getLakeConfig.mockResolvedValue(config);
    api.getPeriods.mockResolvedValue(periodList);
    api.updateLakeConfig.mockResolvedValue({ ...config, revision: '2', visible: true });
    api.savePeriod.mockResolvedValue({ ...period, revision: '2' });
  });
  it('keeps the save result visible and clears it when settings are edited', async () => {
    const view = await renderPage();
    await view.user.click(screen.getByLabelText(copy.visible));
    await view.user.click(screen.getByRole('button', { name: copy.saveAvailability }));
    expect(await screen.findByText(copy.saved)).toBeInTheDocument();
    await view.user.click(screen.getByLabelText(copy.paused));
    expect(screen.queryByText(copy.saved)).not.toBeInTheDocument();
    await view.user.click(screen.getByRole('button', { name: copy.saveAvailability }));
    await waitFor(() => expect(api.updateLakeConfig).toHaveBeenCalledTimes(2));
    expect(api.updateLakeConfig.mock.calls[1][0].expected_revision).toBe('2');
  });
  it('preserves an unknown save and its key when the config cache refreshes', async () => {
    api.updateLakeConfig.mockRejectedValueOnce(new ApiError('network_error', 'Response lost', 0));
    const view = await renderPage();
    await view.user.click(screen.getByRole('button', { name: copy.saveAvailability }));
    await screen.findByText(copy.unknown);
    const first = api.updateLakeConfig.mock.calls[0];
    act(() =>
      view.queryClient.setQueryData(['admin', 'lake-notes', 'config'], {
        ...config,
        revision: '2',
        paused: true,
      }),
    );
    await view.user.click(screen.getByRole('button', { name: copy.retry }));
    await waitFor(() => expect(api.updateLakeConfig).toHaveBeenCalledTimes(2));
    expect(api.updateLakeConfig.mock.calls[1].slice(0, 2)).toEqual(first.slice(0, 2));
  });
  it('retains the edited revision when a newer config arrives', async () => {
    const view = await renderPage();
    await view.user.click(screen.getByLabelText(copy.visible));
    act(() =>
      view.queryClient.setQueryData(['admin', 'lake-notes', 'config'], {
        ...config,
        revision: '2',
        paused: true,
      }),
    );
    expect(screen.getByLabelText(copy.visible)).toBeChecked();
    expect(screen.getByLabelText(copy.paused)).not.toBeChecked();
    await view.user.click(screen.getByRole('button', { name: copy.saveAvailability }));
    await waitFor(() => expect(api.updateLakeConfig).toHaveBeenCalledOnce());
    expect(api.updateLakeConfig.mock.calls[0][0].expected_revision).toBe('1');
  });
  it('drops the editor callback after the account changes during the refresh', async () => {
    let finish!: (value: typeof periodList) => void;
    const refreshed = new Promise<typeof periodList>((resolve) => {
      finish = resolve;
    });
    const view = await renderPage();
    await view.user.click(screen.getByRole('button', { name: copy.edit }));
    const name = screen.getByLabelText(copy.name);
    fireEvent.change(name, { target: { value: 'Local edit' } });
    api.savePeriod.mockResolvedValue({ ...period, name: 'Saved response', revision: '2' });
    api.getPeriods.mockReturnValue(refreshed);
    await view.user.click(screen.getByRole('button', { name: copy.saveDraft }));
    await waitFor(() => expect(api.getPeriods).toHaveBeenCalledTimes(2));
    await act(async () => {
      view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'admin-b' } });
      finish(periodList);
      await refreshed;
    });
    expect(screen.getByLabelText(copy.name)).toHaveValue('Local edit');
    expect(screen.queryByText(copy.saved)).not.toBeInTheDocument();
  });

  it('shows missing period dates as a local input error without making a request', async () => {
    const view = await renderPage();
    await view.user.click(screen.getByRole('button', { name: copy.newPeriod }));
    await view.user.type(screen.getByLabelText(copy.name), 'New lake');
    await view.user.click(screen.getByRole('button', { name: copy.saveDraft }));
    expect(screen.getByRole('alert')).toHaveTextContent(copy.invalid);
    expect(api.savePeriod).not.toHaveBeenCalled();
    expect(screen.getByLabelText(copy.name)).toHaveValue('New lake');
    await view.user.type(screen.getByLabelText(copy.name), ' corrected');
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('shows an incomplete availability window as a local input error', async () => {
    const view = await renderPage();
    fireEvent.change(screen.getByLabelText(copy.start), {
      target: { value: '2030-01-01T00:00:00' },
    });
    await waitFor(() =>
      expect(screen.getByRole('button', { name: copy.saveAvailability })).toBeEnabled(),
    );
    await view.user.click(screen.getByRole('button', { name: copy.saveAvailability }));
    expect(screen.getByRole('alert')).toHaveTextContent(copy.invalid);
    expect(api.updateLakeConfig).not.toHaveBeenCalled();
  });

  it('confirms publishing once and retains the draft when the dialog is cancelled', async () => {
    const view = await renderPage();
    await view.user.click(screen.getByRole('button', { name: copy.edit }));
    await view.user.selectOptions(screen.getByLabelText(copy.status), 'published');
    await view.user.type(screen.getByLabelText(copy.fee), '0');
    await view.user.click(screen.getByRole('button', { name: copy.publish }));
    let dialog = screen.getByRole('alertdialog');
    expect(within(dialog).getByRole('heading', { name: period.name })).toBeInTheDocument();
    expect(api.savePeriod).not.toHaveBeenCalled();
    await view.user.click(within(dialog).getByRole('button', { name: 'Cancel' }));
    expect(screen.getByLabelText(copy.fee)).toHaveValue('0');
    expect(screen.getByLabelText(copy.status)).toHaveValue('published');
    await view.user.click(screen.getByRole('button', { name: copy.publish }));
    dialog = screen.getByRole('alertdialog');
    await view.user.click(within(dialog).getByRole('button', { name: copy.publish }));
    await waitFor(() => expect(api.savePeriod).toHaveBeenCalledOnce());
    expect(api.savePeriod.mock.calls[0][1]).toMatchObject({
      expected_revision: '1',
      status: 'published',
      entry_fee_milli: '0',
      name: period.name,
    });
  });

  it.each(['draft', 'cancelled'] as const)(
    'confirms removing a published period to %s',
    async (status) => {
      api.getPeriods.mockResolvedValue({
        ...periodList,
        items: [{ ...period, status: 'published', entry_fee_milli: '0' }],
      });
      const view = await renderPage();
      await view.user.click(screen.getByRole('button', { name: copy.edit }));
      await view.user.selectOptions(screen.getByLabelText(copy.status), status);
      await view.user.click(
        screen.getByRole('button', { name: status === 'draft' ? copy.saveDraft : copy.savePeriod }),
      );
      expect(api.savePeriod).not.toHaveBeenCalled();
      await view.user.click(
        within(screen.getByRole('alertdialog')).getByRole('button', { name: copy.savePeriod }),
      );
      await waitFor(() => expect(api.savePeriod).toHaveBeenCalledOnce());
      expect(api.savePeriod.mock.calls[0][1]).toMatchObject({ expected_revision: '1', status });
    },
  );

  it('saves an unchanged published status directly', async () => {
    api.getPeriods.mockResolvedValue({
      ...periodList,
      items: [{ ...period, status: 'published', entry_fee_milli: '0' }],
    });
    const view = await renderPage();
    await view.user.click(screen.getByRole('button', { name: copy.edit }));
    await view.user.click(screen.getByRole('button', { name: copy.publish }));
    await waitFor(() => expect(api.savePeriod).toHaveBeenCalledOnce());
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument();
  });
});
