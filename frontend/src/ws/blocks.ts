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
} as const

/** 会话流里的一块。每一种消息类型是它自己的一个变体。 */
export type Block =
  | { kind: 'user'; text: string }
  /** 回答。`text` 是 `content` 事件累加出来的增量。 */
  | { kind: 'answer'; text: string; complete: boolean }
  /** 阶段开始 / 结束。名字与总数都来自能力，不是写死的。 */
  | { kind: 'stage'; name: string; index: number; total: number; done: boolean }
  /** 工具调用。参数与结果都可能还没有。 */
  | { kind: 'tool'; name: string; callId: string; args?: string; result?: string; ok?: boolean }
  /** 模型中途停下来等回答，整轮暂停。 */
  | { kind: 'question'; text: string }
  | { kind: 'error'; text: string; code: string; retryable: boolean }
  | { kind: 'done'; status: string; partial: boolean }

/**
 * 连接的状态。`refused` 与 `lost` 是**两种不同的失败**，不要折叠
 * （`设计说明书.md` §4.2）：
 *
 * - `lost`：连上了又断。
 * - `refused`：连接**根本没建起来**。服务端可能是在让你等几秒，不是网络不通。
 *
 * 这一版不做重连，所以两者都只是给界面的一句话；区分它们的意义在于**把这
 * 个区分先立在那儿**，stage 1 加重连时不必回头拆一个已经折叠了的判断。
 */
export type ConnectionStatus =
  | 'idle'
  | 'connecting'
  | 'open'
  | 'lost'
  | 'refused'
  /** 票都签不出来：凭据被撤回了。落点只有登录页。 */
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
