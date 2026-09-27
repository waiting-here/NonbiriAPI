import { fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import type { EngineState, Level } from './engine/types';
import { FatFishPlayer } from './FatFishPlayer';
import type { FatFishPlayerSnapshot, FatFishSessionController } from './session';

const square = (halfWidth: number, halfHeight: number) => ({ outer: [
  { x: -halfWidth * 64, y: -halfHeight * 64 }, { x: halfWidth * 64, y: -halfHeight * 64 },
  { x: halfWidth * 64, y: halfHeight * 64 }, { x: -halfWidth * 64, y: halfHeight * 64 },
], holes: [] });
const level: Level = {
  format: 'nonbiri-fatfish-level', format_version: 1, engine_version: 1, scoring_version: 1,
  duration_seconds: 90, speed_pixels_per_second: 72, thresholds: [1, 1, 1],
  fish: [{ id: 1, x: 80 * 64, y: 400 * 64, heading: 0 }],
  tools: [
    { id: 100, resource_key: 'memory', polygon: square(5, 55), placed: true, x: 170 * 64, y: 420 * 64 },
    { id: 101, resource_key: 'barrier', polygon: square(36, 9), placed: false, x: 0, y: 0 },
  ],
  solids: [], hazards: [], bowls: [{ id: 2, polygon: { outer: [
    { x: 352 * 64, y: 156 * 64 }, { x: 408 * 64, y: 156 * 64 },
    { x: 408 * 64, y: 204 * 64 }, { x: 352 * 64, y: 204 * 64 },
  ], holes: [] }, required: 0, capacity: 1 }], switches: [], gates: [], directions: [],
};
const state: EngineState = {
  tick: 60, solid_revision: 0, terminal: false, reason: '',
  fish: [{ id: 1, x: 80 * 64, y: 400 * 64, heading: 0, status: 'walking', bowl_id: 0,
    turn_dir: 0, turn_distance: 0, flow_id: 0, speed_remainder: 0, x_remainder: 0, y_remainder: 0,
    rng: [1, 2, 3, 4] }],
  tools: [{ id: 100, placed: true, x: 170 * 64, y: 420 * 64 }, { id: 101, placed: false, x: 0, y: 0 }],
  switches: [], gates: [], bowls: [{ id: 2, count: 0 }],
};
function controller(canPlay = true, scene = level, runState = state) {
  const place = vi.fn(async (...args: number[]) => { void args; });
  const returnTool = vi.fn(async (...args: number[]) => { void args; });
  const snapshot = { phase: canPlay ? 'running' : 'prepared', challenge: { level: scene, state: 'active' },
    state: runState, canPlay, provisional: null, error: null } as FatFishPlayerSnapshot;
  const value = { snapshot: () => snapshot, subscribe: () => () => {}, advance: () => {},
    flush: async () => {}, poll: async () => null, place, returnTool } as unknown as FatFishSessionController;
  return { value, place, returnTool };
}
function pointer(target: Element, type: string, x: number, y: number, pointerID = 7) {
  const event = new Event(type, { bubbles: true }) as PointerEvent;
  Object.defineProperties(event, { pointerId: { value: pointerID }, isPrimary: { value: true },
    button: { value: 0 }, pointerType: { value: 'mouse' }, clientX: { value: x }, clientY: { value: y } });
  fireEvent(target, event);
}
function bounds(element: Element, x: number, y: number, width: number, height: number) {
  vi.spyOn(element, 'getBoundingClientRect').mockReturnValue({ left: x, top: y,
    right: x + width, bottom: y + height, width, height, x, y, toJSON: () => ({}) });
}
afterEach(() => vi.restoreAllMocks());

describe('Fat Fish illustrated player controls', () => {
  it('shows the same credited music control in user play and admin playtest', async () => {
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(null);
    const active = controller();
    const user = await renderWithProviders(<FatFishPlayer controller={active.value} />,
      { station: 'user', role: 'user' });
    expect(screen.getByRole('button', { name: 'Play music' })).toHaveAttribute('aria-pressed', 'false');
    expect(screen.getByText(/Monkeys Spinning Monkeys/)).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'CC BY 4.0' })).toHaveAttribute('href',
      'https://creativecommons.org/licenses/by/4.0/');
    user.unmount();

    await renderWithProviders(<FatFishPlayer controller={active.value} mode="playtest" />,
      { station: 'admin', role: 'admin' });
    expect(screen.getByRole('button', { name: 'Play music' })).toHaveAttribute('aria-pressed', 'false');
  });

  it('exposes the active loop for browser playback inspection and removes it on exit', async () => {
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(null);
    const play = vi.spyOn(HTMLMediaElement.prototype, 'play').mockResolvedValue();
    vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => {});
    vi.spyOn(HTMLMediaElement.prototype, 'load').mockImplementation(() => {});
    const active = controller();
    const view = await renderWithProviders(<FatFishPlayer controller={active.value} />,
      { station: 'user', role: 'user' });
    expect(document.querySelector('[data-fatfish-music]')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Play music' }));
    const audio = document.querySelector<HTMLAudioElement>('[data-fatfish-music]');
    expect(audio).toBeInstanceOf(HTMLAudioElement);
    expect(audio?.getAttribute('src')).toBe('/assets/fatfish/music/monkeys-spinning-monkeys.mp3');
    expect(audio?.loop).toBe(true);
    expect(play).toHaveBeenCalledTimes(1);
    view.unmount();
    expect(document.querySelector('[data-fatfish-music]')).toBeNull();
  });

  it('keeps a preplaced tool on the board and shows unplaced pieces on an external bench', async () => {
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(null);
    const active = controller();
    await renderWithProviders(<FatFishPlayer controller={active.value} />, { station: 'user', role: 'user' });
    expect(screen.getByRole('status')).toHaveAttribute('data-fish-tick', '60');
    expect(screen.getByRole('status')).toHaveAttribute('data-fish-fed', '0');
    expect(screen.getByText('Time left')).toBeInTheDocument();
    expect(screen.getByText('1:29')).toBeInTheDocument();
    expect(document.querySelector('[data-fish-board]')).toBeInstanceOf(HTMLCanvasElement);
    expect(document.querySelector('[data-staging-area] [data-tool-id="100"]')).toBeNull();
    const piece = document.querySelector('[data-staging-area] [data-tool-id="101"]');
    expect(piece).toHaveTextContent('Keycap barrier');
    expect(piece).not.toHaveTextContent('#101');
    expect(piece?.querySelector('img')).toHaveAttribute('draggable', 'false');
  });

  it('drags a preplaced narrow body with grab offset, returns it to the bench, and places a staged piece', async () => {
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(null);
    const active = controller();
    await renderWithProviders(<FatFishPlayer controller={active.value} />, { station: 'user', role: 'user' });
    const board = document.querySelector('.fatfish-player__board')!;
    const canvas = document.querySelector('[data-fish-board]')!;
    const yard = document.querySelector('[data-staging-area]')!;
    bounds(board, 0, 0, 480, 560); bounds(canvas, 0, 0, 480, 560); bounds(yard, 0, 600, 480, 100);
    pointer(canvas, 'pointerdown', 174, 420);
    pointer(canvas, 'pointermove', 194, 420);
    pointer(canvas, 'pointerup', 194, 420);
    expect(active.place).toHaveBeenLastCalledWith(100, 190 * 64, 420 * 64);
    pointer(canvas, 'pointerdown', 174, 420);
    pointer(canvas, 'pointermove', 174, 650);
    pointer(canvas, 'pointerup', 174, 650);
    expect(active.returnTool).toHaveBeenCalledWith(100);
    const piece = document.querySelector('[data-tool-id="101"]')!;
    pointer(piece, 'pointerdown', 50, 650);
    pointer(yard, 'pointermove', 250, 300);
    pointer(yard, 'pointerup', 250, 300);
    expect(active.place).toHaveBeenLastCalledWith(101, 250 * 64, 300 * 64);
  });

  it('does not move pieces during prepared preview', async () => {
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(null);
    const waiting = controller(false);
    await renderWithProviders(<FatFishPlayer controller={waiting.value} />, { station: 'user', role: 'user' });
    const canvas = document.querySelector('[data-fish-board]')!;
    bounds(canvas, 0, 0, 480, 560);
    pointer(canvas, 'pointerdown', 170, 420);
    pointer(canvas, 'pointerup', 180, 420);
    expect(waiting.place).not.toHaveBeenCalled();
    expect(document.querySelector('[data-tool-id="101"]')).toBeDisabled();
  });

  it('lets a keyboard player choose a preplaced tool, adjust it, and return it', async () => {
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(null);
    const active = controller();
    await renderWithProviders(<FatFishPlayer controller={active.value} />, { station: 'user', role: 'user' });
    const canvas = document.querySelector('[data-fish-board]')!;
    fireEvent.keyDown(canvas, { key: 'n' });
    fireEvent.keyDown(canvas, { key: 'ArrowRight' });
    expect(active.place).toHaveBeenCalledWith(100, 180 * 64, 420 * 64);
    fireEvent.keyDown(canvas, { key: 'Delete' });
    expect(active.returnTool).toHaveBeenCalledWith(100);
  });

  it('does not pick through a polygon hole, and pointer cancellation keeps the last sent position', async () => {
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(null);
    const hollow = structuredClone(level);
    hollow.tools[0].polygon.holes = [square(2, 2).outer];
    const active = controller(true, hollow);
    await renderWithProviders(<FatFishPlayer controller={active.value} />, { station: 'user', role: 'user' });
    const board = document.querySelector('.fatfish-player__board')!;
    const canvas = document.querySelector('[data-fish-board]')!;
    bounds(board, 0, 0, 480, 560); bounds(canvas, 0, 0, 480, 560);
    pointer(canvas, 'pointerdown', 170, 420);
    pointer(canvas, 'pointermove', 190, 420);
    pointer(canvas, 'pointerup', 190, 420);
    expect(active.place).not.toHaveBeenCalled();
    pointer(canvas, 'pointerdown', 174, 420);
    pointer(canvas, 'pointermove', 195, 420);
    await waitFor(() => expect(active.place).toHaveBeenCalled());
    const applied = active.place.mock.calls.length;
    pointer(canvas, 'pointercancel', 202, 420);
    fireEvent(window, new Event('blur'));
    expect(active.returnTool).not.toHaveBeenCalled();
    expect(active.place).toHaveBeenCalledTimes(applied);
  });
});
