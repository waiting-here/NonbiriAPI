// Bounded, locale-aware formatting for untrusted ISO/epoch timestamps coming
// from the API. A value that cannot be parsed falls back to '—' so a malformed
// server field never crashes the UI. Output is local time (browser timezone)
// in a short YYYY-MM-DD HH:mm shape, never the raw RFC3339 string.

type Locale = 'zh' | 'en';

const DATETIME_OPTIONS: Intl.DateTimeFormatOptions = {
  year: 'numeric',
  month: '2-digit',
  day: '2-digit',
  hour: '2-digit',
  minute: '2-digit',
};

const formatters = new Map<string, Intl.DateTimeFormat>();

function formatter(locale: Locale): Intl.DateTimeFormat {
  const zone = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
  const key = `${locale}:${zone}`;
  let fmt = formatters.get(key);
  if (!fmt) {
    fmt = new Intl.DateTimeFormat(locale === 'en' ? 'en-US' : 'zh-CN', { ...DATETIME_OPTIONS, timeZone: zone });
    if (formatters.size >= 8) formatters.clear();
    formatters.set(key, fmt);
  }
  return fmt;
}

function currentLocale(): Locale {
  if (typeof document === 'undefined') return 'zh';
  return document.documentElement.lang === 'en' ? 'en' : 'zh';
}

function toDate(value: unknown): Date | null {
  if (typeof value === 'number' && Number.isFinite(value) && value >= 0) {
    const ms = value < 1_000_000_000_000 ? value * 1000 : value;
    const date = new Date(ms);
    return Number.isNaN(date.getTime()) ? null : date;
  }
  if (typeof value === 'string' && /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/.test(value.trim())) {
    const date = new Date(value.trim());
    return Number.isNaN(date.getTime()) ? null : date;
  }
  return null;
}

export function formatDateTime(value: unknown, locale: Locale = currentLocale()): string {
  const date = toDate(value);
  if (!date) return '—';
  try { return formatter(locale).format(date); }
  catch { return date.toISOString().replace('T', ' ').slice(0, 16) + ' UTC'; }
}
