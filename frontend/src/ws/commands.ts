/**
 * 用户动作 → 命令帧。
 *
 * 这里只拼字段与维护本地块，不判断连接是否健康（`isOpen` 由连接层给出）。
 * `start_turn` 与 `submit_reply` 的差别、以及"重试必须换新 command_id"这两件
 * 事后端定死的事，都写在各自的函数旁边。
 */

import type { Block } from './blocks'
import { COMMAND } from './protocol'
import { pushBlock, resetStreamState, touch, type StreamState } from './stream'
import type { PendingSend, SendOptions } from './types'

export interface CommandDeps {
  sessionId: string
  capability: () => string
  isOpen: () => boolean
  command: (type: string, fields?: Record<string, unknown>, withId?: boolean) => void
  nextCommandId: () => string
  state: StreamState
  setPendingStart: (pending: PendingSend | null) => void
  clearUnconfirmed: () => void
}

export function createCommands(deps: CommandDeps) {
  const state = deps.state

  function commandFields(text: string, options: SendOptions): Record<string, unknown> {
    return {
      session_id: deps.sessionId,
      content: text,
      // 空串发一个空串：服务端把空白当成"你没指定"，于是自己按内容路由。
      capability: deps.capability(),
      ...(options.attachments && options.attachments.length > 0
        ? { attachments: options.attachments }
        : {}),
      ...(options.knowledgeBases && options.knowledgeBases.length > 0
        ? { knowledge_bases: options.knowledgeBases }
        : {}),
      ...(options.selection ? { selection: options.selection } : {}),
      // 不指定人格时不发这个字段；服务端把空串也当作"没指定"。
      ...(options.persona ? { persona: options.persona } : {}),
    }
  }

  function send(text: string, options: SendOptions = {}): void {
    if (!deps.isOpen()) return

    // 用户已经往下走了：那一条未确认的发送不再挂着。
    if (state.unconfirmed.value) deps.clearUnconfirmed()

    const block: Extract<Block, { kind: 'user' }> = { kind: 'user', text }
    pushBlock(state, block)
    resetStreamState(state)
    state.running.value = true
    state.waiting.value = false
    state.activeElsewhere.value = false
    // 新一轮的 seq 从 1 开始；在 ack 到达之前不知道 turn_id。
    state.currentTurnId = ''
    state.lastSeq.value = 0

    const commandId = deps.nextCommandId()
    deps.setPendingStart({ text, options, commandId, block })
    // 编号已经在这里取了，所以显式带上，不再让 `command()` 另取一个。
    deps.command(
      COMMAND.START_TURN,
      { ...commandFields(text, options), command_id: commandId },
      false,
    )
  }

  /** 回答服务端的澄清提问。**必须是 `submit_reply`**，见 `Conversation.waiting`。 */
  function reply(text: string): void {
    if (!deps.isOpen() || state.currentTurnId === '') return

    // **这一条不能走 `resetStreamState`**：`submit_reply` 是让**同一轮**接着
    // 跑，阶段与回答的状态都要原样留着。
    pushBlock(state, { kind: 'user', text })
    state.running.value = true
    state.waiting.value = false
    deps.command(COMMAND.SUBMIT_REPLY, { turn_id: state.currentTurnId, content: text })
  }

  function retryUnconfirmed(): void {
    const pending = state.unconfirmed.value
    if (!pending || !deps.isOpen()) return

    const block = [...state.blocks.value]
      .reverse()
      .find(
        (entry): entry is Extract<Block, { kind: 'user' }> =>
          entry.kind === 'user' && entry.unconfirmed === true,
      )
    if (block) {
      block.unconfirmed = false
      touch(state)
    }
    state.unconfirmed.value = null

    state.running.value = true
    state.waiting.value = false
    state.currentTurnId = ''
    state.lastSeq.value = 0

    // **新 command_id**：旧 id 的去重窗口属于上一条连接，已经不存在了。
    const commandId = deps.nextCommandId()
    const next: PendingSend = {
      text: pending.text,
      options: pending,
      commandId,
      block: block ?? { kind: 'user', text: pending.text },
    }
    deps.setPendingStart(next)
    if (!block) pushBlock(state, next.block)
    deps.command(
      COMMAND.START_TURN,
      { ...commandFields(pending.text, pending), command_id: commandId },
      false,
    )
  }

  function cancel(): void {
    if (!deps.isOpen() || state.currentTurnId === '') return
    // 取消**要点名是哪一轮**（`cancel needs a turn_id`）。
    deps.command(COMMAND.CANCEL, { turn_id: state.currentTurnId })
  }

  return { send, reply, retryUnconfirmed, cancel }
}
