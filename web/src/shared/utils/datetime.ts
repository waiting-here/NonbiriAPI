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

const formatters = new Map<Locale, Intl.DateTimeFormat>();

function formatter(locale: Locale): Intl.DateTimeFormat {
  let fmt = formatters.get(locale);
  if (!fmt) {
    fmt = new Intl.DateTimeFormat(locale === 'en' ? 'en-US' : 'zh-CN', DATETIME_OPTIONS);
    formatters.set(locale, fmt);
  }
  return fmt;
}

function currentLocale(): Locale {
  if (typeof document === 'undefined') return 'zh';
  return document.documentElement.lang === 'en' ? 'en' : 'zh';
}

function toDate(value: unknown): Date | null {
  if (typeof value === 'number' && Number.isFinite(value) && value > 0) {
    const ms = value < 1_000_000_000_000 ? value * 1000 : value;
    const date = new Date(ms);
    return Number.isNaN(date.getTime()) ? null : date;
  }
  if (typeof value === 'string' && value.trim()) {
    const date = new Date(value.trim());
    return Number.isNaN(date.getTime()) ? null : date;
  }
  return null;
}

export function formatDateTime(value: unknown, locale: Locale = currentLocale()): string {
  const date = toDate(value);
  if (!date) return '—';
  return formatter(locale).format(date);
}
