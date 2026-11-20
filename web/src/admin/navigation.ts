import {
  ADMIN_ROUTE_DESCRIPTORS,
  requiredRouteById,
  type RouteDescriptor,
} from '@shared/routing/routeRegistry';
import type { IconName } from '@shared/components/Icon';
import type { CopyKey } from '@shared/i18n/useRegisteredCopy';

export interface NavItem {
  to: string;
  key: string;
  end?: boolean;
  icon?: IconName;
  labelKey?: string;
  fallbackLabelKey?: string;
  group: AdminNavGroupKey;
}

/** Sidebar groups, top to bottom. */
export const ADMIN_NAV_GROUPS = [
  'overview',
  'services',
  'users',
  'records',
  'activities',
  'system',
] as const;

export type AdminNavGroupKey = (typeof ADMIN_NAV_GROUPS)[number];

/**
 * Entries per group, most frequently used first. Every navigable admin route must appear
 * exactly once; the registry still owns paths, labels and icons.
 */
const ADMIN_NAV_ORDER: Record<AdminNavGroupKey, readonly string[]> = {
  overview: ['admin-home'],
  services: [
    'admin-charity',
    'admin-mainstream-channels',
    'admin-endpoints',
    'admin-endpoint-policies',
  ],
  users: ['admin-users', 'admin-blacklist', 'admin-reports', 'admin-inactivity-policy'],
  records: [
    'admin-logs',
    'admin-management-operations',
    'admin-risk-audit',
    'admin-economy-audit',
    'admin-alerts',
  ],
  activities: [
    'admin-activities',
    'admin-limited-activities',
    'admin-games',
    'admin-rewards',
    'admin-announcements',
  ],
  system: ['admin-settings', 'admin-agent'],
};

const toNavItem = (route: RouteDescriptor, group: AdminNavGroupKey): NavItem => ({
  to: route.path,
  key: route.id,
  end: route.end,
  icon: route.icon,
  labelKey: route.labelKey,
  fallbackLabelKey: route.fallbackLabelKey,
  group,
});

export const ADMIN_PRIMARY_NAV: NavItem[] = ADMIN_NAV_GROUPS.flatMap((group) =>
  ADMIN_NAV_ORDER[group].map((id) => toNavItem(requiredRouteById('admin', id), group)),
);

/** Routes marked for navigation that the sidebar order does not place (should stay empty). */
export const UNPLACED_ADMIN_NAV_ROUTES = ADMIN_ROUTE_DESCRIPTORS.filter(
  (route) =>
    'nav' in route && route.nav && !ADMIN_PRIMARY_NAV.some((item) => item.key === route.id),
).map((route) => route.id);

interface SearchPage {
  labelKey: CopyKey;
  routeId?: string;
  search?: string;
  keywordKeys?: readonly CopyKey[];
}

/** Search indexes common workspaces with stable URLs, plus terms for their parent pages. */
const SEARCH_PAGES: Partial<
  Record<string, { keywordKeys?: readonly CopyKey[]; subpages?: readonly SearchPage[] }>
> = {
  'admin-charity': {
    subpages: [
      {
        labelKey: 'admin.charity.donationsTitle',
        search: 'charity_section=donations',
      },
      {
        labelKey: 'admin.charity.modelsTitle',
        search: 'charity_section=models',
        keywordKeys: ['admin.navigation.keywords.modelBindings'],
      },
      {
        labelKey: 'common.operations.charity.sourceGroups',
        search: 'charity_section=sources',
      },
      {
        labelKey: 'common.workflow.templates',
        search: 'charity_section=templates',
        keywordKeys: ['admin.navigation.keywords.priceTemplates'],
      },
      {
        labelKey: 'common.bindingPresets.title',
        routeId: 'admin-channel-binding-presets',
      },
    ],
  },
  'admin-logs': {
    keywordKeys: ['admin.logs.logsTitle', 'logs.workspaceRequests'],
    subpages: [
      {
        labelKey: 'logs.workspaceActivity',
        search: 'log_view=activity',
        keywordKeys: ['admin.navigation.keywords.activityLogs'],
      },
    ],
  },
  'admin-rewards': {
    keywordKeys: ['common.rewards.broadcast', 'admin.navigation.keywords.rewards'],
  },
  'admin-activities': {
    subpages: [
      {
        labelKey: 'admin.activities.gacha',
        routeId: 'admin-gacha',
      },
      {
        labelKey: 'admin.navigation.gachaTemplates',
        routeId: 'admin-gacha',
        search: 'gacha_view=presets',
        keywordKeys: ['admin.navigation.keywords.gachaTemplates'],
      },
    ],
  },
  'admin-limited-activities': {
    subpages: [
      {
        labelKey: 'common.limitedActivities.common.pictureBook',
        search: 'activity=picture-book',
      },
      {
        labelKey: 'common.limitedActivities.common.fatFish',
        routeId: 'admin-fat-fish',
      },
    ],
  },
  'admin-games': {
    subpages: [
      { labelKey: 'admin.navigation.whaleHaven', routeId: 'admin-whale-haven' },
      { labelKey: 'admin.games.aiPlayersTitle', routeId: 'admin-game-ai' },
      { labelKey: 'admin.lakeNotes.title', routeId: 'admin-lake-notes' },
      { labelKey: 'admin.navigation.blackjackHistory', routeId: 'admin-blackjack-history' },
    ],
  },
};

export interface SearchNavItem extends NavItem {
  parentLabelKey?: string;
  keywordKeys?: readonly CopyKey[];
}

export const ADMIN_SEARCH_NAV: SearchNavItem[] = ADMIN_PRIMARY_NAV.flatMap((parent) => {
  const page = SEARCH_PAGES[parent.key];
  const subpages = (page?.subpages ?? []).map<SearchNavItem>((subpage) => {
    const path = subpage.routeId ? requiredRouteById('admin', subpage.routeId).path : parent.to;
    const to = subpage.search ? `${path}?${subpage.search}` : path;
    return {
      key: to,
      to,
      end: true,
      group: parent.group,
      icon: parent.icon,
      labelKey: subpage.labelKey,
      parentLabelKey: parent.labelKey,
      keywordKeys: subpage.keywordKeys,
    };
  });
  return [{ ...parent, keywordKeys: page?.keywordKeys }, ...subpages];
});
