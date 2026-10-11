import { useQuery } from '@tanstack/react-query';
import { apiFetch } from '@shared/query/http';
import { useUserSession } from '../../data';

export interface GachaPoolEntry {
  id: string;
  name: string;
  name_en?: string;
  cover: string;
  featured: string[];
  starts_at: number;
  ends_at: number;
}
export interface GachaEntryState {
  enabled: boolean;
  entered: boolean;
  pools: GachaPoolEntry[];
}
export function useGachaEntry() {
  const session = useUserSession();
  return useQuery({
    queryKey: ['user', 'gacha-entry', session.data?.user.id],
    queryFn: ({ signal }) => apiFetch<GachaEntryState>('/api/gacha/entry', { signal }),
    enabled: !!session.data?.user.id && !session.error,
    refetchInterval: 30_000,
  });
}
