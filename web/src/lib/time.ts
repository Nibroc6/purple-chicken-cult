// Dates, numbers and relative times go through Intl in the user's locale
// (ARCHITECTURE §15.4). No hand-formatted dates anywhere else.
export function formatDateTime(d: Date, locale: string): string {
  return new Intl.DateTimeFormat(locale, { dateStyle: 'medium', timeStyle: 'short' }).format(d);
}

export function formatNumber(n: number, locale: string): string {
  return new Intl.NumberFormat(locale).format(n);
}

const units: [Intl.RelativeTimeFormatUnit, number][] = [
  ['year', 365 * 24 * 3600],
  ['month', 30 * 24 * 3600],
  ['week', 7 * 24 * 3600],
  ['day', 24 * 3600],
  ['hour', 3600],
  ['minute', 60],
  ['second', 1],
];

// formatRelative renders "9 h ago"-style times relative to now.
export function formatRelative(d: Date, locale: string, now: Date = new Date()): string {
  const diff = (d.getTime() - now.getTime()) / 1000;
  const fmt = new Intl.RelativeTimeFormat(locale, { numeric: 'auto', style: 'narrow' });
  for (const [unit, secs] of units) {
    if (Math.abs(diff) >= secs) return fmt.format(Math.round(diff / secs), unit);
  }
  return fmt.format(0, 'second');
}
