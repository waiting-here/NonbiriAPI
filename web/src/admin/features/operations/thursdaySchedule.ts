const BEIJING_OFFSET_MS = 8 * 60 * 60 * 1_000;

export function nextThursdaySchedule(now: number) {
  const date = new Date(now + BEIJING_OFFSET_MS);
  const daysAhead = (4 - date.getUTCDay() + 7) % 7 || 7;
  date.setUTCHours(0, 0, 0, 0);
  date.setUTCDate(date.getUTCDate() + daysAhead);
  const opensAt = (date.getTime() - BEIJING_OFFSET_MS) / 1_000;
  return {
    period_key: date.toISOString().slice(0, 10),
    opens_at: opensAt,
    closes_at: opensAt + 86_400,
  };
}

export function formatBeijingTime(unixSecond: number): string {
  return new Date(unixSecond * 1_000 + BEIJING_OFFSET_MS)
    .toISOString()
    .slice(0, 16)
    .replace('T', ' ');
}
