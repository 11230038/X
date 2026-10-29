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

/**
 * 路径对话历史的一行：一段掌握路径对话，和它创建的那条路径。
 *
 * 一个路径只有一段这样的对话 —— 创建它的那段。别的对话可能切换进来过，
 * 但它们各有自己的路径，不在这一列里。
 */
export interface MasteryConversation {
  session_id: string
  title: string
  path_id: string
  /** 路径名：创建这条路的那轮结束时由模型总结的短名，之后可被学习者改。 */
  goal: string
  emoji: string
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

/** 路径对话的第一级：每行一段对话，标出它创建的那条路径。 */
export function listMasteryConversations(): Promise<{ conversations: MasteryConversation[] }> {
  return request<{ conversations: MasteryConversation[] }>('/mastery/conversations')
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

/**
 * 一个时间窗内"从没掌握变成掌握"的知识点数。
 *
 * 等级的缓存只说明现在有多少个已掌握，不说明它们是什么时候学会的，所以这个数
 * 由服务端回放证据算出来。窗口的两端都必须给出：首页的"本周"是按浏览器的日历
 * 划的，服务端不替它猜。
 */
export function getMasteredBetween(from: string, to: string): Promise<{ mastered: number }> {
  return request<{ mastered: number }>('/mastery/mastered', {
    query: { from, to },
  })
}

/**
 * 一条路径在窗口里**完成**的模块数 / 它现在的模块数。
 *
 * 模块"完成"= 模块里每个知识点都到了 mastered。计数用证据回放：窗口末全掌握、
 * 窗口初没有全掌握的模块才算一个；没有知识点的模块两边都不算（它永远完不成，
 * 放分母会让 100% 到不了）。
 */
export interface PathProgress {
  modules_done: number
  modules_total: number
}

export function getPathProgress(
  pathId: string,
  from: string,
  to: string,
): Promise<PathProgress> {
  return request<PathProgress>(`/mastery/paths/${encodeURIComponent(pathId)}/progress`, {
    query: { from, to },
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
