import { useQuery } from '@tanstack/react-query';
import { ApiError } from '@shared/query/http';
import { decoded, queryPath } from '@shared/operations/api';
import {
  isPageNumber,
  isPageSize,
  normalizePageMetadata,
  validatePageResponse,
  type PageMetadata,
  type PageSize,
} from '@shared/operations/pageNumbers';
import { invalidResponse, record } from '@shared/operations/wire';
import { normalizeIssuePage, type Issue, type IssuePage } from './data';

export type IssueState = Issue['state'];

export interface IssuePageResult {
  data: IssuePage['data'];
  next_cursor: null;
  projection_incomplete: boolean;
  pagination: PageMetadata;
}

export function normalizeIssuePageResponse(
  value: unknown,
  state: IssueState,
  requestedPage: string,
  requestedSize: PageSize,
): IssuePageResult {
  const root = record(
    value,
    ['data', 'next_cursor', 'projection_incomplete', 'pagination'],
    'issue page',
  );
  if (root.next_cursor !== null) invalidResponse('issue page cursor');

  const pagination = normalizePageMetadata(root.pagination);
  const domain = normalizeIssuePage({
    data: root.data,
    next_cursor: root.next_cursor,
    projection_incomplete: root.projection_incomplete,
  });
  validatePageResponse(pagination, requestedPage, requestedSize, domain.data.length);

  const ids = new Set<string>();
  for (const issue of domain.data) {
    if (issue.state !== state) invalidResponse('issue state');
    if (ids.has(issue.id)) invalidResponse('issue identities');
    ids.add(issue.id);
  }

  return {
    data: domain.data,
    next_cursor: null,
    projection_incomplete: domain.projection_incomplete,
    pagination,
  };
}

export const issuePageKeys = {
  root: ['user', 'operations', 'issues-page'] as const,
  page: (accountID: string, state: IssueState, page: string, pageSize: PageSize) =>
    ['user', 'operations', 'issues-page', accountID, state, page, pageSize] as const,
};

export async function getIssuePage(
  state: IssueState,
  page: string,
  pageSize: PageSize,
  signal?: AbortSignal,
): Promise<IssuePageResult> {
  if (state !== 'current' && state !== 'closed') {
    throw new ApiError('invalid_request', 'Invalid issue state.', 400);
  }
  if (!isPageNumber(page)) {
    throw new ApiError('invalid_request', 'Invalid issue page.', 400);
  }
  if (!isPageSize(pageSize)) {
    throw new ApiError('invalid_request', 'Invalid issue page size.', 400);
  }
  return decoded(
    queryPath('/api/issues', { state, page, page_size: pageSize }),
    (value) => normalizeIssuePageResponse(value, state, page, pageSize),
    { signal },
  );
}

export function useIssuePage(
  accountID: string | undefined,
  state: IssueState,
  page: string,
  pageSize: PageSize,
  enabled = true,
) {
  const queryKey = issuePageKeys.page(accountID ?? 'none', state, page, pageSize);
  return useQuery({
    queryKey,
    queryFn: ({ signal }) => getIssuePage(state, page, pageSize, signal),
    enabled: enabled && Boolean(accountID) && isPageNumber(page) && isPageSize(pageSize),
    placeholderData: (previous, previousQuery) =>
      previousQuery?.queryKey[3] === accountID ? previous : undefined,
    retry: false,
  });
}
