/**
 * 登录成功后的启动动作，登录页与注册页共用。
 *
 * 三件事：
 * 1. 复位 401 单飞标记（上一个账号已经被处理过的那次不该挡住这个账号）；
 * 2. 取权限快照与能力清单；
 * 3. 都不阻塞跳转 —— 取失败只是页面顶部挂一条部分失败条，不能把人关在登录页。
 */

import { refreshCapabilities } from '../shared/capabilities'
import { resetAuthFailure } from './auth-failure'
import { refreshPermissions } from './permissions'

export async function bootstrapSignedIn(): Promise<void> {
  resetAuthFailure()
  await Promise.allSettled([refreshPermissions(), refreshCapabilities()])
}

/**
 * 只接受站内路径。`//evil.example` 是协议相对 URL，`router.replace` 会把它
 * 当路径，但多一道判断比依赖框架行为便宜。
 */
export function safeRedirect(value: unknown): string | null {
  if (typeof value !== 'string' || value === '') return null
  if (!value.startsWith('/') || value.startsWith('//')) return null
  return value
}
