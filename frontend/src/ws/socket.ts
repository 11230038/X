/**
 * 一条对话：把服务端发来的帧变成界面上的一串块，把界面的动作变成命令。
 *
 * 顺序是《前端设计注意事项》§6 写的那条：**签票 → 连 → 发一句 → 读帧**。这里
 * 守住几条后端定死了的事：
 *
 * - **票每次连接都重新签，不复用。** 它不是一次性的，只是活得短（默认 30 秒，
 *   上限 5 分钟），所以"不缓存"是这一侧要自己守的纪律（§1.1）。
 * - **`done` 之后不能关连接。** 后面还会来一条 `session_meta`，流才算收尾
 *   （§3.2）。
 * - **`lastSeq` 从第一天就记账。** 这一版不做重连（stage 0 的边界），但水位线
 *   是后端的慢消费者机制与 `resume` 唯一要的东西，漏记就等于将来要重写。
 *
 * 三件事被分了出去，各在一个文件里：块与状态的词汇在 `blocks.ts`，连接的生命
 * 周期在 `connection.ts`（`WebSocket` 对象只在那儿），协议常量在 `protocol.ts`。
 * 留在这里的是**一条连接的回合状态**：哪一轮在跑、哪块回答还没收口、哪个阶段
 * 已经开过还没收尾。
 */

import { ref, shallowRef } from 'vue'

import { isApiFailure, type ApiFailure } from '../api/client'
import { describeCommandError } from '../api/errors'
import { wsTicket } from '../api/auth'

import {
  describeArgs,
  META,
  stringMeta,
  type Block,
  type ConnectionStatus,
  type StagePlan,
} from './blocks'
import { openLink, type Link } from './connection'
import { COMMAND, EVENT_TYPE, FRAME, PROTOCOL_VERSION, type EventFrame, type ServerFrame } from './protocol'

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
   * 下一次发送是 `submit_reply` 而不是 `start_turn` —— 后者会因为会话里已经
   * 有一轮在等而被拒（`active_turn_conflict`）。
   */
  readonly waiting: boolean
  connect(): Promise<void>
  send(text: string): void
  reply(text: string): void
  cancel(): void
  close(): void
}

/**
 * 这一轮用哪个能力，以及它的阶段表。
 *
 * 做成**取值的函数**而不是构造时传进来的两个值：两者都是用户能中途改的
 * （界面上有一个能力选择器），而"改了之后从下一轮起生效"比"必须重连一次才
 * 生效"更接近用户的心智。构造时快照下来会让那个选择器在下一次连接之前一直
 * 是个摆设。
 */
export interface TurnContext {
  /** 放在命令的 `capability` 里。空串表示让服务端自己路由（它会先看内容）。 */
  capability(): string
  stages(): StagePlan
}

