/**
 * 活跃轮、错误帧与"未确认的发送"。
 *
 * 这三件事都发生在**一轮的边界**上：重连后先问有没有活跃轮，连接级错误要
 * 收口正在生成的内容，断线前的发送要标成未确认。把它们从收帧循环里拆出来，
 * 是为了让 `socket.ts` 只管握手与重连。
 */

import { describeCommandError } from '../api/errors'

import { closeThinking, pushBlock, resetStreamState, touch, type StreamState } from './stream'
import {
  COMMAND,
  FRAME,
  type ActiveTurnFrame,
  type ServerFrame,
} from './protocol'
import type { PendingSend } from './types'

export interface TurnHandlerDeps {
  sessionId: string
  state: StreamState
  command: (type: string, fields?: Record<string, unknown>, withId?: boolean) => void
  /** 主动关掉连接；重连由连接层安排。 */
  closeLink: () => void
  getPendingStart: () => PendingSend | null
  setPendingStart: (pending: PendingSend | null) => void
  reloadHistory: () => void
}

export function createTurnHandlers(deps: TurnHandlerDeps) {
  const state = deps.state

  /**
   * 重连（或首次连上）之后，先问这个会话有没有活跃轮。
   *
   * 同一个 `turn_id` 就续传；不同的就从头订阅并提示"正在另一处回答"，不与旧轮
   * 混排；没有活跃轮就把断线期间结束的那一轮交给历史重读。
   */
  function onActiveTurn(frame: ActiveTurnFrame): void {
    if (!frame.active || !frame.turn_id) {
      const wasRunning = state.running.value || state.waiting.value || state.currentTurnId !== ''
      state.running.value = false
      state.waiting.value = false
      state.currentTurnId = ''
      state.activeElsewhere.value = false
      if (wasRunning) {
        // 这一轮在断线期间结束了：实时块不完整，落库的消息才是权威。
        deps.reloadHistory()
      }
      return
    }

    if (frame.turn_id === state.currentTurnId) {
      deps.command(COMMAND.RESUME, {
        session_id: deps.sessionId,
        turn_id: frame.turn_id,
        after_seq: state.lastSeq.value,
      })
      return
    }

    // 不是这一侧认识的那一轮。可能是页面刷新前就在跑，也可能是另一个标签页
    // 刚发起的。两种都订阅，但**不混排**：旧块丢掉，从这一轮的第一条事件重放。
    state.currentTurnId = frame.turn_id
    state.blocks.value = []
    state.lastSeq.value = 0
    resetStreamState(state)
    state.running.value = true
    state.waiting.value = false
    state.activeElsewhere.value = true
    // 服务端保证同一 turn 的事件从 1 连续编号，所以从 0 开始最安全。
    deps.command(COMMAND.SUBSCRIBE, {
      session_id: deps.sessionId,
      turn_id: frame.turn_id,
      after_seq: 0,
    })
  }

  function onErrorFrame(frame: Extract<ServerFrame, { frame: typeof FRAME.ERROR }>): void {
    const described = describeCommandError(frame.error.code)

    // 一个被拒的发送：那条消息确实没被接受，标成未确认让用户重试。
    const pending = deps.getPendingStart()
    if (pending) {
      markUnconfirmed(pending)
      deps.setPendingStart(null)
    }
    state.running.value = false
    state.waiting.value = false
    closeThinking(state)

    // 这一帧是**连接的**错误，不是某一轮的事件，所以它不带 `done`。
    pushBlock(state, {
      kind: 'error',
      text: described.text,
      code: frame.error.code,
      retryable: described.retryable,
    })

    if (frame.terminal) {
      // 服务端不会再读一条命令。关掉之后由 `onLost` 安排重连。
      deps.closeLink()
    }
  }

  function markUnconfirmed(pending: PendingSend): void {
    pending.block.unconfirmed = true
    touch(state)
    state.running.value = false
    state.unconfirmed.value = { text: pending.text, ...pending.options }
  }

  function clearUnconfirmed(): void {
    for (const block of state.blocks.value) {
      if (block.kind === 'user' && block.unconfirmed) {
        block.unconfirmed = false
        touch(state)
        break
      }
    }
    state.unconfirmed.value = null
  }

  return { onActiveTurn, onErrorFrame, markUnconfirmed, clearUnconfirmed }
}
