import { describe, expect, test } from 'vitest';
import adminEn from '../../src/admin/i18n/en.json';
import adminZh from '../../src/admin/i18n/zh.json';
import { ADMIN_PRIMARY_NAV as adminNav } from '../../src/admin/navigation';
import { router as adminRouter } from '../../src/admin/routes';
import { USER_PRIMARY_NAV as userNav } from '../../src/user/navigation';
import { router as userRouter } from '../../src/user/routes';

function valueAt(source: unknown, path: string): unknown {
  let current = source;
  for (const segment of path.split('.')) {
    if (!current || typeof current !== 'object' || !(segment in current)) return undefined;
    current = (current as Record<string, unknown>)[segment];
  }
  return current;
}

function childPaths(router: typeof userRouter): string[] {
  return (router.routes[0]?.children ?? []).map(
    (route) => route.path ?? (route.index ? '(index)' : ''),
  );
}

describe('alpha.3 central route and navigation wiring', () => {
  test('exposes lazy user and admin feature routes in the intended order', () => {
    const userChildren = userRouter.routes[0]?.children ?? [];
    const adminChildren = adminRouter.routes[0]?.children ?? [];
    const userPaths = childPaths(userRouter);
    const adminPaths = adminChildren.map((route) => route.path ?? (route.index ? '(index)' : ''));

    expect(userPaths.indexOf('games')).toBe(userPaths.indexOf('models') + 1);
    expect(userPaths.indexOf('debug')).toBe(userPaths.indexOf('keys') + 1);
    expect(userChildren.find((route) => route.path === 'games')?.lazy).toBeTypeOf('function');
    expect(userChildren.find((route) => route.path === 'activities')?.lazy).toBeTypeOf('function');
    expect(
      userChildren.find((route) => route.path === 'charity/donations/:donationId')?.lazy,
    ).toBeTypeOf('function');
    expect(userChildren.find((route) => route.path === 'debug')?.lazy).toBeTypeOf('function');
    expect(adminPaths.indexOf('mainstream-channels')).toBe(adminPaths.indexOf('settings') + 1);
    expect(adminPaths.indexOf('games')).toBe(adminPaths.indexOf('mainstream-channels') + 1);
    expect(adminChildren.find((route) => route.path === 'games')?.lazy).toBeTypeOf('function');
    expect(adminChildren.find((route) => route.path === 'mainstream-channels')?.lazy).toBeTypeOf(
      'function',
    );

    expect(userNav.map(({ to, key }) => `${to}:${key}`)).toEqual([
      '/:home',
      '/keys:caller-key',
      '/endpoints:endpoints',
      '/models:models',
      '/charity:charity',
      '/activities:activities',
      '/games:games',
      '/logs:logs',
    ]);
    expect(adminNav.map(({ to, key }) => `${to}:${key}`)).toEqual([
      '/:admin-home',
      '/users:admin-users',
      '/logs:admin-logs',
      '/endpoints:admin-endpoints',
      '/alerts:admin-alerts',
      '/settings:admin-settings',
      '/mainstream-channels:admin-mainstream-channels',
      '/charity:admin-charity',
      '/activities:admin-activities',
      '/games:admin-games',
      '/reports:admin-reports',
      '/announcements:admin-announcements',
    ]);
  });
});

describe('central catalog completeness', () => {
  test('provides the admin Games navigation label in both stations', () => {
    expect(valueAt(adminEn, 'admin.games.nav')).toBe('Games');
    expect(valueAt(adminZh, 'admin.games.nav')).toBe('游戏');
  });
});
