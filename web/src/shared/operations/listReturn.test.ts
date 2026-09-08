import { describe, expect, it } from 'vitest';
import { listReturnPath } from './listReturn';

describe('list return locations', () => {
  it('preserves the exact list filter and nested pagination context', () => {
    const returnTo = '/endpoints?page=3&page_size=50&key_page=2&q=long%20name';
    expect(listReturnPath({ returnTo }, '/endpoints')).toBe(returnTo);
  });
  it.each([
    null,
    [],
    { returnTo: '//outside.example/endpoints' },
    { returnTo: '/endpoints/123' },
    { returnTo: '/endpoints\n?x=1' },
    { returnTo: 'https://outside.example/endpoints' },
    { returnTo: '/other?page=3' },
  ])('falls back for a foreign or malformed route: %j', (state) => {
    expect(listReturnPath(state, '/endpoints')).toBe('/endpoints');
  });
});
