import { describe, expect, it } from 'vitest';
import { endpointReturnState, listReturnPath } from './listReturn';

describe('list return locations', () => {
  it('preserves the exact list filter and nested pagination context', () => {
    const returnTo = '/endpoints?page=3&page_size=50&key_page=2&q=long%20name';
    expect(listReturnPath({ returnTo }, '/endpoints')).toBe(returnTo);
  });
  it('allows model returns only by exact route and validates the original model parent', () => {
    const state = {
      returnTo: '/models?model_id=101&q=alpha',
      modelSourceEntry: 'history-key',
      modelSourceParent: '/issues?page=3',
    };
    const destination = listReturnPath(state, '/endpoints', '/issues', '/models');
    expect(destination).toBe(state.returnTo);
    expect(endpointReturnState(state, destination)).toEqual({
      returnTo: '/issues?page=3',
      modelSourceEntry: 'history-key',
    });
    expect(
      endpointReturnState({ ...state, modelSourceParent: '//other.example' }, destination),
    ).toMatchObject({ returnTo: '/models' });
    for (const returnTo of [
      '/models/101',
      '/models.example?q=1',
      '//other.example/models',
      '/models\n?x=1',
    ])
      expect(listReturnPath({ returnTo }, '/endpoints', '/issues', '/models')).toBe('/endpoints');
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
