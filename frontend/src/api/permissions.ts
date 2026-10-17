/**
 * 权限快照。
 *
 * 后端只回答**当前调用者**的权限（`GET /me/permissions`），所以这里是一个
 * 模块级单例，登录后取一次，登出/401 时清掉。
 *
 * `layer` 与 `reason` 都原样保留：`preset` 拒的和 `guardian` 拒的是同一个
 * false，但界面上是两句不同的话（`src/copy/permissions.ts`）。
 */

import { request } from './client'

export type PermissionKind =
  | 'model'
  | 'capability'
  | 'surface'
  | 'kb'
  | 'tool'
  | 'exec'
  | 'learning_policy'

export type PermissionLayer =
  | 'role'
  | 'self'
  | 'preset'
  | 'grant'
  | 'floor'
  | 'guardian'

export interface PermissionItem {
  kind: PermissionKind | string
  id: string
  allowed: boolean
  layer: PermissionLayer | string
  reason: string
}

export interface GuardianLink {
  link_id: string
  learner_id: number
  permissions: string[]
}

export interface PermissionSnapshot {
  user_id: number
  role: string
  preset: string
  items: PermissionItem[]
  guardian: GuardianLink[]
}

export function fetchPermissions(): Promise<PermissionSnapshot> {
  return request<PermissionSnapshot>('/me/permissions')
}
