/**
 * 当前这一条连接、落库历史与限流倒计时。
 *
 * 换会话时旧的必须先拆：服务端那条连接不会因为路由变了就自己断。历史一次
 * 性读回，页面只控制一次画多少条。
 */

import { computed, ref, shallowRef, watch, type ComputedRef } from 'vue'

import type { ApiFailure } from '../../api/client'
import { listMessages } from '../../api/sessions'
import { toFailure } from '../../shared/resource'
import type { Block, SourceItem } from '../../ws/blocks'
import { createConversation, type Conversation } from '../../ws/socket'

const HISTORY_CHUNK = 50

export function useChatConnection(deps: {
  activeId: ComputedRef<string | null>
  capability: () => string
  stages: () => readonly string[]
}) {
  const conversation = shallowRef<Conversation | null>(null)
  const history = ref<Block[]>([])
  const historyLoading = ref(false)
  const historyFailure = ref<ApiFailure | null>(null)
  const historyWindow = ref(HISTORY_CHUNK)

  const blocks = computed<readonly Block[]>(() => conversation.value?.blocks ?? [])
  const connectionStatus = computed(() => conversation.value?.status ?? 'idle')
  const running = computed(() => conversation.value?.running ?? false)
  const waiting = computed(() => conversation.value?.waiting ?? false)
  const reconnectAttempt = computed(() => conversation.value?.reconnectAttempt ?? 0)
  const activeElsewhere = computed(() => conversation.value?.activeElsewhere ?? false)
  const sources = computed<readonly SourceItem[]>(() => conversation.value?.sources ?? [])
  const citations = computed<ReadonlySet<number>>(
    () => conversation.value?.citations ?? new Set<number>(),
  )

  const visibleHistory = computed(() => history.value.slice(-historyWindow.value))
  const allBlocks = computed<readonly Block[]>(() => [...visibleHistory.value, ...blocks.value])

  /** 限流倒计时。服务端没说就退成"正在重连"，不编一个秒数。 */
  const retryLeft = ref<number | null>(null)
  let countdownTimer: number | null = null

  function stopCountdown(): void {
    if (countdownTimer !== null) {
      window.clearInterval(countdownTimer)
      countdownTimer = null
    }
  }

  watch(
    [() => conversation.value?.status, () => conversation.value?.retryAfterSeconds],
    ([status, seconds]) => {
      stopCountdown()
      if (status === 'rate_limited' && typeof seconds === 'number') {
        retryLeft.value = seconds
        countdownTimer = window.setInterval(() => {
          if (retryLeft.value === null || retryLeft.value <= 1) {
            stopCountdown()
            retryLeft.value = retryLeft.value === null ? null : 0
            return
          }
          retryLeft.value -= 1
        }, 1000)
        return
      }
      retryLeft.value = null
    },
  )

  /**
   * 换一条连接。**旧的必须先拆**：登出、切换会话、离开页面都不会让服务端那条
   * 连接自己断（《前端设计注意事项》§1.3）。
   */
  function openConversation(sessionId: string): void {
    conversation.value?.close()
    const next = createConversation(sessionId, {
      capability: () => deps.capability(),
      stages: () => [...deps.stages()],
      reloadHistory: () => {
        void resyncHistory()
      },
    })
    conversation.value = next
    void next.connect()
  }

  async function loadHistory(sessionId: string, keepWindow = false): Promise<void> {
    historyLoading.value = true
    historyFailure.value = null
    if (!keepWindow) historyWindow.value = HISTORY_CHUNK
    try {
      const { messages } = await listMessages(sessionId)
      // 连着切会话时会有两次读同时在飞，回来的顺序不保证。**只认还在屏幕上的那
      // 一个**。
      if (deps.activeId.value !== sessionId) return
      history.value = messages.map((message): Block =>
        message.role === 'user'
          ? { kind: 'user', text: message.content }
          : { kind: 'answer', text: message.content, complete: true },
      )
    } catch (cause) {
      if (deps.activeId.value !== sessionId) return
      historyFailure.value = toFailure(cause)
    } finally {
      if (deps.activeId.value === sessionId) historyLoading.value = false
    }
  }

  /**
   * 断线期间这一轮结束了：实时块不完整，落库的消息才是权威。
   * 先丢掉实时块，再重读历史 —— 顺序反过来会让旧块在重读回来的间隙里继续显示。
   */
  async function resyncHistory(): Promise<void> {
    const id = deps.activeId.value
    if (id === null) return
    conversation.value?.resetStream()
    await loadHistory(id, true)
  }

  async function openSession(sessionId: string): Promise<void> {
    await loadHistory(sessionId)
    // **读回来之后要确认还在这个会话上**：读的路上用户可能又换了一个。
    if (deps.activeId.value === sessionId) openConversation(sessionId)
  }

  /** 切会话/删会话/卸载时拆连接并清掉页面上的旧内容。 */
  function closeConversation(): void {
    conversation.value?.close()
    conversation.value = null
    history.value = []
    historyFailure.value = null
  }

  function clearHistory(): void {
    history.value = []
    historyFailure.value = null
  }

  return {
    conversation,
    history,
    historyLoading,
    historyFailure,
    historyWindow,
    blocks,
    connectionStatus,
    running,
    waiting,
    reconnectAttempt,
    activeElsewhere,
    sources,
    citations,
    visibleHistory,
    allBlocks,
    retryLeft,
    stopCountdown,
    openConversation,
    loadHistory,
    resyncHistory,
    openSession,
    closeConversation,
    clearHistory,
  }
}
