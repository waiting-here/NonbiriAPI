export function fishingChanceFromPercent(value: string): number {
  const match = /^(0|[1-9][0-9]*)(?:\.([0-9]{1,2}))?$/.exec(value.trim());
  if (!match) return Number.NaN;
  const bps = Number(match[1]) * 100 + Number((match[2] ?? '').padEnd(2, '0'));
  return Number.isSafeInteger(bps) && bps <= 10_000 ? bps : Number.NaN;
}

export function fishingChanceValid(value: number): boolean {
  return Number.isSafeInteger(value) && value >= 0 && value <= 10_000;
}
