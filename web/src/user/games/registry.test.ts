import { describe, expect, it } from 'vitest';
import { gameRegistry, resolveGameRegistration, type GameRegistration } from './registry';

describe('user game registry', () => {
  it('resolves the registered fishing page by id and version', () => {
    expect(gameRegistry.map(({ id }) => id)).toEqual(['fishing', 'linklink', 'rps']);
    const registration = resolveGameRegistration(gameRegistry, 'fishing', 1);
    expect(registration).toBe(gameRegistry[0]);
    expect(registration?.titleKey).toBe('games.fishing.title');
  });

  it('fails closed when a registry key is duplicated', () => {
    const duplicate: readonly GameRegistration[] = [...gameRegistry, { ...gameRegistry[0] }];
    expect(() => resolveGameRegistration(duplicate, 'fishing', 1)).toThrow(
      /Duplicate game registration/,
    );
  });
});
