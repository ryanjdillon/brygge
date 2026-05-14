export type MonthStyle = 'long' | 'short' | 'narrow'

export function monthName(month: number, locale: string, style: MonthStyle = 'long'): string {
  if (!Number.isInteger(month) || month < 1 || month > 12) return String(month)
  const d = new Date(2000, month - 1, 1)
  return new Intl.DateTimeFormat(locale, { month: style }).format(d)
}

export function monthOptions(locale: string, style: MonthStyle = 'long'): Array<{ value: number; label: string }> {
  return Array.from({ length: 12 }, (_, i) => ({
    value: i + 1,
    label: monthName(i + 1, locale, style),
  }))
}
