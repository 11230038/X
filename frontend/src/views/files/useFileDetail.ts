/**
 * 单个文件的详情与文本抽屉。
 *
 * 请求序号守住快速切换时的竞态：晚到的旧响应不能盖掉新选中的文件。文本按
 * 窗口追加，追加失败只提示"后面的没加载出来"，不把已经读到的那一段清掉。
 */

import { computed, ref } from 'vue'

import type { ApiFailure } from '../../api/client'
import {
  getFile,
  getFileText,
  type FileTextView,
  type FileView,
} from '../../api/files'
import { toFailure } from '../../shared/resource'
import { notify } from '../../shared/toast'

type LoadStatus = 'idle' | 'loading' | 'ready' | 'failed'
const TEXT_WINDOW = 20_000

export function useFileDetail(options: {
  /** 引用深链：这一份文件是否需要直接把文本抽屉展开。 */
  shouldOpenText: (fileId: string) => boolean
  /** 选中行变化时，清掉只属于上一行的面板状态（知识库选择、purge 拒绝原因）。 */
  onClearExtra: () => void
}) {
  const selectedId = ref<string | null>(null)
  const detail = ref<FileView | null>(null)
  const detailStatus = ref<LoadStatus>('idle')
  const detailFailure = ref<ApiFailure | null>(null)
  const actionBusy = ref(false)

  /** 请求序号：快速切换文件时，晚到的旧响应不能盖掉新的。 */
  let detailSeq = 0

  // ── 文本 ────────────────────────────────────────────────────────────────
  const showText = ref(false)
  const text = ref<FileTextView | null>(null)
  const textStatus = ref<LoadStatus>('idle')
  const textFailure = ref<ApiFailure | null>(null)
  const textLoadingMore = ref(false)

  function resetText(): void {
    showText.value = false
    text.value = null
    textStatus.value = 'idle'
    textFailure.value = null
    textLoadingMore.value = false
  }

  async function loadText(append: boolean): Promise<void> {
    const fileId = selectedId.value
    if (!fileId || !detail.value?.document) return
    if (append && (textLoadingMore.value || !text.value)) return
    if (!append) {
      textStatus.value = 'loading'
      textFailure.value = null
    } else {
      textLoadingMore.value = true
    }

    try {
      const part = await getFileText(fileId, {
        offset: append ? text.value?.char_end ?? 0 : 0,
        limit: TEXT_WINDOW,
      })
      if (fileId !== selectedId.value) return
      if (append && text.value) {
        text.value = { ...part, text: text.value.text + part.text, char_start: text.value.char_start }
      } else {
        text.value = part
        textStatus.value = 'ready'
      }
    } catch (cause) {
      if (fileId !== selectedId.value) return
      if (!append) {
        textStatus.value = 'failed'
        textFailure.value = toFailure(cause)
      } else {
        notify('后面的文本没能加载出来，请重试', 'error')
      }
    } finally {
      textLoadingMore.value = false
    }
  }

  function toggleText(): void {
    showText.value = !showText.value
    if (showText.value && textStatus.value === 'idle') void loadText(false)
  }

  // ── 选中 ────────────────────────────────────────────────────────────────
  function clearSelection(): void {
    detailSeq += 1
    selectedId.value = null
    detail.value = null
    detailStatus.value = 'idle'
    detailFailure.value = null
    resetText()
    options.onClearExtra()
  }

  async function selectFile(fileId: string, opts: { force?: boolean } = {}): Promise<void> {
    if (!opts.force && selectedId.value === fileId && detail.value) return
    const seq = ++detailSeq
    selectedId.value = fileId
    detailStatus.value = 'loading'
    detail.value = null
    detailFailure.value = null
    resetText()
    options.onClearExtra()

    try {
      const view = await getFile(fileId)
      if (seq !== detailSeq) return
      detail.value = view
      detailStatus.value = 'ready'
      if (view.document && options.shouldOpenText(fileId)) {
        showText.value = true
        void loadText(false)
      }
    } catch (cause) {
      if (seq !== detailSeq) return
      detailStatus.value = 'failed'
      detailFailure.value = toFailure(cause)
    }
  }

  function retryDetail(): void {
    if (selectedId.value) void selectFile(selectedId.value, { force: true })
  }

  const selectedFile = computed(() => detail.value?.file ?? null)
  const selectedDocument = computed(() => detail.value?.document ?? null)

  return {
    selectedId,
    detail,
    detailStatus,
    detailFailure,
    actionBusy,
    showText,
    text,
    textStatus,
    textFailure,
    textLoadingMore,
    selectedFile,
    selectedDocument,
    clearSelection,
    selectFile,
    retryDetail,
    loadText,
    toggleText,
  }
}
