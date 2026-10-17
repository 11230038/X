/**
 * 对话页的**行为**：发什么、建会话、排队第一句、切会话时拆连接、离开时清理。
 *
 * 这一层与 `Chat.vue` 的分工是：页面负责把各块状态摆到模板上，这里负责在状态
 * 之间做决定。它不渲染任何东西，也不持有自己的持久状态 —— 拿到的都是页面已经
 * 建好的 composable。
 *
 * 两条容易写错的规则留在这里：
 * - **排队只在"会话已建、连接还没好"的窗口里存在**。连接一开口就发出去，之后
 *   不再补发；重连不算新窗口。
 * - **切会话时先拆旧连接，再等历史**。历史要一次往返才回来，中间那段时间不能
 *   还挂着上一个会话。
 */

import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRouter, type RouteLocationNormalizedLoaded } from 'vue-router'

import { isApiFailure } from '../../api/client'
import { describeFailure } from '../../api/errors'
import { createSession } from '../../api/sessions'
import { refreshPermissions } from '../../auth/permissions'
import { CHAT } from '../../copy/chat'
import type { SendOptions } from '../../ws/socket'
import { sessionLabel } from '../../components/chat/session-label'

import type { useChatCapabilities } from './useChatCapabilities'
import type { useChatConnection } from './useChatConnection'
import type { useChatMaterials } from './useChatMaterials'
import type { useChatPersona } from './useChatPersona'
import type { useChatSessions } from './useChatSessions'

type Sessions = ReturnType<typeof useChatSessions>
type Capabilities = ReturnType<typeof useChatCapabilities>
type Connection = ReturnType<typeof useChatConnection>
type Materials = ReturnType<typeof useChatMaterials>
type Personas = ReturnType<typeof useChatPersona>

export interface ChatPageParts {
  route: RouteLocationNormalizedLoaded
  sessionsApi: Sessions
  caps: Capabilities
  connection: Connection
  materials: Materials
  persona: Personas
}

export function useChatPage(parts: ChatPageParts) {
  const router = useRouter()
  const { route, sessionsApi, caps, connection, materials, persona } = parts

  /** 协同写作页带来的第一句话；只在进入时预填一次。 */
  const draft = typeof route.query.draft === 'string' ? route.query.draft : ''

  /** 输入区上方那一句失败提示（建会话失败、连接没就绪）。 */
  const composerError = ref<string | null>(null)
  /** 会话建立后马上要发出去的那一句。见 `startSession`。 */
  let queued: { text: string; options: SendOptions } | null = null

  function describe(cause: unknown, fallback: string): string {
    return isApiFailure(cause) ? describeFailure(cause.failure) : fallback
  }

  /* ── 头部与状态条 ────────────────────────────────────────────────────── */

  /** 内容区头部的那一行。实时标题优先于列表里的旧值。 */
  const headerTitle = computed(() => {
    const live = connection.conversation.value?.title ?? ''
    if (live !== '') return live
    return sessionsApi.activeSession.value ? sessionLabel(sessionsApi.activeSession.value) : ''
  })

  /**
   * 收合后那条细栏上的一句话。§3.5 要的是"用户能随时知道 AI 还在干活 / 干完了"，
   * 所以它报的是**当前状态**。
   */
  const panelStatus = computed(() => {
    if (connection.connectionStatus.value === 'reconnecting') return CHAT.reconnectingShort
    if (connection.connectionStatus.value === 'connecting') return CHAT.connecting
    if (connection.connectionStatus.value === 'rate_limited') {
      return connection.retryLeft.value !== null
        ? CHAT.rateLimited(connection.retryLeft.value)
        : CHAT.rateLimited(0)
    }
    if (connection.running.value) {
      const lastStage = [...connection.blocks.value].reverse().find((block) => block.kind === 'stage')
      return lastStage && lastStage.kind === 'stage' ? lastStage.name : '进行中'
    }
    if (connection.waiting.value) return CHAT.waiting
    return '空闲'
  })

  /* ── 动作 ────────────────────────────────────────────────────────────── */

  async function startSession(firstMessage: string): Promise<void> {
    composerError.value = null
    try {
      const created = await createSession()
      // 先塞进列表再跳转：列表行在跳过去的那一刻就要在。
      sessionsApi.sessions.value = [created, ...sessionsApi.sessions.value]
      queued = { text: firstMessage, options: materials.buildOptions() }
      await router.push({ name: 'conversation', params: { sessionId: created.session_id } })
    } catch (cause) {
      composerError.value = describe(cause, '新会话没能建起来，请重试')
    }
  }

  function onSend(text: string): void {
    composerError.value = null
    if (sessionsApi.activeId.value === null) {
      void startSession(text)
      return
    }
    const current = connection.conversation.value
    if (!current || current.status !== 'open') {
      composerError.value = CHAT.offlineDraft
      return
    }
    // 服务端在等一个回答时，发出去的是 `submit_reply`。发成新一轮会被拒。
    if (current.waiting) {
      current.reply(text)
      return
    }
    current.send(text, materials.buildOptions())
    materials.clearMaterials()
  }

  function onStop(): void {
    connection.conversation.value?.cancel()
  }

  function newSession(): void {
    if (sessionsApi.activeId.value === null) return
    void router.push({ name: 'chat' })
  }

  /* ── 生命周期 ────────────────────────────────────────────────────────── */

  onMounted(async () => {
    materials.readSelectionFromQuery()
    // 登录后 `main.ts` 已经引导过一次；这里再确保一次，直链进来也不会缺能力表。
    await Promise.all([
      caps.loadCapabilities(),
      sessionsApi.loadSessions(true),
      refreshPermissions(),
      persona.load(),
    ])
    if (sessionsApi.activeId.value) {
      await connection.openSession(sessionsApi.activeId.value)
    }
  })

  watch(sessionsApi.activeId, (id, previous) => {
    if (id === previous) return
    // **旧连接现在就拆**：历史要一次往返才回来，中间那段时间不能还挂着上一个会话。
    connection.conversation.value?.close()
    connection.conversation.value = null
    connection.history.value = []
    connection.historyFailure.value = null
    queued = null
    if (id === null) {
      connection.historyLoading.value = false
      return
    }
    void connection.openSession(id)
  })

  // 连接真正可用的那一刻把排队的第一句发出去。首次连接、重连都走这一条 ——
  // 排队只在"会话已建、连接还没好"的窗口里存在。
  watch(
    () => connection.conversation.value?.status,
    (status) => {
      if (status !== 'open' || queued === null || !connection.conversation.value) return
      const pending = queued
      queued = null
      connection.conversation.value.send(pending.text, pending.options)
      materials.clearMaterials()
    },
  )

  // 服务端给的标题到了就地改这一行，不重读整个列表（§3.2）。
  watch(
    () => connection.conversation.value?.title,
    (title) => {
      if (!title) return
      const id = sessionsApi.activeId.value
      if (id === null) return
      sessionsApi.patchTitle(id, title)
    },
  )

  // 离开对话页要拆掉连接。
  onBeforeUnmount(() => {
    connection.stopCountdown()
    connection.conversation.value?.close()
    connection.conversation.value = null
  })

  return {
    draft,
    composerError,
    headerTitle,
    panelStatus,
    startSession,
    onSend,
    onStop,
    newSession,
  }
}
