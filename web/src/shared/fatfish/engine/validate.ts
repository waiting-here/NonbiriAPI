import { containsPolygon, polygonsInteriorOverlap, translatePolygon, validatePolygon } from "./geometry";
import { DRAG_BUFFER, FIELD_HEIGHT, FIELD_WIDTH, FISH_RADIUS, supportedVersions, type Level, type Point, type Polygon } from "./types";

function keys(value: unknown, allowed: string[], required: string[], location: string): void {
  if (value === null || typeof value !== "object" || Array.isArray(value)) throw new Error(`${location} must be an object`);
  const actual = Object.keys(value);
  for (const key of actual) if (!allowed.includes(key)) throw new Error(`${location}.${key} is unsupported`);
  for (const key of required) if (!Object.hasOwn(value, key)) throw new Error(`${location}.${key} is required`);
}
function integer(value: unknown, minimum: number, maximum: number, location: string): void {
  if (typeof value !== "number" || !Number.isSafeInteger(value) || Object.is(value, -0) || value < minimum || value > maximum) throw new Error(`${location} is out of range`);
}
function pointKeys(point: Point, location: string): void { keys(point, ["x", "y"], ["x", "y"], location); }
function polygonKeys(polygon: Polygon, location: string): void {
  keys(polygon, ["outer", "holes"], ["outer"], location);
  if (!Array.isArray(polygon.outer)) throw new Error(`${location}.outer is invalid`);
  polygon.outer.forEach((point, index) => pointKeys(point, `${location}.outer[${index}]`));
  for (const [holeIndex, hole] of (polygon.holes ?? []).entries()) {
    if (!Array.isArray(hole)) throw new Error(`${location}.holes[${holeIndex}] is invalid`);
    hole.forEach((point, index) => pointKeys(point, `${location}.holes[${holeIndex}][${index}]`));
  }
}

