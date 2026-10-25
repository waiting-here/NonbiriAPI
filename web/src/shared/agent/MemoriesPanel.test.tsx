import { screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { ApiError } from '@shared/query/http';
import { MemoriesPanel } from './MemoriesPanel';
const mocks = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn() }));
vi.mock('./api', async (original) => ({
  ...(await original<typeof import('./api')>()),
  getMemory: mocks.get,
  putMemory: mocks.put,
}));
beforeEach(() => vi.resetAllMocks());
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
