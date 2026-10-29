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
import {
  useRouter,
  type RouteLocationNormalizedLoaded,
  type RouteLocationRaw,
} from 'vue-router'

import { isApiFailure } from '../../api/client'
import { describeFailure } from '../../api/errors'
import { createSession, getSession, type SessionKind } from '../../api/sessions'
import { refreshPermissions } from '../../auth/permissions'
import { CHAT } from '../../copy/chat'
import { CO_WRITER } from '../../copy/collaboration'
import type { SendOptions } from '../../ws/socket'

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
  /** 这个页面只开哪一种会话。 */
  kind: SessionKind
  /** 新建、删掉当前会话之后回到哪里。 */
  homeRoute: RouteLocationRaw
  /** 打开一个会话时路由名（对话页是 `conversation`，写作页是 `co-writer`）。 */
  sessionRoute: string
  /** 是否读 `?draft=` 预填。写作页不再从 URL 带第一句话。 */
  draftEnabled?: boolean
}

export function useChatPage(parts: ChatPageParts) {
  const router = useRouter()
  const { route, sessionsApi, caps, connection, materials, persona } = parts
  const kind = parts.kind

  /** 协同写作页带来的第一句话；只在进入时预填一次。 */
  const draft =
    parts.draftEnabled === false || typeof route.query.draft !== 'string'
      ? ''
      : route.query.draft

  /** 输入区上方那一句失败提示（建会话失败、连接没就绪）。 */
  const composerError = ref<string | null>(null)
  /** 建会话的 POST 还在飞。准备页靠它把按钮按住，防止连点建出两条会话。 */
  const starting = ref(false)
  /**
   * 会话建立后马上要发出去的那一句。
   *
   * 它记着**属于哪一条会话**：`startSession` 是先排队再 `router.push`，而
   * `activeId` 的 watcher 会在跳转后立刻跑。若这里不认 id，watcher 会把刚排
   * 好的第一句清掉，新会话就永远等不到开场白。
   */
  let queued: { text: string; options: SendOptions; sessionId: string } | null = null

  function describe(cause: unknown, fallback: string): string {
    return isApiFailure(cause) ? describeFailure(cause.failure) : fallback
  }

  /* ── 头部与状态条 ────────────────────────────────────────────────────── */

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
    if (starting.value) return
    composerError.value = null
    starting.value = true
    try {
      // 锁定人格的页面（题目问答）必须等它解析完再建会话：第一句话可能在
      // 人格列表返回之前就发出去了，那一轮就没有锁定的人格。
      await persona.ensure()
      if (persona.locked && persona.selectedId.value === '') {
        composerError.value = CHAT.personaNotReady
        return
      }
      const created = await createSession(kind)
      // 先塞进列表再跳转：列表行在跳过去的那一刻就要在。
      sessionsApi.sessions.value = [created, ...sessionsApi.sessions.value]
      // 词条记着 session id：watcher 在跳转后立刻跑，只认自己那条。
      queued = { text: firstMessage, options: materials.buildOptions(), sessionId: created.session_id }
      await router.push({ name: parts.sessionRoute, params: { sessionId: created.session_id } })
    } catch (cause) {
      composerError.value = describe(
        cause,
        kind === 'writing' ? CO_WRITER.startFailed : '新会话没能建起来，请重试',
      )
    } finally {
      starting.value = false
    }
  }

  async function onSend(text: string): Promise<void> {
    composerError.value = null
    if (sessionsApi.activeId.value === null) {
      await startSession(text)
      return
    }
    await persona.ensure()
    if (persona.locked && persona.selectedId.value === '') {
      composerError.value = CHAT.personaNotReady
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
    void router.push(parts.homeRoute)
  }

  /**
   * 这条会话是不是本页面的那一种。
   *
   * 列表已经按 kind 过滤过，所以直链打开另一边的会话时它不在列表里。能查到
   * 就按服务端说的算；查不到（404 / 断网）不拦，让读历史那条路去报它自己的
   * 错，别把一个网络失败说成"这条会话不在这里"。
   */
  async function belongsHere(sessionId: string): Promise<boolean> {
    const listed = sessionsApi.sessions.value.find((row) => row.session_id === sessionId)
    if (listed) return listed.kind === kind
    try {
      const record = await getSession(sessionId)
      return (record.kind ?? 'chat') === kind
    } catch {
      return true
    }
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
    const id = sessionsApi.activeId.value
    if (id === null) return
    if (!(await belongsHere(id))) {
      await router.replace(parts.homeRoute)
      return
    }
    // 查的这段时间里用户可能已经换了会话。
    if (sessionsApi.activeId.value === id) await connection.openSession(id)
  })

  watch(sessionsApi.activeId, (id, previous) => {
    if (id === previous) return
    // **旧连接现在就拆**：历史要一次往返才回来，中间那段时间不能还挂着上一个会话。
    connection.conversation.value?.close()
    connection.conversation.value = null
    connection.history.value = []
    connection.historyFailure.value = null
    // 只清掉不属于新会话的排队项：刚建好的那条要留着第一句。
    if (queued?.sessionId !== id) queued = null
    if (id === null) {
      connection.historyLoading.value = false
      return
    }
    void (async () => {
      if (!(await belongsHere(id))) {
        if (sessionsApi.activeId.value === id) await router.replace(parts.homeRoute)
        return
      }
      if (sessionsApi.activeId.value !== id) return
      await connection.openSession(id)
    })()
  })

  // 连接真正可用的那一刻把排队的第一句发出去。首次连接、重连都走这一条 ——
  // 排队只在"会话已建、连接还没好"的窗口里存在。
  watch(
    () => connection.conversation.value?.status,
    (status) => {
      if (status !== 'open' || queued === null || !connection.conversation.value) return
      // 连上的必须正是排队项所属的那一条：换会话后旧连接可能刚好在这一帧
      // 报 open，不能把新会话的第一句发到旧会话里去。
      if (queued.sessionId !== sessionsApi.activeId.value) return
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
    starting,
    panelStatus,
    startSession,
    onSend,
    onStop,
    newSession,
  }
}
