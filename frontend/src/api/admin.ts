/**
 * 管理台。
 *
 * 两个 DELETE 带 JSON 请求体（后端就是这样定的）：撤回授权与解除监护关系
 * 都不在路径里带项目。
 */

import { request } from './client'

export type Role = 'admin' | 'user'
export type Preset = 'standard' | 'learner' | 'custom'
export type GrantKind =
  | 'model'
  | 'capability'
  | 'surface'
  | 'kb'
  | 'tool'
  | 'exec'
  | 'learning_policy'

export interface AdminUser {
  user_id: number
  username: string
  role: Role
  /** 库里存的原值；V5 之前的行可能是空串。 */
  preset: string
  /** 策略实际读到的预设。 */
  preset_of: Preset
  disabled: boolean
}

export interface AdminGrant {
  kind: GrantKind
  /** 空串表示整个 kind。 */
  id: string
  allowed: boolean
}

export type GuardianPermission =
  | 'view_reports'
  | 'assign_materials'
  | 'manage_restrictions'
  | 'reset_credentials'

export interface AdminGuardianLink {
  link_id: string
  guardian_id: number
  learner_id: number
  permissions: GuardianPermission[]
}

export function listUsers(): Promise<{ users: AdminUser[] }> {
  return request<{ users: AdminUser[] }>('/admin/users')
}

export function setUserPreset(
  userId: number,
  preset: Preset,
): Promise<{ user_id: number; preset: Preset }> {
  return request<{ user_id: number; preset: Preset }>(
    `/admin/users/${encodeURIComponent(String(userId))}`,
    { method: 'PATCH', body: { preset } },
  )
}

/** 吊销该账号现有登录。204。 */
export function revokeUserSessions(userId: number): Promise<void> {
  return request<void>(`/admin/users/${encodeURIComponent(String(userId))}/sessions`, {
    method: 'DELETE',
  })
}

/** 清掉连续登录失败计数（解锁）。204。 */
export function unlockUser(userId: number): Promise<void> {
  return request<void>(`/admin/users/${encodeURIComponent(String(userId))}/lock`, {
    method: 'DELETE',
  })
}

export function listGrants(userId: number): Promise<{ user_id: number; grants: AdminGrant[] }> {
  return request<{ user_id: number; grants: AdminGrant[] }>(
    `/admin/users/${encodeURIComponent(String(userId))}/grants`,
  )
}

export function setGrant(
  userId: number,
  grant: AdminGrant,
): Promise<{ user_id: number; grant: AdminGrant }> {
  return request<{ user_id: number; grant: AdminGrant }>(
    `/admin/users/${encodeURIComponent(String(userId))}/grants`,
    { method: 'POST', body: grant },
  )
}

export function withdrawGrant(
  userId: number,
  grant: { kind: GrantKind; id: string },
): Promise<{ user_id: number; withdrawn: { kind: GrantKind; id: string } }> {
  return request<{ user_id: number; withdrawn: { kind: GrantKind; id: string } }>(
    `/admin/users/${encodeURIComponent(String(userId))}/grants`,
    { method: 'DELETE', body: grant },
  )
}

export function listGuardians(): Promise<{ guardians: AdminGuardianLink[] }> {
  return request<{ guardians: AdminGuardianLink[] }>('/admin/guardians')
}

export function createGuardianLink(
  guardianId: number,
  learnerId: number,
  permissions: GuardianPermission[],
): Promise<{ guardian: AdminGuardianLink }> {
  return request<{ guardian: AdminGuardianLink }>('/admin/guardians', {
    method: 'POST',
    body: { guardian_id: guardianId, learner_id: learnerId, permissions },
  })
}

export function revokeGuardianLink(
  linkId: string,
): Promise<{ link_id: string; revoked: true }> {
  return request<{ link_id: string; revoked: true }>('/admin/guardians', {
    method: 'DELETE',
    body: { link_id: linkId },
  })
}

export function getGuardianPermissions(
  linkId: string,
): Promise<{ link_id: string; learner_id: number; permissions: GuardianPermission[] }> {
  return request<{ link_id: string; learner_id: number; permissions: GuardianPermission[] }>(
    `/admin/guardians/${encodeURIComponent(linkId)}/permissions`,
  )
}

export function grantGuardianPermission(
  linkId: string,
  permission: GuardianPermission,
): Promise<{ link_id: string; permission: GuardianPermission; granted: boolean }> {
  return request<{ link_id: string; permission: GuardianPermission; granted: boolean }>(
    `/admin/guardians/${encodeURIComponent(linkId)}/permissions`,
    { method: 'POST', body: { permission } },
  )
}

export function revokeGuardianPermission(
  linkId: string,
  permission: GuardianPermission,
): Promise<{ link_id: string; permission: GuardianPermission; granted: boolean }> {
  return request<{ link_id: string; permission: GuardianPermission; granted: boolean }>(
    `/admin/guardians/${encodeURIComponent(linkId)}/permissions`,
    { method: 'DELETE', body: { permission } },
  )
}
