import type { DrawReceipt, Preferences } from '../user/activities/gacha/types';

export type Scenario = 'ten' | 'plain' | 'ssr' | 'sp2' | 'n' | 'seasonal';
export function sampleReceipt(scenario: Scenario): DrawReceipt {
  const sets: Record<Scenario, string[]> = {
    seasonal: ['s-chatgpt', 's-claude'],
    ten: [
      'a-chatgpt',
      'b-glm',
      'c-claude',
      'g-chatgpt',
      'a-deepseek',
      'e-gemini',
      'l-chatgpt',
      'd-glm',
      'm-deepseek',
      'b-gemini',
    ],
    plain: [
      'a-chatgpt',
      'b-glm',
      'c-claude',
      'd-chatgpt',
      'a-deepseek',
      'e-gemini',
      'c-chatgpt',
      'd-glm',
      'b-deepseek',
      'b-gemini',
    ],
    ssr: ['i-chatgpt'],
    sp2: ['l-chatgpt'],
    n: ['a-chatgpt'],
  };
  return {
    id: 'preview-' + crypto.randomUUID(),
    pool_id: 'preview',
    pool_name: '演出样片',
    created_at: Math.floor(Date.now() / 1000),
    items: sets[scenario].map((card_id, i) => ({
      card_id,
      duplicate: scenario === 'sp2' || i % 3 === 1,
      guarantee: 'none',
    })),
  };
}
export function previewPreferences(): Preferences {
  let saved: Partial<Preferences> = {};
  try {
    saved = JSON.parse(localStorage.getItem('nb.gacha.preferences') ?? '{}');
  } catch {
    /* Use defaults. */
  }
  const query = new URLSearchParams(location.search);
  return {
    music: false,
    sound: true,
    vibration: true,
    cardBackTell: true,
    reduced: matchMedia('(prefers-reduced-motion: reduce)').matches,
    ...saved,
    ...(query.has('tell') ? { cardBackTell: query.get('tell') !== '0' } : {}),
    ...(query.has('reduced') ? { reduced: query.get('reduced') === '1' } : {}),
  };
}
