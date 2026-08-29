import { describe, expect, test } from 'vitest';
import adminEn from '../../src/admin/i18n/en.json';
import adminZh from '../../src/admin/i18n/zh.json';

const groups = ['overview', 'users', 'operations', 'content'] as const;

describe('admin navigation group catalog', () => {
  test('defines one localized label for every shell group', () => {
    const english = groups.map((group) => adminEn.admin.navigation[group]);
    const chinese = groups.map((group) => adminZh.admin.navigation[group]);

    expect(english).toEqual(['Overview', 'Users & permissions', 'Operations', 'Content & economy']);
    expect(chinese).toEqual(['概览', '用户与权限', '运营', '内容与经济']);
    expect(new Set(english).size).toBe(groups.length);
    expect(new Set(chinese).size).toBe(groups.length);
    expect(english.every((label) => label !== 'Site settings')).toBe(true);
    expect(chinese.every((label) => label !== '站点参数')).toBe(true);
  });
});
