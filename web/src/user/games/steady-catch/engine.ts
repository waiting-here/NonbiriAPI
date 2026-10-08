export const RULES_VERSION = 1,
  HZ = 60,
  LAST_TICK = 5400,
  GOAL = 600;
export const WIDTH = 600000,
  HEIGHT = 560000,
  CATCH_Y = 446000,
  PAD_HALF = 43000;
export interface Phrase {
  id: string;
  text: string;
  category: string;
  gold: boolean;
}
export interface CollectionEvent {
  kind: Item['kind'] | 'miss';
  x: number;
  lostCombo: number;
  payload: number;
  points: number;
  combo: number;
  hpDelta: number;
  chargeReady: boolean;
  blocked: boolean;
}
export interface Input {
  tick: number;
  target: number;
  direction: number;
  shield?: boolean;
}
export interface Item {
  id: number;
  kind: 'phrase' | 'hazard' | 'prop';
  payload: number;
  x: number;
  y: number;
  width: number;
  height: number;
  speed: number;
  checked?: boolean;
}
export interface State {
  version: number;
  tick: number;
  score: number;
  hp: number;
  combo: number;
  max_combo: number;
  caught: number;
  missed: number;
  hits: number;
  charge: number;
  x: number;
  target: number;
  direction: number;
  invulnerable: number;
  effects: { shield: number; slow: number; magnet: number; double: number };
  spawn_in: number;
  serial: number;
  rng: number;
  normal_bag: number[];
  gold_bag: number[];
  items: Item[];
  cause: '' | 'time' | 'hp';
}
const clamp = (v: number, low: number, high: number) => Math.min(high, Math.max(low, v));
const div = (n: number, d: number) => Math.trunc(n / d);
export function newGame(seed: number): State {
  return {
    version: 1,
    tick: 0,
    score: 0,
    hp: 5,
    combo: 0,
    max_combo: 0,
    caught: 0,
    missed: 0,
    hits: 0,
    charge: 0,
    x: WIDTH / 2,
    target: WIDTH / 2,
    direction: 0,
    invulnerable: 0,
    effects: { shield: 0, slow: 0, magnet: 0, double: 0 },
    spawn_in: 33,
    serial: 0,
    rng: seed || 0x9e3779b9,
    normal_bag: [],
    gold_bag: [],
    items: [],
    cause: '',
  };
}
export const cleared = (s: State) => s.cause === 'time' && s.hp > 0 && s.score >= GOAL;