export function validateLevel(level: Level): void {
  const fields = ["format", "format_version", "engine_version", "scoring_version", "duration_seconds", "speed_pixels_per_second", "thresholds", "fish", "tools", "solids", "hazards", "bowls", "switches", "gates", "directions"];
  keys(level, fields, fields.filter((field) => !["tools", "solids", "hazards", "switches", "gates", "directions"].includes(field)), "level");
  if (level.format !== "nonbiri-fatfish-level" || level.format_version !== 1 || !supportedVersions(level.engine_version, level.scoring_version)) throw new Error("level format or rules version is unsupported");
  integer(level.duration_seconds, 10, 600, "duration_seconds");
  integer(level.speed_pixels_per_second, 16, 160, "speed_pixels_per_second");
  if (!Array.isArray(level.fish) || level.fish.length < 1 || level.fish.length > 40) throw new Error("fish count exceeds bounds");
  if (!Array.isArray(level.thresholds) || level.thresholds.length !== 3) throw new Error("threshold shape is invalid");
  level.thresholds.forEach((value, index) => integer(value, 1, level.fish.length, `thresholds[${index}]`));
  if (level.thresholds[0] > level.thresholds[1] || level.thresholds[1] > level.thresholds[2]) throw new Error("star thresholds are invalid");
  const tools = level.tools ?? [], solids = level.solids ?? [], hazards = level.hazards ?? [], switches = level.switches ?? [], gates = level.gates ?? [], directions = level.directions ?? [];
  if (![tools, solids, hazards, switches, gates, directions, level.bowls].every(Array.isArray)) throw new Error("level object collections are invalid");
  if (tools.length > 24 || solids.length > 24 || hazards.length > 24 || switches.length > 16 || gates.length > 16 || directions.length > 24 || level.bowls.length < 1 || level.bowls.length > 8) throw new Error("object count exceeds bounds");
  const ids = new Map<number, string>();
  const addID = (id: number, location: string) => {
    integer(id, 1, 65535, `${location}.id`);
    if (ids.has(id)) throw new Error(`${location}.id duplicates ${ids.get(id)}`);
    ids.set(id, location);
  };
  let vertices = 0;
  const shape = (item: { id: number; polygon: Polygon }, location: string, local: boolean) => {
    keys(item, ["id", "polygon", "resource_key", "placed", "x", "y", "required", "capacity", "mode", "initially_open", "switch_ids", "heading"], ["id", "polygon"], location);
    addID(item.id, location);
    polygonKeys(item.polygon, `${location}.polygon`);
    validatePolygon(item.polygon, local);
    vertices += item.polygon.outer.length + (item.polygon.holes ?? []).reduce((count, hole) => count + hole.length, 0);
  };
  level.fish.forEach((fish, index) => {
    const location = `fish[${index}]`;
    keys(fish, ["id", "x", "y", "heading"], ["id", "x", "y", "heading"], location);
    addID(fish.id, location);
    integer(fish.x, FISH_RADIUS, FIELD_WIDTH - FISH_RADIUS, `${location}.x`);
    integer(fish.y, FISH_RADIUS, FIELD_HEIGHT - FISH_RADIUS, `${location}.y`);
    integer(fish.heading, 0, 4095, `${location}.heading`);
  });
  tools.forEach((item, index) => {
    const location = `tools[${index}]`;
    keys(item, ["id", "polygon", "resource_key", "placed", "x", "y"], ["id", "polygon", "resource_key", "placed", "x", "y"], location);
    shape(item, location, true);
    if (!["barrier", "memory", "fan", "light", "cup"].includes(item.resource_key) || typeof item.placed !== "boolean") throw new Error(`${location} resource or placement is invalid`);
    integer(item.x, -DRAG_BUFFER, FIELD_WIDTH + DRAG_BUFFER, `${location}.x`);
    integer(item.y, -DRAG_BUFFER, FIELD_HEIGHT + DRAG_BUFFER, `${location}.y`);
  });
  solids.forEach((item, index) => { keys(item, ["id", "polygon"], ["id", "polygon"], `solids[${index}]`); shape(item, `solids[${index}]`, false); });
  hazards.forEach((item, index) => { keys(item, ["id", "polygon"], ["id", "polygon"], `hazards[${index}]`); shape(item, `hazards[${index}]`, false); });
  let quota = 0, capacity = 0;
  level.bowls.forEach((item, index) => {
    const location = `bowls[${index}]`;
    keys(item, ["id", "polygon", "required", "capacity"], ["id", "polygon", "required", "capacity"], location);
    shape(item, location, false);
    integer(item.required, 0, level.fish.length, `${location}.required`);
    integer(item.capacity, 1, level.fish.length, `${location}.capacity`);
    if (item.required > item.capacity) throw new Error(`${location} quota exceeds capacity`);
    quota += item.required; capacity += item.capacity;
  });
  if (quota > level.fish.length || capacity < level.thresholds[0]) throw new Error("bowl quotas or capacities cannot satisfy the first star");
  switches.forEach((item, index) => {
    const location = `switches[${index}]`;
    keys(item, ["id", "polygon", "mode"], ["id", "polygon", "mode"], location);
    shape(item, location, false);
    if (item.mode !== "latch" && item.mode !== "hold") throw new Error(`${location} mode is invalid`);
  });
  gates.forEach((item, index) => {
    const location = `gates[${index}]`;
    keys(item, ["id", "polygon", "initially_open", "mode", "switch_ids"], ["id", "polygon", "initially_open", "mode"], location);
    shape(item, location, false);
    if (typeof item.initially_open !== "boolean" || item.mode !== "all" && item.mode !== "any" || !Array.isArray(item.switch_ids ?? [])) throw new Error(`${location} gate control is invalid`);
    const seen = new Set<number>();
    for (const switchID of item.switch_ids ?? []) {
      integer(switchID, 1, 65535, `${location}.switch_ids`);
      if (seen.has(switchID) || !switches.some((candidate) => candidate.id === switchID)) throw new Error(`${location} switch ID is repeated or unknown`);
      seen.add(switchID);
    }
  });
  directions.forEach((item, index) => {
    const location = `directions[${index}]`;
    keys(item, ["id", "polygon", "mode", "heading"], ["id", "polygon", "mode", "heading"], location);
    shape(item, location, false);
    if (item.mode !== "entry" && item.mode !== "oneway") throw new Error(`${location} mode is invalid`);
    integer(item.heading, 0, 4095, `${location}.heading`);
  });
  if (vertices > 2048) throw new Error("total contour vertex count exceeds 2048");
  level.fish.forEach((fish, index) => {
    const point: Point = { x: fish.x, y: fish.y };
    if (solids.some((item) => containsPolygon(item.polygon, point)) || tools.some((item) => item.placed && containsPolygon(translatePolygon(item.polygon, item.x, item.y), point)) || gates.some((item) => !item.initially_open && containsPolygon(item.polygon, point)) || hazards.some((item) => containsPolygon(item.polygon, point)) || level.bowls.some((item) => containsPolygon(item.polygon, point))) throw new Error(`fish[${index}] center begins inside an object`);
  });
  level.bowls.forEach((bowl, bowlIndex) => hazards.forEach((hazard, hazardIndex) => {
    if (polygonsInteriorOverlap(bowl.polygon, hazard.polygon)) throw new Error(`bowls[${bowlIndex}] overlaps hazards[${hazardIndex}]`);
  }));
}
