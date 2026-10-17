/** 学习首页。四个区各是一个数组；全空是合法状态，不是错误。 */

import { request } from './client'

export interface DashboardEntry {
  id: string
  title: string
  context?: string
  updated_at?: string
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
