/**
 * 批注。
 *
 * 写入必须带 `session_id`；没有会话时由页面的 `needSession()` 拒绝。写成功后
 * 重新读整份批注列表，而不是把返回值塞进数组 —— 列表的顺序与去重由服务端
 * 决定，界面只负责显示。
 */

import { ref, type Ref } from 'vue'

import type { ApiFailure } from '../../../api/client'
import {
  addMark as createMark,
  listMarks,
  type MarkKind,
  type ReadingLocator,
  type ReadingMark,
} from '../../../api/reading'
import { READING } from '../../../copy/reading'
import { toFailure } from '../../../shared/resource'
import { notify } from '../../../shared/toast'

type Status = 'idle' | 'loading' | 'ready' | 'failed'

export function useReadingMarks(options: {
  currentId: Ref<string>
  sessionId: Ref<string>
  needSession: () => boolean
  currentLocator: () => ReadingLocator | null
  clearSelection: () => void
  jumpToLocator: (locator: ReadingLocator) => void
}) {
  const marks = ref<ReadingMark[]>([])
  const marksStatus = ref<Status>('idle')
  const marksFailure = ref<ApiFailure | null>(null)
  const markFailure = ref<ApiFailure | null>(null)
  const markSaving = ref(false)

  async function loadMarks(): Promise<void> {
    const materialId = options.currentId.value
    marksStatus.value = 'loading'
    try {
      const loaded = await listMarks(materialId)
      if (options.currentId.value !== materialId) return
      marks.value = loaded.marks
      marksStatus.value = 'ready'
      marksFailure.value = null
    } catch (cause) {
      if (options.currentId.value !== materialId) return
      marksFailure.value = toFailure(cause)
      marksStatus.value = 'failed'
    }
  }

  async function addMark(kind: MarkKind, body: string): Promise<boolean> {
    if (!options.needSession()) return false
    const locator = options.currentLocator()
    if (!locator) {
      notify(READING.noLoadedUnit, 'error')
      return false
    }
    markSaving.value = true
    markFailure.value = null
    try {
      await createMark(options.currentId.value, {
        session_id: options.sessionId.value,
        kind,
        locator,
        body,
      })
      await loadMarks()
      options.clearSelection()
      return true
    } catch (cause) {
      markFailure.value = toFailure(cause)
      return false
    } finally {
      markSaving.value = false
    }
  }

  async function addHighlight(): Promise<void> {
    await addMark('highlight', '')
  }

  /**
   * 收藏选中的这一段。书签在正文里没有可见的痕迹（只有高亮会在原文上画），
   * 所以这里补一条 toast —— 否则按下之后屏幕上看不出发生了什么。位置由
   * 收藏夹按坐标回读原文，书签自己不复制那段文字。
   */
  async function addBookmark(): Promise<void> {
    if (await addMark('bookmark', '')) notify(READING.favoriteSaved, 'success')
  }

  function jumpToMark(mark: ReadingMark): void {
    options.jumpToLocator(mark.locator)
  }

  function resetMarks(): void {
    marks.value = []
    marksStatus.value = 'idle'
    marksFailure.value = null
    markFailure.value = null
  }

  return {
    marks,
    marksStatus,
    marksFailure,
    markFailure,
    markSaving,
    loadMarks,
    addMark,
    addHighlight,
    addBookmark,
    jumpToMark,
    resetMarks,
  }
}
