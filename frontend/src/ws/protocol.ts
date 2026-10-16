/**
 * WebSocket 协议 v2.0 的前端镜像。
 *
 * 这份文件的唯一职责是**让前端的常量与后端逐字对上**。后端那边是
 * `backend/internal/ws/protocol.go` 与 `backend/internal/event/types.go`，
 * 而事件名还与 `turn_event_types` 表以及协议本身三方共用（见 `types.go` 的
 * 包注释）。所以这里**不是**一份可以自由整理的文件：名字改一个字符，连接就
 * 会在某个分支上静默走错路。
 *
 * 判断"要不要在这里加一个常量"的标准是：后端会不会把它当字面量发过来或
 * 收过去。会，就加；只是前端自己的显示逻辑，不放这里。
 */

/** 这个 build 唯一会说的版本。发别的版本会被在升级前拒掉。 */
export const PROTOCOL_VERSION = '2.0'

/** 服务端帧的类型，放在 `frame` 字段里。 */
export const FRAME = {
  /** 唯一携带一轮对话自身流量的帧。 */
  EVENT: 'event',
  /** 回答一个改了东西的命令。 */
  COMMAND_ACK: 'command_ack',
  ERROR: 'error',
  PONG: 'pong',
  /** 回答 `check_active_turn`。 */
  ACTIVE_TURN: 'active_turn',
} as const

export type FrameKind = (typeof FRAME)[keyof typeof FRAME]

/** 客户端命令，放在 `type` 字段里。 */
export const COMMAND = {
  START_TURN: 'start_turn',
  /** PRD 里在已有会话中开一轮的另一个名字 —— 同一个操作，同一条代码路径。 */
  MESSAGE: 'message',
  SUBSCRIBE: 'subscribe',
  /** 与 `subscribe` 同一个操作：没断过的从零订，断过的从水位线续。 */
  RESUME: 'resume',
  UNSUBSCRIBE: 'unsubscribe',
  CANCEL: 'cancel',
  SUBMIT_REPLY: 'submit_reply',
  CHECK_ACTIVE_TURN: 'check_active_turn',
  REGENERATE: 'regenerate',
  PING: 'ping',
} as const

export type CommandKind = (typeof COMMAND)[keyof typeof COMMAND]

/**
 * 这一层会报的错误码 —— 是给客户端的"你能做什么"，不是"服务端内部出了
 * 什么"。与 `protocol.go` 的 `Code*` 一一对应。
 */
export const ERROR_CODE = {
  UNSUPPORTED_PROTOCOL_VERSION: 'unsupported_protocol_version',
  MALFORMED_COMMAND: 'malformed_command',
  UNSUPPORTED_COMMAND: 'unsupported_command',
  SESSION_NOT_FOUND: 'session_not_found',
  TURN_NOT_FOUND: 'turn_not_found',
  NO_ACTIVE_TURN: 'no_active_turn',
  TURN_CONFLICT: 'active_turn_conflict',
  NOT_WAITING: 'turn_not_waiting',
  EMPTY_CONTENT: 'empty_content',
  EMPTY_TITLE: 'empty_title',
  NOT_FOUND: 'not_found',
  SLOW_CONSUMER: 'slow_consumer',
  INTERNAL: 'internal_error',
} as const

/**
 * 一轮对话能发布的事件类型，顺序与 migration 00002 播种的 id 顺序一致。
 *
 * 这里的顺序**不是**显示顺序：阶段名与阶段总数由所选能力决定（`设计说明书.md`
 * §4.2 明写不许写死成"分析 → 生成 → 校对"）。要显示"第几步 / 共几步"得去
 * `/api/v1/capabilities` 拿那个能力自己声明的 `stages`。
 */
export const EVENT_TYPE = {
  STAGE_START: 'stage_start',
  STAGE_END: 'stage_end',
  THINKING: 'thinking',
  OBSERVATION: 'observation',
  /** 回答的增量，已经按 50ms / 1024 字节合并过。 */
  CONTENT: 'content',
  TOOL_CALL: 'tool_call',
  TOOL_RESULT: 'tool_result',
  PROGRESS: 'progress',
  SOURCES: 'sources',
  /** 一个阶段的完整回答，不是增量。 */
  RESULT: 'result',
  ERROR: 'error',
  SESSION: 'session',
  /** 排在 `done` **之后**，是整轮真正的收尾。 */
  SESSION_META: 'session_meta',
  DONE: 'done',
  WAIT_FOR_INPUT: 'wait_for_input',
} as const

export type EventType = (typeof EVENT_TYPE)[keyof typeof EVENT_TYPE]

/** `progress` 事件元数据里的三个键，一个不多一个不少。 */
export const PROGRESS_KEY = {
  STEP: 'step',
  TOTAL: 'total',
  LABEL: 'label',
} as const

/** 服务端发来的一帧事件。字段是平铺的，不是嵌套的。 */
export interface EventFrame {
  protocol_version: string
  frame: typeof FRAME.EVENT
  turn_id: string
  seq: number
  type: string
  source?: string
  /** 阶段名。`stage_start` / `stage_end` 上一定有。 */
  stage?: string
  content?: string
  metadata?: Record<string, unknown>
  timestamp: string
}

export interface AckFrame {
  protocol_version: string
  frame: typeof FRAME.COMMAND_ACK
  command_id?: string
  command: string
  status: string
  turn_id?: string
  session_id?: string
}

export interface ErrorFrame {
  protocol_version: string
  frame: typeof FRAME.ERROR
  /** true 表示这条连接到此为止：修好错误再重连，服务端不会再读一条命令。 */
  terminal: boolean
  error: { code: string; message: string }
}

export interface ActiveTurnFrame {
  protocol_version: string
  frame: typeof FRAME.ACTIVE_TURN
  session_id: string
  active: boolean
  turn_id?: string
  status?: string
  last_seq: number
}

export interface PongFrame {
  protocol_version: string
  frame: typeof FRAME.PONG
}

export type ServerFrame = EventFrame | AckFrame | ErrorFrame | ActiveTurnFrame | PongFrame

/**
 * 关闭码。后端一共只有两处 `socket.Close`（`conn.go:306` 与 `handler.go:88`），
 * 所以这张表是穷举：`1000` 正常、`1008` 慢消费者、`1001` 服务端关机。
 *
 * `1006` 是例外：它**不是服务端说的话**，是浏览器在没有收到关闭帧时自己报的
 * ——网络断了、进程被杀了，都不会有 1000 或 1008 过来。所以"看到 1006"和
 * "看到 1008"在前端是两种处境，不要折叠成一种。
 */
export const CLOSE = {
  NORMAL: 1000,
  /** 服务端关机。 */
  SHUTDOWN: 1001,
  /** 慢消费者：客户端没在读自己的帧，服务端把它断开。 */
  SLOW_CONSUMER: 1008,
  /** 浏览器自己的：连接掉了，没有关闭帧。 */
  ABNORMAL: 1006,
} as const

/** 判断一帧服务端消息是不是我们认识的那种帧。 */
export function frameKind(frame: ServerFrame | null | undefined): string | null {
  return frame && typeof frame.frame === 'string' ? frame.frame : null
}
