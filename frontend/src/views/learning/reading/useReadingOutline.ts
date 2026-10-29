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

  /** 正在路上的单元读取。跳转要等它们，否则滚动目标会被后到的正文推走。 */
  const inflight = new Map<number, Promise<void>>()

  function resetUnits(): void {
    for (const key of Object.keys(units)) delete units[Number(key)]
    inflight.clear()
  }

  function unitAt(ordinal: number): ReadingUnit | null {
    return outline.value?.units.find((item) => item.ordinal === ordinal) ?? null
  }

  function loadUnit(ordinal: number): Promise<void> {
    const existing = units[ordinal]
    if (existing?.status === 'ready') return Promise.resolve()
    // 同一个单元可能同时被滚动观察器和一次跳转点到：两边等的是同一个请求。
    if (existing?.status === 'loading') return inflight.get(ordinal) ?? Promise.resolve()
    const unit = unitAt(ordinal)
    if (!unit) return Promise.resolve()

    const materialId = options.currentId.value
    units[ordinal] = { status: 'loading', text: '', failure: null }
    let self: Promise<void> | null = null
    const task = (async () => {
      try {
        // 正文按坐标读：`ordinal` 只是告诉服务端"我以为自己在第几个单元"的
        // 提示，`char_start` / `char_end` 才是真正的读取范围。只发 ordinal
        // 会被服务端以 400 `invalid_request` 拒绝，材料就一个字都出不来。
        const loaded = await getReadingContent(materialId, {
          char_start: unit.char_start,
          char_end: unit.char_end,
          ordinal,
        })
        if (options.currentId.value !== materialId) return
        units[ordinal] = { status: 'ready', text: loaded.passage.text, failure: null }
      } catch (cause) {
        if (options.currentId.value !== materialId) return
        units[ordinal] = { status: 'failed', text: '', failure: toFailure(cause) }
      } finally {
        if (self !== null && inflight.get(ordinal) === self) inflight.delete(ordinal)
      }
    })()
    self = task
    inflight.set(ordinal, task)
    return task
  }

  /**
   * 打开材料时的落点：只有 URL 明确点名了第几段（对话引用、批注深链）才滚动。
   * 上次读到哪里不替人翻页 —— 打开一份材料先看到开头，是"打开"这个词的意思。
   */
  function deepLinkOrdinal(): number | null {
    const queryOrdinal = Number(options.route.query.ordinal)
    if (Number.isFinite(queryOrdinal) && queryOrdinal >= 0) return queryOrdinal
    return null
  }

  function deepLinkHighlight(): void {
    const start = Number(options.route.query.start)
    const end = Number(options.route.query.end)
    const ordinal = Number(options.route.query.ordinal)
    if (!Number.isFinite(start) || !Number.isFinite(end) || end <= start) {
      highlight.value = null
      return
    }
    if (!Number.isFinite(ordinal) || ordinal < 0) {
      highlight.value = null
      return
    }
    highlight.value = { ordinal, start, end }
  }

  async function jumpToOrdinal(ordinal: number): Promise<void> {
    // 先把目标之前的单元都读出来：它们晚到一步，目标就会被推下去一次。目标
    // 之后的单元在下方，不影响目标的位置，不必为一次跳转把整篇都拉下来。
    const earlier = (outline.value?.units ?? []).filter((unit) => unit.ordinal < ordinal)
    await Promise.all([...earlier.map((unit) => loadUnit(unit.ordinal)), loadUnit(ordinal)])
    await nextTick()
    // 单元就位就跳，不再停顿。`start` 而不是 `center`，目标是"从这一段读起"；
    // 立即跳而不是平滑滚动，动画的落点会被推走。
    const element = document.querySelector(`[data-unit="${ordinal}"]`)
    if (element instanceof HTMLElement) {
      element.scrollIntoView({ block: 'start', behavior: 'auto' })
    }
  }

  async function focusRange(ordinal: number, start: number, end: number): Promise<void> {
    highlight.value = { ordinal, start, end }
    await jumpToOrdinal(ordinal)
  }

  async function loadOutline(): Promise<void> {
    const materialId = options.currentId.value
    if (!materialId) return
    const previous = outline.value?.material.material_id ?? ''
    // 换材料时旧正文先留在屏幕上：大纲和第一段正文都读出来，再一次换过去。
    // 先塌成骨架再长回来，一眼能看见两次跳动；旧正文不动，只有末尾这一下。
    const switching = previous !== '' && previous !== materialId

    outlineFailure.value = null
    if (!switching) {
      outlineStatus.value = 'loading'
      outline.value = null
      highlight.value = null
      resetUnits()
      options.onReset()
    }

    try {
      const loaded = await getReadingOutline(materialId, options.sessionId.value || undefined)
      if (options.currentId.value !== materialId) return

      // 第一段正文和大纲一起读出来。只换大纲的话，新材料换上去的第一帧还
      // 没有字，满屏「加载这一段」，底部却已经亮着「记录阅读位置」——
      // 等正文回来才填上，看着就像换页换了一半。读到字再换，是一步到位。
      const first = loaded.outline.units[0]
      let firstUnit: { ordinal: number; text: string } | null = null
      if (first !== undefined) {
        try {
          const content = await getReadingContent(materialId, {
            char_start: first.char_start,
            char_end: first.char_end,
            ordinal: first.ordinal,
          })
          if (options.currentId.value !== materialId) return
          firstUnit = { ordinal: first.ordinal, text: content.passage.text }
        } catch {
          // 第一段读不出来就交给懒加载：它会在原位报错，并给出重试。
        }
      }

      // 旧正文一直留到这一刻：新材料的大纲和第一段都到了，才一次性换过去。
      if (switching) {
        highlight.value = null
        resetUnits()
        options.onReset()
      }
      outline.value = loaded.outline
      outlineStatus.value = 'ready'
      if (firstUnit !== null) {
        units[firstUnit.ordinal] = { status: 'ready', text: firstUnit.text, failure: null }
      }

      // 只有地址**点名了这一份材料**时，地址里的坐标才是给它的。换材料时
      // `router.replace` 还没落定，旧地址里的 `ordinal` 会晚一步被读到 ——
      // 拿它去跳新材料，就会在内容还没加载时跳到一段无关的位置。
      const named = options.route.query.material
      const isNamed = named === undefined || named === '' || named === materialId
      if (isNamed) {
        deepLinkHighlight()
        const ordinal = deepLinkOrdinal()
        if (ordinal !== null) await jumpToOrdinal(ordinal)
      }
      options.afterReady()
    } catch (cause) {
      if (options.currentId.value !== materialId) return
      // 换材料失败：旧正文不能继续冒充新材料，一并撤掉。
      outline.value = null
      highlight.value = null
      resetUnits()
      options.onReset()
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
