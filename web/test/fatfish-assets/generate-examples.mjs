import fs from 'node:fs';

const out = new URL('../../public/examples/fatfish/', import.meta.url);
const px = (value) => value * 64;
const rect = (x1, y1, x2, y2) => ({ outer: [
  { x: px(x1), y: px(y1) }, { x: px(x2), y: px(y1) },
  { x: px(x2), y: px(y2) }, { x: px(x1), y: px(y2) },
], holes: [] });
const shape = (id, polygon) => ({ id, polygon });
const bowl = (id, polygon, required, capacity) => ({ id, polygon, required, capacity });
const tool = (id, resource_key, polygon, x, y) => ({ id, resource_key, polygon, placed: true, x: px(x), y: px(y) });
const direction = (id, polygon, mode, heading) => ({ id, polygon, mode, heading });
const fishRows = (count, x1, x2, y1, gap, heading = 0) => Array.from({ length: count }, (_, index) => ({
  id: index + 1, x: px(index % 2 === 0 ? x1 : x2), y: px(y1 + Math.floor(index / 2) * gap), heading,
}));
const base = (fish, duration_seconds, speed_pixels_per_second, thresholds) => ({
  format: 'nonbiri-fatfish-level', format_version: 1, engine_version: 1, scoring_version: 1,
  duration_seconds, speed_pixels_per_second, thresholds,
  fish, tools: [], solids: [], hazards: [], bowls: [], switches: [], gates: [], directions: [],
});

const firstRice = base(fishRows(8, 80, 105, 380, 20), 90, 80, [5, 7, 8]);
firstRice.tools.push(tool(100, 'memory', rect(-5, -55, 5, 55), 170, 420));
firstRice.directions.push(direction(700, rect(245, 365, 290, 470), 'entry', 3072));
firstRice.bowls.push(bowl(400, rect(236, 145, 300, 195), 0, 8));

const bufferPool = base([
  { id: 1, x: px(80), y: px(350), heading: 0 },
  { id: 2, x: px(80), y: px(500), heading: 0 },
  ...fishRows(8, 80, 105, 390, 20).map((fish, index) => ({ ...fish, id: index + 3 })),
], 100, 76, [7, 8, 10]);
bufferPool.solids.push(shape(200, rect(150, 340, 160, 360)), shape(201, rect(150, 490, 160, 510)));
bufferPool.hazards.push(shape(300, rect(390, 490, 435, 535)));
bufferPool.bowls.push(bowl(400, rect(330, 380, 385, 470), 0, 10));

const narrowBridge = base([
  { id: 1, x: px(110), y: px(450), heading: 2048 },
  { id: 2, x: px(250), y: px(210), heading: 0 },
  ...fishRows(8, 70, 95, 260, 17).map((fish, index) => ({ ...fish, id: index + 3 })),
], 120, 76, [7, 9, 10]);
narrowBridge.solids.push(shape(200, rect(114, 440, 124, 460)));
narrowBridge.hazards.push(shape(300, { outer: rect(180, 180, 310, 245).outer, holes: [rect(235, 195, 275, 230).outer] }), shape(301, rect(180, 345, 310, 410)));
narrowBridge.bowls.push(bowl(400, rect(35, 435, 65, 465), 0, 1), bowl(401, rect(380, 245, 430, 345), 0, 9));

const twoTurns = base(fishRows(12, 80, 105, 370, 18), 120, 80, [8, 10, 12]);
twoTurns.directions.push(direction(700, rect(190, 355, 240, 470), 'entry', 3072));
twoTurns.directions.push(direction(701, rect(185, 205, 240, 245), 'entry', 0));
twoTurns.tools.push(tool(100, 'barrier', rect(-5, -30, 5, 30), 270, 225));
twoTurns.bowls.push(bowl(400, rect(380, 205, 430, 245), 0, 12));

const lastBarrier = base(fishRows(12, 390, 415, 380, 18, 2048), 150, 80, [10, 11, 12]);
lastBarrier.tools.push(tool(100, 'barrier', rect(-5, -55, 5, 55), 230, 425));
lastBarrier.hazards.push(shape(300, rect(150, 280, 205, 340)), shape(301, rect(260, 280, 315, 340)));
lastBarrier.bowls.push(bowl(400, rect(45, 365, 100, 475), 0, 12));

const powerButton = base(fishRows(8, 80, 105, 380, 20), 120, 76, [6, 7, 8]);
powerButton.switches.push({ id: 500, polygon: rect(150, 365, 190, 465), mode: 'latch' });
powerButton.gates.push({ id: 600, polygon: rect(265, 365, 275, 465), initially_open: false, mode: 'any', switch_ids: [500] });
powerButton.bowls.push(bowl(400, rect(375, 365, 430, 475), 0, 8));

const oneWayStream = base(fishRows(10, 80, 105, 375, 18), 120, 80, [8, 9, 10]);
oneWayStream.directions.push(direction(700, rect(190, 355, 240, 470), 'entry', 3072));
oneWayStream.directions.push(direction(701, rect(185, 205, 445, 245), 'oneway', 0));
oneWayStream.bowls.push(bowl(400, rect(415, 205, 455, 245), 0, 10));

const riceBuffet = base(fishRows(10, 80, 105, 380, 20), 120, 80, [8, 9, 10]);
riceBuffet.tools.push(tool(100, 'memory', rect(-20, -60, 20, 60), 220, 420));
riceBuffet.bowls.push(bowl(400, rect(210, 365, 250, 475), 3, 4), bowl(401, rect(370, 365, 420, 475), 3, 6));

const examples = [
  ['01-first-rice', firstRice], ['02-buffer-pool', bufferPool],
  ['03-narrow-bridge', narrowBridge], ['04-two-turns', twoTurns],
  ['05-last-barrier', lastBarrier], ['06-power-button', powerButton],
  ['07-one-way-stream', oneWayStream], ['08-rice-buffet', riceBuffet],
];
for (const [id, level] of examples) {
  fs.writeFileSync(new URL(`${id}.fatfish.json`, out), `${JSON.stringify(level, null, 2)}\n`);
}
