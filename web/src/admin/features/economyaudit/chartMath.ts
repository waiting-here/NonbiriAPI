const PLOT_SCALE = 1_000_000n;

export function chartRatio(value: string, maximum: bigint): number {
  if (maximum <= 0n) return 0;
  return Number((BigInt(value) * PLOT_SCALE) / maximum);
}

export function chartAmountAtRatio(value: number, maximum: bigint): string {
  if (!Number.isFinite(value) || maximum <= 0n) return '0';
  const scaled = BigInt(Math.max(0, Math.round(value)));
  return ((maximum * scaled) / PLOT_SCALE).toString();
}
