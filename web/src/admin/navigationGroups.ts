import { useState } from 'react';
import { matchPath } from 'react-router';
import { ADMIN_NAV_GROUPS, ADMIN_PRIMARY_NAV } from './navigation';

export type AdminNavGroup = (typeof ADMIN_NAV_GROUPS)[number];

const STORAGE_KEY = 'nb.admin.nav.collapsed';

function readCollapsed(): AdminNavGroup[] {
  try {
    const saved: unknown = JSON.parse(localStorage.getItem(STORAGE_KEY) ?? '[]');
    return Array.isArray(saved) ? ADMIN_NAV_GROUPS.filter((group) => saved.includes(group)) : [];
  } catch {
    return [];
  }
}

/** The group holding the current route, so it can stay open while the user is inside it. */
export function activeAdminNavGroup(pathname: string): AdminNavGroup | undefined {
  const item = ADMIN_PRIMARY_NAV.find((entry) =>
    matchPath({ path: entry.to, end: entry.end ?? false }, pathname),
  );
  return ADMIN_NAV_GROUPS.find((group) => group === item?.group);
}

export function useCollapsedAdminNavGroups() {
  const [collapsed, setCollapsed] = useState(readCollapsed);
  const toggle = (group: AdminNavGroup) => {
    setCollapsed((current) => {
      const next = current.includes(group)
        ? current.filter((entry) => entry !== group)
        : [...current, group];
      try {
        localStorage.setItem(STORAGE_KEY, JSON.stringify(next));
      } catch {
        // The menu still works for this visit when storage is unavailable.
      }
      return next;
    });
  };
  return { collapsed, toggle };
}
