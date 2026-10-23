import { describe, expect, it } from 'vitest';
import {
  normalizeModelTypes,
  groupedModelTypes,
  setModelTypeGroup,
  sameModelTypes,
} from './modelTypes';

describe('model API capabilities', () => {
  it('accepts an explicit set without rewriting legacy or image capabilities', () => {
    for (const types of [
      ['chat_completions', 'embeddings'],
      ['images_generations'],
      ['decisions'],
      ['responses'],
      ['systemone', 'images_edits'],
      ['chat_completions', 'embeddings', 'images_generations', 'decisions'],
    ]) {
      expect(normalizeModelTypes(types)).toEqual(types);
    }
  });

  it.each(
    [undefined, [], ['unsupported'], ['embeddings', 'embeddings']].map((value) => ({ value })),
  )('rejects an invalid server capability set $value', ({ value }) => {
    expect(() => normalizeModelTypes(value)).toThrow(/invalid model type/);
  });
});

it('groups legacy Chat or Responses values and writes the two compatible operations together', () => {
  expect(groupedModelTypes(['chat_completions', 'responses', 'images_edits'])).toEqual([
    'chat_completions',
    'images_edits',
  ]);
  expect(groupedModelTypes(['responses'])).toEqual(['chat_completions']);
  expect(sameModelTypes(['responses'], ['chat_completions', 'responses'])).toBe(true);
  expect(setModelTypeGroup(['responses'], 'images_edits', true)).toEqual([
    'chat_completions',
    'responses',
    'images_edits',
  ]);
  expect(
    setModelTypeGroup(['chat_completions', 'responses', 'images_edits'], 'chat_completions', false),
  ).toEqual(['images_edits']);
});