export function createConversation(sessionId: string, context: TurnContext): Conversation {
  // shallow：每次变更都换掉整个数组（见 `touch`），所以不需要 Vue 去逐层代理
  // 每一个块 —— 一个正在生成的回答每 50ms 追加一次，深代理会把这些追加变成
  // 对整棵列表的依赖追踪。
  const blocks = shallowRef<Block[]>([])
  const status = ref<ConnectionStatus>('idle')
  const lastSeq = ref(0)
  const failure = ref<ApiFailure | null>(null)
  const retryAfterSeconds = ref<number | null>(null)
  const running = ref(false)
  const waiting = ref(false)

  /**
   * 断掉之后这个把手还在，只是 `isOpen()` 会说 false —— 所以"还能不能发"只有
   * 一个问法，不必再配一个 `connected` 布尔。
   */
  let link: Link | null = null
  let commandCounter = 0
  /**
   * 这一轮在服务端的 id。每个事件帧都带着它，所以不需要另外记 ack —— 收第一
   * 条事件时就有了。`submit_reply` 要它，那是这一个字段存在的唯一理由。
   */
  let currentTurnId = ''
  /** 正在累加的那一块回答；`null` 表示此刻没有在生成的回答。 */
  let openAnswer: Extract<Block, { kind: 'answer' }> | null = null
  /** 已经开了还没收尾的那个阶段。 `stage_end` 只带名字，靠它配回去。 */
  let openStage: Extract<Block, { kind: 'stage' }> | null = null

  function push(block: Block): void {
    blocks.value = [...blocks.value, block]
  }

  /**
   * 就地改过的块要让列表重新渲染一次。`shallowRef` 只在 `.value` 被赋值时触
   * 发，所以这里必须换掉数组本身 —— 但**不必**深拷每一个元素。
   */
  function touch(): void {
    blocks.value = [...blocks.value]
  }

  async function connect(): Promise<void> {
    status.value = 'connecting'
    failure.value = null
    retryAfterSeconds.value = null

    let ticket: string
    try {
      // 第 1 步是普通 HTTP 请求，所以它的问题**看得见** —— 401 与 429 都能
      // 分辨，这也正是它被做成两步的原因之一。
      ticket = (await wsTicket()).ticket
    } catch (cause) {
      if (!isApiFailure(cause)) {
        throw cause
      }
      failure.value = cause.failure
      retryAfterSeconds.value = cause.failure.retryAfterSeconds ?? null
      if (cause.failure.kind === 'unauthorized') {
        status.value = 'credential_lost'
      } else if (cause.failure.kind === 'rate_limited') {
        status.value = 'rate_limited'
      } else {
        status.value = 'refused'
      }
      return
    }

    await openSocket(ticket)
  }

  function openSocket(ticket: string): Promise<void> {
    const pending = openLink(ticket, {
      onOpen() {
        status.value = 'open'
      },
      onFrame(raw) {
        receive(raw)
      },
      onRefused() {
        status.value = 'refused'
      },
      onLost() {
        // 连上了又断。服务端关机是 1001，慢消费者是 1008，网络掉了是浏览器自
        // 己报的 1006 —— 这一版对三者的处置相同（都不重连），所以都归到"掉
        // 了"。stage 1 会在这里把它们分开。
        if (status.value === 'open') {
          status.value = 'lost'
        }
        // 断了就没有"正在跑的那一轮"可言了。这一版不重连，所以它不会自己接
        // 上；输入区应该能再发一条，而不是停在一个按不动的"停止"上。
        running.value = false
        waiting.value = false
        currentTurnId = ''
      },
    })
    link = pending.link
    return pending.settled
  }

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
        onEvent(frame)
        return
      case FRAME.ERROR: {
        // terminal 为 true 表示这条连接到此为止：服务端不会再读一条命令，只能
        // 修好它说的问题再重连。这一版不重连，所以画出来就好。
        //
        // 这一帧是**连接的**错误，不是某一轮的事件，所以它不带 `done`。跑了
        // 一半的那一轮也跟着停了，标志要在这里放下。
        running.value = false
        waiting.value = false
        // 服务端的 `message` 是**说给开发者听的英文**（`dispatch.go` 的
        // `describe`），直接摆出去就是一段英文落到中文界面上。文案与"能不能重
        // 试"都在 `api/errors.ts` 那一张表里，与 HTTP 那条路同一处收着。
        const described = describeCommandError(frame.error.code)
        push({
          kind: 'error',
          text: described.text,
          code: frame.error.code,
          retryable: described.retryable,
        })
        return
      }
      case FRAME.COMMAND_ACK:
      case FRAME.ACTIVE_TURN:
      case FRAME.PONG:
        // 收到了，这一版不渲染。ack 上的 turn_id 只在订阅、续传、取消才用得上
        // —— 这一版都还没有。
        return
      default:
        return
    }
  }

  function onEvent(frame: EventFrame): void {
    // 水位线：**先记账再处理**。处理里如果抛了，这一条也已经算收到了，将来带
    // 着 after_seq 回来时不会重复要它。
    if (frame.seq > lastSeq.value) {
      lastSeq.value = frame.seq
    }
    if (frame.turn_id) {
      currentTurnId = frame.turn_id
    }

    const meta = frame.metadata ?? {}

    switch (frame.type) {
      case EVENT_TYPE.CONTENT: {
        // 增量，且已经被服务端按 50ms / 1024 字节合并过——不是一个 token 一帧。
        // 所以逐块追加就够，这一侧不需要再做一次节流。
        if (!openAnswer) {
          openAnswer = { kind: 'answer', text: '', complete: false }
          push(openAnswer)
        }
        openAnswer.text += frame.content ?? ''
        touch()
        return
      }
      case EVENT_TYPE.RESULT: {
        // 整段的完整回答。**用它收口**：增量因为断流而不完整时，只有这里说的
        // 是全的。
        if (openAnswer) {
          openAnswer.complete = true
          openAnswer = null
        } else {
          push({ kind: 'answer', text: frame.content ?? '', complete: true })
          return
        }
        touch()
        return
      }
      case EVENT_TYPE.STAGE_START: {
        const name = frame.stage ?? ''
        const plan = context.stages()
        // `indexOf` 找不到时是 0，`+1` 之后是 0 —— 恰好就是"不显示第几步"。这
        // 就是真的会出现的那种情况：事件里的 stage 不在能力的阶段表里时，界面
        // 显示名字但不编一个序号。
        openStage = {
          kind: 'stage',
          name,
          index: plan.indexOf(name) + 1,
          total: plan.length,
          done: false,
        }
        push(openStage)
        return
      }
      case EVENT_TYPE.STAGE_END: {
        if (openStage && openStage.name === (frame.stage ?? '')) {
          openStage.done = true
          openStage = null
          touch()
        }
        return
      }
      case EVENT_TYPE.TOOL_CALL: {
        // 工具名在 `Content`，参数在 `Metadata.args`（`internal/event/event.go`
        // 写明了这个分工：`Content` 放简单值，结构化负载进 `Metadata`）。
        push({
          kind: 'tool',
          name: frame.content ?? '',
          callId: stringMeta(meta, META.CALL_ID) ?? '',
          ...(describeArgs(meta[META.ARGS]) ? { args: describeArgs(meta[META.ARGS]) } : {}),
        })
        return
      }
      case EVENT_TYPE.TOOL_RESULT: {
        // 按 `call_id` 配对，**不按名字**：同一个工具可以在一个阶段里连着调两
        // 次，按名字配会把第二次的结果写到第一次上面。
        const callId = stringMeta(meta, META.CALL_ID) ?? ''
        const target = blocks.value.find(
          (block) => block.kind === 'tool' && block.callId === callId,
        )
        if (target && target.kind === 'tool') {
          target.result = frame.content ?? ''
          target.ok = meta[META.SUCCESS] === true
          touch()
        }
        return
      }
      case EVENT_TYPE.WAIT_FOR_INPUT: {
        // 整轮暂停：**没有东西在跑了，但这一轮也还没结束**。两个标志一起翻，
        // 输入区才知道下一次发送该走哪条命令。
        running.value = false
        waiting.value = true
        // 提问之前那段回答到此为止，**必须收口**。暂停发生在阶段内部 ——
        // `ask_user` 是从 `RunLoop` 里回调出去的（`turn/waiting.go`），回答完
        // 之后的文字是同一阶段的后续。不收口的话：那一块会一直挂着"还有字要
        // 来"，而续写的文字会跑到提问**上面**去（它在数组里排在 question 之
        // 前），读起来就是答非所问。
        if (openAnswer) {
          openAnswer.complete = true
          openAnswer = null
          touch()
        }
        push({ kind: 'question', text: frame.content ?? '' })
        return
      }
      case EVENT_TYPE.ERROR: {
        // 这一轮失败了。后面还会来一条 `done`（`turn/ending.go` 里错误与终态
        // 是先后两条），但状态在这里就得放下 —— 否则从错误到 done 之间输入区
        // 还显示"停止"。
        running.value = false
        waiting.value = false
        // `Content` 是给客户端看的那句话，码在 `Metadata.error_code`
        // （`internal/turn/ending.go`：文案由服务端挑，客户端不自己拼）。
        push({
          kind: 'error',
          text: frame.content ?? '',
          code: stringMeta(meta, META.ERROR_CODE) ?? '',
          retryable: meta[META.RETRYABLE] === true,
        })
        return
      }
      case EVENT_TYPE.DONE: {
        // 终态。**连接不关** —— 后面还有一条 `session_meta`（§3.2），而且同一
        // 条连接上可以接着发下一轮。
        running.value = false
        waiting.value = false
        if (openAnswer) {
          openAnswer.complete = true
          openAnswer = null
          touch()
        }
        // `partial_response` 说的是"**已经有回答文字产出过了**"，不是"这一段不
        // 完整"（`event.go`：whether some answer text was already produced）。
        // 两个字段要一起读：`failed` + `partial` 才是"没写完，你能看到的那一段
        // 是半截的"。所以两个都留下来，判断交给界面 —— 这里替它下结论会把这个
        // 组合表达不了。
        push({
          kind: 'done',
          status: stringMeta(meta, META.STATUS) ?? '',
          partial: meta['partial_response'] === true,
        })
        return
      }
      case EVENT_TYPE.SESSION_META:
      case EVENT_TYPE.SESSION:
      case EVENT_TYPE.SOURCES:
      case EVENT_TYPE.THINKING:
      case EVENT_TYPE.OBSERVATION:
      case EVENT_TYPE.PROGRESS:
        // 这一版不渲染这几类。它们仍然是收到的事件，水位线也照记——只是屏幕
        // 上没有对应的块。
        return
      default:
        return
    }
  }

  function command(type: string, fields: Record<string, unknown> = {}): void {
    // command_id 是"这条命令我可能重发"的标记，服务端会把重复的 ack 再回一次
    // 而不是再做一次。这一版没有重发路径，但 id 现在就得有 —— 补在协议外面比
    // 补在协议里面贵。
    link?.send(
      JSON.stringify({
        protocol_version: PROTOCOL_VERSION,
        type,
        command_id: `c${++commandCounter}`,
        ...fields,
      }),
    )
  }

  function beginTurn(text: string): void {
    push({ kind: 'user', text })
    openAnswer = null
    openStage = null
    running.value = true
    waiting.value = false
  }

  function send(text: string): void {
    if (!link?.isOpen()) {
      return
    }
    beginTurn(text)
    command(COMMAND.START_TURN, {
      session_id: sessionId,
      content: text,
      // 空串发一个空串：服务端把空白当成"你没指定"，于是自己按内容路由
      // （`turn/service.go`：pre-routing 填空白，不推翻一个明确的答案）。
      capability: context.capability(),
    })
  }

  /** 回答服务端的澄清提问。**必须是 `submit_reply`**，见 `Conversation.waiting`。 */
  function reply(text: string): void {
    if (!link?.isOpen() || currentTurnId === '') {
      return
    }
    // **这一条不能走 `beginTurn`**：`submit_reply` 是让**同一轮**接着跑，不是
    // 开新一轮，所以那一轮的状态要原样留着。尤其是 `openStage` —— 一个阶段只
    // 发一次 `stage_start`（`capability/stage.go` 的 `Answer` 进出各一发），暂
    // 停不会重发，这里要是把它清掉，后面那条 `stage_end` 就再没有对象可配，那
    // 个阶段会永远停在"没完成"上，直到这个会话被关掉。
    push({ kind: 'user', text })
    running.value = true
    waiting.value = false
    command(COMMAND.SUBMIT_REPLY, { turn_id: currentTurnId, content: text })
  }

  function cancel(): void {
    if (!link?.isOpen() || currentTurnId === '') {
      return
    }
    // 取消**要点名是哪一轮**（`dispatch.go`：`cancel needs a turn_id`）。容易
    // 以为"这条连接上就一轮，服务端自己知道"，但同一个会话里换一轮之后旧连接
    // 仍在，所以点名的责任在这一侧。
    command(COMMAND.CANCEL, { turn_id: currentTurnId })
  }

  function close(): void {
    // 界面上没有"关掉连接"这个动作——这条是给组件卸载用的。**登出不走这里**：
    // 凭据在服务端被撤回之后，这条已经建好的连接不会自己断（§1.3），得由调用
    // 方显式拆掉。
    link?.close()
    link = null
    status.value = 'idle'
    running.value = false
    waiting.value = false
    currentTurnId = ''
  }

  return {
    get blocks() {
      return blocks.value
    },
    get status() {
      return status.value
    },
    get lastSeq() {
      return lastSeq.value
    },
    get failure() {
      return failure.value
    },
    get retryAfterSeconds() {
      return retryAfterSeconds.value
    },
    get running() {
      return running.value
    },
    get waiting() {
      return waiting.value
    },
    connect,
    send,
    reply,
    cancel,
    close,
  }
}
