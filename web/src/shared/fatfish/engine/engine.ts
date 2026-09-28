import { contentHash, normalizeLevel, stateDigest } from "./canonical";
import { boundsOverlap, containsPolygon, polygonIntersectionArea, Rat, ringBounds, translatePolygon, type Bounds } from "./geometry";
import { preparedFootprintOverlap, preparedIntersectsFish, preparePolygon, extendBounds, rectIntersectsCenter, type PreparedPolygon } from "./prepared";
import { initialFishRNG, nextTurnBit, nextTurnWord, scoreUnits, stars, validateInputs } from "./protocol";
import { firstSweptContact } from "./sweep";
import { fishFootprint, positiveMod, sinCos } from "./trig_helpers";
import { ENGINE_VERSION, FIELD_HEIGHT, FIELD_WIDTH, FISH_RADIUS, SUBSTEPS, TICKS_PER_SECOND, type BowlState, type Direction, type EngineState, type FishState, type Gate, type InputTuple, type Level, type Point, type Polygon, type ReplayResult, type Switch } from "./types";
import type { OverlapResult } from "./geometry_union";

const SCALE = 1 << 20;
const signedRemainder = (value: number, modulus: number): number => { const result = value % modulus; return result === 0 ? 0 : result; };

export interface ReplayOptions {
  shouldCancel?: () => boolean;
  onTick?: (state: EngineState) => void;
}

export class Engine {
  readonly level: Level;
  readonly contentHash: string;
  private current: EngineState;
  private solids: Polygon[] = [];
  private prepared: PreparedPolygon[] = [];
  private overlapCache = new Map<string, OverlapResult>();
  private contactArea: Bounds | null = null;
  private flowBounds: Bounds[] = [];
  private switchBounds: Bounds[] = [];

  constructor(level: Level, seed: Uint8Array) {
    if (seed.length !== 32) throw new Error("seed must contain 32 bytes");
    this.level = normalizeLevel(level);
    this.contentHash = contentHash(this.level);
    this.current = {
      tick: 0, solid_revision: 0,
      fish: this.level.fish.map((fish): FishState => ({ id: fish.id, x: fish.x, y: fish.y, heading: fish.heading, status: "walking", bowl_id: 0, turn_dir: 0, turn_distance: 0, flow_id: 0, speed_remainder: 0, x_remainder: 0, y_remainder: 0, rng: initialFishRNG(seed, fish.id), ...(this.level.engine_version === ENGINE_VERSION ? { motion: { turn_remainder: 0, ambiguous_turn_dir: 0 } } : {}) })).sort((a, b) => a.id - b.id),
      tools: this.level.tools.map((tool) => ({ id: tool.id, placed: tool.placed, x: tool.x, y: tool.y })).sort((a, b) => a.id - b.id),
      switches: this.level.switches.map((object) => ({ id: object.id, active: false, triggered: false, occupied: false })).sort((a, b) => a.id - b.id),
      gates: this.level.gates.map((gate) => ({ id: gate.id, open: gate.initially_open, pending: false })).sort((a, b) => a.id - b.id),
      bowls: this.level.bowls.map((bowl) => ({ id: bowl.id, count: 0 })).sort((a, b) => a.id - b.id),
      terminal: false, reason: "",
    };
    this.level.bowls.sort((a, b) => a.id - b.id);
    this.level.hazards.sort((a, b) => a.id - b.id);
    this.level.directions.sort((a, b) => a.id - b.id);
    this.level.switches.sort((a, b) => a.id - b.id);
    this.level.gates.sort((a, b) => a.id - b.id);
    this.flowBounds = this.level.directions.map((item) => ringBounds(item.polygon.outer));
    this.switchBounds = this.level.switches.map((item) => ringBounds(item.polygon.outer));
    for (const item of [...this.level.hazards, ...this.level.bowls]) this.contactArea = extendBounds(this.contactArea, ringBounds(item.polygon.outer));
    this.refreshSolids();
  }

  state(): EngineState {
    return {
      ...this.current,
      fish: this.current.fish.map((fish) => ({ ...fish, rng: [...fish.rng] as [number, number, number, number], ...(fish.motion ? { motion: { ...fish.motion } } : {}) })),
      tools: this.current.tools.map((tool) => ({ ...tool })),
      switches: this.current.switches.map((item) => ({ ...item })),
      gates: this.current.gates.map((item) => ({ ...item })),
      bowls: this.current.bowls.map((item) => ({ ...item })),
    };
  }

