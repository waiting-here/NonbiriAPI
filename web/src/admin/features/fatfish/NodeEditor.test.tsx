import { screen, waitFor } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import { NodeEditor } from './NodeEditor';
import type { Amounts, Condition } from './api';

const api = vi.hoisted(() => ({ getNode: vi.fn(), getVersion: vi.fn(), listLevels: vi.fn(), listVersions: vi.fn(), saveNode: vi.fn() }));
vi.mock('./api', () => ({ ...api }));

it('preserves a reachable self-reference and uses the returned revision for a subsequent edit', async () => {
  const amounts: Amounts = { unlock_cost: '0', ticket_price: '0', first_clear_reward: '0', star_rewards: ['0', '0', '0'] };
  const node = { id: 'ffn_one', period_id: 'ffp_one', title: 'First node', description: '',
    map_x: 200, map_y: 200, order: 0, revision: '1', version_id: 'ffv_one', hidden: false,
    condition: { any: [{}, { passed: 'ffn_one' }] } as Condition, amounts };
  const period = { id: 'ffp_one', title: 'Period', description: '', state: 'draft' as const,
    visible: false, paused: false, past_public: false, starts_at: 1, ends_at: 2,
    revision: '1', leaderboard_final: false, nodes: [node] };
  api.getNode.mockResolvedValue(node);
  api.getVersion.mockResolvedValue({ id: 'ffv_one', level_id: 'ffl_one', content_hash: 'a'.repeat(64) });
  api.listLevels.mockResolvedValue({ items: [{ id: 'ffl_one', title: 'Level' }], page: 1, page_size: 20, has_more: false });
  api.listVersions.mockResolvedValue({ items: [{ id: 'ffv_one', content_hash: 'a'.repeat(64) }], page: 1, page_size: 20, has_more: false });
  api.saveNode.mockImplementation(async (_periodID: string, _nodeID: string, input: { title: string }) => ({ ...node, title: input.title, revision: String(api.saveNode.mock.calls.length + 1) }));
  const view = await renderWithProviders(<NodeEditor period={period} nodeID={node.id} onDirty={() => {}} onSaved={() => {}} />, { station: 'admin', role: 'admin' });
  view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'fixture-admin' } });
  await screen.findByRole('heading', { name: 'Edit node' });
  expect(screen.getByRole('option', { name: /First node.*this node/ })).toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Save node' })).toBeEnabled();
  await view.user.clear(screen.getByLabelText('Title'));
  await view.user.type(screen.getByLabelText('Title'), 'First edit');
  await view.user.click(screen.getByRole('button', { name: 'Save node' }));
  await waitFor(() => expect(api.saveNode).toHaveBeenCalledTimes(1));
  await screen.findByDisplayValue('First edit');
  await view.user.clear(screen.getByLabelText('Title'));
  await view.user.type(screen.getByLabelText('Title'), 'Second edit');
  await view.user.click(screen.getByRole('button', { name: 'Save node' }));
  await waitFor(() => expect(api.saveNode).toHaveBeenCalledTimes(2));
  expect(api.saveNode.mock.calls[0][2].expected_revision).toBe('1');
  expect(api.saveNode.mock.calls[1][2].expected_revision).toBe('2');
});
