import { expect, it } from 'vitest';
import { responseFields } from './responseFields';
import { exactRecord, responseRecord } from '../../user/games/common/strict';

const invalid = () => {
  throw new Error('invalid response');
};

it('projects recognized display fields so extensions cannot enter an edit payload', () => {
  const response = JSON.parse(
    '{"id":"item","note":"visible","admin":true,"__proto__":{"elevated":true}}',
  );
  expect(responseFields(response, ['id'], ['id', 'note'], invalid)).toEqual({
    id: 'item',
    note: 'visible',
  });
  expect(responseFields({ id: 'item' }, ['id'], ['id', 'note'], invalid)).toEqual({ id: 'item' });
});

it.each([null, [], 'object', {}, Object.create({ id: 'inherited' })])(
  'still requires an object with its own required fields: %s',
  (value) => expect(() => responseFields(value, ['id'], ['id'], invalid)).toThrow(),
);

it('keeps exact protocols separate from extensible display responses', () => {
  const extended = { revision: '1', future: true };
  expect(responseRecord(extended, ['revision'])).toEqual({ revision: '1' });
  expect(() => exactRecord(extended, ['revision'])).toThrow();
});