  get tick(): number { return this.current.tick; }
  get terminal(): boolean { return this.current.terminal; }
  stateHash(): string { return stateDigest(this.current); }

  private refreshSolids(): void {
    const solids = this.level.solids.map((item) => item.polygon);
    for (const tool of this.level.tools) {
      const current = this.current.tools.find((item) => item.id === tool.id);
      if (current?.placed) solids.push(translatePolygon(tool.polygon, current.x, current.y));
    }
    for (const gate of this.level.gates) {
      if (this.current.gates.find((item) => item.id === gate.id)?.open === false) solids.push(gate.polygon);
    }
    this.solids = solids;
    this.prepared = solids.map(preparePolygon);
    this.current.solid_revision++;
    this.overlapCache.clear();
  }

  private overlapAt(x: number, y: number): OverlapResult {
    const key = `${x}:${y}:${this.current.solid_revision}`;
    const cached = this.overlapCache.get(key);
    if (cached) return cached;
    const overlap = this.intersectsAnySolid(x, y, true)
      ? preparedFootprintOverlap(fishFootprint(x, y), this.prepared)
      : { areas: this.prepared.map(() => Rat.int(0)), full: false };
    if (this.overlapCache.size >= 256) this.overlapCache.clear();
    this.overlapCache.set(key, overlap);
    return overlap;
  }

  private intersectsAnySolid(x: number, y: number, positiveOnly: boolean): boolean {
    const bounds = { minX: x - FISH_RADIUS, minY: y - FISH_RADIUS, maxX: x + FISH_RADIUS, maxY: y + FISH_RADIUS };
    let footprint: Point[] | null = null;
    for (const solid of this.prepared) {
      if (!boundsOverlap(bounds, solid.bounds)) continue;
      if (solid.rect) {
        if (rectIntersectsCenter(x, y, solid.bounds, positiveOnly)) return true;
        continue;
      }
      footprint ??= fishFootprint(x, y);
      if (preparedIntersectsFish(footprint, bounds, solid, positiveOnly)) return true;
    }
    return false;
  }

  private centerShielded(point: Point): boolean {
    for (const solid of this.prepared) {
      const bounds = solid.bounds;
      if (point.x >= bounds.minX && point.x <= bounds.maxX && point.y >= bounds.minY && point.y <= bounds.maxY && containsPolygon(solid.polygon, point)) return true;
    }
    return false;
  }

  private canMove(fish: FishState, heading: number, distance: number): [Point, number, number] | null {
    const [sine, cosine] = sinCos(heading), xNumerator = distance * cosine + fish.x_remainder, yNumerator = distance * sine + fish.y_remainder;
    const next = { x: fish.x + Math.trunc(xNumerator / SCALE), y: fish.y + Math.trunc(yNumerator / SCALE) };
    if (next.x - FISH_RADIUS < 0 || next.x + FISH_RADIUS > FIELD_WIDTH || next.y - FISH_RADIUS < 0 || next.y + FISH_RADIUS > FIELD_HEIGHT) return null;
    const current = this.overlapAt(fish.x, fish.y);
    if (current.full) return null;
    const currentPositive = current.areas.some((area) => area.sign() > 0);
    if (!currentPositive) {
      if (this.intersectsAnySolid(next.x, next.y, false)) return null;
      return [next, signedRemainder(xNumerator, SCALE), signedRemainder(yNumerator, SCALE)];
    }
    const after = this.overlapAt(next.x, next.y);
    let decreasing = false;
    for (let index = 0; index < current.areas.length; index++) {
      const oldArea = current.areas[index], newArea = after.areas[index];
      if (oldArea.sign() > 0) {
        if (newArea.cmp(oldArea) > 0) return null;
        if (newArea.cmp(oldArea) < 0) decreasing = true;
      } else if (newArea.sign() > 0) return null;
    }
    return decreasing ? [next, signedRemainder(xNumerator, SCALE), signedRemainder(yNumerator, SCALE)] : null;
  }

