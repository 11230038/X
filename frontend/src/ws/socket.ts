/**
 * 一条对话：把服务端发来的帧变成界面上的一串块，把界面的动作变成命令。
 *
 * 顺序是《前端设计注意事项》§6 写的那条：**签票 → 连 → 问有没有活跃轮 → 读帧**。
 * 这里守住几条后端定死了的事：
 *
 * - **票每次连接都重新签，不复用。** 它不是一次性的，只是活得短（默认 30 秒，
 *   上限 5 分钟），所以"不缓存"是这一侧要自己守的纪律（§1.1）。
 * - **`done` 之后不能关连接。** 后面还会来一条 `session_meta`，流才算收尾
 *   （§3.2）；同一个会话的下一轮也在同一条连接上。
 * - **`lastSeq` 是续传的唯一坐标。** 逐事件按 `seq` 幂等：`seq <= lastSeq` 丢弃。
 * - **`command_id` 的去重窗口随连接消失。** 断了之后没收到 ack 的发送不能当作
 *   已发出，要标成"未确认"让用户手动重试，且重试用**新的** `command_id`。
 *
 * 连接的生命周期分两层：`connection.ts` 只管一次握手的成败与关闭码，这里管
 * "断了之后怎么办"——签新票、退避、`check_active_turn`、`resume`/`subscribe`、
 * 心跳。所有关闭码都重连（1000 也重连）：一条正常的关闭只说明这条连接结束
 * 了，不代表这个会话不该继续被看到。
 *
 * 收帧、命令与一轮边界分别在同目录的 `stream.ts`、`commands.ts`、`turn.ts`；
 * 这个文件只做连接生命周期与对象组装。
 */

import { computed, ref } from 'vue'

import { isApiFailure, type ApiFailure } from '../api/client'
import { wsTicket } from '../api/auth'
import { registerSocketCloser } from '../auth/auth-failure'

import type { ConnectionStatus, SourceItem } from './blocks'
import { createCommands } from './commands'
import { openLink, probeHandshakeRetry, type Link } from './connection'
import { COMMAND, FRAME, PROTOCOL_VERSION, type ServerFrame } from './protocol'
import { createStreamState, handleEvent, resetStreamState } from './stream'
import { createTurnHandlers } from './turn'
import type { Conversation, PendingSend, TurnContext } from './types'

export type {
  Conversation,
  PendingSend,
  SendOptions,
  TurnContext,
  TurnSelection,
} from './types'

const PING_INTERVAL_MS = 20_000
const PONG_TIMEOUT_MS = 45_000
const MAX_BACKOFF_MS = 30_000

/** 活着的那几条对话。凭据失效时由 `auth-failure` 一次性拆掉。 */
const live = new Set<Conversation>()
let closerRegistered = false

function registerCloser(): void {
  if (closerRegistered) return
  closerRegistered = true
  registerSocketCloser(() => {
    for (const conversation of [...live]) conversation.close()
    live.clear()
  })
}

