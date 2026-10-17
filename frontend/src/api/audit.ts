/**
 * 审计。
 *
 * 没有 offset / 游标，只有 `limit`；向后翻页靠把 `until` 设为当前最老一条的
 * `created_at`（`until` 是排他的，所以边界那条不会重复）。同一时刻的多条
 * 事件有可能被跳过 —— 页面在"请求没带回新行"时要说出来，而不是假装到底了。
 */

import { request } from './client'

export type AuditTargetKind =
  | 'account'
  | 'grant'
  | 'guardian_link'
  | 'guardian_permission'
  | 'session'
  | 'library_file'
  | 'workspace_job'
  | 'surface'

export type AuditResult = 'ok' | 'denied' | 'failed'

export interface AuditRef {
  kind: AuditTargetKind
  id: string
}

export interface AuditDetail {
  reason?: string
  layer?: string
  counts?: Record<string, number>
  items?: AuditRef[]
}

export interface AuditEvent {
  audit_id: number
  actor_id?: number
  actor_name: string
  actor_role: string
  action: string
  target_kind: AuditTargetKind
  target_id: string
  subject_id?: number
  result: AuditResult
  detail?: AuditDetail
  request_id?: string
  created_at: string
}

export interface AuditList {
  events: AuditEvent[]
  max_page_size: number
}

export interface AuditFilters {
  actor_id?: number
  subject_id?: number
  action?: string
  target_kind?: string
  target_id?: string
  since?: string
  until?: string
  limit?: number
}

export function listAudit(filters: AuditFilters = {}): Promise<AuditList> {
  return request<AuditList>('/audit', {
    query: {
      actor_id: filters.actor_id,
      subject_id: filters.subject_id,
      action: filters.action,
      target_kind: filters.target_kind,
      target_id: filters.target_id,
      since: filters.since,
      until: filters.until,
      limit: filters.limit,
    },
  })
}
