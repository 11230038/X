/**
 * 知识库文档预览：按窗口读取一份文档的抽取正文。
 *
 * 读的是这一行自己的 document，而不是"这个文件碰巧被解析出的某一份"：同一个
 * 文件可以同时是文件库附件和多个知识库的文档，按 file 读会打开别人的那一份。
 *
 * 窗口按字符追加；追加失败只提示"后面的没加载出来"，不清掉已经读到的正文。
 * 请求序号守住换文档 / 关弹窗时的竞态。
 */

import { ref, watch, type ComputedRef } from 'vue'

import type { ApiFailure } from '../../api/client'
import {
  getKnowledgeDocumentText,
  type KbDocument,
  type KbDocumentTextView,
} from '../../api/knowledgeBases'
import { KNOWLEDGE } from '../../copy/knowledge'
import { toFailure } from '../../shared/resource'
import { notify } from '../../shared/toast'

type LoadStatus = 'idle' | 'loading' | 'ready' | 'failed'
const TEXT_WINDOW = 20_000

export function useKbPreview(selectedId: ComputedRef<string | null>) {
  const previewDocument = ref<KbDocument | null>(null)
  const text = ref<KbDocumentTextView | null>(null)
  const status = ref<LoadStatus>('idle')
  const failure = ref<ApiFailure | null>(null)
  const loadingMore = ref(false)

  /** 请求序号：换文档、关弹窗或换库时，晚到的旧响应不能盖掉新的。 */
  let seq = 0

  function reset(): void {
    seq += 1
    previewDocument.value = null
    text.value = null
    status.value = 'idle'
    failure.value = null
    loadingMore.value = false
  }

  watch(selectedId, reset)

  async function open(document: KbDocument): Promise<void> {
    const kbId = selectedId.value
    if (!kbId) return
    seq += 1
    const run = seq
    previewDocument.value = document
    text.value = null
    failure.value = null
    loadingMore.value = false
    status.value = 'loading'

    try {
      const part = await getKnowledgeDocumentText(kbId, document.document_id, {
        offset: 0,
        limit: TEXT_WINDOW,
      })
      if (run !== seq) return
      text.value = part
      status.value = 'ready'
    } catch (cause) {
      if (run !== seq) return
      status.value = 'failed'
      failure.value = toFailure(cause)
    }
  }

  function close(): void {
    reset()
  }

  function retry(): void {
    const current = previewDocument.value
    if (current) void open(current)
  }

  async function loadMore(): Promise<void> {
    const kbId = selectedId.value
    const current = text.value
    if (!kbId || !previewDocument.value || !current) return
    if (loadingMore.value || current.char_end >= current.total_chars) return
    const run = seq
    loadingMore.value = true

    try {
      const part = await getKnowledgeDocumentText(kbId, previewDocument.value.document_id, {
        offset: current.char_end,
        limit: TEXT_WINDOW,
      })
      if (run !== seq) return
      text.value = {
        ...part,
        text: current.text + part.text,
        char_start: current.char_start,
      }
    } catch {
      if (run !== seq) return
      notify(KNOWLEDGE.previewLoadMoreFailed, 'error')
    } finally {
      if (run === seq) loadingMore.value = false
    }
  }

  return {
    previewDocument,
    text,
    status,
    failure,
    loadingMore,
    open,
    close,
    retry,
    loadMore,
  }
}
