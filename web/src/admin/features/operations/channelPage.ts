import { useQuery } from '@tanstack/react-query';
import { ApiError } from '@shared/query/http';
import { decoded, queryPath } from '@shared/operations/api';
import { array, invalidResponse, record } from '@shared/operations/wire';
import {
  isPageNumber,
  isPageSize,
  normalizePageMetadata,
  validatePageResponse,
  type PageMetadata,
  type PageSize,
} from '@shared/operations/pageNumbers';
import {
  adminMainstreamChannelKeys,
  normalizeAdminMainstreamChannel,
  type AdminMainstreamChannel,
  type MainstreamChannelListState,
} from './channels';

export interface AdminMainstreamChannelPage {
  data: AdminMainstreamChannel[];
  next_cursor: null;
  pagination: PageMetadata;
}

const adminMainstreamChannelPageRoot = [...adminMainstreamChannelKeys.root, 'page'] as const;

function requestState(value: unknown): MainstreamChannelListState {
  if (value !== 'active' && value !== 'retired' && value !== 'all') {
    throw new ApiError('invalid_request', 'Invalid mainstream channel list state.', 400);
  }
  return value;
}

export function normalizeAdminMainstreamChannelPage(
  value: unknown,
  state: MainstreamChannelListState,
  requestedPage: string,
  requestedSize: PageSize,
): AdminMainstreamChannelPage {
  const root = record(value, ['data', 'next_cursor', 'pagination'], 'mainstream channel page');
  if (root.next_cursor !== null) invalidResponse('mainstream channel page cursor');
  const pagination = normalizePageMetadata(root.pagination);
  const channels = array(root.data, 'mainstream channel page data', 100).map(
    normalizeAdminMainstreamChannel,
  );
  validatePageResponse(pagination, requestedPage, requestedSize, channels.length);

  const ids = new Set<string>();
  for (const channel of channels) {
    if (state !== 'all' && channel.state !== state) {
      invalidResponse('mainstream channel page state');
    }
    if (ids.has(channel.id)) invalidResponse('mainstream channel page identities');
    ids.add(channel.id);
  }

  return { data: channels, next_cursor: null, pagination };
}

export const adminMainstreamChannelPageKeys = {
  root: adminMainstreamChannelPageRoot,
  page: (accountID: string, state: MainstreamChannelListState, page: string, pageSize: PageSize) =>
    [...adminMainstreamChannelPageRoot, accountID, state, page, pageSize] as const,
};

export async function getAdminMainstreamChannelPage(
  state: MainstreamChannelListState,
  page: string,
  pageSize: PageSize,
  signal?: AbortSignal,
): Promise<AdminMainstreamChannelPage> {
  const requestedState = requestState(state);
  if (!isPageNumber(page)) {
    throw new ApiError('invalid_request', 'Invalid mainstream channel page.', 400);
  }
  if (!isPageSize(pageSize)) {
    throw new ApiError('invalid_request', 'Invalid mainstream channel page size.', 400);
  }
  return decoded(
    queryPath('/admin/api/mainstream-channels', {
      state: requestedState,
      page,
      page_size: pageSize,
    }),
    (value) => normalizeAdminMainstreamChannelPage(value, requestedState, page, pageSize),
    { signal },
  );
}

export function useAdminMainstreamChannelPage(
  accountID: string | undefined,
  state: MainstreamChannelListState,
  page: string,
  pageSize: PageSize,
  enabled = true,
) {
  const queryKey = adminMainstreamChannelPageKeys.page(accountID ?? 'none', state, page, pageSize);
  return useQuery({
    queryKey,
    queryFn: ({ signal }) => getAdminMainstreamChannelPage(state, page, pageSize, signal),
    enabled: enabled && Boolean(accountID) && isPageNumber(page) && isPageSize(pageSize),
    placeholderData: (previous, previousQuery) =>
      previousQuery?.queryKey[4] === accountID && previousQuery?.queryKey[5] === state
        ? previous
        : undefined,
    retry: false,
  });
}
