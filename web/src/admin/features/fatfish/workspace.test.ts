import { describe, expect, it, vi } from 'vitest';
import { addPassedPrerequisite, EmptyConditionGroup, conditionEdges, removeConditionEdge } from './conditionEdges';
import { historyReducer, type History } from './history';
import { blankLevel, px } from './draft';
import { insertVertex, polygonEdit, polygonError, selectedPolygon } from './polygonEdit';
import { PlaytestDraft } from './playtestDraft';
import type { Condition, LevelInput } from './api';

const api = vi.hoisted(() => ({ save: vi.fn(), publish: vi.fn(), validate: vi.fn() }));
vi.mock('./api', () => ({ saveLevel: api.save, publishVersion: api.publish, validateLevelOnServer: api.validate }));

describe('exact condition tree edits', () => {
  it('adds one passed leaf without replacing any/count/stars semantics', () => {
    const original: Condition = { any: [{ stars: { node: 'A', min: 2 } }, { passed_count: 3 }, { total_stars: 7 }] };
    const linked = addPassedPrerequisite(original, 'A');
    expect(linked).toEqual({ all: [original, { passed: 'A' }] });
    expect(addPassedPrerequisite(linked, 'A')).toBe(linked);
    expect(removeConditionEdge(linked, [{ group: 'all', index: 1 }])).toEqual({ all: [original] });
  });
  it('removes only the addressed duplicate edge and requires an explicit empty-group choice', () => {
    const original: Condition = { all: [{ passed: 'A' }, { any: [{ passed: 'A' }] }, { total_stars: 2 }] };
    const edges = conditionEdges(original);
    expect(edges).toHaveLength(2);
    expect(() => removeConditionEdge(original, edges[1].path)).toThrow(EmptyConditionGroup);
    expect(removeConditionEdge(original, edges[1].path, 'remove_group')).toEqual({ all: [{ passed: 'A' }, { total_stars: 2 }] });
    expect(removeConditionEdge(original, edges[1].path, 'always')).toEqual({ all: [{ passed: 'A' }, {}, { total_stars: 2 }] });
  });
});
describe('workspace geometry and history', () => {
  it('preserves 1/64 coordinates and isolates unfinished edge insertion', () => {
    const level = blankLevel(), selected = { kind: 'bowls' as const, id: 2 };
    const polygon = selectedPolygon(level, selected)!;
    const a = polygon.outer[0], b = polygon.outer[1];
    const inserted = insertVertex(level, selected, { hole: null, index: 0 }, { x: (a.x + b.x) / 2, y: a.y });
    expect(selectedPolygon(level, selected)!.outer).toHaveLength(4);
    expect(polygonError(inserted, selected)).toMatch(/collinear/);
    const changed = polygonEdit(inserted, selected, (shape) => { shape.outer[1].y += 64; shape.outer[1].x += 1; });
    expect(polygonError(changed, selected)).toBeNull();
    expect(px(selectedPolygon(changed, selected)!.outer[1].x) * 64).toBe((a.x + b.x) / 2 + 1);
  });
  it('retains 100 undo actions, bounds bytes, and drops redo after a new edit', () => {
    let state: History<string> = { past: [], present: '0', future: [] };
    for (let i = 1; i <= 110; i++) state = historyReducer(state, { type: 'commit', value: String(i) });
    expect(state.past).toHaveLength(100);
    state = historyReducer(state, { type: 'undo' });
    expect(state.present).toBe('109');
    state = historyReducer(state, { type: 'commit', value: 'changed' });
    expect(state.future).toEqual([]);
    const large = 'a'.repeat(1024 * 1024);
    for (let i = 0; i < 12; i++) state = historyReducer(state, { type: 'commit', value: large + i });
    expect(new TextEncoder().encode(JSON.stringify([...state.past, state.present, ...state.future])).byteLength).toBeLessThanOrEqual(8 * 1024 * 1024);
  });
});
it('reuses each lost step with the captured draft, without saving or publishing it twice', async () => {
  api.validate.mockReset().mockResolvedValue({});
  api.save.mockReset().mockRejectedValueOnce(new Error('reply lost')).mockResolvedValue({ id: 'level', revision: '2' });
  api.publish.mockReset().mockRejectedValueOnce(new Error('version reply lost')).mockResolvedValue({ id: 'version', content_hash: 'a'.repeat(64) });
  const input: LevelInput = { title: 'Captured', description: '', draft: blankLevel(), expected_revision: '1' };
  const action = new PlaytestDraft('level', input), onSaved = vi.fn(), guard = vi.fn();
  input.title = 'New unsaved edit'; input.draft.duration_seconds = 60;
  await expect(action.run(onSaved, guard)).rejects.toThrow('reply lost');
  await expect(action.run(onSaved, guard)).rejects.toThrow('version reply lost');
  await expect(action.run(onSaved, guard)).resolves.toMatchObject({ id: 'version' });
  expect(api.validate).toHaveBeenCalledTimes(1);
  expect(api.save.mock.calls.map((call) => call[2])).toEqual([action.saveKey, action.saveKey]);
  expect(api.publish.mock.calls.map((call) => call[2])).toEqual([action.versionKey, action.versionKey]);
  expect(api.save.mock.calls[1][1]).toMatchObject({ title: 'Captured', draft: { duration_seconds: 90 } });
  expect(onSaved).toHaveBeenCalledTimes(1);
});
