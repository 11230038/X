/**
 * 用量统计。
 *
 * `unfinished`（没有回来的调用）**单独一个数**，不并进 `error`：没能结束的
 * 调用不等于失败，它可能是进程被杀、连接断了、或者还在跑。
 */

import { request } from './client'

export type UsageGroupBy = 'model' | 'provider' | 'source' | 'session' | 'day'

export interface UsageTotals {
  calls: number
  ok: number
  error: number
  cancelled: number
  prompt_tokens: number
  completion_tokens: number
  total_tokens: number
  duration_ms: number
  ttft_ms: number
}

export interface UsageGroup {
  key: string
  finished: UsageTotals
  unfinished: number
}

export interface UsageReport {
  since?: string
  until?: string
  group_by?: UsageGroupBy
  finished: UsageTotals
  unfinished: number
  groups: UsageGroup[]
}

export interface UsageQuery {
  model?: string
  provider?: string
  source?: string
  session_id?: string
  group_by?: UsageGroupBy
  since?: string
  until?: string
}

export function getUsage(query: UsageQuery = {}): Promise<UsageReport> {
  return request<UsageReport>('/usage', {
    query: {
      model: query.model,
      provider: query.provider,
      source: query.source,
      session_id: query.session_id,
      group_by: query.group_by,
      since: query.since,
      until: query.until,
    },
  })
}
