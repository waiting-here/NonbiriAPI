import { expect, it } from 'vitest';
import { cards } from './catalog';
import { PRELUDE, SUMMARY, VOICE, VOICE_AGAIN, summaryLine, voiceLines } from './voice';
import type { DrawReceipt } from './types';

it('covers every card with bounded, nonempty bilingual paragraphs', () => {
  expect(Object.keys(VOICE).sort()).toEqual(cards.map((card) => card.id).sort());
  for (const [id, voice] of [
    ...Object.entries(VOICE),
    ...Object.entries(VOICE_AGAIN),
    ...Object.entries(PRELUDE),
  ]) {
    expect(voice.length % 2, id).toBe(0);
    voice.forEach((line, i) => {
      expect(line?.trim(), id).toBeTruthy();
      expect(Array.from(line!).length, id + ' paragraph ' + i).toBeLessThanOrEqual(
        i % 2 ? 110 : 40,
      );
    });
  }
  expect(voiceLines('l-chatgpt', false)).toHaveLength(2);
  expect(voiceLines('l-chatgpt', true, true)).toHaveLength(1);
});
it.each([
  [['l-chatgpt', 'g-chatgpt'], true, 'sp'],
  [['g-chatgpt', 'e-chatgpt'], true, 'ssr'],
  [['e-chatgpt'], true, 'allDup'],
  [['e-chatgpt'], false, 'sr'],
  [['a-chatgpt'], false, 'single'],
  [['a-chatgpt', 'c-chatgpt'], false, 'any'],
] as const)('selects the expected summary for %s', (ids, duplicate, key) => {
  const draw: DrawReceipt = {
    id: 'x',
    pool_id: 'p',
    pool_name: 'p',
    created_at: 0,
    items: ids.map((card_id) => ({ card_id, duplicate, guarantee: 'none' })),
  };
  expect(summaryLine(draw, true)).toBe(SUMMARY.find((value) => value[0] === key)![2]);
});
