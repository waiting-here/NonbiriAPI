import { expect, it } from 'vitest';
import { momentHue } from './artwork-hue';
import { momentByKey, defaultMoment } from './moments';

it('matches a portrait and its group to their painted theme before decoding, with a neutral cover fallback', () => {
  expect(momentHue(momentByKey.get('card:o-claude')!)).toBe(
    momentHue(momentByKey.get('illustration:o')!),
  );
  expect(momentHue(momentByKey.get('card:l-chatgpt')!)).not.toBe(
    momentHue(momentByKey.get('illustration:o')!),
  );
  expect(momentHue(defaultMoment)).toBe(215);
});
