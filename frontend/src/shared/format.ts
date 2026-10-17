/**
 * 时间、字节、计数、时长的格式化。
 *
 * 每个概念只有一个函数，页面不自己 `toLocaleString`。这样做的原因是
 * `设计说明书.md` §6.2：同一个数在列表、详情、导出里必须是同一种写法；
 * 一旦允许页面各自格式化，"1.2 MB" 与 "1234567 字节" 就会出现在同一屏。
 */

const DATE_TIME = new Intl.DateTimeFormat('zh-CN', {
  year: 'numeric',
  month: '2-digit',
  day: '2-digit',
  hour: '2-digit',
  minute: '2-digit',
})

const DATE_ONLY = new Intl.DateTimeFormat('zh-CN', {
  year: 'numeric',
  month: '2-digit',
  day: '2-digit',
})

const TIME_ONLY = new Intl.DateTimeFormat('zh-CN', {
  hour: '2-digit',
  minute: '2-digit',
  second: '2-digit',
})

const NUMBER = new Intl.NumberFormat('zh-CN')

/** 空值统一画一个长横线，不画 "null" 或空字符串造成的视觉缺口。 */
export const EMPTY = '—'

function parse(value: string | number | Date | null | undefined): Date | null {
  if (value === null || value === undefined || value === '') return null
  const date = value instanceof Date ? value : new Date(value)
  return Number.isNaN(date.getTime()) ? null : date
}

/** 2026/10/17 14:03 */
export function formatDateTime(value: string | number | Date | null | undefined): string {
  const date = parse(value)
  return date ? DATE_TIME.format(date) : EMPTY
}

/** 2026/10/17 */
export function formatDate(value: string | number | Date | null | undefined): string {
  const date = parse(value)
  return date ? DATE_ONLY.format(date) : EMPTY
}

/** 14:03:22 */
export function formatTime(value: string | number | Date | null | undefined): string {
  const date = parse(value)
  return date ? TIME_ONLY.format(date) : EMPTY
}

/**
 * 相对时间用于列表（"3 分钟前"）。后端时间都是 UTC；这里按浏览器本地时区
 * 折算。超过七天就退回日期 —— "23 天前" 不如 "2026/09/24" 有用。
 */
export function formatRelative(value: string | number | Date | null | undefined): string {
  const date = parse(value)
  if (!date) return EMPTY
  const delta = Date.now() - date.getTime()
  if (delta < 0) return formatDateTime(date)
  const minute = 60_000
  const hour = 60 * minute
  const day = 24 * hour
  if (delta < minute) return '刚刚'
  if (delta < hour) return `${Math.floor(delta / minute)} 分钟前`
  if (delta < day) return `${Math.floor(delta / hour)} 小时前`
  if (delta < 7 * day) return `${Math.floor(delta / day)} 天前`
  return formatDate(date)
}

/** 千分位。计数与 token 都用它。 */
export function formatNumber(value: number | null | undefined): string {
  if (value === null || value === undefined || !Number.isFinite(value)) return EMPTY
  return NUMBER.format(value)
}

/** 字节。1024 进制，保留一位小数，去掉多余的 ".0"。 */
export function formatBytes(value: number | null | undefined): string {
  if (value === null || value === undefined || !Number.isFinite(value) || value < 0) return EMPTY
  if (value < 1024) return `${value} B`
  const units = ['KB', 'MB', 'GB', 'TB']
  let size = value / 1024
  let index = 0
  while (size >= 1024 && index < units.length - 1) {
    size /= 1024
    index += 1
  }
  return `${size >= 100 ? Math.round(size) : size.toFixed(1).replace(/\.0$/, '')} ${units[index]}`
}

/** 毫秒 → "1.2 秒" / "3 分 05 秒" / "1 小时 02 分"。 */
export function formatDurationMs(value: number | null | undefined): string {
  if (value === null || value === undefined || !Number.isFinite(value) || value < 0) return EMPTY
  if (value < 1000) return `${Math.round(value)} 毫秒`
  const totalSeconds = Math.round(value / 1000)
  if (totalSeconds < 60) return `${(value / 1000).toFixed(1).replace(/\.0$/, '')} 秒`
  const seconds = totalSeconds % 60
  const minutes = Math.floor(totalSeconds / 60) % 60
  const hours = Math.floor(totalSeconds / 3600)
  if (hours > 0) return `${hours} 小时 ${String(minutes).padStart(2, '0')} 分`
  return `${minutes} 分 ${String(seconds).padStart(2, '0')} 秒`
}

/** 秒数（`Retry-After` 与耗时字段里常见）。 */
export function formatSeconds(value: number | null | undefined): string {
  if (value === null || value === undefined) return EMPTY
  return formatDurationMs(value * 1000)
}

/**
 * 秒数 → 时钟写法（`29:36` / `1:02:05`），用于视频时长。
 *
 * 与 `formatSeconds` 是两件事：那里说的是"过了多久"，句子里读；这里是
 * 播放器上的刻度，和进度条里的数字要对得上，所以固定 `m:ss` 或 `h:mm:ss`。
 */
export function formatClock(value: number | null | undefined): string {
  if (value === null || value === undefined || !Number.isFinite(value) || value < 0) {
    return EMPTY
  }
  const total = Math.floor(value)
  const seconds = String(total % 60).padStart(2, '0')
  const minutes = Math.floor(total / 60) % 60
  const hours = Math.floor(total / 3600)
  if (hours > 0) return `${hours}:${String(minutes).padStart(2, '0')}:${seconds}`
  return `${minutes}:${seconds}`
}

/** 0.83 → "83%"。传 null 时画横线，不画 "0%" —— 后者是一个结论。 */
export function formatPercent(value: number | null | undefined, digits = 0): string {
  if (value === null || value === undefined || !Number.isFinite(value)) return EMPTY
  return `${(value * 100).toFixed(digits)}%`
}

/** 截断长文本（列表里的标题、片段）。按字符数，不按字节。 */
export function truncate(value: string, max: number): string {
  if (value.length <= max) return value
  return `${value.slice(0, Math.max(0, max - 1))}…`
}