// This predictor mirrors the integer server rules. Only controls are submitted;
// the server owns the recorded score, first clear and final payment.
export function advance(
  before: State,
  inputs: readonly Input[],
  until: number,
  phrases: readonly Phrase[],
  onCollect?: (event: CollectionEvent) => void,
): State {
  const s: State = {
    ...before,
    effects: { ...before.effects },
    items: before.items.map((item) => ({ ...item })),
    normal_bag: [...before.normal_bag],
    gold_bag: [...before.gold_bag],
  };
  let index = 0;
  const random = (n: number) => {
    let x = s.rng;
    x ^= x << 13;
    x ^= x >>> 17;
    x ^= x << 5;
    s.rng = x >>> 0;
    return s.rng % n;
  };
  const phrase = () => {
    const gold = random(96) < 5,
      bag = gold ? s.gold_bag : s.normal_bag;
    if (!bag.length) {
      phrases.forEach((p, i) => {
        if (p.gold === gold) bag.push(i);
      });
      for (let i = bag.length - 1; i > 0; i--) {
        const j = random(i + 1);
        [bag[i], bag[j]] = [bag[j], bag[i]];
      }
    }
    return bag.pop()!;
  };
  const spawn = () => {
    const n = s.serial;
    const item: Item = {
      id: n + 1,
      kind: 'phrase',
      width: 152000,
      height: 62000,
      y: -80000,
      x: WIDTH / 2,
      payload: 0,
      speed: 0,
    };
    if (n >= 4 && n % 13 === 8) {
      item.kind = 'prop';
      item.payload = div(n, 13) % 5;
      item.width = 160000;
    } else if (n >= 4 && random(100) < (s.tick >= 30 * HZ ? 25 : 18)) {
      item.kind = 'hazard';
      item.payload = random(6);
      item.width = 128000;
    } else item.payload = phrase();
    if (n > 0) item.x = random(WIDTH - 100000) + 50000;
    item.x = clamp(item.x, item.width / 2 + 10000, WIDTH - item.width / 2 - 10000);
    item.speed = div((CATCH_Y + 80000) * 2 * (54000 + 7 * s.tick), 9 * HZ * 54000);
    if (item.kind === 'hazard')
      for (let attempt = 0; attempt < 10; attempt++) {
        const blocked = s.items.some(
          (other) =>
            other.y < 85000 &&
            other.kind !== 'prop' &&
            Math.abs(other.x - item.x) < (other.width + item.width) / 2 + 30000,
        );
        if (!blocked) break;
        item.x = clamp(random(WIDTH), item.width / 2 + 10000, WIDTH - item.width / 2 - 10000);
        if (attempt === 9) {
          item.kind = 'phrase';
          item.payload = phrase();
        }
      }
    s.serial++;
    s.items.push(item);
  };
  const collect = (item: Item) => {
    if (item.kind === 'phrase') {
      s.combo++;
      s.max_combo = Math.max(s.max_combo, s.combo);
      s.caught++;
      s.charge = Math.min(10, s.charge + 1);
      s.score +=
        (phrases[item.payload].gold ? 20 : 10) *
        (1 + Math.min(2, div(s.combo, 5))) *
        (s.effects.double > s.tick ? 2 : 1);
    } else if (item.kind === 'hazard') {
      if (s.effects.shield > s.tick || s.invulnerable > s.tick) return;
      s.hp--;
      s.combo = 0;
      s.hits++;
      s.invulnerable = s.tick + 66;
      if (s.hp === 0) s.cause = 'hp';
    } else if (item.payload === 4) s.hp = Math.min(5, s.hp + 1);
    else {
      const key = (['shield', 'slow', 'magnet', 'double'] as const)[item.payload];
      s.effects[key] = Math.max(s.effects[key], s.tick) + 7 * HZ;
    }
  };
  while (s.tick < until && !s.cause) {
    s.tick++;
    if (index < inputs.length && inputs[index].tick === s.tick) {
      const input = inputs[index++];
      s.target = input.target;
      s.direction = input.direction;
      if (input.shield && s.charge >= 10) {
        s.charge = 0;
        s.effects.shield = Math.max(s.effects.shield, s.tick + 4 * HZ);
      }
    }
    if (s.direction) {
      s.x = clamp(s.x + s.direction * 9000, PAD_HALF + 12000, WIDTH - PAD_HALF - 12000);
      s.target = s.x;
    } else
      s.x = clamp(
        s.x + clamp(s.target - s.x, -15000, 15000),
        PAD_HALF + 12000,
        WIDTH - PAD_HALF - 12000,
      );
    s.spawn_in--;
    if (s.spawn_in <= 0 && s.tick < LAST_TICK - 2 * HZ) {
      spawn();
      s.spawn_in = div(518400 - 41 * s.tick + 8999, 9000);
    }
    const remaining: Item[] = [];
    for (const item of s.items) {
      if (s.cause) {
        remaining.push(item);
        continue;
      }
      const oldY = item.y;
      item.y += s.effects.slow > s.tick ? div(item.speed * 58, 100) : item.speed;
      if (
        s.effects.magnet > s.tick &&
        item.kind === 'phrase' &&
        item.y > CATCH_Y - 145000 &&
        Math.abs(item.x - s.x) < 145000
      )
        item.x += div((s.x - item.x) * 8, 150);
      if (
        !item.checked &&
        oldY + item.height / 2 < CATCH_Y &&
        item.y + item.height / 2 >= CATCH_Y
      ) {
        item.checked = true;
        if (Math.abs(item.x - s.x) < item.width / 2 + PAD_HALF) {
          const score = s.score,
            hp = s.hp,
            hits = s.hits,
            charge = s.charge;
          collect(item);
          onCollect?.({
            kind: item.kind,
            x: item.x,
            lostCombo: 0,
            payload: item.payload,
            points: s.score - score,
            combo: s.combo,
            hpDelta: s.hp - hp,
            chargeReady: charge < 10 && s.charge === 10,
            blocked: item.kind === 'hazard' && s.hits === hits,
          });
          continue;
        }
        if (item.kind === 'phrase') {
          const lostCombo = s.combo;
          s.combo = 0;
          s.missed++;
          onCollect?.({
            kind: 'miss',
            payload: item.payload,
            x: item.x,
            lostCombo,
            points: 0,
            combo: 0,
            hpDelta: 0,
            chargeReady: false,
            blocked: false,
          });
        }
      }
      if (item.y - item.height / 2 < HEIGHT + 10000) remaining.push(item);
    }
    s.items = remaining;
    if (!s.cause && s.tick >= LAST_TICK) s.cause = 'time';
  }
  return s;
}
