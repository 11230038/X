/** 学习首页。四个区各是一个数组；全空是合法状态，不是错误。 */

import { request } from './client'

export interface DashboardProgress {
  done: number
  total: number
}

export interface DashboardEntry {
  id: string
  title: string
  context?: string
  updated_at?: string
  /**
   * 只有掌握路径带这个比例（已掌握 / 知识点总数）。会话与复习队列没有可数的
   * 东西，所以字段缺席 —— 缺席是"没有这一项统计"，不是"完成了 0"。
   */
  progress?: DashboardProgress
}

export interface DashboardRecommendation {
  kind: 'learn' | 'review' | 'done'
  path_id: string
  knowledge_point_id?: string
  title: string
  reason: string
  evidence_ids: string[]
}

export interface DashboardView {
  sessions: DashboardEntry[]
  paths: DashboardEntry[]
  reviews: DashboardEntry[]
  next: DashboardRecommendation[]
}

export function getDashboard(): Promise<DashboardView> {
  return request<DashboardView>('/dashboard')
}
