import { describe, expect, it } from 'vitest';
import { normalizeModelTypes } from './modelTypes';

describe('model API capabilities', () => {
  it('accepts an explicit set without rewriting legacy or image capabilities', () => {
    for (const types of [['chat_completions', 'embeddings'], ['images_generations']]) {
      expect(normalizeModelTypes(types)).toEqual(types);
    }
  });

  it.each([undefined, [], ['responses'], ['embeddings', 'embeddings']].map((value) => ({ value })))(
    'rejects an invalid server capability set $value',
    ({ value }) => {
      expect(() => normalizeModelTypes(value)).toThrow(/invalid model type/);
    },
  );
});
