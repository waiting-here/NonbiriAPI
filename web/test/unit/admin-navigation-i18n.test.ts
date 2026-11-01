import { describe, expect, test } from 'vitest';
import adminEn from '../../src/admin/i18n/en.json';
import adminZh from '../../src/admin/i18n/zh.json';
import {
  ADMIN_NAV_GROUPS,
  ADMIN_PRIMARY_NAV,
  UNPLACED_ADMIN_NAV_ROUTES,
} from '../../src/admin/navigation';

describe('admin navigation group catalog', () => {
  test('defines one localized label for every shell group', () => {
    const english = ADMIN_NAV_GROUPS.map((group) => adminEn.admin.navigation[group]);
    const chinese = ADMIN_NAV_GROUPS.map((group) => adminZh.admin.navigation[group]);

    expect(english).toEqual([
      'Overview',
      'Charity & services',
      'Users',
      'Logs & audits',
      'Activities & content',
      'System',
    ]);
    expect(chinese).toEqual(['概览', '公益与服务', '用户', '日志与审计', '活动与内容', '系统']);
    expect(new Set(english).size).toBe(ADMIN_NAV_GROUPS.length);
    expect(new Set(chinese).size).toBe(ADMIN_NAV_GROUPS.length);
  });

  test('places every navigation item in a known group of at most six items', () => {
    expect(UNPLACED_ADMIN_NAV_ROUTES).toEqual([]);
    expect(new Set(ADMIN_PRIMARY_NAV.map((item) => item.key)).size).toBe(ADMIN_PRIMARY_NAV.length);
    for (const item of ADMIN_PRIMARY_NAV) expect(ADMIN_NAV_GROUPS).toContain(item.group);
    for (const group of ADMIN_NAV_GROUPS)
      expect(ADMIN_PRIMARY_NAV.filter((item) => item.group === group).length).toBeLessThanOrEqual(
        6,
      );
  });
});
