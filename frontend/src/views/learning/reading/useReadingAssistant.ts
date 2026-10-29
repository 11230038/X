/**
 * 文档问答助手。
 *
 * 一问 = 正文里的一段选区。问题就是用户选中的原文，回答走一次性问答
 * （`POST /chat/complete`）：不开会话、不写消息、不带历史，答案只活在这
 * 条记录里。面板里留着"问的是哪一段"，所以答案有依据，也不会被别处的
 * 对话污染。
 *
 * 换一份材料，问答记录就清空：一条回答搬不到另一篇文章里。
 */

import { computed, ref, type Ref } from 'vue'

import type { ApiFailure } from '../../../api/client'
import { completeQuestion } from '../../../api/directChat'
import type { ReadingLocator } from '../../../api/reading'
import { toFailure } from '../../../shared/resource'

export interface AssistantExchange {
  id: number
  /** 用户选中的原文，面板原样显示。 */
  question: string
  locator: ReadingLocator
  status: 'asking' | 'ready' | 'failed'
  /** 服务端给的 markdown 正文。 */
  answer: string
  failure: ApiFailure | null
}

export function useReadingAssistant(currentId: Ref<string>, materialTitle: () => string) {
  const exchanges = ref<AssistantExchange[]>([])
  /** 还有一问没有回来。面板用它把再次提问的入口关掉。 */
  const asking = computed(() =>
    exchanges.value.some((item) => item.status === 'asking'),
  )
  let sequence = 0

  /**
   * 发给模型的是"解释这段话"，不是把原文裸着送过去：一次性问答没有会话上
   * 下文，不说明来意的话，一段摘录看起来就是一个没头没尾的句子。带上文献
   * 名，模型才知道它出自哪里。
   */
  function promptFor(passage: string): string {
    const title = materialTitle().replace(/\.[^./\\]+$/, '')
    return title ? `我在读《${title}》。请解释下面这段文字：\n\n${passage}` : `请解释下面这段文字：\n\n${passage}`
  }

  async function ask(passage: string, locator: ReadingLocator): Promise<void> {
    const materialId = locator.material_id
    const entry: AssistantExchange = {
      id: (sequence += 1),
      question: passage,
      locator,
      status: 'asking',
      answer: '',
      failure: null,
    }
    exchanges.value = [entry, ...exchanges.value]
    try {
      const { answer } = await completeQuestion(promptFor(passage))
      // 换了材料：这条回答已经不属于眼前这篇。
      if (currentId.value !== materialId) return
      const target = exchanges.value.find((item) => item.id === entry.id)
      if (!target) return
      target.answer = answer
      target.status = 'ready'
    } catch (cause) {
      if (currentId.value !== materialId) return
      const target = exchanges.value.find((item) => item.id === entry.id)
      if (!target) return
      target.status = 'failed'
      target.failure = toFailure(cause)
    }
  }

  function reset(): void {
    exchanges.value = []
  }

  return { exchanges, asking, ask, reset }
}
