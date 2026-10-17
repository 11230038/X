/**
 * 把服务端的 15 种事件变成界面上的一串块。
 *
 * 这里只管"已收下的帧怎么落成块"，不碰连接、重连与命令。水位线（`lastSeq`）
 * 的判重在 `socket.ts` 里完成，因为"收到过"是连接层的事实。
 */

import { ref, shallowRef, type Ref, type ShallowRef } from 'vue'

import {
  describeArgs,
  META,
  stringMeta,
  type Block,
  type SourceItem,
  type StagePlan,
} from './blocks'
import { EVENT_TYPE, type EventFrame } from './protocol'
import type { SendOptions } from './types'

/** 一条对话在流式期间的可变状态。连接层与事件层共用同一份。 */
export interface StreamState {
  blocks: ShallowRef<Block[]>
  lastSeq: Ref<number>
  running: Ref<boolean>
  waiting: Ref<boolean>
  activeElsewhere: Ref<boolean>
  sources: Ref<SourceItem[]>
  title: Ref<string>
  /** 连接断在一句发送收到 ack 之前。重试会换一个新的 `command_id`。 */
  unconfirmed: Ref<(SendOptions & { text: string }) | null>
  /** 这一轮在服务端的 id。事件帧都带着它，`submit_reply`/`cancel` 靠它点名。 */
  currentTurnId: string
  /** 正在累加的那一块回答；`null` 表示此刻没有在生成的回答。 */
  openAnswer: Extract<Block, { kind: 'answer' }> | null
  /** 已经开了还没收尾的那个阶段。`stage_end` 只带名字，靠它配回去。 */
  openStage: Extract<Block, { kind: 'stage' }> | null
  /** 正在累加的思考块。任何别的类型到来就收口。 */
  openThinking: Extract<Block, { kind: 'thinking' }> | null
}

export function createStreamState(): StreamState {
  return {
    // shallow：每次变更都换掉整个数组（见 `touch`），所以不需要 Vue 去逐层代理
    // 每一个块 —— 一个正在生成的回答每 50ms 追加一次，深代理会把这些追加变成
    // 对整棵列表的依赖追踪。
    blocks: shallowRef<Block[]>([]),
    lastSeq: ref(0),
    running: ref(false),
    waiting: ref(false),
    activeElsewhere: ref(false),
    sources: ref<SourceItem[]>([]),
    title: ref(''),
    unconfirmed: ref<(SendOptions & { text: string }) | null>(null),
    currentTurnId: '',
    openAnswer: null,
    openStage: null,
    openThinking: null,
  }
}

function numberMeta(meta: Record<string, unknown>, key: string): number | undefined {
  const value = meta[key]
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined
}

/** `metadata.sources` 是数组；形态不对就当没有，不造一条假来源。 */
function parseSources(value: unknown): SourceItem[] {
  if (!Array.isArray(value)) return []
  return value.filter((item): item is SourceItem => {
    if (typeof item !== 'object' || item === null) return false
    const source = item as Partial<SourceItem>
    return (
      typeof source.index === 'number' &&
      typeof source.document_id === 'string' &&
      typeof source.file_id === 'string' &&
      typeof source.title === 'string' &&
      typeof source.snippet === 'string' &&
      typeof source.locator === 'object' &&
      source.locator !== null
    )
  })
}

export function pushBlock(state: StreamState, block: Block): void {
  state.blocks.value = [...state.blocks.value, block]
}

/**
 * 就地改过的块要让列表重新渲染一次。`shallowRef` 只在 `.value` 被赋值时触
 * 发，所以这里必须换掉数组本身 —— 但**不必**深拷每一个元素。
 */
export function touch(state: StreamState): void {
  state.blocks.value = [...state.blocks.value]
}

export function closeThinking(state: StreamState): void {
  state.openThinking = null
}

export function resetStreamState(state: StreamState): void {
  state.openAnswer = null
  state.openStage = null
  state.openThinking = null
  state.sources.value = []
  state.waiting.value = false
}

