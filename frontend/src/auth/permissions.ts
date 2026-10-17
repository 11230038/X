/**
 * 权限快照的模块级 store（照 `session.ts` 的写法，不引 pinia）。
 *
 * 三种回答，**不要压成两种**：
 * - `true` / `false`：快照里明确写了；
 * - `'unknown'`：快照还没到、取失败，或者这一项不在快照里。
 *
 * `'unknown'` 时不锁页面 —— 后端每个请求都会再判一次，前端在这里猜"没有"
 * 只会让一个本来能用的页面打不开。此时页面照常渲染，只挂一条部分失败条。
 */

import { computed, ref } from 'vue'

import { isApiFailure, type ApiFailure } from '../api/client'
import { fetchPermissions, type PermissionItem, type PermissionSnapshot } from '../api/permissions'

export type PermissionStatus = 'idle' | 'loading' | 'ready' | 'failed'

const snapshot = ref<PermissionSnapshot | null>(null)
const status = ref<PermissionStatus>('idle')
const failure = ref<ApiFailure | null>(null)

let inFlight: Promise<void> | null = null

export const permissionSnapshot = computed(() => snapshot.value)
export const permissionStatus = computed(() => status.value)
export const permissionFailure = computed(() => failure.value)
export const isAdmin = computed(() => snapshot.value?.role === 'admin')
export const guardianLinks = computed(() => snapshot.value?.guardian ?? [])
export const presetName = computed(() => snapshot.value?.preset ?? '')

function asFailure(cause: unknown): ApiFailure {
  if (isApiFailure(cause)) return cause.failure
  if (cause instanceof Error) return { kind: 'malformed', status: 0, code: '', message: cause.message }
  return { kind: 'malformed', status: 0, code: '', message: '权限信息加载失败' }
}

/**
 * 取一次快照。并发调用共享同一个 Promise —— 登录后多个页面同时挂载时，
 * 不应该打出三份 `/me/permissions`。
 */
export function refreshPermissions(): Promise<void> {
  if (inFlight) return inFlight
  if (!snapshot.value) status.value = 'loading'
  failure.value = null

  inFlight = (async () => {
    try {
      snapshot.value = await fetchPermissions()
      status.value = 'ready'
      failure.value = null
    } catch (cause) {
      failure.value = asFailure(cause)
      if (!snapshot.value) status.value = 'failed'
    } finally {
      inFlight = null
    }
  })()

  return inFlight
}

/** 登出或凭据失效时清空：下一个人不能看到上一个人的权限。 */
export function resetPermissions(): void {
  snapshot.value = null
  status.value = 'idle'
  failure.value = null
  inFlight = null
}

/** 快照里这一项的原始决定（含 layer / reason）。 */
export function decisionFor(kind: string, id: string): PermissionItem | null {
  return snapshot.value?.items.find((item) => item.kind === kind && item.id === id) ?? null
}

/**
 * `true` / `false` / `'unknown'`。快照没到或这一项不在快照里都算 unknown。
 */
export function can(kind: string, id: string): boolean | 'unknown' {
  if (status.value !== 'ready' || !snapshot.value) return 'unknown'
  const item = decisionFor(kind, id)
  return item ? item.allowed : 'unknown'
}

export function canSurface(surface: string): boolean | 'unknown' {
  return can('surface', surface)
}

/** 只用于展示；`can()` 的布尔语义请勿从这里推导。 */
export function surfaceDecision(surface: string): PermissionItem | null {
  return decisionFor('surface', surface)
}
