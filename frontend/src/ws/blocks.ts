/**
 * 一条会话流里有哪些块，以及连接处于什么状态。
 *
 * 这里是**界面侧对协议的词汇表**，与 `protocol.ts` 分工不同：那个文件是后端帧
 * 与命令的逐字镜像，改后端就得同步改它；这个文件是"那些帧在屏幕上变成什么"，
 * 后端换一版协议不一定动它，界面重新排一次版却一定会动它。
 *
 * 放在 `ws/` 下而不是组件里，是因为谁渲染这些块谁就得知道它们的形状 —— 组件
 * 之间互相导入类型会把两个组件绑在一起，导入这里则只是各自面向同一份词汇。
 */

/**
 * 补上 `event.Event` 里那几个**内容不在 `Content` 里**的负载的键名
 * （`internal/event/event.go` 的 `MetaArgs` 等）。写在这里而不是从别处导入，
 * 是因为它们是协议的一部分，与 `protocol.ts` 里那些常量同类。
 */
export const META = {
  /** `tool_call` 的调用参数。可能是一个对象，也可能是模型没给出合法 JSON 时的原文。 */
  ARGS: 'args',
  /** `tool_call` 与 `tool_result` 配对的键。同名工具可以连着调两次，靠它分。 */
  CALL_ID: 'call_id',
  /** `tool_result` 成功与否。不用读文本就知道这次调用没成。 */
  SUCCESS: 'success',
  /** 结束类事件上的失败码。 */
  ERROR_CODE: 'error_code',
  /** 这一轮重来一次有没有可能成功。 */
  RETRYABLE: 'retryable',
  /** `done` 上的落点：completed / cancelled / failed 之类。 */
  STATUS: 'status',
  /** `done` 上「已经有回答文字产出过」。 */
  PARTIAL_RESPONSE: 'partial_response',
  /** `sources` 事件上的来源数组。 */
  SOURCES: 'sources',
  /** `result` 上对不上的引用编号。 */
  UNRESOLVED_CITATIONS: 'unresolved_citations',
  /** `progress` 的三个键。 */
  STEP: 'step',
  TOTAL: 'total',
  LABEL: 'label',
  /** 额度耗尽时是哪一项、什么时候恢复。 */
  QUOTA_ITEM: 'quota_item',
  QUOTA_RESETS_AT: 'quota_resets_at',
} as const

/** 一条引用来源。字段与后端 `event.Source` 一致，不做裁剪。 */
export interface SourceItem {
  index: number
  document_id: string
  file_id: string
  title: string
  engine?: string
  locator: {
    kind: string
    chunk_id?: string
    ordinal?: number
    char_start: number
    char_end: number
    page?: number | null
  }
  snippet: string
  score?: number
  revision: number
}

/** 会话流里的一块。每一种消息类型是它自己的一个变体。 */
export type Block =
  | { kind: 'user'; text: string; unconfirmed?: boolean }
  /** 回答。`text` 是 `content` 事件累加出来的增量。 */
  | { kind: 'answer'; text: string; complete: boolean }
  /** 阶段开始 / 结束。名字与总数都来自能力，不是写死的。 */
  | { kind: 'stage'; name: string; index: number; total: number; done: boolean }
  /** 工具调用。参数与结果都可能还没有。 */
  | { kind: 'tool'; name: string; callId: string; args?: string; result?: string; ok?: boolean }
  /** 模型在思考。`content` 是推理增量，不是回答。 */
  | { kind: 'thinking'; text: string }
  /** 模型对材料/环境的观察，通常是完整的一段。 */
  | { kind: 'observation'; text: string }
  /** 阶段内部的多步进度，与“第几个阶段”是两件事。 */
  | { kind: 'progress'; step: number; total: number; label: string }
  /** 这一轮拿到的来源。引用编号只在其中才可点。 */
  | { kind: 'sources'; sources: SourceItem[] }
  /** 模型中途停下来等回答，整轮暂停。 */
  | { kind: 'question'; text: string }
  | {
      kind: 'error'
      text: string
      code: string
      retryable: boolean
      /** 额度类失败时，是哪一项、什么时候恢复。 */
      quotaItem?: string
      quotaResetsAt?: string
    }
  | { kind: 'done'; status: string; partial: boolean }

/**
 * 连接的状态。**每一种都有各自的出路**：
 *
 * - `connecting`：首次连接；
 * - `open`：可用；
 * - `reconnecting`：连过又断，或第一次没连上，正在按退避重试；已收到的文字留在
 *   屏幕上，不把这一轮判失败；
 * - `rate_limited`：握手被限流，正在等 `Retry-After`（或退避），永不登出；
 * - `refused`：第一次就没建起来且不重试（只在停止重连后出现）；
 * - `credential_lost`：票都签不出来，落点只有登录页。
 */
export type ConnectionStatus =
  | 'idle'
  | 'connecting'
  | 'open'
  | 'reconnecting'
  | 'refused'
  | 'credential_lost'
  | 'rate_limited'

/**
 * 能力的阶段清单，用来把 `stage_start` 渲染成"第几步 / 共几步"。
 *
 * 由调用方从 `/api/v1/capabilities` 取来 —— **不写死**。阶段名与阶段总数随能
 * 力不同而不同，《设计说明书》§4.2 与《前端设计注意事项》§4.2 都把这条单独说
 * 了，因为「分析 → 生成 → 校对」是一个很容易被顺手写死的假设。取不到时
 * `index` 是 0、`total` 是 0，界面就只显示阶段名，不显示"第几步"。
 */
export type StagePlan = readonly string[]

/** `metadata` 里的字符串字段。不是字符串就当没有——不 `String()` 一个对象出来。 */
export function stringMeta(meta: Record<string, unknown>, key: string): string | undefined {
  const value = meta[key]
  return typeof value === 'string' ? value : undefined
}

/**
 * 把调用参数收成一行给人看的文本。
 *
 * 服务端给的可能是**解析好的对象**，也可能是模型没产出合法 JSON 时的原文
 * （`agentloop/toolcall.go` 的 `metadataArgs` 两种都发）。所以两种都要接。
 */
export function describeArgs(value: unknown): string | undefined {
  if (value === undefined || value === null) {
    return undefined
  }
  if (typeof value === 'string') {
    return value
  }
  try {
    return JSON.stringify(value)
  } catch {
    return undefined
  }
}
