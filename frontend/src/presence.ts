/**
 * 在线心跳：登录之后，只要这个标签页看得见，每分钟记一次"我在"。
 *
 * 为什么是每分钟一次而不是开一次、关一次：页面关掉没有可靠的信号 —— 浏览器
 * 被杀掉、网络断掉、机器休眠，都不会走任何"结束"回调。所以服务端记的是
 * *活过的分钟*，客户端只负责在活着的时候持续报到，掉线了自然就停了。
 *
 * 标签页不可见时不打：在后台放着的页面不是"在学习"，把它算进时长会让这个数
 * 失去意义。切回来时立刻补一次，那一分钟算作开始。
 *
 * 心跳失败不打扰任何人：它一分钟一次，偶发的 500 或断网下一分钟会自己好。
 * 但 401 例外 —— 那由 `api/client.ts` 的全局出口处理，和别的请求一样。
 */

import { ref, watch } from 'vue'

import { getPresence, touchPresence, type Presence } from './api/presence'
import { isSignedIn } from './session'

/** 最近一次拿到的统计；首页读它，心跳写它。 */
export const presenceSummary = ref<Presence | null>(null)

const BEAT_MS = 60_000

let timer: number | null = null

async function beat(): Promise<void> {
  if (document.hidden) return
  try {
    presenceSummary.value = await touchPresence()
  } catch {
    // 下一分钟再试。这一分钟的丢失是真实的（那段时间没有报到），不补记。
  }
}

/** 只读一次，不写。首页第一次画的时候用它，免得等一分钟才看到数。 */
export async function readPresence(): Promise<void> {
  try {
    presenceSummary.value = await getPresence()
  } catch {
    // 读不到就保持 `null`，卡片画横线。
  }
}

function onVisibilityChange(): void {
  if (!document.hidden) void beat()
}

function start(): void {
  if (timer !== null) return
  void beat()
  timer = window.setInterval(() => {
    void beat()
  }, BEAT_MS)
  document.addEventListener('visibilitychange', onVisibilityChange)
}

function stop(): void {
  if (timer !== null) {
    window.clearInterval(timer)
    timer = null
  }
  document.removeEventListener('visibilitychange', onVisibilityChange)
  // 登出之后这些数不再是"这个人"的，留着会在下一个人登录前的一瞬间画出来。
  presenceSummary.value = null
}

/**
 * 跟着登录态起停心跳。在根组件里调一次。
 *
 * 用 `watch(..., { immediate: true })` 而不是在 `onMounted` 里判断一次：登出
 * 之后又登录（换账号）时，心跳要跟着新的 token 重新开始。
 */
export function startPresenceHeartbeat(): void {
  watch(
    isSignedIn,
    (signed) => {
      if (signed) start()
      else stop()
    },
    { immediate: true },
  )
}
