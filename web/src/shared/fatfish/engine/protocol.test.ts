import { describe, expect, it } from "vitest";
import invalid from "./invalid.json";
import pinned from "./golden.json";
import { parseLevel } from "./canonical";
import { replay } from "./engine";
import { polygonIntersectionArea, polygonsInteriorOverlap, ringBounds, validatePolygon } from "./geometry";
import { preparedFootprintOverlap, preparePolygon, rectIntersectsCenter } from "./prepared";
import { parseInputs, scoreUnits, validateInputs } from "./protocol";
import { firstSweptContact } from "./sweep";
import { compileEllipse, compileRotatedRectangle, compileRoundedRectangle, fishFootprint } from "./trig_helpers";
import { decodeHex } from "./sha256";
import { MAX_INPUT_BYTES, MAX_INPUTS, MAX_LEVEL_BYTES, type Level, type Point, type Polygon } from "./types";

const corpus = invalid as unknown as { base: Level; levels: { name: string; raw: string }[]; inputs: { name: string; raw: string }[] };
const rect = (x: number, y: number, width: number, height: number): Polygon => ({ outer: [
  { x: x * 64, y: y * 64 }, { x: (x + width) * 64, y: y * 64 },
  { x: (x + width) * 64, y: (y + height) * 64 }, { x: x * 64, y: (y + height) * 64 },
], holes: [] });

it("bounds shape compiler inputs before integer multiplication", () => {
  const center = { x: 200 * 64, y: 200 * 64 };
  for (const size of [Number.MAX_SAFE_INTEGER, 2 ** 53, 133121]) {
    expect(() => compileEllipse(center, size, 10 * 64, 512)).toThrow();
    expect(() => compileRotatedRectangle(center, size, 10 * 64, 512)).toThrow();
    expect(() => compileRoundedRectangle(center, 20 * 64, 20 * 64, size, 512)).toThrow();
  }
  for (const x of [Number.MAX_SAFE_INTEGER, -1, 480 * 64 + 1, 0.5, NaN, -0]) {
    expect(() => compileEllipse({ x, y: 0 }, 10 * 64, 10 * 64, 512)).toThrow();
  }
  for (const heading of [0, 512, 1023, 4095]) {
    expect(() => compileEllipse(center, 20 * 64, 10 * 64, heading)).not.toThrow();
    expect(() => compileRotatedRectangle(center, 40 * 64, 20 * 64, heading)).not.toThrow();
    expect(() => compileRoundedRectangle(center, 40 * 64, 20 * 64, 5 * 64, heading)).not.toThrow();
  }
});

describe("shared rejection vectors", () => {
  for (const entry of corpus.levels) it(`rejects level ${entry.name}`, () => expect(() => parseLevel(entry.raw)).toThrow());
  for (const entry of corpus.inputs) it(`rejects input ${entry.name}`, () => expect(() => parseInputs(entry.raw, corpus.base)).toThrow());
});

