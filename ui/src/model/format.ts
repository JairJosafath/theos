// How the Work and Factory areas write durations, clock times, bytes, CPU and shares.

/**
 * "45 s", "3 min", "2 h 5 min", "3 d 4 h". `fine` is for short measured times
 * such as the API's response: "12 ms" under a second, "1.5 s" above.
 */
export function formatDuration(ms: number, { fine = false } = {}): string {
  if (fine && ms < 1000) return `${Math.round(ms)} ms`
  if (fine) return `${(ms / 1000).toFixed(1)} s`
  const s = Math.max(0, Math.round(ms / 1000))
  if (s < 60) return `${s} s`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m} min`
  const h = Math.floor(m / 60)
  if (h < 24) return m % 60 ? `${h} h ${m % 60} min` : `${h} h`
  const d = Math.floor(h / 24)
  return h % 24 ? `${d} d ${h % 24} h` : `${d} d`
}

const WEEKDAYS = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat']

/** A time of day, "15:04"; with the weekday when it isn't today, "Fri 03:00". */
export function formatClock(t: number, now: number): string {
  const d = new Date(t)
  const clock = `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`
  if (d.toDateString() === new Date(now).toDateString()) return clock
  return `${WEEKDAYS[d.getDay()]} ${clock}`
}

/** A moment as a date and time in the reader's locale, "1 Jun 2026, 11:48:05"; '' for no valid time. */
export function absoluteTime(at: string | number | undefined): string {
  if (at === undefined || at === '') return ''
  const d = new Date(at)
  if (Number.isNaN(d.getTime())) return ''
  return d.toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'medium' })
}

/** "1.2 GiB", "93 MiB". */
export function formatBytes(bytes: number | undefined): string {
  if (bytes === undefined) return '–'
  if (bytes >= 2 ** 30) return `${(bytes / 2 ** 30).toFixed(1)} GiB`
  return `${Math.round(bytes / 2 ** 20)} MiB`
}

/** "163m" as "0.16 cores", small numbers as millicores. */
export function formatCpu(milli: number | undefined): string {
  if (milli === undefined) return '–'
  if (milli >= 1000) return `${(milli / 1000).toFixed(1)} cores`
  return `${milli}m`
}

export function percent(part: number | undefined, whole: number | undefined): number | undefined {
  if (part === undefined || !whole) return undefined
  return Math.min(100, Math.round((part / whole) * 100))
}
