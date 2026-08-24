import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ApiError, apiFetch } from '@shared/query/http';
import { asRecord } from '@shared/query/normalize';

export interface AdminGameConfig {
  master_enabled: boolean;
  fishing: {
    enabled: boolean;
    bait_prices: { worm: string; lure: string; premium: string };
    rtp_percent: { standard: number; premium: number };
    treasure_multipliers: { bottle: number; clover: number; shell: number };
  };
}

export interface AdminGameConfigPatch {
  master_enabled?: boolean;
  fishing?: {
    enabled?: boolean;
    bait_prices?: Partial<AdminGameConfig['fishing']['bait_prices']>;
    rtp_percent?: Partial<AdminGameConfig['fishing']['rtp_percent']>;
    treasure_multipliers?: Partial<AdminGameConfig['fishing']['treasure_multipliers']>;
  };
}

export const adminGameConfigKey = ['admin', 'games', 'config'] as const;

function invalid(field: string): never {
  throw new ApiError('invalid_response', `The server returned an invalid game ${field}.`, 200);
}

function requiredRecord(value: unknown, field: string): Record<string, unknown> {
  return asRecord(value) ?? invalid(field);
}

function requiredBoolean(value: unknown, field: string): boolean {
  if (typeof value !== 'boolean') return invalid(field);
  return value;
}

function requiredInteger(value: unknown, minimum: number, maximum: number, field: string): number {
  if (!Number.isSafeInteger(value) || (value as number) < minimum || (value as number) > maximum) {
    return invalid(field);
  }
  return value as number;
}

function positiveAmount(value: unknown, field: string): string {
  if (typeof value !== 'string' || !/^[1-9]\d*$/.test(value) || value.length > 19) return invalid(field);
  try {
    if (BigInt(value) > 9_223_372_036_854_775_807n) return invalid(field);
  } catch {
    return invalid(field);
  }
  return value;
}

export function normalizeAdminGameConfig(value: unknown): AdminGameConfig {
  const root = requiredRecord(value, 'configuration');
  const fishing = requiredRecord(root.fishing, 'fishing configuration');
  const prices = requiredRecord(fishing.bait_prices, 'bait prices');
  const rtp = requiredRecord(fishing.rtp_percent, 'RTP values');
  const multipliers = requiredRecord(fishing.treasure_multipliers, 'treasure multipliers');
  return {
    master_enabled: requiredBoolean(root.master_enabled, 'master switch'),
    fishing: {
      enabled: requiredBoolean(fishing.enabled, 'fishing switch'),
      bait_prices: {
        worm: positiveAmount(prices.worm, 'worm price'),
        lure: positiveAmount(prices.lure, 'lure price'),
        premium: positiveAmount(prices.premium, 'premium price'),
      },
      rtp_percent: {
        standard: requiredInteger(rtp.standard, 0, 100, 'standard RTP'),
        premium: requiredInteger(rtp.premium, 0, 100, 'premium RTP'),
      },
      treasure_multipliers: {
        bottle: requiredInteger(multipliers.bottle, 1, 1_000, 'bottle multiplier'),
        clover: requiredInteger(multipliers.clover, 1, 1_000, 'clover multiplier'),
        shell: requiredInteger(multipliers.shell, 1, 1_000, 'shell multiplier'),
      },
    },
  };
}

export function useAdminGameConfig(enabled = true) {
  return useQuery({
    queryKey: adminGameConfigKey,
    queryFn: async () => normalizeAdminGameConfig(
      await apiFetch<unknown>('/admin/api/games/config'),
    ),
    enabled,
    staleTime: 0,
  });
}

export function usePatchAdminGameConfig() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (patch: AdminGameConfigPatch) => normalizeAdminGameConfig(
      await apiFetch<unknown>('/admin/api/games/config', { method: 'PATCH', json: patch }),
    ),
    onSuccess: (snapshot) => {
      queryClient.setQueryData(adminGameConfigKey, snapshot);
    },
  });
}
