import { fireEvent, screen, waitFor } from '@testing-library/react';
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
vi.mock('./useDraftGuard', () => ({ useDraftGuard: () => () => true }));
vi.mock('./ExamplePicker', () => ({ ExamplePicker: () => null }));

const first = { id: 'ffl_first', title: 'First level', description: 'One', draft: blankLevel(), revision: '1', created_at: 1, updated_at: 1 };
const second = { ...first, id: 'ffl_second', title: 'Second level' };

describe('Fat Fish level editor', () => {
  beforeEach(() => {
    api.listLevels.mockResolvedValue({ items: [first, second], page: 1, page_size: 20, has_more: false });
    api.getLevel.mockImplementation(async (id: string) => id === first.id ? first : second);
    api.listVersions.mockResolvedValue({ items: [], page: 1, page_size: 20, has_more: false });
    api.listPlaytests.mockResolvedValue([]);
    api.validateLevelOnServer.mockResolvedValue({ content_hash: 'a'.repeat(64) });
    api.saveLevel.mockReset();
    api.deleteLevel.mockReset();
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
    expect(input).toMatchObject({ title: 'New level', draft: { duration_seconds: 120 } });
    expect(key).toMatch(/^[A-Za-z0-9_-]{22}$/);
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
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false);
    await view.user.click(screen.getByRole('button', { name: /Second level · r1/ }));
    expect(confirm).toHaveBeenCalled();
    expect(screen.getByLabelText('Title')).toHaveValue('My edit');
    expect(api.saveLevel.mock.calls[0][1].expected_revision).toBe('1');
  });

  it('applies the server UTF-8 byte limit to non-ASCII titles before saving', async () => {
    await renderWithProviders(<LevelManager />, { station: 'admin', role: 'admin' });
    await screen.findByRole('heading', { name: 'Level directory' });
    const title = screen.getByLabelText('Title');
    fireEvent.change(title, { target: { value: '鱼'.repeat(43) } });
    expect(screen.getByText(/Title is limited to 128 UTF-8 bytes/)).toBeInTheDocument();
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
    expect(screen.getByRole('button', { name: /Local error/ })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Save draft' })).toBeDisabled();
    await view.user.click(screen.getByRole('button', { name: 'Undo' }));
    expect(screen.getByLabelText('Duration (seconds)')).toHaveValue(90);
    expect(screen.queryByRole('button', { name: /Local error/ })).not.toBeInTheDocument();
  });

  it('confirms library deletion, sends its revision, and clears the deleted editor', async () => {
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false);
    api.deleteLevel.mockImplementation(async () => {
      api.listLevels.mockResolvedValue({ items: [second], page: 1, page_size: 20, has_more: false });
      return { id: first.id, revision: '2', deleted_at: 10 };
    });
    const view = await renderWithProviders(<LevelManager />, { station: 'admin', role: 'admin' });
    view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'fixture-admin' } });
    await view.user.click(await screen.findByRole('button', { name: /First level · r1/ }));
    await view.user.click(await screen.findByRole('button', { name: 'Delete level' }));
    expect(api.deleteLevel).not.toHaveBeenCalled();
    confirm.mockReturnValue(true);
    await view.user.click(screen.getByRole('button', { name: 'Delete level' }));
    await waitFor(() => expect(api.deleteLevel).toHaveBeenCalledTimes(1));
    expect(api.deleteLevel.mock.calls[0]).toEqual([first.id, '1', expect.stringMatching(/^[A-Za-z0-9_-]{22}$/)]);
    expect(confirm).toHaveBeenLastCalledWith(expect.stringContaining('Existing activities, plays and scores'));
    await screen.findByRole('heading', { name: 'New level draft' });
    expect(screen.getByLabelText('Title')).toHaveValue('');
    await waitFor(() => expect(screen.queryByRole('button', { name: /First level · r1/ })).not.toBeInTheDocument());
  });

  it('freezes an uncertain deletion and retries the original request with the same key', async () => {
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true);
    api.deleteLevel.mockRejectedValueOnce(new ApiError('unavailable', 'Response lost', 502))
      .mockResolvedValueOnce({ id: first.id, revision: '2', deleted_at: 10 });
    const view = await renderWithProviders(<LevelManager />, { station: 'admin', role: 'admin' });
    view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'fixture-admin' } });
    await view.user.click(await screen.findByRole('button', { name: /First level · r1/ }));
    await view.user.click(await screen.findByRole('button', { name: 'Delete level' }));
    await screen.findByText('Deletion outcome unknown. Retry the same deletion.');
    expect(screen.getByRole('button', { name: 'Save draft' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Publish immutable version from saved draft' })).toBeDisabled();
    expect(screen.getByLabelText('Title').closest('[inert]')).not.toBeNull();
    await view.user.click(screen.getByRole('button', { name: 'Retry same deletion' }));
    await waitFor(() => expect(api.deleteLevel).toHaveBeenCalledTimes(2));
    expect(api.deleteLevel.mock.calls[1]).toEqual(api.deleteLevel.mock.calls[0]);
    expect(confirm).toHaveBeenCalledTimes(1);
    await screen.findByRole('heading', { name: 'New level draft' });
  });

  it('preserves the draft and revision when deletion conflicts', async () => {
    vi.spyOn(window, 'confirm').mockReturnValue(true);
    api.deleteLevel.mockRejectedValue(new ApiError('conflict', 'Level changed elsewhere', 409));
    const view = await renderWithProviders(<LevelManager />, { station: 'admin', role: 'admin' });
    view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'fixture-admin' } });
    await view.user.click(await screen.findByRole('button', { name: /First level · r1/ }));
    fireEvent.change(await screen.findByLabelText('Title'), { target: { value: 'Unsaved work' } });
    await view.user.click(screen.getByRole('button', { name: 'Delete level' }));
    await screen.findByText('Level changed elsewhere');
    expect(screen.getByLabelText('Title')).toHaveValue('Unsaved work');
    expect(screen.getByRole('button', { name: 'Delete level' })).toBeEnabled();
  });
});
