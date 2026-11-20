import { useQuery } from '@tanstack/react-query';
import { gameRequest } from './request';

export function useWhaleHavenOpen(enabled: boolean): boolean {
  const availability = useQuery({
    queryKey: ['user', 'games', 'whale-haven', 'availability'],
    queryFn: async ({ signal }) => {
      const { data } = await gameRequest<{ enabled: boolean }>(
        '/api/games/whale-haven/availability',
        { signal },
      );
      return data?.enabled === true;
    },
    enabled,
    staleTime: 0,
    refetchOnWindowFocus: true,
    retry: false,
  });
  return enabled && availability.isFetchedAfterMount && availability.isSuccess && availability.data;
}
