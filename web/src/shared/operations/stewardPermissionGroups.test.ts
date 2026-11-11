import { describe, expect, it } from 'vitest';
import { defaultStewardPermissions, stewardPermissionKeys } from './stewardPermissions';
import {
  applyPermissionPair,
  permissionGroups,
  permissionPairMode,
} from './stewardPermissionGroups';

describe('permission presentation groups', () => {
  it('shows every supported editable grant once and keeps the reserved memory grant hidden', () => {
    const keys = permissionGroups.flatMap((group) => group.keys);
    expect([...keys].sort()).toEqual(
      stewardPermissionKeys.filter((key) => key !== 'ai.memory.edit').sort(),
    );
    expect(new Set(keys).size).toBe(keys.length);
    for (const group of permissionGroups)
      for (const pair of group.pairs ?? []) {
        expect(group.keys).toContain(pair.read);
        expect(group.keys).toContain(pair.manage);
      }
  });

  it('preserves custom combinations and changes only the explicitly selected pair', () => {
    for (const pair of permissionGroups.flatMap((group) => group.pairs ?? [])) {
      const original = {
        ...defaultStewardPermissions(5).permissions,
        [pair.read]: false,
        [pair.manage]: true,
      };
      expect(permissionPairMode(original, pair)).toBe('custom');
      expect(applyPermissionPair(original, pair, 'custom')).toBe(original);
      for (const mode of ['off', 'read', 'manage'] as const) {
        const next = applyPermissionPair(original, pair, mode);
        expect(permissionPairMode(next, pair)).toBe(mode);
        for (const key of stewardPermissionKeys.filter(
          (key) => key !== pair.read && key !== pair.manage,
        ))
          expect(next[key]).toBe(original[key]);
      }
      expect(original[pair.read]).toBe(false);
      expect(original[pair.manage]).toBe(true);
    }
  });
});
