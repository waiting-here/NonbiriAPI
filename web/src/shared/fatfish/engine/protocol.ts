import { strictJSON } from "./canonical";
import { concatBytes, sha256, utf8 } from "./sha256";
import { DRAG_BUFFER, FIELD_HEIGHT, FIELD_WIDTH, MAX_INPUT_BYTES, MAX_INPUTS, TICKS_PER_SECOND, type Bowl, type BowlState, type InputTuple, type Level } from "./types";

export function initialFishRNG(seed: Uint8Array, fishID: number): [number, number, number, number] {
  if (seed.length !== 32 || !Number.isInteger(fishID) || fishID < 1 || fishID > 65535) throw new Error("fish seed or ID is invalid");
  const id = new Uint8Array(4);
  new DataView(id.buffer).setUint32(0, fishID, true);
  const digest = sha256(concatBytes(utf8("nonbiri-fatfish-turn-v1"), new Uint8Array([0]), seed, id));
  const view = new DataView(digest.buffer, digest.byteOffset, digest.byteLength);
  const state: [number, number, number, number] = [view.getUint32(0, true), view.getUint32(4, true), view.getUint32(8, true), view.getUint32(12, true)];
  if (state.every((word) => word === 0)) state[3] = 1;
  return state;
}
function rotateLeft(value: number, count: number): number { return (value << count) | (value >>> (32 - count)); }
// Blackman and Vigna's xoshiro128** 1.1, with explicit uint32 overflow.
export function nextTurnBit(state: [number, number, number, number]): number {
  return nextTurnWord(state) & 1;
}
export function nextTurnWord(state: [number, number, number, number]): number {
  const result = Math.imul(rotateLeft(Math.imul(state[1], 5), 7), 9) >>> 0;
  const t = state[1] << 9;
  state[2] = (state[2] ^ state[0]) >>> 0;
  state[3] = (state[3] ^ state[1]) >>> 0;
  state[1] = (state[1] ^ state[2]) >>> 0;
  state[0] = (state[0] ^ state[3]) >>> 0;
  state[2] = (state[2] ^ t) >>> 0;
  state[3] = rotateLeft(state[3], 11) >>> 0;
  return result;
}

export function scoreUnits(total: number, fed: number, durationSeconds: number, terminalTick: number): number {
  if (!Number.isInteger(total) || total < 1 || total > 40 || !Number.isInteger(fed) || fed < 0 || fed > total || !Number.isInteger(durationSeconds) || durationSeconds < 10 || durationSeconds > 600 || !Number.isInteger(terminalTick) || terminalTick < 0 || terminalTick > durationSeconds * TICKS_PER_SECOND) throw new Error("score inputs are out of range");
  const maxTick = durationSeconds * TICKS_PER_SECOND;
  const denominator = (total + 1) * (maxTick + 1) - 1;
  const numerator = fed * (maxTick + 1) + maxTick - terminalTick;
  return Math.floor(100000000 * numerator / denominator);
}
export function stars(thresholds: [number, number, number], fed: number, bowls: Bowl[], counts: BowlState[]): [number, boolean] {
  if (fed < thresholds[0]) return [0, false];
  for (const bowl of bowls) if ((counts.find((item) => item.id === bowl.id)?.count ?? 0) < bowl.required) return [0, false];
  for (let index = 2; index >= 0; index--) if (fed >= thresholds[index]) return [index + 1, true];
  return [0, false];
}

export function parseInputs(raw: string, level: Level): InputTuple[] {
  const size = utf8(raw).length;
  if (size === 0 || size > MAX_INPUT_BYTES) throw new Error("input payload exceeds bounds");
  const inputs = strictJSON(raw, 3);
  if (!Array.isArray(inputs)) throw new Error("input payload must be an array");
  validateInputs(inputs as InputTuple[], level);
  return inputs as InputTuple[];
}
export function validateInputs(inputs: InputTuple[], level: Level): void {
  if (!Array.isArray(inputs) || inputs.length > MAX_INPUTS) throw new Error("too many input events");
  const tools = new Set((level.tools ?? []).map((tool) => tool.id));
  let lastTick = -1, lastSeq = -1, effective = 0, lastPlaceTool = -1, terminal = false;
  for (let index = 0; index < inputs.length; index++) {
    const input = inputs[index];
    if (!Array.isArray(input) || terminal) throw new Error(`input[${index}] occurs after terminal operation`);
    const [tick, seq, op, toolID] = input;
    if (![tick, seq, toolID].every((value) => Number.isSafeInteger(value) && value >= 0 && !Object.is(value, -0)) || tick >= level.duration_seconds * TICKS_PER_SECOND || tick < lastTick || tick === lastTick && seq <= lastSeq) throw new Error(`input[${index}] tick or sequence is invalid`);
    if (tick !== lastTick) { effective = 0; lastPlaceTool = -1; }
    switch (op) {
      case "place":
        if (input.length !== 6 || !tools.has(toolID) || !Number.isSafeInteger(input[4]) || !Number.isSafeInteger(input[5]) || Object.is(input[4], -0) || Object.is(input[5], -0) || input[4] < -DRAG_BUFFER || input[4] > FIELD_WIDTH + DRAG_BUFFER || input[5] < -DRAG_BUFFER || input[5] > FIELD_HEIGHT + DRAG_BUFFER) throw new Error(`input[${index}] tool or position is invalid`);
        if (lastPlaceTool !== toolID) effective++;
        lastPlaceTool = toolID;
        break;
      case "return":
        if (input.length !== 4 || !tools.has(toolID)) throw new Error(`input[${index}] tool is unknown`);
        effective++; lastPlaceTool = -1;
        break;
      case "finish": case "abandon":
        if (input.length !== 4 || toolID !== 0) throw new Error(`input[${index}] terminal tool ID must be zero`);
        effective++; terminal = true; lastPlaceTool = -1;
        break;
      default: throw new Error(`input[${index}] operation is invalid`);
    }
    if (effective > 2) throw new Error(`input[${index}] exceeds the two-event tick limit`);
    lastTick = tick; lastSeq = seq;
  }
}
