import { act, fireEvent } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import { NodeMap } from './NodeMap';

it('does not begin a map drag after a dirty node selection is refused', async () => {
  const onSelect = vi.fn(() => false);
  const onMove = vi.fn();
  const view = await renderWithProviders(<NodeMap nodes={[{
    id: 'ffn_one', period_id: 'ffp_one', title: 'First node', description: '',
    map_x: 200, map_y: 200, order: 0, revision: '1', version_id: 'ffv_one', hidden: false,
  }]} selectedID={null} onSelect={onSelect} onMove={onMove} onConnect={vi.fn()} onRemove={vi.fn()} />, { station: 'admin', role: 'admin' });
  const stage = view.container.querySelector('svg');
  const node = stage?.querySelector('g');
  expect(stage).not.toBeNull();
  expect(node).not.toBeNull();
  fireEvent.pointerDown(node!, { pointerId: 1, clientX: 100, clientY: 100 });
  fireEvent.pointerMove(stage!, { pointerId: 1, clientX: 200, clientY: 200 });
  fireEvent.pointerUp(stage!, { pointerId: 1 });
  expect(onSelect).toHaveBeenCalledWith('ffn_one');
  expect(onMove).not.toHaveBeenCalled();
});

it('does not revive a released pointer when asynchronous selection is confirmed later', async () => {
  let confirm: (value: boolean) => void = () => {};
  const onMove = vi.fn();
  const view = await renderWithProviders(<NodeMap nodes={[{
    id: 'ffn_one', period_id: 'ffp_one', title: 'First node', description: '',
    map_x: 200, map_y: 200, order: 0, revision: '1', version_id: 'ffv_one', hidden: false,
  }]} selectedID={null} onSelect={() => new Promise((resolve) => { confirm = resolve; })}
    onMove={onMove} onConnect={vi.fn()} onRemove={vi.fn()} />, { station: 'admin', role: 'admin' });
  const stage = view.container.querySelector('svg')!;
  fireEvent.pointerDown(stage.querySelector('g')!, { pointerId: 1, clientX: 100, clientY: 100 });
  fireEvent.pointerUp(stage, { pointerId: 1 });
  await act(async () => confirm(true));
  fireEvent.pointerMove(stage, { pointerId: 1, clientX: 200, clientY: 200 });
  fireEvent.pointerUp(stage, { pointerId: 1 });
  expect(onMove).not.toHaveBeenCalled();
});