export function handleEvent(
  state: StreamState,
  frame: EventFrame,
  stages: () => StagePlan,
): void {
  // 一条连接可能先后看到两轮。事件里的 turn_id 变了就是新一轮，水位线归零。
  if (frame.turn_id && frame.turn_id !== state.currentTurnId) {
    if (state.currentTurnId !== '') {
      state.currentTurnId = frame.turn_id
      state.lastSeq.value = 0
      resetStreamState(state)
      state.running.value = true
    } else {
      state.currentTurnId = frame.turn_id
    }
  }

  // 水位线：**先判重再处理**。处理里如果抛了，这一条也已经算收到了。
  if (frame.seq <= state.lastSeq.value) return
  state.lastSeq.value = frame.seq

  const meta = frame.metadata ?? {}

  // 思考是唯一一种可以连续追加的类型；别的事件一到，上一段思考就收口。
  if (frame.type !== EVENT_TYPE.THINKING) closeThinking(state)

  switch (frame.type) {
    case EVENT_TYPE.THINKING: {
      let block = state.openThinking
      if (!block) {
        block = { kind: 'thinking', text: '' }
        state.openThinking = block
        pushBlock(state, block)
      }
      block.text += frame.content ?? ''
      touch(state)
      return
    }
    case EVENT_TYPE.OBSERVATION: {
      const text = (frame.content ?? '').trim()
      if (text !== '') pushBlock(state, { kind: 'observation', text })
      return
    }
    case EVENT_TYPE.PROGRESS: {
      const step = numberMeta(meta, META.STEP) ?? 0
      const total = numberMeta(meta, META.TOTAL) ?? 0
      const label = stringMeta(meta, META.LABEL) ?? ''
      if (step > 0 || total > 0 || label !== '') {
        pushBlock(state, { kind: 'progress', step, total, label })
      }
      return
    }
    case EVENT_TYPE.SOURCES: {
      const parsed = parseSources(meta[META.SOURCES])
      state.sources.value = parsed
      if (parsed.length > 0) pushBlock(state, { kind: 'sources', sources: parsed })
      return
    }
    case EVENT_TYPE.CONTENT: {
      // 增量，且已经被服务端按 50ms / 1024 字节合并过——不是一个 token 一帧。
      let block = state.openAnswer
      if (!block) {
        block = { kind: 'answer', text: '', complete: false }
        state.openAnswer = block
        pushBlock(state, block)
      }
      block.text += frame.content ?? ''
      touch(state)
      return
    }
    case EVENT_TYPE.RESULT: {
      // 整段的完整回答。**用它收口并替换增量**：断流时增量会缺一段，只有
      // 这里说的是全的。
      const content = frame.content ?? ''
      const block = state.openAnswer
      if (block) {
        if (content !== '') block.text = content
        block.complete = true
        state.openAnswer = null
        touch(state)
      } else if (content !== '') {
        pushBlock(state, { kind: 'answer', text: content, complete: true })
      }
      return
    }
    case EVENT_TYPE.STAGE_START: {
      const name = frame.stage ?? ''
      const plan = stages()
      // `indexOf` 找不到时是 0，`+1` 之后是 0 —— 恰好就是"不显示第几步"。
      const block: Extract<Block, { kind: 'stage' }> = {
        kind: 'stage',
        name,
        index: plan.indexOf(name) + 1,
        total: plan.length,
        done: false,
      }
      state.openStage = block
      pushBlock(state, block)
      return
    }
    case EVENT_TYPE.STAGE_END: {
      if (state.openStage && state.openStage.name === (frame.stage ?? '')) {
        state.openStage.done = true
        state.openStage = null
        touch(state)
      }
      return
    }
    case EVENT_TYPE.TOOL_CALL: {
      // 工具名在 `Content`，参数在 `Metadata.args`。
      const args = describeArgs(meta[META.ARGS])
      pushBlock(state, {
        kind: 'tool',
        name: frame.content ?? '',
        callId: stringMeta(meta, META.CALL_ID) ?? '',
        ...(args ? { args } : {}),
      })
      return
    }
    case EVENT_TYPE.TOOL_RESULT: {
      // 按 `call_id` 配对，**不按名字**：同一个工具可以在一个阶段里连着调两
      // 次，按名字配会把第二次的结果写到第一次上面。
      const callId = stringMeta(meta, META.CALL_ID) ?? ''
      const target = state.blocks.value.find(
        (block) => block.kind === 'tool' && block.callId === callId,
      )
      if (target && target.kind === 'tool') {
        target.result = frame.content ?? ''
        target.ok = meta[META.SUCCESS] === true
        touch(state)
      }
      return
    }
    case EVENT_TYPE.WAIT_FOR_INPUT: {
      // 整轮暂停：**没有东西在跑了，但这一轮也还没结束**。
      state.running.value = false
      state.waiting.value = true
      // 提问之前那段回答到此为止，**必须收口**（否则回答完的文字会跑到提问
      // 上面去）。
      if (state.openAnswer) {
        state.openAnswer.complete = true
        state.openAnswer = null
        touch(state)
      }
      pushBlock(state, { kind: 'question', text: frame.content ?? '' })
      return
    }
    case EVENT_TYPE.ERROR: {
      state.running.value = false
      state.waiting.value = false
      // `Content` 是给客户端看的那句话，码在 `Metadata.error_code`。
      const quotaItem = stringMeta(meta, META.QUOTA_ITEM)
      const quotaResetsAt = stringMeta(meta, META.QUOTA_RESETS_AT)
      pushBlock(state, {
        kind: 'error',
        text: frame.content ?? '',
        code: stringMeta(meta, META.ERROR_CODE) ?? '',
        retryable: meta[META.RETRYABLE] === true,
        ...(quotaItem !== undefined ? { quotaItem } : {}),
        ...(quotaResetsAt !== undefined ? { quotaResetsAt } : {}),
      })
      return
    }
    case EVENT_TYPE.DONE: {
      // 终态。**连接不关** —— 后面还有一条 `session_meta`，而且同一条连接上
      // 可以接着发下一轮。
      state.running.value = false
      state.waiting.value = false
      state.activeElsewhere.value = false
      if (state.openAnswer) {
        state.openAnswer.complete = true
        state.openAnswer = null
        touch(state)
      }
      // `partial_response` 说的是"**已经有回答文字产出过了**"，不是"这一段不
      // 完整"。`failed` + `partial` 才是"没写完，你能看到的那一段是半截的"。
      pushBlock(state, {
        kind: 'done',
        status: stringMeta(meta, META.STATUS) ?? '',
        partial: meta[META.PARTIAL_RESPONSE] === true,
      })
      return
    }
    case EVENT_TYPE.SESSION_META: {
      // 标题由服务端在这一轮结束后给出。**不再用 `done` 去猜**，也不再为
      // 了标题重读整个列表。
      const next = (frame.content ?? '').trim()
      if (next !== '') state.title.value = next
      return
    }
    case EVENT_TYPE.SESSION:
      // 路由与租约的元数据随第一帧到来；界面目前不显示，但水位线要记。
      return
    default:
      return
  }
}
