/**
 * 选区。
 *
 * 浏览器里的选区先量成**单元内**的字符偏移，再加上单元的起点，得到材料内的
 * 绝对坐标。批注、扩展与对话里的 `selection` 拿到的都是这一份坐标。
 */

import { ref, type Ref } from 'vue'
import type { Router } from 'vue-router'

import type { ReadingLocator, ReadingUnit } from '../../../api/reading'
import { truncate } from '../../../shared/format'
import { notify } from '../../../shared/toast'
import { READING } from '../../../copy/reading'

import { clamp } from './text'

export interface ReadingSelection {
  locator: ReadingLocator
  text: string
}

export function useReadingSelection(
  router: Router,
  currentId: Ref<string>,
  sessionId: Ref<string>,
  unitAt: (ordinal: number) => ReadingUnit | null,
) {
  const selection = ref<ReadingSelection | null>(null)

  function captureSelection(): void {
    const browserSelection = window.getSelection()
    if (!browserSelection || browserSelection.rangeCount === 0 || browserSelection.isCollapsed) {
      return
    }
    const range = browserSelection.getRangeAt(0)
    const ancestor = range.commonAncestorContainer
    const element = ancestor instanceof Element ? ancestor : ancestor.parentElement
    const body = element?.closest('[data-unit-body]')
    if (!(body instanceof HTMLElement)) return

    const ordinal = Number(body.dataset.ordinal)
    const unit = unitAt(ordinal)
    if (!unit) return
    const total = unit.char_end - unit.char_start

    const before = document.createRange()
    before.selectNodeContents(body)
    before.setEnd(range.startContainer, range.startOffset)

    const upto = document.createRange()
    upto.selectNodeContents(body)
    upto.setEnd(range.endContainer, range.endOffset)

    const start = clamp(before.toString().length, 0, total)
    const end = clamp(upto.toString().length, 0, total)
    if (end <= start) return
    const text = range.toString()
    if (text.trim() === '') return

    selection.value = {
      locator: {
        material_id: currentId.value,
        ordinal,
        char_start: unit.char_start + start,
        char_end: unit.char_start + end,
        ...(unit.page !== undefined ? { page: unit.page } : {}),
      },
      text,
    }
  }

  function clearSelection(): void {
    selection.value = null
    window.getSelection()?.removeAllRanges()
  }

  async function copySelection(): Promise<void> {
    if (!selection.value) return
    try {
      await navigator.clipboard.writeText(selection.value.text)
      notify(READING.copied, 'success')
    } catch {
      notify(READING.copyFailed, 'error')
    }
  }

  function askInChat(): void {
    if (!selection.value) return
    const target = sessionId.value
      ? { name: 'conversation' as const, params: { sessionId: sessionId.value } }
      : { name: 'chat' as const }
    void router.push({
      ...target,
      query: {
        material: selection.value.locator.material_id,
        ordinal: String(selection.value.locator.ordinal),
        start: String(selection.value.locator.char_start),
        end: String(selection.value.locator.char_end),
        text: truncate(selection.value.text, 500),
      },
    })
  }

  return {
    selection,
    captureSelection,
    clearSelection,
    copySelection,
    askInChat,
  }
}
