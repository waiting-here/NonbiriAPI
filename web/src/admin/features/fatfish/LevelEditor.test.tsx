import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { ApiError } from '@shared/query/http';
import { renderWithProviders } from '../../../../test/unit/support';
import { blankLevel } from './draft';
import { LevelManager } from './LevelEditor';

const api = vi.hoisted(() => ({
  listLevels: vi.fn(), getLevel: vi.fn(), saveLevel: vi.fn(), deleteLevel: vi.fn(),
  listVersions: vi.fn(), getVersion: vi.fn(), listPlaytests: vi.fn(),
  publishVersion: vi.fn(), validateLevelOnServer: vi.fn(),
}));
vi.mock('./api', () => ({ ...api }));
vi.mock('@shared/components/AsyncLeaveDialog', () => ({ AsyncLeaveDialog: () => null }));
vi.mock('./playtestApi', () => ({ currentPlaytest: async () => null }));
vi.mock('./ExamplePicker', () => ({ ExamplePicker: () => null }));

const first = { id: 'ffl_first', title: 'First level', description: 'One', draft: blankLevel(), revision: '1', created_at: 1, updated_at: 1 };
const second = { ...first, id: 'ffl_second', title: 'Second level' };

describe('Fat Fish level editor', () => {
  beforeEach(() => {
    api.listLevels.mockResolvedValue({ items: [first, second], page: 1, page_size: 20, has_more: false });
    api.getLevel.mockImplementation(async (id: string) => id === first.id ? first : second);
    api.listVersions.mockResolvedValue({ items: [], page: 1, page_size: 20, has_more: false });
    api.listPlaytests.mockResolvedValue([]);
    api.validateLevelOnServer.mockReset().mockResolvedValue({ content_hash: 'a'.repeat(64) });
    api.saveLevel.mockReset();
    api.deleteLevel.mockReset();
    api.publishVersion.mockReset();
  });

  it('supports undo/redo and sends the edited validated draft with a retained key', async () => {
    const next = { ...first, id: 'ffl_new', title: 'New level', revision: '1' };
    api.saveLevel.mockResolvedValue(next);
    const view = await renderWithProviders(<LevelManager />, { station: 'admin', role: 'admin' });
    view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'fixture-admin' } });
    await screen.findByRole('heading', { name: 'Level directory' });
    await view.user.type(screen.getByLabelText('Title'), 'New level');
    const duration = screen.getByLabelText('Duration (seconds)');
    fireEvent.change(duration, { target: { value: '120' } });
    expect(duration).toHaveValue(120);
    await view.user.click(screen.getByRole('button', { name: 'Undo' }));
    expect(duration).toHaveValue(90);
    await view.user.click(screen.getByRole('button', { name: 'Redo' }));
    expect(duration).toHaveValue(120);
    await view.user.click(screen.getByRole('button', { name: 'Save draft' }));
    await waitFor(() => expect(api.saveLevel).toHaveBeenCalledTimes(1));
    const [id, input, key] = api.saveLevel.mock.calls[0];
    expect(id).toBeNull();
    expect(input).toMatchObject({ title: 'New level', draft: { engine_version: 3, duration_seconds: 120 } });
    expect(key).toMatch(/^[A-Za-z0-9_-]{22}$/);
  });

  it.each([1, 2] as const)('explicitly converts version %s with undo and a required save before publication', async (version) => {
    const legacy = { ...first, draft: { ...first.draft, engine_version: version, speed_pixels_per_second: 76 } };
    api.getLevel.mockResolvedValue(legacy);
    api.saveLevel.mockImplementation(async (_id, input) => ({ ...legacy, ...input, revision: '2' }));
    const view = await renderWithProviders(<LevelManager />, { station: 'admin', role: 'admin' });
    view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'fixture-admin' } });
    await view.user.click(await screen.findByRole('button', { name: /First level · r1/ }));
    expect(await screen.findByText(`Draft rules version: ${version} · Legacy`)).toBeInTheDocument();
    const publish = screen.getByRole('button', { name: 'Publish saved draft' });
    expect(publish).toBeEnabled();
    await view.user.click(screen.getByRole('button', { name: 'Convert to new draft' }));
    expect(screen.getByText('Draft rules version: 3 · Current')).toBeInTheDocument();
    expect(screen.getByLabelText('Speed (pixels/second)')).toHaveValue(76);
    expect(publish).toBeDisabled();
    expect(api.saveLevel).not.toHaveBeenCalled();
    expect(api.publishVersion).not.toHaveBeenCalled();
    await view.user.click(screen.getByRole('button', { name: 'Undo' }));
    expect(screen.getByText(`Draft rules version: ${version} · Legacy`)).toBeInTheDocument();
    expect(publish).toBeEnabled();
    await view.user.click(screen.getByRole('button', { name: 'Redo' }));
    expect(screen.getByText('Draft rules version: 3 · Current')).toBeInTheDocument();
    expect(publish).toBeDisabled();
    await view.user.click(screen.getByRole('button', { name: 'Save draft' }));
    await waitFor(() => expect(api.saveLevel).toHaveBeenCalledTimes(1));
    expect(api.saveLevel.mock.calls[0].slice(0, 2)).toEqual([legacy.id, {
      title: legacy.title, description: legacy.description,
      draft: { ...legacy.draft, engine_version: 3 }, expected_revision: '1',
    }]);
    await waitFor(() => expect(screen.getByRole('button', { name: 'Publish saved draft' })).toBeEnabled());
    expect(legacy.draft.engine_version).toBe(version);
    expect(api.publishVersion).not.toHaveBeenCalled();
  });

  it('keeps an edited draft after a revision conflict and refuses a directory switch', async () => {
    api.saveLevel.mockRejectedValue(new ApiError('conflict', 'Revision changed', 409));
    const view = await renderWithProviders(<LevelManager />, { station: 'admin', role: 'admin' });
    view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'fixture-admin' } });
    await view.user.click(await screen.findByRole('button', { name: /First level · r1/ }));
    const title = await screen.findByLabelText('Title');
    await view.user.clear(title); await view.user.type(title, 'My edit');
    await view.user.click(screen.getByRole('button', { name: 'Save draft' }));
    await screen.findByText('Revision changed');
    expect(title).toHaveValue('My edit');
    await view.user.click(screen.getByRole('button', { name: /Second level · r1/ }));
    expect(screen.getByRole('alertdialog')).toBeInTheDocument();
    await view.user.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(screen.getByLabelText('Title')).toHaveValue('My edit');
    expect(api.saveLevel.mock.calls[0][1].expected_revision).toBe('1');
  });

  it('applies the server UTF-8 byte limit to non-ASCII titles before saving', async () => {
    await renderWithProviders(<LevelManager />, { station: 'admin', role: 'admin' });
    await screen.findByRole('heading', { name: 'Level directory' });
    const title = screen.getByLabelText('Title');
    fireEvent.change(title, { target: { value: '鱼'.repeat(43) } });
    expect(screen.getByText(/The title or description is too long/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Save draft' })).toBeDisabled();
    fireEvent.change(title, { target: { value: '鱼'.repeat(42) } });
    expect(screen.getByRole('button', { name: 'Save draft' })).toBeEnabled();
    expect(api.saveLevel).not.toHaveBeenCalled();
  });

  it('keeps an invalid draft visible and lets undo restore it instead of crashing the preview', async () => {
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(null);
    const view = await renderWithProviders(<LevelManager />, { station: 'admin', role: 'admin' });
    await screen.findByRole('heading', { name: 'Level directory' });
    fireEvent.change(screen.getByLabelText('Duration (seconds)'), { target: { value: '0' } });
    expect(screen.getByLabelText('Fat Fish level map')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Check your input/ })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Save draft' })).toBeDisabled();
    await view.user.click(screen.getByRole('button', { name: 'Undo' }));
    expect(screen.getByLabelText('Duration (seconds)')).toHaveValue(90);
    expect(screen.queryByRole('button', { name: /Check your input/ })).not.toBeInTheDocument();
  });

  it('confirms library deletion, sends its revision, and clears the deleted editor', async () => {
    api.deleteLevel.mockImplementation(async () => {
      api.listLevels.mockResolvedValue({ items: [second], page: 1, page_size: 20, has_more: false });
      return { id: first.id, revision: '2', deleted_at: 10 };
    });
    const view = await renderWithProviders(<LevelManager />, { station: 'admin', role: 'admin' });
    view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'fixture-admin' } });
    await view.user.click(await screen.findByRole('button', { name: /First level · r1/ }));
    await view.user.click(await screen.findByRole('button', { name: 'Delete level' }));
    expect(api.deleteLevel).not.toHaveBeenCalled();
    await view.user.click(screen.getByRole('button', { name: 'Cancel' }));
    await view.user.click(screen.getByRole('button', { name: 'Delete level' }));
    await view.user.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: 'Delete level' }));
    await waitFor(() => expect(api.deleteLevel).toHaveBeenCalledTimes(1));
    expect(api.deleteLevel.mock.calls[0]).toEqual([first.id, '1', expect.stringMatching(/^[A-Za-z0-9_-]{22}$/)]);
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument();
    await screen.findByRole('heading', { name: 'New level draft' });
    expect(screen.getByLabelText('Title')).toHaveValue('');
    await waitFor(() => expect(screen.queryByRole('button', { name: /First level · r1/ })).not.toBeInTheDocument());
  });

  it('freezes an uncertain deletion and retries the original request with the same key', async () => {
    api.deleteLevel.mockRejectedValueOnce(new ApiError('unavailable', 'Response lost', 502))
      .mockResolvedValueOnce({ id: first.id, revision: '2', deleted_at: 10 });
    const view = await renderWithProviders(<LevelManager />, { station: 'admin', role: 'admin' });
    view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'fixture-admin' } });
    await view.user.click(await screen.findByRole('button', { name: /First level · r1/ }));
    await view.user.click(await screen.findByRole('button', { name: 'Delete level' }));
    await view.user.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: 'Delete level' }));
    await screen.findByText('Deletion is unconfirmed. Try deleting again.');
    expect(screen.getByRole('button', { name: 'Save draft' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Publish saved draft' })).toBeDisabled();
    expect(screen.getByLabelText('Title').closest('[inert]')).not.toBeNull();
    await view.user.click(screen.getByRole('button', { name: 'Retry deletion' }));
    await waitFor(() => expect(api.deleteLevel).toHaveBeenCalledTimes(2));
    expect(api.deleteLevel.mock.calls[1]).toEqual(api.deleteLevel.mock.calls[0]);
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument();
    await screen.findByRole('heading', { name: 'New level draft' });
  });

  it('preserves the draft and revision when deletion conflicts', async () => {
    api.deleteLevel.mockRejectedValue(new ApiError('conflict', 'Level changed elsewhere', 409));
    const view = await renderWithProviders(<LevelManager />, { station: 'admin', role: 'admin' });
    view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'fixture-admin' } });
    await view.user.click(await screen.findByRole('button', { name: /First level · r1/ }));
    fireEvent.change(await screen.findByLabelText('Title'), { target: { value: 'Unsaved work' } });
    await view.user.click(screen.getByRole('button', { name: 'Delete level' }));
    await view.user.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: 'Delete level' }));
    await screen.findByText('Level changed elsewhere');
    expect(screen.getByLabelText('Title')).toHaveValue('Unsaved work');
    expect(screen.getByRole('button', { name: 'Delete level' })).toBeEnabled();
  });

  it.each([400, 502])('uses edited input after a confirmed rejection and retains unknown input (%s)', async (status) => {
    api.validateLevelOnServer.mockRejectedValue(new ApiError('validation_failed', 'Cannot prepare yet', status));
    const view = await renderWithProviders(<LevelManager />, { station: 'admin', role: 'admin' });
    view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'fixture-admin' } });
    await screen.findByRole('heading', { name: 'Level directory' });
    fireEvent.change(screen.getByLabelText('Title'), { target: { value: 'Edited level' } });
    await view.user.click(screen.getByRole('button', { name: 'Playtest this level' }));
    await screen.findByText('Cannot prepare yet');
    fireEvent.change(screen.getByLabelText('Duration (seconds)'), { target: { value: '120' } });
    await view.user.click(screen.getByRole('button', { name: status === 502 ? 'Continue preparing this playtest' : 'Playtest this level' }));
    await waitFor(() => expect(api.validateLevelOnServer).toHaveBeenCalledTimes(2));
    const [firstCall, nextCall] = api.validateLevelOnServer.mock.calls;
    expect(nextCall[0].duration_seconds).toBe(status === 502 ? 90 : 120);
    if (status === 502) expect(nextCall[1]).toBe(firstCall[1]);
    else expect(nextCall[1]).not.toBe(firstCall[1]);
    expect(api.saveLevel).not.toHaveBeenCalled();
  });

  it('clears the saved notice when the draft changes', async () => {
    api.saveLevel.mockImplementation(async (_id, input) => ({ ...first, ...input, revision: '2' }));
    const view = await renderWithProviders(<LevelManager />, { station: 'admin', role: 'admin' });
    view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'fixture-admin' } });
    await view.user.click(await screen.findByRole('button', { name: /First level · r1/ }));
    await view.user.click(await screen.findByRole('button', { name: 'Save draft' }));
    await screen.findByText('Draft saved.');
    fireEvent.change(screen.getByLabelText('Title'), { target: { value: 'Changed after saving' } });
    expect(screen.queryByText('Draft saved.')).not.toBeInTheDocument();
  });

});
