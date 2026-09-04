import { describe, expect, it } from 'vitest';
import { fishingItemName } from './text';

describe('fishing localized item names', () => {
  it('covers canonical catches in both supported languages', () => {
    expect(fishingItemName('whitebait', 'en')).toBe('Whitebait');
    expect(fishingItemName('whitebait', 'zh')).toBe('银鱼');
    expect(fishingItemName('old_tire', 'zh')).toBe('旧轮胎');
  });

  it('uses a localized closed fallback for unknown server keys', () => {
    expect(fishingItemName('future_item', 'en')).toBe('Unknown catch');
    expect(fishingItemName('future_item', 'zh')).toBe('未知收获');
  });
});
