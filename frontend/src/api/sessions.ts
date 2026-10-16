/**
 * 会话。路径是 `/api/v1/sessions` —— **没有 `chat` 那一段**（路由分组变量
 * 叫 `chat`，但挂载的路径不是它）。
 */

import { request } from './client'

export interface SessionSummary {
  session_id: string
  /**
   * **新建的会话在最初几秒是没有标题的。** 标题由服务端在第一轮回答结束之
   * 后才生成，所以这里会是空串。列表行要有这个形态，标题到了之后就地更新
   * ——不要整行重新加载（`设计说明书.md` §3.2）。
   */
  title: string
  created_at: string
  updated_at: string
  /** 只有回收站里的会话才有；用来区分"在列表里"和"在回收站里"。 */
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
  capability?: string
  created_at: string
}

/** 开一个新会话。返回的标题是空的，原因见 `SessionSummary.title`。 */
export function createSession(): Promise<SessionSummary> {
  return request<SessionSummary>('/sessions', { method: 'POST' })
}

export function listSessions(query?: {
  limit?: number
  offset?: number
  deleted?: boolean
}): Promise<SessionList> {
  return request<SessionList>('/sessions', { query })
}

/** 一个会话的**已落库**消息。正在生成的那一轮不在这里 —— 它在事件里。 */
export function listMessages(sessionId: string): Promise<{ messages: MessageSummary[] }> {
  return request<{ messages: MessageSummary[] }>(
    `/sessions/${encodeURIComponent(sessionId)}/messages`,
  )
}
