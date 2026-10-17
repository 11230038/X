/**
 * 凭据失效的**唯一**出口。
 *
 * 401 可能同时来自页面上的三个请求；如果每处都自己 `clearSession()` 加
 * `router.push()`，就会出现三次跳转、三条提示。所以这里做三件事：
 *
 * 1. 单飞：一次失效只处理一次，直到重新登录才复位；
 * 2. 拆 socket：连接还握着旧凭据，留着它只会在重连时反复 401；
 * 3. 清缓存：权限与能力都是按人算的，下一个人不能看到上一个人的。
 *
 * 429 **不走这里**：服务端认识你，只是让你慢一点。
 */

import { setUnauthorizedHandler } from '../api/client'
import { clearSession } from '../session'
import { notify } from '../shared/toast'
import { resetCapabilities } from '../shared/capabilities'
import { resetPermissions } from './permissions'

let socketCloser: (() => void) | null = null
let navigateToLogin: ((redirect: string) => void) | null = null
let handled = false

/** 由 socket 层注册：登出时要有一个办法把正在跑的连接拆掉。 */
export function registerSocketCloser(closer: (() => void) | null): void {
  socketCloser = closer
}

/** 由 `main.ts` 注册，因为路由不能在 `client.ts` 里被直接 import。 */
export function registerLoginRedirect(navigate: (redirect: string) => void): void {
  navigateToLogin = navigate
}

export function handleUnauthorized(): void {
  if (handled) return
  handled = true

  try {
    socketCloser?.()
  } catch {
    // 关连接失败不改变"凭据已失效"这个结论。
  }
  socketCloser = null

  clearSession()
  resetPermissions()
  resetCapabilities()

  const here = window.location.pathname + window.location.search
  if (here.startsWith('/login')) return

  notify('登录状态已失效，请重新登录', 'error')
  if (navigateToLogin) {
    navigateToLogin(here)
  } else {
    window.location.assign(`/login?redirect=${encodeURIComponent(here)}`)
  }
}

/** 登录成功后调用，让下一次 401 还能被处理。 */
export function resetAuthFailure(): void {
  handled = false
}

// 注册到 HTTP 出口；`main.ts` 只要 import 本模块，这条线就生效。
setUnauthorizedHandler(handleUnauthorized)