describe("exact collision and scoring boundaries", () => {
  it("keeps a shared interior hole uncovered but accepts a solid union seam", () => {
    const fish = fishFootprint(100 * 64, 100 * 64);
    const withGap = { outer: rect(90, 90, 20, 20).outer, holes: [rect(99, 99, 2, 2).outer] };
    expect(preparedFootprintOverlap(fish, [preparePolygon(withGap), preparePolygon(withGap)]).full).toBe(false);
    expect(preparedFootprintOverlap(fish, [preparePolygon(rect(90, 90, 10, 20)), preparePolygon(rect(100, 90, 10, 20))]).full).toBe(true);
  });
  it("decomposes a concave contour while preserving its notch and safety hole", () => {
    const concave: Polygon = { outer: [
      { x: 200 * 64, y: 200 * 64 }, { x: 230 * 64, y: 200 * 64 }, { x: 230 * 64, y: 210 * 64 },
      { x: 210 * 64, y: 210 * 64 }, { x: 210 * 64, y: 230 * 64 }, { x: 200 * 64, y: 230 * 64 },
    ], holes: [rect(204, 204, 2, 2).outer] };
    expect(() => validatePolygon(concave, false)).not.toThrow();
    expect(polygonsInteriorOverlap(concave, rect(215, 215, 3, 3))).toBe(false);
    expect(polygonsInteriorOverlap(concave, rect(204, 204, 2, 2))).toBe(false);
    expect(polygonsInteriorOverlap(concave, rect(202, 215, 3, 3))).toBe(true);
  });
  it("distinguishes positive overlap from a blocking outer tangent", () => {
    const fish = fishFootprint(100 * 64, 100 * 64);
    for (const x of [94, 98, 103, 107, 109]) {
      const solid = rect(x, 96, 3, 8);
      expect(rectIntersectsCenter(100 * 64, 100 * 64, ringBounds(solid.outer), true)).toBe(polygonIntersectionArea(fish, solid).sign() > 0);
    }
    const tangent = ringBounds(rect(108, 96, 3, 8).outer);
    expect(rectIntersectsCenter(100 * 64, 100 * 64, tangent, true)).toBe(false);
    expect(rectIntersectsCenter(100 * 64, 100 * 64, tangent, false)).toBe(true);
  });
  it("sweeps through a hazard thinner than one substep and preserves hole safety", () => {
    const start: Point = { x: 100 * 64, y: 100 * 64 };
    const thin: Polygon = { outer: [
      { x: start.x + 30, y: start.y - 64 }, { x: start.x + 31, y: start.y - 64 },
      { x: start.x + 31, y: start.y + 64 }, { x: start.x + 30, y: start.y + 64 },
    ], holes: [] };
    expect(firstSweptContact(start, { x: start.x + 85, y: start.y }, [{ id: 1, polygon: thin }], [], [], []).kind).toBe("lost");
    const hole = { outer: rect(90, 90, 20, 20).outer, holes: [rect(98, 98, 4, 4).outer] };
    expect(firstSweptContact(start, { x: 102 * 64, y: start.y }, [{ id: 2, polygon: hole }], [], [], []).kind).toBe("");
    expect(firstSweptContact(start, { x: 102 * 64 + 1, y: start.y }, [{ id: 2, polygon: hole }], [], [], []).kind).toBe("lost");
  });
  it("orders saved fish before elapsed time, then faster ticks", () => {
    for (let total = 1; total <= 40; total++) for (const duration of [10, 600]) {
      const maximumTick = duration * 60;
      expect(scoreUnits(total, total, duration, 0)).toBe(100000000);
      for (let fed = 1; fed <= total; fed++) {
        expect(scoreUnits(total, fed, duration, maximumTick)).toBeGreaterThan(scoreUnits(total, fed - 1, duration, 0));
        expect(scoreUnits(total, fed, duration, maximumTick - 1)).toBeGreaterThan(scoreUnits(total, fed, duration, maximumTick));
      }
    }
  });
});

it("observes replay cancellation at 256-tick checkpoints", () => {
  const maximum = (pinned as unknown as { cases: { name: string; level: Level; seed: string }[] }).cases.find((entry) => entry.name === "all-count-and-vertex-ceilings");
  if (!maximum) throw new Error("maximum replay vector is missing");
  let checks = 0;
  expect(() => replay(maximum.level, decodeHex(maximum.seed), [], { shouldCancel: () => ++checks === 3 })).toThrow("replay canceled");
  expect(checks).toBe(3);
});

it("requires quota before finish and rejects input after automatic terminal state", () => {
  const first = (pinned as unknown as { cases: { level: Level; seed: string }[] }).cases[0];
  const seed = decodeHex(first.seed);
  expect(() => replay(first.level, seed, [[0, 1, "finish", 0]])).toThrow();
  expect(() => replay(first.level, seed, [[100, 1, "finish", 0]])).toThrow();
});

it("ends when aggregate bowl quotas exceed the remaining fish", () => {
  const vector = (pinned as unknown as { cases: { name: string; level: Level; seed: string }[] }).cases.find((entry) => entry.name === "unreachable-aggregate-bowl-quotas");
  if (!vector) throw new Error("aggregate quota vector is missing");
  const result = replay(vector.level, decodeHex(vector.seed), []);
  expect(result.reason).toBe("unreachable");
  expect(result.terminal_tick).toBe(1);
});

it("rejects oversized level and input payloads and event arrays", () => {
  expect(() => parseLevel(" ".repeat(MAX_LEVEL_BYTES + 1))).toThrow();
  expect(() => parseInputs(" ".repeat(MAX_INPUT_BYTES + 1), corpus.base)).toThrow();
  expect(() => validateInputs(Array(MAX_INPUTS + 1).fill([0, 0, "finish", 0]), corpus.base)).toThrow();
});
