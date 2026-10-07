import { readFileSync } from 'node:fs';
import { gunzipSync } from 'node:zlib';
import { expect, it } from 'vitest';
import { advance, newGame, type Input, type Phrase, type State } from './engine';

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
