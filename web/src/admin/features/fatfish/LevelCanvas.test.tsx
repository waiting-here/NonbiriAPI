import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import { LevelCanvas } from './LevelCanvas';
import { blankLevel, toolPolygon, unit } from './draft';

function pointer(target: Element, type: string, x: number, y: number, id = 1, primary = true) {
  const event = new Event(type, { bubbles: true });
  Object.defineProperties(event, { pointerId: { value: id }, isPrimary: { value: primary },
    pointerType: { value: 'touch' }, button: { value: 0 }, clientX: { value: x }, clientY: { value: y } });
  fireEvent(target, event);
}

describe('continuous level workbench', () => {
  it('moves the selected piece displayed above another overlapping piece', async () => {
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(null);
    const level = blankLevel(), commit = vi.fn(), select = vi.fn();
    for (const id of [10, 11]) level.tools.push({ id, resource_key: 'barrier', polygon: toolPolygon('barrier'), x: unit(100), y: unit(624), placed: true });
    await renderWithProviders(<LevelCanvas level={level} selected={{ kind: 'tools', id: 10 }} grid={false} onSelect={select} onCommit={commit} />, { station: 'admin', role: 'admin' });
    const svg = screen.getByLabelText('Fat Fish level map');
    vi.spyOn(svg, 'getBoundingClientRect').mockReturnValue({ left: -128, top: -128, width: 736, height: 816 } as DOMRect);
    Object.assign(svg, { setPointerCapture: vi.fn() });
    pointer(svg, 'pointerdown', 100, 624); pointer(svg, 'pointermove', 150, 600); pointer(svg, 'pointerup', 150, 600);
    expect(select).toHaveBeenLastCalledWith({ kind: 'tools', id: 10 });
    expect(commit.mock.calls[0][0].tools).toEqual([
      expect.objectContaining({ id: 10, x: unit(150), y: unit(600) }),
      expect.objectContaining({ id: 11, x: unit(100), y: unit(624) }),
    ]);
  });

  it('scopes keyboard nudging, fine movement, history, deletion and cancellation to the map', async () => {
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(null);
    const level = blankLevel(), commit = vi.fn(), select = vi.fn(), remove = vi.fn(), undo = vi.fn(), redo = vi.fn(), cancel = vi.fn();
    level.tools.push({ id: 10, resource_key: 'memory', polygon: toolPolygon('memory'), x: unit(100), y: unit(624), placed: true });
    await renderWithProviders(<><input aria-label="Unrelated field" /><LevelCanvas level={level} selected={{ kind: 'tools', id: 10 }} grid onSelect={select} onCommit={commit}
      onDelete={remove} onUndo={undo} onRedo={redo} onCancelPlacement={cancel} /></>, { station: 'admin', role: 'admin' });
    const svg = screen.getByLabelText('Fat Fish level map');
    expect(svg).toHaveAttribute('tabindex', '0');
    fireEvent.keyDown(svg, { key: 'ArrowRight' });
    expect(commit.mock.calls[0][0].tools[0].x).toBe(unit(105));
    fireEvent.keyDown(svg, { key: 'ArrowUp', shiftKey: true });
    expect(commit.mock.calls[1][0].tools[0].y).toBe(unit(623));
    fireEvent.keyDown(screen.getByLabelText('Unrelated field'), { key: 'z', ctrlKey: true });
    expect(undo).not.toHaveBeenCalled();
    fireEvent.keyDown(svg, { key: 'z', ctrlKey: true });
    fireEvent.keyDown(svg, { key: 'Z', metaKey: true, shiftKey: true });
    expect(undo).toHaveBeenCalledTimes(1); expect(redo).toHaveBeenCalledTimes(1);
    fireEvent.keyDown(svg, { key: 'Delete' }); expect(remove).toHaveBeenCalledTimes(1);
    fireEvent.keyDown(svg, { key: 'Escape' }); expect(select).toHaveBeenLastCalledWith(null); expect(cancel).toHaveBeenCalledTimes(1);
  });

  it('ignores secondary releases and commits the primary drag from the bench into the field', async () => {
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(null);
    const level = blankLevel(), commit = vi.fn();
    level.tools.push({ id: 10, resource_key: 'barrier', polygon: toolPolygon('barrier'), x: unit(100), y: unit(624), placed: false });
    await renderWithProviders(<LevelCanvas level={level} selected={null} grid={false} onSelect={() => {}} onCommit={commit} />, { station: 'admin', role: 'admin' });
    const svg = screen.getByLabelText('Fat Fish level map');
    vi.spyOn(svg, 'getBoundingClientRect').mockReturnValue({ left: -128, top: -128, width: 736, height: 816 } as DOMRect);
    Object.assign(svg, { setPointerCapture: vi.fn() });
    pointer(svg, 'pointerdown', 100, 624);
    pointer(svg, 'pointermove', 200, 400);
    pointer(svg, 'pointerup', 0, 0, 2, false);
    pointer(svg, 'pointercancel', 0, 0, 2, false);
    expect(commit).not.toHaveBeenCalled();
    pointer(svg, 'pointermove', 250, 300);
    pointer(svg, 'pointerup', 250, 300);
    expect(commit).toHaveBeenCalledTimes(1);
    expect(commit.mock.calls[0][0].tools[0]).toMatchObject({ x: unit(250), y: unit(300), placed: true });
    expect(level.tools[0]).toMatchObject({ x: unit(100), y: unit(624), placed: false });
  });

  it('discards only the unfinished editor preview when the active pointer is cancelled', async () => {
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(null);
    const level = blankLevel(), commit = vi.fn();
    level.tools.push({ id: 10, resource_key: 'memory', polygon: toolPolygon('memory'), x: unit(100), y: unit(624), placed: false });
    await renderWithProviders(<LevelCanvas level={level} selected={null} grid={false} onSelect={() => {}} onCommit={commit} />, { station: 'admin', role: 'admin' });
    const svg = screen.getByLabelText('Fat Fish level map');
    vi.spyOn(svg, 'getBoundingClientRect').mockReturnValue({ left: -128, top: -128, width: 736, height: 816 } as DOMRect);
    Object.assign(svg, { setPointerCapture: vi.fn() });
    pointer(svg, 'pointerdown', 100, 624); pointer(svg, 'pointermove', 200, 300);
    pointer(svg, 'pointercancel', 200, 300); pointer(svg, 'pointerup', 200, 300);
    expect(commit).not.toHaveBeenCalled();
  });
});
