import { readFileSync } from 'node:fs';
import { gunzipSync } from 'node:zlib';
import { expect, it } from 'vitest';
import {
  advance,
  newGame,
  type Input,
  type Phrase,
  type State,
  type CollectionEvent,
} from './engine';

const phrases = JSON.parse(
  readFileSync('../internal/game/steadycatch/engine/phrases.json', 'utf8'),
) as Phrase[];
const scenarios = JSON.parse(
  gunzipSync(
    readFileSync('../internal/game/steadycatch/engine/testdata/browser-parity.json.gz'),
  ).toString(),
) as { seed: number; inputs: Input[]; states: State[] }[];
it('matches server checkpoints under idle, keyboard and pointer controls', () => {
  for (const scenario of scenarios) {
    let state = newGame(scenario.seed);
    for (const expected of scenario.states) {
      const inputs = scenario.inputs.filter(
        (input) => input.tick > state.tick && input.tick <= expected.tick,
      );
      state = advance(state, inputs, expected.tick, phrases);
      expect(state, `seed ${scenario.seed}, tick ${expected.tick}`).toEqual(expected);
    }
  }
});
it('is independent of rendering frequency and does not mutate acknowledged state', () => {
  for (const scenario of scenarios) {
    let state = newGame(scenario.seed);
    const old = structuredClone(state);
    for (const expected of scenario.states) {
      while (state.tick < expected.tick) {
        const next = Math.min(expected.tick, state.tick + 7);
        const previous = state,
          saved = structuredClone(state);
        state = advance(
          previous,
          scenario.inputs.filter((input) => input.tick > state.tick && input.tick <= next),
          next,
          phrases,
        );
        expect(previous).toEqual(saved);
      }
      expect(state).toEqual(expected);
    }
    expect(newGame(scenario.seed)).toEqual(old);
  }
});

it.each([
  { combo: 0, double: false, points: [10, 20] },
  { combo: 3, double: true, points: [20, 80] },
])(
  'reports exact same-tick card points at combo $combo, double $double',
  ({ combo, double, points }) => {
    const cards: Phrase[] = [
      { id: 'white', text: 'White', category: 'test', gold: false },
      { id: 'gold', text: 'Gold', category: 'test', gold: true },
    ];
    const before = newGame(1);
    before.combo = combo;
    before.spawn_in = 100;
    before.effects.double = double ? 100 : 0;
    before.items = cards.map((_, payload) => ({
      id: payload + 1,
      kind: 'phrase',
      payload,
      x: 300000,
      y: 414000,
      width: 152000,
      height: 62000,
      speed: 2000,
    }));
    const saved = structuredClone(before);
    const events: CollectionEvent[] = [];
    const inputs = [{ tick: 1, target: 300000, direction: 0 }];
    const actual = advance(before, inputs, 1, cards, (event) => events.push(event));
    expect(events.map((event) => event.points)).toEqual(points);
    expect(events.map((event) => event.combo)).toEqual([combo + 1, combo + 2]);
    expect(actual).toEqual(advance(before, inputs, 1, cards));
    expect(before).toEqual(saved);
  },
);
