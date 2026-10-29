/**
 * 选区。
 *
 * 浏览器里的选区先量成**单元内**的字符偏移，再加上单元的起点，得到材料内的
 * 绝对坐标。批注、问答与对话里的 `selection` 拿到的都是这一份坐标。
 *
 * markdown 材料的正文渲染丢掉了 `**`、`##` 这类语法字符，DOM 里第几个字和
 * 原文第几个字不再一一对应。`ReadingRuns` 给每个文字片段标了 `data-src`
 * （它在原文里的起点），所以这里先按渲染顺序摊开一张"渲染下标 → 原文偏移"
 * 的对照表，再把浏览器的选区换算回原文。纯文本材料没有这张表，直接按
 * 渲染出来的字数算 —— 两边本来就相等。
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

  /** 渲染出来的每一个字对应原文的哪个偏移；没有 `data-src` 的片段记 `null`。 */
  function sourceOffsets(body: HTMLElement): (number | null)[] {
    const offsets: (number | null)[] = []
    const walker = document.createTreeWalker(body, NodeFilter.SHOW_TEXT)
    for (let node = walker.nextNode(); node !== null; node = walker.nextNode()) {
      const text = node.nodeValue ?? ''
      const holder = node.parentElement?.closest('[data-src]')
      const base = holder instanceof HTMLElement ? Number(holder.dataset.src) : Number.NaN
      for (let i = 0; i < text.length; i += 1) {
        offsets.push(Number.isFinite(base) ? base + i : null)
      }
    }
    return offsets
  }

  /** 选区起点：从第 `index` 个字往后找第一个有原文坐标的字。 */
  function startSource(offsets: readonly (number | null)[], index: number): number | null {
    for (let i = index; i < offsets.length; i += 1) {
      const value = offsets[i]
      if (value !== null && value !== undefined) return value
    }
    for (let i = index - 1; i >= 0; i -= 1) {
      const value = offsets[i]
      if (value !== null && value !== undefined) return value + 1
    }
    return null
  }

  /** 选区终点：最后一个被选中的字之后。 */
  function endSource(offsets: readonly (number | null)[], index: number): number | null {
    for (let i = index - 1; i >= 0; i -= 1) {
      const value = offsets[i]
      if (value !== null && value !== undefined) return value + 1
    }
    for (let i = index; i < offsets.length; i += 1) {
      const value = offsets[i]
      if (value !== null && value !== undefined) return value
    }
    return null
  }

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

    const offsets = sourceOffsets(body)
    const mapped = offsets.some((value) => value !== null)
    const startPoint = mapped ? startSource(offsets, before.toString().length) : null
    const endPoint = mapped ? endSource(offsets, upto.toString().length) : null

    const start = clamp(
      startPoint === null ? before.toString().length : startPoint - unit.char_start,
      0,
      total,
    )
    const end = clamp(
      endPoint === null ? upto.toString().length : endPoint - unit.char_start,
      0,
      total,
    )
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
