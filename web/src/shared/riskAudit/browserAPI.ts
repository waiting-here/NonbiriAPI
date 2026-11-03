import { apiFetch } from '@shared/query/http';
import { queryPath } from '@shared/operations/api';
import {
  normalizePageMetadata,
  type PageMetadata,
  type PageSize,
} from '@shared/operations/pageNumbers';
import { array, decimalID, integer, record, string, unixSecond } from '@shared/operations/wire';
import type { RiskRole } from './api';

export interface BrowserAssociation {
  user_id: string;
  first_seen: number;
  last_seen: number;
}

export interface SharedBrowser {
  browser_id: string;
  users: number;
  first_seen: number;
  last_seen: number;
}

export interface SharedBrowserPage extends PageMetadata {
  items: SharedBrowser[];
}

export interface BrowserAccountsPage extends PageMetadata {
  associations: BrowserAssociation[];
}

function browserAccounts(value: unknown): BrowserAssociation[] {
  return array(value, 'browser accounts').map((value) => {
    const item = record(value, ['user_id', 'first_seen', 'last_seen'], 'browser account');
    return {
      user_id: decimalID(item.user_id, 'user ID'),
      first_seen: unixSecond(item.first_seen, 'first observation'),
      last_seen: unixSecond(item.last_seen, 'latest observation'),
    };
  });
}

function sharedBrowser(value: unknown): SharedBrowser {
  const row = record(value, ['browser_id', 'users', 'first_seen', 'last_seen'], 'shared browser');
  return {
    browser_id: string(row.browser_id, 'browser group'),
    users: integer(row.users, 'account count'),
    first_seen: unixSecond(row.first_seen, 'first observation'),
    last_seen: unixSecond(row.last_seen, 'latest observation'),
  };
}

export async function getBrowserAccounts(
  role: RiskRole,
  browserID: string,
  page: string,
  pageSize: PageSize,
  signal?: AbortSignal,
): Promise<BrowserAccountsPage> {
  const base = role === 'admin' ? '/admin/api' : '/api/steward';
  const value = await apiFetch<unknown>(
    queryPath(base + '/abuse-audit/shared-browsers/' + encodeURIComponent(browserID), {
      page,
      page_size: pageSize,
    }),
    { signal },
  );
  const root = record(value, ['associations'], 'browser accounts');
  return { ...normalizePageMetadata(value), associations: browserAccounts(root.associations) };
}

export async function getSharedBrowsers(
  role: RiskRole,
  page: string,
  pageSize: PageSize,
  signal?: AbortSignal,
): Promise<SharedBrowserPage> {
  const base = role === 'admin' ? '/admin/api' : '/api/steward';
  const value = await apiFetch<unknown>(
    queryPath(base + '/abuse-audit/shared-browsers', { page, page_size: pageSize }),
    { signal },
  );
  const root = record(value, ['items'], 'browser audit');
  return {
    ...normalizePageMetadata(value),
    items: array(root.items, 'browser groups').map(sharedBrowser),
  };
}
