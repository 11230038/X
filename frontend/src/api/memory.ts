/**
 * 记忆：轨迹（L1）、事实（L2）、综合（L3）、去重清理与导出。
 *
 * 形状全部照后端 view struct 抄。**可选字段是"缺省"不是 `null`** —— 后端
 * 从不下发 null，所以这里不要写 `| null`，更不要把缺失画成空字符串。
 *
 * 三种取法各不一样：
 * - traces 只有 `limit`，没有游标（页面上写"最近 N 条"）；
 * - facts 用 `before` 游标，`status` 是**重复的查询键**（`?status=a&status=b`）；
 * - audit 只有 `action` + `limit`。
 */

import { request } from './client'

export type MemorySurface = 'mastery' | 'reading' | 'practice' | 'user'
export type FactStatus = 'active' | 'retracted' | 'superseded'
export type Confirmer = 'gate' | 'user'
export type MemoryAction =
  | 'fact.stated'
  | 'fact.edited'
  | 'fact.retracted'
  | 'fact.restored'
  | 'fact.merged'
  | 'fact.purged'
  | 'memory.exported'

export interface TraceView {
  trace_id: string
  surface: MemorySurface
  kind: string
  statement: string
  session_id?: string
  turn_id?: string
  payload?: unknown
  created_at: string
}

export interface FactView {
  fact_id: string
  surface: MemorySurface
  scope?: string
  statement: string
  confidence: number
  status: FactStatus
  sensitive: boolean
  confirmed_by: Confirmer
  version: number
  created_at: string
  updated_at?: string
  retract_time?: string
}

export interface FactDetailView extends FactView {
  evidence: TraceView[]
}

export interface MemoryAuditView {
  audit_id: string
  actor: 'user'
  action: MemoryAction
  target_kind: 'fact' | 'memory'
  target_id?: string
  result: 'done' | 'partial'
  detail?: unknown
  created_at: string
}

export interface SurfaceProfileView {
  surface: MemorySurface
  facts: FactView[]
  truncated: number
}

export interface ScopeGroupView {
  scope: string
  facts: FactView[]
}

export interface SynthesisView {
  owner_id: number
  computed_at: string
  window_days: number
  recent: FactView[]
  profile: SurfaceProfileView[]
  scopes: ScopeGroupView[]
  preferences: FactView[]
}

export interface DedupeGroupView {
  fingerprint: string
  statement: string
  survivor: string
  superseded: string[]
}

export interface DedupeReportView {
  dry_run: boolean
  groups: DedupeGroupView[]
  superseded: number
  result: 'done' | 'partial'
}

export interface CleanupReportView {
  dry_run: boolean
  before: string
  facts: FactView[]
  purged: number
}

export interface MemoryExportView {
  owner_id: number
  exported_at: string
  facts: FactDetailView[]
  observations: TraceView[]
  actions: MemoryAuditView[]
}

export interface TracesQuery {
  since?: string
  until?: string
  limit?: number
  surface?: MemorySurface
  kind?: string
}

export interface FactsQuery {
  before?: string
  limit?: number
  status?: readonly FactStatus[]
  surface?: MemorySurface
}

export interface MemoryAuditQuery {
  action?: MemoryAction
  limit?: number
}

export interface EditFactRequest {
  /** 缺省 = 不改；空串会被领域层拒绝。 */
  statement?: string
  /** 缺省 = 不改；空串 = 清掉 scope。 */
  scope?: string
}

export interface StateFactRequest {
  statement: string
  scope?: string
}

export interface DedupeRequest {
  dry_run?: boolean
}

export interface CleanupRequest {
  before: string
  surface?: string
  status?: readonly FactStatus[]
  dry_run?: boolean
}

function idPath(factId: string): string {
  return `/memory/facts/${encodeURIComponent(factId)}`
}

export function listTraces(query: TracesQuery = {}): Promise<TraceView[]> {
  return request<{ traces: TraceView[] }>('/memory/traces', {
    query: {
      since: query.since,
      until: query.until,
      limit: query.limit,
      surface: query.surface,
      kind: query.kind,
    },
  }).then((data) => data.traces)
}

export function listFacts(query: FactsQuery = {}): Promise<FactView[]> {
  return request<{ facts: FactView[] }>('/memory/facts', {
    query: {
      before: query.before,
      limit: query.limit,
      status: query.status,
      surface: query.surface,
    },
  }).then((data) => data.facts)
}

export function getFact(factId: string): Promise<FactDetailView> {
  return request<{ fact: FactDetailView }>(idPath(factId)).then((data) => data.fact)
}

export function editFact(factId: string, body: EditFactRequest): Promise<FactDetailView> {
  return request<{ fact: FactDetailView }>(idPath(factId), { method: 'PATCH', body }).then(
    (data) => data.fact,
  )
}

export function stateFact(body: StateFactRequest): Promise<FactDetailView> {
  return request<{ fact: FactDetailView }>('/memory/facts', { method: 'POST', body }).then(
    (data) => data.fact,
  )
}

export function retractFact(factId: string): Promise<FactDetailView> {
  return request<{ fact: FactDetailView }>(`${idPath(factId)}/retract`, { method: 'POST' }).then(
    (data) => data.fact,
  )
}

export function restoreFact(factId: string): Promise<FactDetailView> {
  return request<{ fact: FactDetailView }>(`${idPath(factId)}/restore`, { method: 'POST' }).then(
    (data) => data.fact,
  )
}

export function dedupeFacts(body: DedupeRequest = {}): Promise<DedupeReportView> {
  return request<{ dedupe: DedupeReportView }>('/memory/facts/dedupe', {
    method: 'POST',
    body,
  }).then((data) => data.dedupe)
}

export function cleanupFacts(body: CleanupRequest): Promise<CleanupReportView> {
  return request<{ cleanup: CleanupReportView }>('/memory/cleanup', {
    method: 'POST',
    body,
  }).then((data) => data.cleanup)
}

/**
 * L3 综合**每次现算**，所以这个函数没有缓存层。页面也不该缓存结果 ——
 * `computed_at` 是它的时间戳，界面必须显示出来。
 */
export function getSynthesis(): Promise<SynthesisView> {
  return request<{ synthesis: SynthesisView }>('/memory/synthesis').then((data) => data.synthesis)
}

export function listMemoryAudit(query: MemoryAuditQuery = {}): Promise<MemoryAuditView[]> {
  return request<{ audits: MemoryAuditView[] }>('/memory/audit', {
    query: { action: query.action, limit: query.limit },
  }).then((data) => data.audits)
}

export function exportMemory(): Promise<MemoryExportView> {
  return request<{ export: MemoryExportView }>('/memory/export').then((data) => data.export)
}