export function createConversation(sessionId: string, context: TurnContext): Conversation {
  registerCloser()

  const state = createStreamState()
  const status = ref<ConnectionStatus>('idle')
  const failure = ref<ApiFailure | null>(null)
  const retryAfterSeconds = ref<number | null>(null)
  const reconnectAttempt = ref(0)

  /**
   * 断掉之后这个把手还在，只是 `isOpen()` 会说 false —— 所以"还能不能发"只有
   * 一个问法，不必再配一个 `connected` 布尔。
   */
  let link: Link | null = null
  /** 用户主动关掉（切会话、离页、登出）：不再重连。 */
  let stopped = false
  /** 这条对话**曾经**连上过。首次连不上与断了是两句不同的话。 */
  let everConnected = false
  let commandCounter = 0
  let reconnectTimer: number | null = null
  let heartbeatTimer: number | null = null
  let lastPongAt = 0
  let pongPending = false
  let reconnectNumber = 0
  let pendingStart: PendingSend | null = null

  const citations = computed<ReadonlySet<number>>(
    () => new Set(state.sources.value.map((source) => source.index)),
  )

  /* ── 发命令 ──────────────────────────────────────────────────────── */

  function nextCommandId(): string {
    commandCounter += 1
    return `c${commandCounter}`
  }

  function command(
    type: string,
    fields: Record<string, unknown> = {},
    withId = true,
  ): void {
    if (!link?.isOpen()) return
    link.send(
      JSON.stringify({
        protocol_version: PROTOCOL_VERSION,
        type,
        ...(withId ? { command_id: nextCommandId() } : {}),
        ...fields,
      }),
    )
  }

  const turnHandlers = createTurnHandlers({
    sessionId,
    state,
    command,
    closeLink: () => link?.close(),
    getPendingStart: () => pendingStart,
    setPendingStart: (pending) => {
      pendingStart = pending
    },
    reloadHistory: () => context.reloadHistory?.(),
  })

  const commands = createCommands({
    sessionId,
    capability: () => context.capability(),
    isOpen: () => link?.isOpen() === true,
    command,
    nextCommandId,
    state,
    setPendingStart: (pending) => {
      pendingStart = pending
    },
    clearUnconfirmed: turnHandlers.clearUnconfirmed,
  })

  /* ── 连接与重连 ──────────────────────────────────────────────────── */

  async function connect(): Promise<void> {
    stopped = false
    await attempt()
  }

  async function attempt(): Promise<void> {
    if (stopped) return
    clearReconnect()
    status.value = everConnected ? 'reconnecting' : 'connecting'
    failure.value = null
    retryAfterSeconds.value = null

    let ticket: string
    try {
      // 第 1 步是普通 HTTP 请求，所以它的问题**看得见** —— 401 与 429 都能
      // 分辨，这也正是它被做成两步的原因之一。
      ticket = (await wsTicket()).ticket
    } catch (cause) {
      if (stopped) return
      if (isApiFailure(cause)) {
        failure.value = cause.failure
        retryAfterSeconds.value = cause.failure.retryAfterSeconds ?? null
        if (cause.failure.kind === 'unauthorized') {
          // 全局 401 出口已经接管：拆连接、回登录页。不再重连。
          status.value = 'credential_lost'
          stopped = true
          return
        }
        if (cause.failure.kind === 'rate_limited') {
          status.value = 'rate_limited'
          scheduleReconnect(cause.failure.retryAfterSeconds ?? undefined)
          return
        }
      }
      scheduleReconnect()
      return
    }

    if (stopped) return
    await openSocket(ticket)
  }

  function openSocket(ticket: string): Promise<void> {
    const pending = openLink(ticket, {
      onOpen() {
        if (stopped) return
        everConnected = true
        reconnectNumber = 0
        reconnectAttempt.value = 0
        retryAfterSeconds.value = null
        failure.value = null
        status.value = 'open'
        lastPongAt = Date.now()
        pongPending = false
        startHeartbeat()
        // 连上先问这个会话有没有在跑的一轮。没有就是没有；有就要么续，要么订阅。
        command(COMMAND.CHECK_ACTIVE_TURN, { session_id: sessionId })
      },
      onFrame(raw) {
        if (!stopped) receive(raw)
      },
      async onRefused() {
        if (stopped) return
        // 浏览器只说"握手失败"。补一次普通请求，看看是不是 429、要等几秒。
        const seconds = await probeHandshakeRetry(ticket)
        if (stopped) return
        if (seconds !== null) {
          retryAfterSeconds.value = seconds
          status.value = 'rate_limited'
          scheduleReconnect(seconds)
          return
        }
        scheduleReconnect()
      },
      onLost(closeCode) {
        link = null
        stopHeartbeat()
        if (stopped) return
        // 断开时还没收到 ack：这条消息是否被服务端接受，这一侧不知道。按
        // "未确认"处理，让用户决定要不要重发（重发用新 command_id）。
        if (pendingStart && state.currentTurnId === '') {
          turnHandlers.markUnconfirmed(pendingStart)
          pendingStart = null
        }
        // **不把 running 判失败**：服务端那一轮可能还在跑，重连后会续上。
        // `closeCode` 只影响给不给用户看具体原因，不影响是否重连。
        if (closeCode === 1008) {
          // 慢消费者：跳过的事件必须靠 resume 补齐，退避也照常。
          lastPongAt = 0
        }
        scheduleReconnect()
      },
    })
    link = pending.link
    return pending.settled
  }

  function scheduleReconnect(afterSeconds?: number): void {
    if (stopped) return
    clearReconnect()
    reconnectNumber += 1
    reconnectAttempt.value = reconnectNumber

    const base = Math.min(MAX_BACKOFF_MS, 1000 * 2 ** Math.min(reconnectNumber - 1, 5))
    // 抖动：多个标签页同时掉线时不要在同一毫秒一起回来。
    const delay =
      afterSeconds !== undefined
        ? afterSeconds * 1000
        : Math.round(base * (0.7 + Math.random() * 0.6))

    if (status.value !== 'rate_limited') status.value = 'reconnecting'
    reconnectTimer = window.setTimeout(() => {
      reconnectTimer = null
      void attempt()
    }, delay)
  }

  function clearReconnect(): void {
    if (reconnectTimer !== null) {
      window.clearTimeout(reconnectTimer)
      reconnectTimer = null
    }
  }

  function startHeartbeat(): void {
    stopHeartbeat()
    heartbeatTimer = window.setInterval(tickHeartbeat, PING_INTERVAL_MS)
    document.addEventListener('visibilitychange', onVisibilityChange)
  }

  function stopHeartbeat(): void {
    if (heartbeatTimer !== null) {
      window.clearInterval(heartbeatTimer)
      heartbeatTimer = null
    }
    document.removeEventListener('visibilitychange', onVisibilityChange)
  }

  /**
   * 每 20 秒一次 `ping`。上一拍还没回就又到时间了，说明这条连接已经听不见
   * 了（TCP 可能还没察觉），主动拆掉重连。
   */
  function tickHeartbeat(): void {
    if (!link?.isOpen()) return
    if (pongPending && Date.now() - lastPongAt > PONG_TIMEOUT_MS) {
      link.close()
      return
    }
    pongPending = true
    command(COMMAND.PING, {}, false)
  }

  function onVisibilityChange(): void {
    if (document.visibilityState !== 'visible') return
    if (status.value === 'open' && Date.now() - lastPongAt > PONG_TIMEOUT_MS) {
      link?.close()
      // 若连接已经半死，`close()` 不一定会触发 onclose；安排一次重连兜底。
      scheduleReconnect()
    }
  }

  /* ── 收帧 ────────────────────────────────────────────────────────── */

  function receive(raw: string): void {
    let frame: ServerFrame
    try {
      frame = JSON.parse(raw) as ServerFrame
    } catch {
      // 服务端不会发读不懂的帧。真收到了，丢掉比让整个读循环停下要好。
      return
    }

    switch (frame.frame) {
      case FRAME.EVENT:
        handleEvent(state, frame, () => context.stages())
        return
      case FRAME.ACTIVE_TURN:
        turnHandlers.onActiveTurn(frame)
        return
      case FRAME.COMMAND_ACK:
        if (frame.turn_id) state.currentTurnId = frame.turn_id
        if (frame.command === COMMAND.START_TURN || frame.command === COMMAND.MESSAGE) {
          pendingStart = null
        }
        return
      case FRAME.PONG:
        pongPending = false
        lastPongAt = Date.now()
        return
      case FRAME.ERROR:
        turnHandlers.onErrorFrame(frame)
        return
      default:
        return
    }
  }

  /* ── 生命周期 ────────────────────────────────────────────────────── */

  function resetStream(): void {
    state.blocks.value = []
    state.lastSeq.value = 0
    state.currentTurnId = ''
    resetStreamState(state)
    state.running.value = false
    state.activeElsewhere.value = false
  }

  function close(): void {
    // 界面上没有"关掉连接"这个动作——这条是给组件卸载、切会话、登出用的。
    stopped = true
    clearReconnect()
    stopHeartbeat()
    link?.close()
    link = null
    status.value = 'idle'
    state.running.value = false
    state.waiting.value = false
    state.currentTurnId = ''
    pendingStart = null
    live.delete(conversation)
  }

  const conversation: Conversation = {
    get blocks() {
      return state.blocks.value
    },
    get status() {
      return status.value
    },
    get lastSeq() {
      return state.lastSeq.value
    },
    get failure() {
      return failure.value
    },
    get retryAfterSeconds() {
      return retryAfterSeconds.value
    },
    get running() {
      return state.running.value
    },
    get waiting() {
      return state.waiting.value
    },
    get reconnectAttempt() {
      return reconnectAttempt.value
    },
    get activeElsewhere() {
      return state.activeElsewhere.value
    },
    get unconfirmed() {
      return state.unconfirmed.value
    },
    get sources(): SourceItem[] {
      return state.sources.value
    },
    get citations() {
      return citations.value
    },
    get title() {
      return state.title.value
    },
    connect,
    send: commands.send,
    retryUnconfirmed: commands.retryUnconfirmed,
    reply: commands.reply,
    cancel: commands.cancel,
    resetStream,
    close,
  }

  live.add(conversation)
  return conversation
}
