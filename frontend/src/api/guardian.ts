/**
 * 监护（监护人自己的视角）。
 *
 * 报告接口要求这条关系上持有 `view_reports`。没有这项权限时**不画空报告**
 * —— 空报告会被读成"孩子没有学习"，而事实是"你没被允许看"。
 */

import { request } from './client'
import type { DashboardView } from './dashboard'
import type { AdminGuardianLink } from './admin'

export function listLearners(): Promise<{ learners: AdminGuardianLink[] }> {
  return request<{ learners: AdminGuardianLink[] }>('/guardian/learners')
}

export function getLearnerReport(userId: number): Promise<DashboardView> {
  return request<DashboardView>(
    `/guardian/learners/${encodeURIComponent(String(userId))}/reports`,
  )
}
