import { screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { ApiError } from '@shared/query/http';
import { MemoriesPanel } from './MemoriesPanel';
const mocks = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), owners: vi.fn() }));
vi.mock('./api', async (original) => ({
  ...(await original<typeof import('./api')>()),
  getMemory: mocks.get,
  putMemory: mocks.put,
  getMemoryOwners: mocks.owners,
}));
beforeEach(() => {
  vi.resetAllMocks();
  mocks.owners.mockResolvedValue({
    items: [],
    total: '0',
    page: 1,
    page_size: 20,
    current_owner_id: '1',
  });
});
const original = {
  kind: 'chat',
  content: 'Original',
  revision: '1',
  updated_at: 1700000000000,
  updated_by: 'operator',
};
async function mount() {
  const view = await renderWithProviders(
    <MemoriesPanel role="admin" account="synthetic" active />,
    { station: 'admin', role: 'admin' },
  );
  view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'synthetic' } });
  const editor = await screen.findByRole('textbox', { name: 'Markdown content' });
  await view.user.clear(editor);
  await view.user.type(editor, 'My draft');
  await view.user.click(screen.getByRole('button', { name: 'Save' }));
  return { ...view, editor };
}
describe('shared memory recovery', () => {
  it('releases clean editors and their bodies while preserving an unsaved personal draft', async () => {
    mocks.owners.mockResolvedValue({
      items: [
        { id: '1', role: 'admin', label: 'Current administrator' },
        { id: '2', role: 'level5', label: 'First interlocutor' },
        { id: '3', role: 'level6', label: 'Second interlocutor' },
      ],
      total: '3',
      page: 1,
      page_size: 20,
      current_owner_id: '1',
    });
    mocks.get.mockImplementation(async (_role, kind, _signal, owner) => ({
      ...original,
      kind,
      owner_id: owner || '1',
      content: 'Original ' + (owner || 'self'),
      writable: true,
    }));
    const view = await renderWithProviders(
      <MemoriesPanel role="admin" account="synthetic" active />,
      { station: 'admin', role: 'admin' },
    );
    const editor = await screen.findByRole('textbox', { name: 'Markdown content' });
    await view.user.clear(editor);
    await view.user.type(editor, 'My unsaved preference');
    const picker = screen.getByRole('combobox', { name: 'Interlocutor', exact: true });
    await view.user.selectOptions(picker, '2');
    await screen.findByDisplayValue('Original 2');
    expect(document.querySelectorAll('textarea')).toHaveLength(2);
    await view.user.selectOptions(picker, '3');
    await screen.findByDisplayValue('Original 3');
    expect(document.querySelectorAll('textarea')).toHaveLength(2);
    await waitFor(() =>
      expect(
        view.queryClient.getQueryData(['admin', 'ai', 'synthetic', 'memory', 'personal', '2']),
      ).toBeUndefined(),
    );
    await view.user.selectOptions(picker, '');
    await screen.findByDisplayValue('My unsaved preference');
    expect(document.querySelectorAll('textarea')).toHaveLength(1);
    await view.user.click(screen.getByRole('button', { name: 'Cancel' }));
    await view.user.selectOptions(picker, '2');
    await screen.findByDisplayValue('Original 2');
    expect(document.querySelectorAll('textarea')).toHaveLength(1);
  });
  it('normalizes a current-administrator ID link to the same personal editor and cache', async () => {
    mocks.owners.mockResolvedValue({
      items: [{ id: '1', role: 'admin', label: 'Current administrator' }],
      total: '1',
      page: 1,
      page_size: 20,
      current_owner_id: '1',
    });
    mocks.get.mockResolvedValue({ ...original, kind: 'personal', owner_id: '1', writable: true });
    const view = await renderWithProviders(
      <MemoriesPanel role="admin" account="synthetic" active />,
      { station: 'admin', role: 'admin', route: '/ai?ai_memory_owner=1' },
    );
    await screen.findByRole('textbox', { name: 'Markdown content' });
    expect(screen.getByRole('combobox', { name: 'Interlocutor', exact: true })).toHaveValue('');
    expect(
      screen.queryByRole('option', { name: 'Current administrator · 1' }),
    ).not.toBeInTheDocument();
    expect(mocks.get.mock.calls.every((call) => call[3] === '')).toBe(true);
    expect(
      view.queryClient.getQueryData(['admin', 'ai', 'synthetic', 'memory', 'personal', '1']),
    ).toBeUndefined();
    expect(document.querySelectorAll('textarea')).toHaveLength(1);
  });
  it('starts with personal memory and keeps shared memories read-only for stewards', async () => {
    mocks.get.mockImplementation(async (_role, kind) => ({
      ...original,
      kind,
      writable: kind === 'personal',
    }));
    const view = await renderWithProviders(
      <MemoriesPanel role="steward" account="member:5" active />,
      { station: 'user', role: 'user' },
    );
    await screen.findByRole('textbox', { name: 'Markdown content' });
    expect(mocks.get.mock.calls[0]?.[1]).toBe('personal');
    await view.user.click(screen.getByRole('tab', { name: 'Chat memory', exact: true }));
    await screen.findByText(
      'Shared memories are maintained by administrators. Save your own habits and preferences in personal memory.',
    );
    expect(screen.queryByRole('textbox', { name: 'Markdown content' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Save' })).not.toBeInTheDocument();
    expect(mocks.put).not.toHaveBeenCalled();
  });
  it('replays the same CAS body when an uncertain write did not reach the server', async () => {
    let current = original;
    mocks.get.mockImplementation(async () => current);
    mocks.put
      .mockRejectedValueOnce(new ApiError('network_error', 'Response lost', 0))
      .mockImplementation(async (_role, _kind, input) => {
        current = { ...current, content: input.content, revision: '2' };
        return current;
      });
    const { user, editor } = await mount();
    await user.click(await screen.findByRole('button', { name: 'Check saved result' }));
    await waitFor(() => expect(mocks.put).toHaveBeenCalledTimes(2));
    await waitFor(() =>
      expect(screen.queryByRole('button', { name: 'Check saved result' })).not.toBeInTheDocument(),
    );
    expect(mocks.put.mock.calls[1]![2]).toEqual(mocks.put.mock.calls[0]![2]);
    expect(editor).toHaveValue('My draft');
    expect(editor).toBeEnabled();
  });
  it('confirms an uncertain write already visible in current memory without another PUT', async () => {
    let current = original;
    mocks.get.mockImplementation(async () => current);
    mocks.put.mockImplementation(async (_role, _kind, input) => {
      current = { ...current, content: input.content, revision: '2' };
      throw new ApiError('network_error', 'Response lost', 0);
    });
    const { editor } = await mount();
    await waitFor(() => expect(editor).toBeEnabled());
    expect(mocks.put).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole('button', { name: 'Check saved result' })).not.toBeInTheDocument();
    expect(editor).toHaveValue('My draft');
  });
  it('ends an uncertain write in recoverable conflict when a different revision was saved', async () => {
    let current = original;
    mocks.get.mockImplementation(async () => current);
    mocks.put
      .mockImplementationOnce(async () => {
        current = { ...current, content: 'Another edit', revision: '2' };
        throw new ApiError('network_error', 'Response lost', 0);
      })
      .mockRejectedValueOnce(new ApiError('conflict', 'Memory changed', 409));
    const { user, editor } = await mount();
    await user.click(await screen.findByRole('button', { name: 'Check saved result' }));
    await screen.findByText('Memory changed');
    expect(screen.queryByRole('button', { name: 'Check saved result' })).not.toBeInTheDocument();
    expect(editor).toHaveValue('My draft');
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeEnabled();
    expect(screen.getByRole('button', { name: 'Keep draft with current revision' })).toBeEnabled();
    expect(mocks.put.mock.calls[1]![2]).toEqual({ content: 'My draft', expected_revision: '1' });
  });
  it('keeps a conflicting draft, compares the current text, and saves only after adopting its revision', async () => {
    let current = {
      kind: 'chat',
      content: 'Original',
      revision: '1',
      updated_at: 1700000000000,
      updated_by: 'operator',
    };
    mocks.get.mockImplementation(async () => current);
    mocks.put
      .mockImplementationOnce(async () => {
        current = { ...current, content: 'Another edit', revision: '2' };
        throw new ApiError('conflict', 'Memory changed', 409);
      })
      .mockImplementation(async (_role, _kind, input) => {
        current = { ...current, content: input.content, revision: '3' };
        return current;
      });
    const view = await renderWithProviders(
      <MemoriesPanel role="admin" account="synthetic" active />,
      { station: 'admin', role: 'admin' },
    );
    view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'synthetic' } });
    const editor = await screen.findByRole('textbox', { name: 'Markdown content' });
    await view.user.clear(editor);
    await view.user.type(editor, 'My draft');
    await view.user.click(screen.getByRole('button', { name: 'Save' }));
    await screen.findByText('Memory changed');
    expect(editor).toHaveValue('My draft');
    await view.user.click(screen.getByRole('button', { name: 'Load current values' }));
    await screen.findByRole('button', { name: 'Keep draft with current revision' });
    expect(editor).toHaveValue('My draft');
    await view.user.click(screen.getByRole('button', { name: 'Keep draft with current revision' }));
    await view.user.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(mocks.put).toHaveBeenCalledTimes(2));
    expect(mocks.put.mock.calls[1]![2]).toEqual({ content: 'My draft', expected_revision: '2' });
  });
});
