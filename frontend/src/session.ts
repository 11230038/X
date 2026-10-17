/**
 * 登录态：token 存在哪、谁是当前账号、什么时候清掉。
 *
 * **这个模块是唯一写 token 的地方**，`src/api/client.ts` 只持有它。
 */

import { computed, ref } from 'vue'

import { setAuthToken } from './api/client'
import type { Account, Session } from './api/auth'

/** 与重写之前那一版前端用的是同一个键名，见 `.agents/security.md`。 */
const TOKEN_KEY = 'auth_token'
const ACCOUNT_KEY = 'auth_account'
const EXPIRES_KEY = 'auth_expires_at'

/**
 * token 放在 `localStorage` 而不是 httpOnly Cookie。**取舍是明确的**：Cookie
 * 方案需要 CSRF 防护与服务端 Cookie 处理，这一版都没有；代价是任意同源脚本
 * 都能读到 token（XSS 面）。所以界面里**不许出现 `v-html`** —— Vue 的文本插
 * 值会把内容当文本渲染，这是这条取舍成立的前提，不是风格偏好。
 *
 * 登出**不只是本地清理**：它会把账号的所有设备一起下线（后端写
 * `users.token_valid_after`）。所以清完之后界面的落点是登录页，且文案要说
 * "所有设备都需要重新登录"——不是"所有设备都已断开"。
 */

const account = ref<Account | null>(null)
const token = ref<string | null>(null)

export const currentAccount = computed(() => account.value)
export const isSignedIn = computed(() => token.value !== null)

/** 从存储里恢复。启动时调一次。 */
export function restoreSession(): void {
  const storedToken = localStorage.getItem(TOKEN_KEY)
  const storedAccount = localStorage.getItem(ACCOUNT_KEY)
  if (!storedToken || !storedAccount) {
    return
  }

  // 过期的 token 直接丢掉，不发一个注定 401 的请求去发现这件事。
  //
  // 这只滤掉"时间到了"这一种。**被撤回的 token 看不出来** —— `token_valid_after`
  // 在服务端，只有请求回来才知道。所以启动时宁可留着一个可能已经作废的
  // token，让第一个请求去回答，也不要在这里猜。
  const expiresAt = localStorage.getItem(EXPIRES_KEY)
  if (expiresAt && Date.parse(expiresAt) <= Date.now()) {
    clearSession()
    return
  }

  try {
    account.value = JSON.parse(storedAccount) as Account
  } catch {
    clearSession()
    return
  }
  token.value = storedToken
  setAuthToken(storedToken)
}

/** 登录或注册成功之后：注册也走这里，因为后端回的是一份完整的登录态。 */
export function startSession(session: Session): void {
  token.value = session.token
  account.value = session.user
  setAuthToken(session.token)

  localStorage.setItem(TOKEN_KEY, session.token)
  localStorage.setItem(ACCOUNT_KEY, JSON.stringify(session.user))
  localStorage.setItem(EXPIRES_KEY, session.expires_at)
}

/**
 * 用服务端刚回答的账号替换本地那份，**不碰 token**。
 *
 * 存在的理由只有一个：`super_admin` 是部署配置投影到账号上的字段，白名单改
 * 了之后同一张 token 解出来的账号会变。刷新页面时重新问一次 `/auth/me`，
 * 按钮的显隐就跟着变，不需要重新登录 —— 而重新登录本来也改变不了白名单。
 *
 * 不写 token 是硬约束：这个模块是唯一写 token 的地方，`replaceAccount` 只
 * 负责账号那一半，写错另一半的入口不在这里。
 */
export function replaceAccount(next: Account): void {
  account.value = next
  localStorage.setItem(ACCOUNT_KEY, JSON.stringify(next))
}

/**
 * 清掉本地登录态。**不发请求** —— 凭据已经被服务端撤回了（或本来就没
 * 有），再去调登出只会拿到另一个 401。
 *
 * 界面上"用户主动登出"和"发现凭据失效了"是两个不同的动作：前者要先调
 * `POST /auth/logout` 再走这里，后者直接走这里。共同点是落点相同。
 */
export function clearSession(): void {
  token.value = null
  account.value = null
  setAuthToken(null)
  localStorage.removeItem(TOKEN_KEY)
  localStorage.removeItem(ACCOUNT_KEY)
  localStorage.removeItem(EXPIRES_KEY)
}
