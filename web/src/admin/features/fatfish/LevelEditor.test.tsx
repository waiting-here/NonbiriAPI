import { fireEvent, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { ApiError } from '@shared/query/http';
import { renderWithProviders } from '../../../../test/unit/support';
import { blankLevel } from './draft';
import { LevelManager } from './LevelEditor';

const api = vi.hoisted(() => ({
  listLevels: vi.fn(), getLevel: vi.fn(), saveLevel: vi.fn(),
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
});