  private activeDirection(fish: FishState): Direction | null {
    const point = { x: fish.x, y: fish.y };
    for (let index = 0; index < this.level.directions.length; index++) {
      const bounds = this.flowBounds[index], zone = this.level.directions[index];
      if (point.x >= bounds.minX && point.x <= bounds.maxX && point.y >= bounds.minY && point.y <= bounds.maxY && containsPolygon(zone.polygon, point) && !this.centerShielded(point)) return zone;
    }
    return null;
  }

  private resolveContact(fish: FishState, from: Point, to: Point): void {
    if (fish.status !== "walking" || this.contactArea === null || !boundsOverlap(ringBounds([from, to]), this.contactArea)) return;
    const contact = firstSweptContact(from, to, this.level.hazards, this.level.bowls, this.current.bowls, this.solids);
    if (contact.kind === "lost") fish.status = "lost";
    if (contact.kind === "fed") {
      fish.status = "fed"; fish.bowl_id = contact.id;
      const bowl = this.current.bowls.find((item) => item.id === contact.id);
      if (bowl) bowl.count++;
    }
  }

  private moveSubstep(fish: FishState): void {
    if (fish.status !== "walking") return;
    const start = { x: fish.x, y: fish.y };
    this.resolveContact(fish, start, start);
    if (fish.status !== "walking") return;
    const numerator = this.level.speed_pixels_per_second * 64 + fish.speed_remainder;
    const distance = Math.trunc(numerator / (TICKS_PER_SECOND * SUBSTEPS));
    fish.speed_remainder = numerator % (TICKS_PER_SECOND * SUBSTEPS);
    const zone = this.activeDirection(fish);
    if (zone === null) fish.flow_id = 0;
    else {
      if (zone.id !== fish.flow_id) { fish.heading = zone.heading; fish.turn_dir = 0; fish.turn_distance = 0; if (fish.motion) { fish.motion.turn_remainder = 0; fish.motion.ambiguous_turn_dir = 0; } }
      fish.flow_id = zone.id;
    }
    if (zone?.mode === "oneway") {
      fish.heading = zone.heading; fish.turn_dir = 0; fish.turn_distance = 0;
      if (fish.motion) { fish.motion.turn_remainder = 0; fish.motion.ambiguous_turn_dir = 0; }
      const move = this.canMove(fish, zone.heading, distance);
      if (move) { fish.x = move[0].x; fish.y = move[0].y; fish.x_remainder = move[1]; fish.y_remainder = move[2]; this.resolveContact(fish, start, move[0]); }
      return;
    }
    if (this.level.engine_version === ENGINE_VERSION) { this.moveSubstepV2(fish, start, distance); return; }
    if (fish.turn_dir !== 0) fish.heading = positiveMod(fish.heading + fish.turn_dir * 23, 4096);
    else {
      const move = this.canMove(fish, fish.heading, distance);
      if (move) { fish.x = move[0].x; fish.y = move[0].y; fish.x_remainder = move[1]; fish.y_remainder = move[2]; this.resolveContact(fish, start, move[0]); return; }
      const left = this.canMove(fish, positiveMod(fish.heading - 1024, 4096), distance) !== null;
      const right = this.canMove(fish, positiveMod(fish.heading + 1024, 4096), distance) !== null;
      if (left && !right) fish.turn_dir = -1;
      else if (right && !left) fish.turn_dir = 1;
      else fish.turn_dir = nextTurnBit(fish.rng) === 0 ? -1 : 1;
      fish.turn_distance = 0;
      fish.heading = positiveMod(fish.heading + fish.turn_dir * 23, 4096);
      return;
    }
    const move = this.canMove(fish, fish.heading, distance);
    if (move) {
      fish.x = move[0].x; fish.y = move[0].y; fish.x_remainder = move[1]; fish.y_remainder = move[2];
      fish.turn_distance += distance;
      if (fish.turn_distance >= 12 * 64) { fish.turn_dir = 0; fish.turn_distance = 0; }
      this.resolveContact(fish, start, move[0]);
    } else fish.turn_distance = 0;
  }

  private probeBlocked(fish: FishState, forward: number, right: number): boolean {
    const [sine, cosine] = sinCos(fish.heading);
    const point = {
      x: fish.x + Math.trunc(64 * (forward * cosine - right * sine) / SCALE),
      y: fish.y + Math.trunc(64 * (forward * sine + right * cosine) / SCALE),
    };
    return point.x < 0 || point.x > FIELD_WIDTH || point.y < 0 || point.y > FIELD_HEIGHT || this.centerShielded(point);
  }

