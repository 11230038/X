/**
 * 大纲、单元与阅读坐标。
 *
 * 正文按单元懒加载：进入页面只拉第一个单元，滚动到哪里再拉哪里。引用深链
 * 也只加载它落到的那一个单元。所有跳转最终都走 `focusRange`，保证"高亮什么"
 * 与"滚动到哪里"用的是同一份坐标。
 */

import { computed, nextTick, onBeforeUnmount, reactive, ref, type Ref } from 'vue'
import type { RouteLocationNormalizedLoaded } from 'vue-router'

import type { ApiFailure } from '../../../api/client'
import {
  getReadingContent,
  getReadingOutline,
  type ReadingOutline,
  type ReadingUnit,
} from '../../../api/reading'
import { toFailure } from '../../../shared/resource'
import type { HighlightRange } from './text'
import type { ReadingStatus, UnitEntry, UnitState } from './types'

export function useReadingOutline(options: {
  route: RouteLocationNormalizedLoaded
  currentId: Ref<string>
  sessionId: Ref<string>
  onReset: () => void
  afterReady: () => void
}) {
  const outline = ref<ReadingOutline | null>(null)
  const outlineStatus = ref<ReadingStatus>('idle')
  const outlineFailure = ref<ApiFailure | null>(null)
  const units = reactive<Record<number, UnitState>>({})
  const highlight = ref<HighlightRange | null>(null)

  const unitsWithState = computed<UnitEntry[]>(() =>
    (outline.value?.units ?? []).map((unit) => ({
      unit,
      state: units[unit.ordinal] ?? null,
    })),
  )

  const observer =
    typeof IntersectionObserver === 'undefined'
      ? null
      : new IntersectionObserver(
          (entries) => {
            for (const entry of entries) {
              if (!entry.isIntersecting) continue
              const ordinal = Number((entry.target as HTMLElement).dataset.unit)
              if (Number.isFinite(ordinal)) void loadUnit(ordinal)
              observer?.unobserve(entry.target)
            }
          },
          { rootMargin: '600px 0px' },
        )

  onBeforeUnmount(() => {
    observer?.disconnect()
  })

  function bindUnit(el: unknown, ordinal: number): void {
    if (!(el instanceof HTMLElement)) return
    el.dataset.unit = String(ordinal)
    observer?.observe(el)
  }

  function resetUnits(): void {
    for (const key of Object.keys(units)) delete units[Number(key)]
  }

  function unitAt(ordinal: number): ReadingUnit | null {
    return outline.value?.units.find((item) => item.ordinal === ordinal) ?? null
  }

  async function loadUnit(ordinal: number): Promise<void> {
    const existing = units[ordinal]
    if (existing && existing.status !== 'failed') return
    const unit = unitAt(ordinal)
    if (!unit) return

    const materialId = options.currentId.value
    units[ordinal] = { status: 'loading', text: '', failure: null }
    try {
      const loaded = await getReadingContent(materialId, { ordinal })
      if (options.currentId.value !== materialId) return
      units[ordinal] = { status: 'ready', text: loaded.passage.text, failure: null }
    } catch (cause) {
      if (options.currentId.value !== materialId) return
      units[ordinal] = { status: 'failed', text: '', failure: toFailure(cause) }
    }
  }

  function initialOrdinal(): number | null {
    const queryOrdinal = Number(options.route.query.ordinal)
    if (Number.isFinite(queryOrdinal) && queryOrdinal > 0) return queryOrdinal
    const saved = outline.value?.position?.locator.ordinal
    if (saved !== undefined && saved > 0) return saved
    return outline.value?.units[0]?.ordinal ?? null
  }

  function deepLinkHighlight(): void {
    const start = Number(options.route.query.start)
    const end = Number(options.route.query.end)
    const ordinal = Number(options.route.query.ordinal)
    if (!Number.isFinite(start) || !Number.isFinite(end) || end <= start) {
      highlight.value = null
      return
    }
    if (!Number.isFinite(ordinal) || ordinal <= 0) {
      highlight.value = null
      return
    }
    highlight.value = { ordinal, start, end }
  }

  function scrollToUnit(ordinal: number): void {
    const element = document.querySelector(`[data-unit="${ordinal}"]`)
    if (element instanceof HTMLElement) {
      element.scrollIntoView({ block: 'center', behavior: 'smooth' })
    }
  }

  async function jumpToOrdinal(ordinal: number): Promise<void> {
    await loadUnit(ordinal)
    await nextTick()
    scrollToUnit(ordinal)
  }

  async function focusRange(ordinal: number, start: number, end: number): Promise<void> {
    highlight.value = { ordinal, start, end }
    await jumpToOrdinal(ordinal)
  }

  async function loadOutline(): Promise<void> {
    const materialId = options.currentId.value
    if (!materialId) return
    outlineStatus.value = 'loading'
    outlineFailure.value = null
    outline.value = null
    highlight.value = null
    resetUnits()
    options.onReset()

    try {
      const loaded = await getReadingOutline(materialId, options.sessionId.value || undefined)
      if (options.currentId.value !== materialId) return
      outline.value = loaded.outline
      outlineStatus.value = 'ready'
      deepLinkHighlight()

      const ordinal = initialOrdinal()
      if (ordinal !== null) await jumpToOrdinal(ordinal)
      options.afterReady()
    } catch (cause) {
      if (options.currentId.value !== materialId) return
      outlineFailure.value = toFailure(cause)
      outlineStatus.value = 'failed'
    }
  }

  /** 材料被判不存在时的出口：清掉大纲，不留下死坐标。 */
  function clear(): void {
    outline.value = null
    outlineStatus.value = 'idle'
    outlineFailure.value = null
    highlight.value = null
    resetUnits()
  }

  return {
    outline,
    outlineStatus,
    outlineFailure,
    unitsWithState,
    highlight,
    bindUnit,
    unitAt,
    loadUnit,
    loadOutline,
    clear,
    jumpToOrdinal,
    focusRange,
  }
}
