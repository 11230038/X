/**
 * 登录成功后的启动动作，登录页与注册页共用。
 *
 * 三件事：
 * 1. 复位 401 单飞标记（上一个账号已经被处理过的那次不该挡住这个账号）；
 * 2. 取账号本身、权限快照与能力清单；
 * 3. 都不阻塞跳转 —— 取失败只是页面顶部挂一条部分失败条，不能把人关在登录页。
 */

import { me } from '../api/auth'
import { replaceAccount } from '../session'
import { refreshCapabilities } from '../shared/capabilities'
import { resetAuthFailure } from './auth-failure'
import { refreshPermissions } from './permissions'

export async function bootstrapSignedIn(): Promise<void> {
  resetAuthFailure()
  await Promise.allSettled([refreshAccount(), refreshPermissions(), refreshCapabilities()])
}

/**
 * 重新问一次"我是谁"。
 *
 * 登录时它和登录响应是同一个账号，看起来多余；但启动时本地那份账号是上次
 * 存下来的，而 `super_admin` 由部署的白名单决定 —— 白名单改了，同一张 token
 * 解出来的账号就变了。不刷新的话，改完配置要重新登录一次才看得到按钮，而重
 * 新登录对白名单毫无影响。失败只是不更新，本地那份还在，和另外两个刷新一样
 * 不阻塞跳转。
 */
async function refreshAccount(): Promise<void> {
  replaceAccount(await me())
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
