/**
 * 掌握路径的出题弹窗。
 *
 * `mastery_question` 是 `mastery_path` 能力里的一个工具：模型把题干、题型与
 * 选项写在**调用参数**里，题目本身存进题库。这里从会话流里找最后一次出题，
 * 并且只有在它后面还没有用户消息时才认为"这道题待作答"——用户在输入区直接
 * 答了，也算答过。
 *
 * **失败的调用也算出题。** 参数已经发出来了，题面就在模型的消息里；调用失败
 * 只说明它没进题库（租约被占、路径上已经有题在等答），不说明这道题不存在。
 * 这时界面照样弹题，并去题库按题干核对一次：核对到了就说"题库里已有"，没核
 * 对到就说"没能存进题库"。两句话都是查过的，不是猜的。
 *
 * 它只读**实时块**：历史接口只回用户与助手的正文，工具调用不在其中。所以刷新
 * 页面之后弹窗不会重开，题干仍在助手那条消息里，作答照旧走输入区。
 *
 * 弹窗只在回合结束后自动打开（`running` / `waiting` 都落下）。回合进行中打开
 * 没有意义：输入区正在跑，提交不出去；服务端停下来等澄清回答时也不行，那一轮
 * 要先答澄清。
 */

import { computed, ref, watch, type Ref } from 'vue'

import { listQuestions } from '../../api/questions'
import type { Block } from '../../ws/blocks'

export type MasteryQuestionType = 'choice' | 'fill_blank' | 'short_answer'

/** 一道待作答的题，字段来自 `mastery_question` 的调用参数。 */
export interface MasteryQuestion {
  /** 配对的 `call_id`。它同时是"这道题是哪一次调用"的标识。 */
  callId: string
  stem: string
  type: MasteryQuestionType
  options: readonly { key: string; text: string }[]
  knowledgePointId: string
  /**
   * 这次调用是否成功。成功就一定在题库里；不成功还要按题干查一次——它可能
   * 是之前某一次成功出题存下的同一道题，也可能真的没存进去。
   */
  saved: boolean
}

/**
 * 题库核对的结果。
 *
 * - `saved`：这次调用成功，题目刚存进去；
 * - `checking`：调用失败，正在按题干查题库；
 * - `found`：调用失败，但题库里有这道题；
 * - `missing`：调用失败，题库里也没有；
 * - `unknown`：查不了（请求失败），不对题库下结论。
 */
export type MasteryQuestionBank = 'saved' | 'checking' | 'found' | 'missing' | 'unknown'

type ToolBlock = Extract<Block, { kind: 'tool' }>

const QUESTION_TYPES: readonly MasteryQuestionType[] = ['choice', 'fill_blank', 'short_answer']

/**
 * 把一次调用的参数读成一道题。
 *
 * 参数可能是模型没给出合法 JSON 时的原文（`agentloop/toolcall.go` 的
 * `metadataArgs` 两种都发），读不懂就当没有这道题，不去猜。
 */
export function parseMasteryQuestion(block: ToolBlock): MasteryQuestion | null {
  if (!block.args) return null
  let raw: unknown
  try {
    raw = JSON.parse(block.args)
  } catch {
    return null
  }
  if (typeof raw !== 'object' || raw === null) return null

  const args = raw as Record<string, unknown>
  const stem = typeof args.question === 'string' ? args.question.trim() : ''
  if (stem === '') return null

  const type = QUESTION_TYPES.includes(args.question_type as MasteryQuestionType)
    ? (args.question_type as MasteryQuestionType)
    : 'short_answer'

  const options: { key: string; text: string }[] = []
  if (Array.isArray(args.options)) {
    for (const item of args.options) {
      if (typeof item !== 'object' || item === null) continue
      const option = item as Record<string, unknown>
      if (typeof option.key !== 'string' || option.key === '') continue
      if (typeof option.text !== 'string') continue
      options.push({ key: option.key, text: option.text })
    }
  }

  return {
    callId: block.callId,
    stem,
    type,
    options,
    knowledgePointId: typeof args.knowledge_point_id === 'string' ? args.knowledge_point_id : '',
    saved: block.ok === true,
  }
}

export function useMasteryQuestion(deps: {
  /** 当前连接的实时块。 */
  blocks: Readonly<Ref<readonly Block[]>>
  running: Readonly<Ref<boolean>>
  waiting: Readonly<Ref<boolean>>
  activeId: Readonly<Ref<string | null>>
}) {
  /**
   * 待作答的那道题。从末尾往前找最后一次**能读出题干**的出题；碰到用户消息
   * 就说明它已经被答过了。读不出参数的调用跳过——它没有题面，前面那一道仍然
   * 是用户在等的。
   */
  const question = computed<MasteryQuestion | null>(() => {
    for (let index = deps.blocks.value.length - 1; index >= 0; index -= 1) {
      const block = deps.blocks.value[index]
      if (block.kind === 'user') return null
      if (block.kind === 'tool' && block.name === 'mastery_question') {
        const parsed = parseMasteryQuestion(block)
        if (parsed) return parsed
      }
    }
    return null
  })

  /**
   * 题库对这一道题的答复。查询按题干搜，再在结果里逐字比对题干——搜索是
   * 模糊的，这里要回答的是"这一道题在不在"，差一个字都不算。
   */
  const bank = ref<MasteryQuestionBank>('unknown')
  /** 已经核对过的那一次调用与它的成败。块每次变化都会重算，靠它不去重发。 */
  let checked: string | null = null
  /** 并发查询的顺序号：只让最后一次查询写结果。 */
  let lookup = 0

  watch(
    question,
    (next) => {
      if (!next) {
        lookup += 1
        checked = null
        bank.value = 'unknown'
        return
      }

      const key = `${next.callId}:${next.saved ? 'saved' : 'attempt'}`
      if (checked === key) return

      lookup += 1
      const token = lookup
      checked = key

      if (next.saved) {
        bank.value = 'saved'
        return
      }

      bank.value = 'checking'
      void listQuestions({ q: next.stem, limit: 50 })
        .then((result) => {
          if (token !== lookup) return
          const stem = next.stem.trim()
          bank.value = result.questions.some((entry) => entry.question.text.trim() === stem)
            ? 'found'
            : 'missing'
        })
        .catch(() => {
          if (token !== lookup) return
          bank.value = 'unknown'
        })
    },
    { immediate: true },
  )

  const visible = ref(false)
  /**
   * 用户点了「稍后作答」的那一次调用。只有**那一道题**不再自动弹；下一次出题
   * 是一新的 call id，照常弹。
   */
  const dismissedCallId = ref<string | null>(null)

  function eyeIsFree(): boolean {
    return !deps.running.value && !deps.waiting.value
  }

  watch(
    [deps.running, deps.waiting],
    () => {
      if (!eyeIsFree()) {
        visible.value = false
        return
      }
      const pending = question.value
      if (pending && pending.callId !== dismissedCallId.value) visible.value = true
    },
    { immediate: true },
  )

  watch(question, (next) => {
    if (!next) {
      visible.value = false
      return
    }
    if (eyeIsFree() && next.callId !== dismissedCallId.value) visible.value = true
  })

  // 换会话：新的一条连接有自己的题目，旧会话的"稍后"不该带过来。
  watch(deps.activeId, () => {
    visible.value = false
    dismissedCallId.value = null
  })

  function dismiss(): void {
    visible.value = false
    dismissedCallId.value = question.value?.callId ?? null
  }

  function reopen(): void {
    if (question.value) visible.value = true
  }

  return { question, bank, visible, dismiss, reopen }
}
