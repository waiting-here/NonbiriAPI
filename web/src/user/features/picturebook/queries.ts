import { useInfiniteQuery, useQuery, useQueryClient } from '@tanstack/react-query';
import { getModels, getQueue, getTask, getTasks } from '@shared/picturebook/publicApi';
import { activeTask } from '@shared/picturebook/publicTypes';
import { economySessionRequest } from '../economy/queries';
import { limitedActivityKeys } from '../limitedactivities/queries';

export const pictureBookKeys = {
  root: (account: string) => ['user', 'picture-book', account] as const,
  models: (account: string) => ['user', 'picture-book', account, 'models'] as const,
  queue: (account: string) => ['user', 'picture-book', account, 'queue'] as const,
  tasks: (account: string) => ['user', 'picture-book', account, 'tasks'] as const,
  task: (account: string, id: string) => ['user', 'picture-book', account, 'task', id] as const,
};
export function useImageModels(account: string) {
  const client = useQueryClient();
  return useInfiniteQuery({
    queryKey: pictureBookKeys.models(account),
    initialPageParam: undefined as string | undefined,
    queryFn: ({ pageParam, signal }) =>
      economySessionRequest(client, () => getModels(pageParam, signal), account),
    getNextPageParam: (page) => page.next_cursor ?? undefined,
    maxPages: 3,
    staleTime: 30000,
  });
}
export function useImageQueue(account: string) {
  const client = useQueryClient();
  return useQuery({
    queryKey: pictureBookKeys.queue(account),
    queryFn: ({ signal }) => economySessionRequest(client, () => getQueue(signal), account),
    refetchInterval: 5000,
    retry: false,
  });
}
export function useImageTasks(account: string, cursor?: string) {
  const client = useQueryClient();
  return useQuery({
    queryKey: [...pictureBookKeys.tasks(account), cursor ?? ''],
    queryFn: ({ signal }) => economySessionRequest(client, () => getTasks(cursor, signal), account),
    refetchInterval: 10000,
    retry: false,
  });
}
export function useImageTask(account: string, id: string) {
  const client = useQueryClient();
  return useQuery({
    queryKey: pictureBookKeys.task(account, id),
    queryFn: ({ signal }) => economySessionRequest(client, () => getTask(id, signal), account),
    enabled: !!id,
    retry: false,
    refetchInterval: (query) =>
      query.state.data && (activeTask(query.state.data) || query.state.data.result_available)
        ? 5000
        : false,
  });
}
export function useImageReconcile(account: string) {
  const client = useQueryClient();
  return async () => {
    await Promise.all([
      client.invalidateQueries({ queryKey: pictureBookKeys.root(account) }),
      client.invalidateQueries({ queryKey: limitedActivityKeys.root(account) }),
    ]);
  };
}
