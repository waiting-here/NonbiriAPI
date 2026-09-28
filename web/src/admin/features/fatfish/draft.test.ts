import { describe, expect, it } from 'vitest';
import { contentHash } from '@shared/fatfish/engine/canonical';
import { fishCollision, polygonPath } from './LevelCanvas';
import { blankLevel, cloneLevel, convertToCurrentDraft, hitTest, importDraft, localValidation, moveSelection, rect, toolPolygon, unit } from './draft';
import { laidOutLevel } from '@shared/fatfish/workspace';

describe('Fat Fish level authoring', () => {
  it('keeps tool footprints distinct and persists continuous bench coordinates', () => {
    const level = blankLevel();
    for (const [index, key] of ['barrier', 'memory', 'fan', 'light', 'cup'].entries()) {
      level.tools.push({ id: 10 + index, resource_key: key, polygon: toolPolygon(key), x: 0, y: 0, placed: false });
    }
    const laidOut = laidOutLevel(level);
    expect(localValidation(laidOut)).toBeNull();
    expect(laidOut.tools.every((tool) => tool.y > 560 * 64)).toBe(true);
    expect(laidOutLevel({ ...level, tools: [...level.tools].reverse() }).tools.reverse()).toEqual(laidOut.tools);
    const width = (key: string) => { const ring = level.tools.find((tool) => tool.resource_key === key)!.polygon.outer;
      return Math.max(...ring.map((p) => p.x)) - Math.min(...ring.map((p) => p.x)); };
    expect(width('barrier')).toBe(122 * 64); expect(width('memory')).toBe(50 * 64);
    expect(width('cup')).toBe(62 * 64);
    const moved = moveSelection(laidOut, { kind: 'tools', id: 10 }, -80 * 64, -200 * 64, false);
    expect(moved.tools[0]).toMatchObject({ x: -48 * 64, y: 424 * 64, placed: true });
    expect(localValidation(moved)).toBeNull();
    const roundtrip = importDraft(JSON.stringify(moved));
    expect(roundtrip.level.tools[0]).toEqual(moved.tools[0]);
    const clamped = moveSelection(moved, { kind: 'tools', id: 10 }, -1000 * 64, 1000 * 64, false);
    expect(clamped.tools[0]).toMatchObject({ x: -128 * 64, y: 688 * 64 });
  });
  it('starts from a valid draft, supports undoable immutable movement and export/import round trip', () => {
    const first = blankLevel();
    expect(first.engine_version).toBe(2);
    expect(localValidation(first)).toBeNull();
    const beforeHash = contentHash(first);
    const moved = moveSelection(first, { kind: 'fish', id: 1 }, unit(16), 0, true);
    expect(first.fish[0].x).toBe(unit(88));
    expect(moved.fish[0].x).toBe(unit(104));
    expect(contentHash(first)).toBe(beforeHash);
    const imported = importDraft(JSON.stringify({ title: 'Round trip', description: 'ordinary text', draft: moved }));
    expect(imported.title).toBe('Round trip');
    expect(imported.level).toEqual(moved);
    expect(imported.pendingFishCount).toBe(0);
  });

  it('preserves imported v1 rules until an explicit conversion changes only the draft version', () => {
    const legacy = { ...blankLevel(), engine_version: 1 as const, speed_pixels_per_second: 76 };
    legacy.tools.push({ id: 3, resource_key: 'barrier', polygon: toolPolygon('barrier'), placed: false, x: unit(-48), y: unit(624) });
    const imported = importDraft(JSON.stringify({ title: 'Legacy', description: 'Preserved', draft: legacy }));
    expect(imported.level).toEqual(legacy);
    const converted = convertToCurrentDraft(imported.level);
    expect(converted).toEqual({ ...legacy, engine_version: 2 });
    expect(localValidation(converted)).toBeNull();
    expect(contentHash(converted)).not.toBe(contentHash(legacy));
    converted.fish[0].x += 64;
    expect(imported.level).toEqual(legacy);
    expect(legacy.engine_version).toBe(1);
  });

  it('renders hazard holes and tests fish body against polygon geometry', () => {
    const level = blankLevel();
    level.hazards.push({ id: 3, polygon: {
      outer: rect(unit(200), unit(200), unit(120), unit(120)).outer,
      holes: [rect(unit(200), unit(200), unit(64), unit(64)).outer],
    } });
    expect(polygonPath(level.hazards[0].polygon).match(/M/g)).toHaveLength(2);
    expect(fishCollision(level, { x: unit(200), y: unit(200) })).toBe(false);
    expect(fishCollision(level, { x: unit(150), y: unit(200) })).toBe(true);
    expect(hitTest(level, { x: unit(200), y: unit(200) })).toBeNull();
    expect(hitTest(level, { x: unit(150), y: unit(200) })).toEqual({ kind: 'hazards', id: 3 });
  });

  it('converts v0.6 only to an incomplete draft requiring every fish placement', () => {
    const legacy = { format: 'fat-fish-level', version: 2, level: {
      title: 'Legacy', brief: 'Text', total: 2, target: 1, duration: 90, speed: 72,
      spawn: { x: 90, y: 420, heading: 0 }, passed: true,
      goal: { id: 'bowl', x: 380, y: 300, rx: 30, ry: 20, required: 0, capacity: 0 },
      tools: [], hazards: [], solids: [], gates: [], switches: [], flows: [],
    } };
    const converted = importDraft(JSON.stringify(legacy));
    expect(converted.pendingFishCount).toBe(2);
    expect(converted.level.fish).toEqual([]);
    expect(converted.warning).toMatch(/Old passed results are not carried over/);
    expect(localValidation(converted.level)).toMatch(/fish count/);
    const complete = cloneLevel(converted.level);
    complete.fish = [{ id: 2, x: unit(90), y: unit(420), heading: 0 }, { id: 3, x: unit(110), y: unit(420), heading: 0 }];
    expect(localValidation(complete)).toBeNull();
  });

  it('rejects oversized and invalid imports before any server mutation', () => {
    expect(() => importDraft('x'.repeat(256 * 1024 + 1))).toThrow(/256 KiB/);
    const invalid = blankLevel();
    invalid.hazards.push({ id: 3, polygon: { outer: [{ x: 1, y: 1 }], holes: [] } });
    expect(() => importDraft(JSON.stringify({ title: 'invalid', description: '', draft: invalid }))).toThrow(/outer contour/);
  });
});