  private clearMotionTurn(fish: FishState): void {
    if (!fish.motion) throw new Error("version 2 motion state is missing");
    fish.turn_dir = 0; fish.turn_distance = 0;
    fish.motion.turn_remainder = 0; fish.motion.ambiguous_turn_dir = 0;
  }

  private moveSubstepV2(fish: FishState, start: Point, distance: number): void {
    const motion = fish.motion;
    if (!motion) throw new Error("version 2 motion state is missing");
    fish.turn_distance = 0;
    const move = this.canMove(fish, fish.heading, distance);
    if (move && this.overlapAt(fish.x, fish.y).areas.some((area) => area.sign() > 0)) {
      this.clearMotionTurn(fish);
      fish.x = move[0].x; fish.y = move[0].y; fish.x_remainder = move[1]; fish.y_remainder = move[2];
      this.resolveContact(fish, start, move[0]);
      return;
    }
    let front = this.probeBlocked(fish, 15, 0);
    const right = this.probeBlocked(fish, 9, 6), left = this.probeBlocked(fish, 9, -6);
    if (!front && !right && !left && !move) front = true;
    let base = 115;
    if (front || right && left) {
      base = 138;
      if (motion.ambiguous_turn_dir === 0) motion.ambiguous_turn_dir = (nextTurnWord(fish.rng) & 1) === 0 ? -1 : 1;
      fish.turn_dir = motion.ambiguous_turn_dir;
    } else if (right) { motion.ambiguous_turn_dir = 0; fish.turn_dir = -1; }
    else if (left) { motion.ambiguous_turn_dir = 0; fish.turn_dir = 1; }
    else {
      this.clearMotionTurn(fish);
      if (!move) throw new Error("clear probes require a legal move");
      fish.x = move[0].x; fish.y = move[0].y; fish.x_remainder = move[1]; fish.y_remainder = move[2];
      this.resolveContact(fish, start, move[0]);
      return;
    }
    const factor = 950 + Math.floor(nextTurnWord(fish.rng) * 101 / 4294967296);
    const accumulated = motion.turn_remainder + base * factor;
    motion.turn_remainder = accumulated % 5000;
    fish.heading = positiveMod(fish.heading + fish.turn_dir * Math.trunc(accumulated / 5000), 4096);
  }

  private updateMechanisms(): void {
    for (let index = 0; index < this.current.switches.length; index++) {
      const state = this.current.switches[index], shape: Switch = this.level.switches[index], bounds = this.switchBounds[index];
      let occupied = false;
      for (const fish of this.current.fish) {
        const point = { x: fish.x, y: fish.y };
        if (fish.status === "walking" && point.x >= bounds.minX && point.x <= bounds.maxX && point.y >= bounds.minY && point.y <= bounds.maxY && containsPolygon(shape.polygon, point) && !this.centerShielded(point)) { occupied = true; break; }
      }
      if (occupied && !state.occupied) state.triggered = true;
      state.occupied = occupied;
      state.active = shape.mode === "hold" ? occupied : state.triggered;
    }
    let changed = false;
    for (let index = 0; index < this.current.gates.length; index++) {
      const state = this.current.gates[index], gate: Gate = this.level.gates[index];
      let wanted = gate.initially_open;
      if (gate.switch_ids.length > 0) {
        let triggered = false, active = 0;
        for (const switchID of gate.switch_ids) {
          const object = this.current.switches.find((item) => item.id === switchID);
          if (object) { triggered ||= object.triggered; if (object.active) active++; }
        }
        if (triggered) wanted = gate.mode === "all" && active === gate.switch_ids.length || gate.mode === "any" && active > 0;
      }
      let occupied = false;
      if (state.open && !wanted) for (const fish of this.current.fish) {
        if (fish.status === "walking" && polygonIntersectionArea(fishFootprint(fish.x, fish.y), gate.polygon).sign() > 0) { occupied = true; break; }
      }
      state.pending = state.open && !wanted && occupied;
      const next = wanted || state.pending;
      if (next !== state.open) { state.open = next; changed = true; }
    }
    if (changed) this.refreshSolids();
  }

  private fedCount(): number { return this.current.fish.filter((fish) => fish.status === "fed").length; }

