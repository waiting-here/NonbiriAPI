import { concatBytes, decodeHex, hex, sha256, utf8 } from "./sha256";
import { ENGINE_VERSION, MAX_LEVEL_BYTES, SCORING_VERSION, type EngineState, type Level, type Polygon } from "./types";
import { validateLevel } from "./validate";

export function strictJSON(source: string, maxDepth: number): unknown {
  let position = 0;
  const whitespace = () => { while (position < source.length && /[ \t\n\r]/.test(source[position])) position++; };
  function stringValue(): string {
    const start = position++;
    let escaped = false;
    while (position < source.length) {
      const char = source[position++];
      if (char === '"' && !escaped) return JSON.parse(source.slice(start, position)) as string;
      if (char === "\\" && !escaped) escaped = true;
      else escaped = false;
    }
    throw new Error("unterminated JSON string");
  }
  function value(depth: number): unknown {
    if (depth > maxDepth) throw new Error("JSON depth exceeds limit");
    whitespace();
    const char = source[position];
    if (char === '"') return stringValue();
    if (char === "{") {
      position++;
      const result: Record<string, unknown> = Object.create(null) as Record<string, unknown>;
      whitespace();
      if (source[position] === "}") { position++; return result; }
      for (;;) {
        whitespace();
        if (source[position] !== '"') throw new Error("JSON object key must be a string");
        const key = stringValue();
        if (Object.hasOwn(result, key)) throw new Error(`duplicate JSON key ${key}`);
        whitespace();
        if (source[position++] !== ":") throw new Error("missing JSON colon");
        result[key] = value(depth + 1);
        whitespace();
        const separator = source[position++];
        if (separator === "}") return result;
        if (separator !== ",") throw new Error("invalid JSON object separator");
      }
    }
    if (char === "[") {
      position++;
      const result: unknown[] = [];
      whitespace();
      if (source[position] === "]") { position++; return result; }
      for (;;) {
        result.push(value(depth + 1));
        whitespace();
        const separator = source[position++];
        if (separator === "]") return result;
        if (separator !== ",") throw new Error("invalid JSON array separator");
      }
    }
    for (const [literal, parsed] of [["true", true], ["false", false], ["null", null]] as const) {
      if (source.startsWith(literal, position)) { position += literal.length; return parsed; }
    }
    const match = /^-?(?:0|[1-9][0-9]*)/.exec(source.slice(position));
    if (!match || match[0] === "-0") throw new Error("non-canonical JSON number");
    position += match[0].length;
    if (/[.eE]/.test(source[position] ?? "")) throw new Error("floating JSON number");
    const integer = Number(match[0]);
    if (!Number.isSafeInteger(integer) || Object.is(integer, -0)) throw new Error("JSON integer exceeds safe range");
    return integer;
  }
  const result = value(0);
  whitespace();
  if (position !== source.length) throw new Error("trailing JSON data");
  return result;
}

function normalizePolygon(polygon: Polygon): Polygon {
  return { outer: polygon.outer.map((point) => ({ x: point.x, y: point.y })), holes: (polygon.holes ?? []).map((hole) => hole.map((point) => ({ x: point.x, y: point.y }))) };
}

export function normalizeLevel(level: Level): Level {
  validateLevel(level);
  return {
    format: level.format, format_version: level.format_version,
    engine_version: level.engine_version, scoring_version: level.scoring_version,
    duration_seconds: level.duration_seconds, speed_pixels_per_second: level.speed_pixels_per_second,
    thresholds: [...level.thresholds] as [number, number, number],
    fish: level.fish.map((fish) => ({ ...fish })),
    tools: (level.tools ?? []).map((tool) => ({ ...tool, polygon: normalizePolygon(tool.polygon) })),
    solids: (level.solids ?? []).map((shape) => ({ id: shape.id, polygon: normalizePolygon(shape.polygon) })),
    hazards: (level.hazards ?? []).map((shape) => ({ id: shape.id, polygon: normalizePolygon(shape.polygon) })),
    bowls: level.bowls.map((bowl) => ({ ...bowl, polygon: normalizePolygon(bowl.polygon) })),
    switches: (level.switches ?? []).map((item) => ({ ...item, polygon: normalizePolygon(item.polygon) })),
    gates: (level.gates ?? []).map((gate) => ({ ...gate, polygon: normalizePolygon(gate.polygon), switch_ids: [...(gate.switch_ids ?? [])] })),
    directions: (level.directions ?? []).map((zone) => ({ ...zone, polygon: normalizePolygon(zone.polygon) })),
  };
}

export function parseLevel(raw: string): Level {
  if (utf8(raw).length === 0 || utf8(raw).length > MAX_LEVEL_BYTES) throw new Error("level size exceeds bounds");
  const level = strictJSON(raw, 16) as Level;
  validateLevel(level);
  return normalizeLevel(level);
}

export function canonicalJSON(data: unknown): string {
  if (data === null || typeof data === "boolean" || typeof data === "string") return JSON.stringify(data);
  if (typeof data === "number") {
    if (!Number.isSafeInteger(data) || Object.is(data, -0)) throw new Error("non-canonical number");
    return String(data);
  }
  if (Array.isArray(data)) return `[${data.map(canonicalJSON).join(",")}]`;
  if (typeof data === "object") {
    const record = data as Record<string, unknown>;
    return `{${Object.keys(record).sort().map((key) => `${JSON.stringify(key)}:${canonicalJSON(record[key])}`).join(",")}}`;
  }
  throw new Error("unsupported canonical value");
}

export function normalizedLevelBytes(level: Level): Uint8Array { return utf8(canonicalJSON(normalizeLevel(level))); }
export function contentHash(level: Level): string { return hex(sha256(normalizedLevelBytes(level))); }
export function stateDigest(state: EngineState): string { return hex(sha256(utf8(canonicalJSON(state)))); }

export function seedCommit(challengeID: string, periodID: string, nodeID: string, hash: string, seed: Uint8Array): string {
  if (![challengeID, periodID, nodeID].every((id) => /^[A-Za-z0-9_-]{1,128}$/.test(id))) throw new Error("commit ID is invalid");
  const content = decodeHex(hash);
  if (content.length !== 32 || seed.length !== 32) throw new Error("commit hash or seed length is invalid");
  const fields = [utf8("nonbiri-fatfish-commit-v1"), utf8(challengeID), utf8(periodID), utf8(nodeID), content, utf8(String(ENGINE_VERSION)), utf8(String(SCORING_VERSION)), seed];
  const prefixed = fields.map((field) => {
    const prefix = new Uint8Array(4);
    new DataView(prefix.buffer).setUint32(0, field.length, false);
    return concatBytes(prefix, field);
  });
  return hex(sha256(concatBytes(...prefixed)));
}
