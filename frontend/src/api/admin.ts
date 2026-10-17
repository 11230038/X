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
  /**
   * 部署白名单里的超级管理员。**这一行是不是受保护的**也由它说了算：超管
   * 账号不能在这里被停用、重置密码或删除。
   */
  super_admin: boolean
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

/* ---------------------------------------------------------------------------
 * 只有超级管理员能用的四条。普通管理员调用一律 403 `super_admin_required`。
 * 界面按 `currentAccount.super_admin` 决定画不画入口，但那只是显隐：改一下
 * 本地存储就能让按钮出现，后端仍然是唯一的门。
 * ------------------------------------------------------------------------- */

export interface CreateUserInput {
  username: string
  password: string
  role: Role
  preset: Preset
}

/** 新建账号。201，**不签发票**——建的是别人的凭据，不是把这个人登进来。 */
export function createUser(input: CreateUserInput): Promise<{ user: AdminUser }> {
  return request<{ user: AdminUser }>('/admin/users', { method: 'POST', body: input })
}

/**
 * 删除账号。204。
 *
 * 是硬删除：这个人的会话、文件行、知识库、题库、活书等随外键级联一起没，
 * 不能恢复；审计记录不级联，保留。已上传文件的字节留在磁盘上。
 */
export function deleteUser(userId: number): Promise<void> {
  return request<void>(`/admin/users/${encodeURIComponent(String(userId))}`, {
    method: 'DELETE',
  })
}

/** 重置密码。204。该账号现有登录全部立即失效，需要用新密码重新登录。 */
export function resetUserPassword(userId: number, password: string): Promise<void> {
  return request<void>(`/admin/users/${encodeURIComponent(String(userId))}/password`, {
    method: 'PUT',
    body: { password },
  })
}

/**
 * 启用 / 停用。204。
 *
 * 停用同时吊销该账号现有登录，立即生效；启用只把 sign-in 标志翻回来，
 * 不恢复之前的 token —— 那个人要重新登录。
 */
export function setUserDisabled(userId: number, disabled: boolean): Promise<void> {
  return request<void>(`/admin/users/${encodeURIComponent(String(userId))}/disabled`, {
    method: 'PUT',
    body: { disabled },
  })
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
