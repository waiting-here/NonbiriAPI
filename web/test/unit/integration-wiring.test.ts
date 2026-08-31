import { describe, expect, test } from 'vitest';
import adminEn from '../../src/admin/i18n/en.json';
import adminZh from '../../src/admin/i18n/zh.json';
import { ADMIN_PRIMARY_NAV as adminNav } from '../../src/admin/navigation';
import { router as adminRouter } from '../../src/admin/routes';
import userEn from '../../src/user/i18n/en.json';
import userZh from '../../src/user/i18n/zh.json';
import { USER_PRIMARY_NAV as userNav } from '../../src/user/navigation';
import { router as userRouter } from '../../src/user/routes';
import { itemNames } from '../../src/user/games/fishing/text';

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
    expect(adminPaths.indexOf('games')).toBe(adminPaths.indexOf('settings') + 1);
    expect(adminChildren.find((route) => route.path === 'games')?.lazy).toBeTypeOf('function');

    expect(userNav.map(({ to, key }) => `${to}:${key}`)).toEqual([
      '/:home',
      '/endpoints:endpoints',
      '/models:models',
      '/charity:charity',
      '/activities:activities',
      '/games:games',
      '/debug:debug',
    ]);
    expect(adminNav.map(({ to, key }) => `${to}:${key}`)).toEqual([
      '/:admin-home',
      '/users:admin-users',
      '/logs:admin-logs',
      '/endpoints:admin-endpoints',
      '/alerts:admin-alerts',
      '/settings:admin-settings',
      '/charity:admin-charity',
      '/games:admin-games',
    ]);
  });
});

describe('alpha.3 central catalog completeness', () => {
  const fishingPaths = [
    'eyebrow',
    'title',
    'description',
    'loading',
    'balance',
    'balanceHint',
    'balanceRetry',
    'balanceRetrying',
    'balanceUnknown',
    'balanceUnknownAlert',
    'balanceUnknownHint',
    'disabledTitle',
    'disabledBody',
    'baitsTitle',
    'baitsHint',
    'baitsLabel',
    'ticket',
    'creditsUnit',
    'cast',
    'castWorking',
  ];
  const fishingGroups = {
    baits: ['worm', 'lure', 'premium'],
    errors: ['unavailable', 'insufficient', 'pending', 'resultPending', 'disabled', 'rateLimited'],
    phase: ['idle', 'casting', 'waiting', 'reeling', 'settling', 'result', 'error', 'startError'],
    stage: ['title', 'pending', 'autoSettle', 'retry', 'retryWorking'],
    result: [
      'fishTitle',
      'treasureTitle',
      'junkTitle',
      'recovered',
      'serverConfirmed',
      'fishDescription',
      'treasureDescription',
      'junkDescription',
      'size',
      'meter',
      'won',
      'balance',
      'ack',
      'ackWorking',
      'more',
    ],
    profile: [
      'title',
      'hint',
      'public',
      'publicWarning',
      'privateWarning',
      'reconciling',
      'uncertain',
      'retry',
      'makePrivate',
    ],
    leaderboard: [
      'avatarFallback',
      'avatarAlt',
      'anonymous',
      'level4Hint',
      'level4',
      'totalScore',
      'me',
      'singleTitle',
      'totalTitle',
      'singleDescription',
      'totalDescription',
      'mine',
      'loading',
      'empty',
      'rank',
      'angler',
      'catch',
      'kind',
      'size',
      'payout',
      'you',
      'yourRank',
    ],
  } as const;

  test('provides bilingual Fishing leaves, item names, nav labels, and interpolation markers', () => {
    const sources = [userEn, userZh];
    for (const source of sources) {
      expect(valueAt(source, 'user.games.nav')).toEqual(expect.any(String));
      expect(valueAt(source, 'user.debug.nav')).toEqual(expect.any(String));
      expect(valueAt(source, 'games.fishing')).toEqual(expect.any(Object));
      for (const path of fishingPaths) {
        expect(valueAt(source, `games.fishing.${path}`), path).toEqual(expect.any(String));
      }
      for (const [group, keys] of Object.entries(fishingGroups)) {
        for (const key of keys) {
          expect(valueAt(source, `games.fishing.${group}.${key}`), `${group}.${key}`).toEqual(
            expect.any(String),
          );
        }
      }
      for (const key of Object.keys(itemNames)) {
        expect(valueAt(source, `games.fishing.items.${key}`), `items.${key}`).toEqual(
          expect.any(String),
        );
      }
    }

    expect(valueAt(userEn, 'games.fishing.result.fishDescription')).toContain('{{name}}');
    expect(valueAt(userEn, 'games.fishing.result.fishDescription')).toContain('{{tier}}');
    expect(valueAt(userZh, 'games.fishing.result.fishDescription')).toContain('{{name}}');
    expect(valueAt(userZh, 'games.fishing.result.fishDescription')).toContain('{{tier}}');
    expect(valueAt(userEn, 'games.fishing.leaderboard.avatarAlt')).toContain('{{name}}');
    expect(valueAt(userZh, 'games.fishing.leaderboard.avatarAlt')).toContain('{{name}}');
    expect(valueAt(userEn, 'games.fishing.leaderboard.yourRank')).toContain('{{rank}}');
    expect(valueAt(userZh, 'games.fishing.leaderboard.yourRank')).toContain('{{rank}}');
  });

  test('provides the admin Games navigation label in both stations', () => {
    expect(valueAt(adminEn, 'admin.games.nav')).toBe('Games');
    expect(valueAt(adminZh, 'admin.games.nav')).toBe('游戏');
  });
});
