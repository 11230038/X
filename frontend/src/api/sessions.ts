/**
 * 会话与消息。
 *
 * 会话列表是 `limit` / `offset`；消息列表是**一次性完整数组**，没有服务端
 * 分页（`backend/internal/handler/session.go`）。前端按 50 条分块渲染"加载
 * 更早"，不假装有游标。
 */

import { request } from './client'

/**
 * 会话属于哪个页面。它是**分区**，不是权限：写作、掌握路径与题目问答会话仍然
 * 走 `chat` 权限面，`kind` 只决定它出现在哪一份列表里。
 */
export type SessionKind = 'chat' | 'writing' | 'mastery' | 'question_bank'

/** 列表可以要一半，也可以显式地要全部（回收站、导出）。 */
export type SessionKindFilter = SessionKind | 'all'

export interface SessionSummary {
  session_id: string
  title: string
  kind: SessionKind
  created_at: string
  updated_at: string
  /** 只有回收站里的会话才有这个字段；活跃会话不带（不是 null）。 */
  deleted_at?: string
}

export interface SessionList {
  sessions: SessionSummary[]
  limit: number
  offset: number
}

export interface MessageSummary {
  message_id: string
  session_id: string
  role: string
  content: string
  /** 用户消息没有这个字段。 */
  capability?: string
  created_at: string
}

export interface ListSessionsOptions {
  deleted?: boolean
  /** 不传时后端按 `chat` 处理；要几份合在一起看才显式传 `all`。 */
  kind?: SessionKindFilter
  limit?: number
  offset?: number
}

export function createSession(kind: SessionKind = 'chat'): Promise<SessionSummary> {
  return request<SessionSummary>('/sessions', { method: 'POST', query: { kind } })
}

export function listSessions(options: ListSessionsOptions = {}): Promise<SessionList> {
  return request<SessionList>('/sessions', {
    query: {
      deleted: options.deleted,
      kind: options.kind,
      limit: options.limit,
      offset: options.offset,
    },
  })
}

export function getSession(sessionId: string): Promise<SessionSummary> {
  return request<SessionSummary>(`/sessions/${encodeURIComponent(sessionId)}`)
}

export function renameSession(sessionId: string, title: string): Promise<SessionSummary> {
  return request<SessionSummary>(`/sessions/${encodeURIComponent(sessionId)}`, {
    method: 'PATCH',
    body: { title },
  })
}

/** 软删：进回收站，可恢复。204。 */
export function deleteSession(sessionId: string): Promise<void> {
  return request<void>(`/sessions/${encodeURIComponent(sessionId)}`, { method: 'DELETE' })
}

export function restoreSession(sessionId: string): Promise<SessionSummary> {
  return request<SessionSummary>(`/sessions/${encodeURIComponent(sessionId)}/restore`, {
    method: 'POST',
  })
}

/** 永久删除：会话与消息都不可恢复。204。 */
export function purgeSession(sessionId: string): Promise<void> {
  return request<void>(`/sessions/${encodeURIComponent(sessionId)}/permanent`, {
    method: 'DELETE',
  })
}

/**
 * 认领会话。后端有这个接口，但列表不返回"无主"标记，界面认不出哪一行可
 * 认领 —— 所以这个函数目前没有调用方，留给以后后端补字段时用。
 */
export function claimSession(sessionId: string): Promise<SessionSummary> {
  return request<SessionSummary>(`/sessions/${encodeURIComponent(sessionId)}/claim`, {
    method: 'POST',
  })
}

export function listMessages(sessionId: string): Promise<{ messages: MessageSummary[] }> {
  return request<{ messages: MessageSummary[] }>(
    `/sessions/${encodeURIComponent(sessionId)}/messages`,
  )
}