  private applyInputs(inputs: InputTuple[]): void {
    const lastPlace = new Map<number, number>();
    inputs.forEach((input, index) => { if (input[2] === "place") lastPlace.set(input[3], index); });
    inputs.forEach((input, index) => {
      if (input[2] === "place" && lastPlace.get(input[3]) !== index) return;
      switch (input[2]) {
        case "place": case "return": {
          const tool = this.current.tools.find((item) => item.id === input[3]);
          if (!tool) throw new Error(`unknown tool ID ${input[3]}`);
          if (input[2] === "place") { tool.placed = true; tool.x = input[4]; tool.y = input[5]; }
          else tool.placed = false;
          this.refreshSolids();
          break;
        }
        case "finish": {
          const [, passed] = stars(this.level.thresholds, this.fedCount(), this.level.bowls, this.current.bowls);
          if (!passed) throw new Error("finish requires the minimum fish and bowl quotas");
          this.current.terminal = true; this.current.reason = "finish";
          break;
        }
        case "abandon": this.current.terminal = true; this.current.reason = "abandon"; break;
      }
    });
  }

  private terminalReason(): string {
    let fed = 0, walking = 0;
    for (const fish of this.current.fish) { if (fish.status === "fed") fed++; else if (fish.status === "walking") walking++; }
    if (walking === 0) return "all_resolved";
    if (this.current.tick >= this.level.duration_seconds * TICKS_PER_SECOND) return "timeout";
    let capacityLeft = 0, requiredLeft = 0;
    for (const bowl of this.level.bowls) {
      const count = this.current.bowls.find((item) => item.id === bowl.id);
      if (count) {
        requiredLeft += Math.max(0, bowl.required - count.count);
        capacityLeft += bowl.capacity - count.count;
      }
    }
    if (requiredLeft > walking || fed + Math.min(walking, capacityLeft) < this.level.thresholds[0]) return "unreachable";
    if (capacityLeft === 0) return "all_resolved";
    return "";
  }

  step(inputs: InputTuple[] = []): void {
    if (this.current.terminal) throw new Error("engine already reached terminal state");
    if (inputs.length > 0) {
      validateInputs(inputs, this.level);
      if (inputs.some((input) => input[0] !== this.current.tick)) throw new Error("input tick does not match engine tick");
      this.applyInputs(inputs);
      if (this.current.terminal) return;
    }
    this.updateMechanisms();
    for (const fish of this.current.fish) for (let substep = 0; substep < SUBSTEPS && fish.status === "walking"; substep++) this.moveSubstep(fish);
    this.updateMechanisms();
    this.current.tick++;
    const reason = this.terminalReason();
    if (reason) { this.current.terminal = true; this.current.reason = reason; }
  }

  result(): ReplayResult {
    if (!this.current.terminal) throw new Error("engine result requires terminal state");
    const fed = this.fedCount();
    let [starsEarned, passed] = stars(this.level.thresholds, fed, this.level.bowls, this.current.bowls);
    if (this.current.reason === "abandon") { starsEarned = 0; passed = false; }
    return {
      engine_version: this.level.engine_version, scoring_version: this.level.scoring_version, content_hash: this.contentHash,
      final_state_hash: this.stateHash(), terminal_tick: this.current.tick, reason: this.current.reason,
      fed, total: this.current.fish.length, bowl_counts: this.current.bowls.map((item): BowlState => ({ ...item })),
      passed, stars: starsEarned, score_units: passed ? scoreUnits(this.current.fish.length, fed, this.level.duration_seconds, this.current.tick) : 0,
    };
  }
}

export function replay(level: Level, seed: Uint8Array, inputs: InputTuple[], options: ReplayOptions = {}): ReplayResult {
  validateInputs(inputs, level);
  const engine = new Engine(level, seed);
  options.onTick?.(engine.state());
  let inputIndex = 0;
  while (!engine.terminal) {
    const tick = engine.tick;
    if (tick % 256 === 0 && options.shouldCancel?.()) throw new Error("replay canceled");
    const start = inputIndex;
    while (inputIndex < inputs.length && inputs[inputIndex][0] === tick) inputIndex++;
    engine.step(inputs.slice(start, inputIndex));
    options.onTick?.(engine.state());
  }
  if (inputIndex !== inputs.length) throw new Error("input occurs after terminal state");
  return engine.result();
}
