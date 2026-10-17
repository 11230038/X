/**
 * 掌握路径。
 *
 * 四种掌握状态是四个不同的东西，不能合成一个"未掌握"：
 * `unknown`（未接触）/ `weak`（薄弱）/ `learning`（学习中）/ `mastered`。
 *
 * 路径**没有创建接口**：它由一次 `mastery_path` 能力的对话产生。所以界面上
 * 的"新建路径"是一个跳去对话的入口，不是一个 POST。
 */

import { request } from './client'

export type MasteryLevel = 'unknown' | 'weak' | 'learning' | 'mastered'
export type EvidenceResult = 'correct' | 'incorrect' | 'partial'
export type StepKind = 'map' | 'learn' | 'review' | 'done'

export interface MasteryKnowledgePoint {
  id: string
  name: string
  type?: string
  module_id: string
  description?: string
  mastery?: MasteryLevel
}

export interface MasteryModule {
  id: string
  name: string
  description?: string
  /** nil 切片在 Go 侧编码成 null。 */
  knowledge_points: MasteryKnowledgePoint[] | null
}

export interface MasterySummary {
  path_id: string
  goal: string
  description: string
  emoji: string
  status: string
  version: number
  modules: number
  knowledge_points: number
  mastered: number
  /** 零值时间表示从未看过。 */
  last_seen_at: string
  updated_at: string
}

export interface MasteryPath {
  path_id: string
  goal: string
  description: string
  emoji: string
  status: string
  version: number
  tree: { modules: MasteryModule[] | null }
  created_at: string
  updated_at: string
}

export interface MasteryPointInsight {
  knowledge_point_id: string
  name: string
  module_id: string
  level: MasteryLevel
  total: number
  correct: number
  incorrect: number
  partial: number
  last_observed_at: string
  due_at: string
}

export interface MasteryEvidence {
  evidence_id: string
  knowledge_point_id: string
  result: EvidenceResult
  source?: string
  assessment_type?: string
  quality?: string
  /** 任意形状的原始 JSON；只按文本展示，不当结构用。 */
  detail?: unknown
  created_at: string
}

export interface MasteryNextStep {
  kind: StepKind
  knowledge_point_id?: string
  reason: string
  /** nil 切片编码成 null。 */
  evidence_ids: string[] | null
}

export interface MasteryInsight {
  path: MasteryPath
  points: MasteryPointInsight[]
  weak: string[]
  evidence: MasteryEvidence[]
  next: MasteryNextStep
}

export interface MasteryLease {
  session_id: string
  turn_id: string
  acquired_at: string
}

export interface LeaseState {
  path_id: string
  /** null = 没有人在写这条路径。 */
  lease: MasteryLease | null
  released: boolean
}

export function listMasteryPaths(sessionId?: string): Promise<{ paths: MasterySummary[] }> {
  return request<{ paths: MasterySummary[] }>('/mastery/paths', {
    query: { session_id: sessionId },
  })
}

export function getMasteryPath(pathId: string): Promise<MasteryPath> {
  return request<MasteryPath>(`/mastery/paths/${encodeURIComponent(pathId)}`)
}

export interface PatchMasteryPath {
  goal?: string
  description?: string
  emoji?: string
  status?: 'active'
}

export function patchMasteryPath(
  pathId: string,
  patch: PatchMasteryPath,
): Promise<MasteryPath> {
  return request<MasteryPath>(`/mastery/paths/${encodeURIComponent(pathId)}`, {
    method: 'PATCH',
    body: patch,
  })
}

export function getMasteryInsight(pathId: string): Promise<MasteryInsight> {
  return request<MasteryInsight>(
    `/mastery/paths/${encodeURIComponent(pathId)}/insight`,
  )
}

export function getMasteryLease(pathId: string): Promise<LeaseState> {
  return request<LeaseState>(`/mastery/paths/${encodeURIComponent(pathId)}/lease`)
}

/**
 * 释放（接管）另一端持有的写权。
 *
 * 注意这是"让对方停下"，不是"我拿到"：拿到写权只能靠在这个会话里发一轮
 * `mastery_path` 对话。
 */
export function releaseMasteryLease(pathId: string): Promise<LeaseState> {
  return request<LeaseState>(`/mastery/paths/${encodeURIComponent(pathId)}/lease`, {
    method: 'DELETE',
  })
}
