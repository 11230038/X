/**
 * 对话连接对外公开的形状。
 *
 * `Conversation` 是界面唯一能看到的东西：它把内部的可变状态封在闭包里，
 * 只暴露只读的 getter 与几个命令方法。拆到这个文件，是为了让 `socket.ts`
 * 专心处理握手、重连与收帧。
 */

import type { ApiFailure } from '../api/client'
import type { Block, ConnectionStatus, SourceItem, StagePlan } from './blocks'

/**
 * 一轮带上的材料与人格。附件是文件库的文件 id，知识库是 kb_id，人格是
 * `/personas` 里的 persona_id。
 *
 * 三者都是**这一轮**的，服务端不会把它们写进会话：会话存的是说过的话，不是
 * 说那句话时带着的东西。
 */
export interface SendOptions {
  attachments?: string[]
  knowledgeBases?: string[]
  selection?: TurnSelection
  persona?: string
}

/**
 * 阅读器里选中的一段。坐标与引用是同一套：材料 id + 字符区间。
 * `locator` 不带 `material_id`，因为线协议里它是外面那个字段。
 */
export interface TurnSelection {
  material_id: string
  locator: {
    kind: string
    ordinal?: number
    char_start: number
    char_end: number
    page?: number
  }
  text: string
}

export interface PendingSend {
  text: string
  options: SendOptions
  commandId: string
  block: Extract<Block, { kind: 'user' }>
}

export interface Conversation {
  readonly blocks: Block[]
  readonly status: ConnectionStatus
  readonly lastSeq: number
  readonly failure: ApiFailure | null
  /** 服务端让等多少秒。只有 `rate_limited` 时有值。 */
  readonly retryAfterSeconds: number | null
  /** 这一轮还在跑。发出去到 `done` 之间为真 —— 输入区靠它把发送换成停止。 */
  readonly running: boolean
  /**
   * 服务端停下来等一个回答，整轮暂停（`wait_for_input`）。
   *
   * 与 `running` 是两件事：这时**没有东西在跑**，但这一轮也**没有结束**。所以
   * 下一次发送是 `submit_reply` 而不是 `start_turn`。
   */
  readonly waiting: boolean
  /** 第几次重连。0 表示没在重连。 */
  readonly reconnectAttempt: number
  /** 订阅到的那一轮是别处发起的（页面刷新前的旧连接、另一个标签页）。 */
  readonly activeElsewhere: boolean
  /** 连接断在一句发送收到 ack 之前。重试会换一个新的 `command_id`。 */
  readonly unconfirmed: SendOptions & { text: string } | null
  /** 最近一轮的来源。引用面板与 Markdown 里的可点编号都读它。 */
  readonly sources: SourceItem[]
  /** 能对上的引用编号。对不上的按普通文本画。 */
  readonly citations: ReadonlySet<number>
  /** 服务端在本轮结束后给出的会话标题。空串表示还没有。 */
  readonly title: string
  connect(): Promise<void>
  send(text: string, options?: SendOptions): void
  retryUnconfirmed(): void
  reply(text: string): void
  cancel(): void
  /** 丢掉实时块（历史重读之前用）。 */
  resetStream(): void
  close(): void
}

/**
 * 这一轮用哪个能力，以及它的阶段表。
 *
 * 做成**取值的函数**而不是构造时传进来的两个值：两者都是用户能中途改的
 * （界面上有一个能力选择器），而"改了之后从下一轮起生效"比"必须重连一次才
 * 生效"更接近用户的心智。
 */
export interface TurnContext {
  /** 放在命令的 `capability` 里。空串表示让服务端自己路由（它会先看内容）。 */
  capability(): string
  stages(): StagePlan
  /** 重连后发现有东西在断线期间结束/开始：让调用方重读一次落库的消息。 */
  reloadHistory?(): void
}
