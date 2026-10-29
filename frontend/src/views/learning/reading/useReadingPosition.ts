/**
 * 阅读位置。
 *
 * 位置与批注共用同一套坐标：优先取当前选区，没有选区就取第一个已加载的
 * 单元。保存必须带 `session_id`，`revision` 用来让服务端拒绝过期写入。
 */

import { ref, type ComputedRef, type Ref } from 'vue'

import type { ApiFailure } from '../../../api/client'
import {
  savePosition,
  type ReadingLocator,
  type ReadingOutline,
} from '../../../api/reading'
import { READING } from '../../../copy/reading'
import { toFailure } from '../../../shared/resource'
import { notify } from '../../../shared/toast'

import type { UnitEntry } from './types'
import type { ReadingSelection } from './useReadingSelection'

export function useReadingPosition(options: {
  currentId: Ref<string>
  sessionId: Ref<string>
  needSession: () => boolean
  selection: Ref<ReadingSelection | null>
  outline: Ref<ReadingOutline | null>
  unitsWithState: ComputedRef<UnitEntry[]>
  jumpToOrdinal: (ordinal: number) => Promise<void>
  focusRange: (ordinal: number, start: number, end: number) => Promise<void>
}) {
  const positionSaving = ref(false)
  const positionFailure = ref<ApiFailure | null>(null)

  function currentLocator(): ReadingLocator | null {
    if (options.selection.value) return options.selection.value.locator
    const loaded = options.unitsWithState.value.find((item) => item.state?.status === 'ready')
    if (!loaded) return null
    return {
      material_id: options.currentId.value,
      ordinal: loaded.unit.ordinal,
      char_start: loaded.unit.char_start,
      char_end: loaded.unit.char_end,
      ...(loaded.unit.page !== undefined ? { page: loaded.unit.page } : {}),
    }
  }

  function jumpToLocator(locator: ReadingLocator): void {
    void options.focusRange(locator.ordinal, locator.char_start, locator.char_end)
  }

  async function recordPosition(): Promise<void> {
    if (!options.needSession()) return
    const locator = currentLocator()
    if (!locator) {
      notify(READING.noLoadedUnit, 'error')
      return
    }
    positionSaving.value = true
    positionFailure.value = null
    try {
      const saved = await savePosition(options.currentId.value, {
        session_id: options.sessionId.value,
        locator,
        revision: options.outline.value?.position?.revision ?? 0,
      })
      if (options.outline.value) options.outline.value.position = saved.position
      notify(READING.positionSaved, 'success')
    } catch (cause) {
      positionFailure.value = toFailure(cause)
    } finally {
      positionSaving.value = false
    }
  }

  function jumpToPosition(): void {
    const saved = options.outline.value?.position?.locator.ordinal
    // 第 0 段是合法的第一段：单单元的材料永远停在这里。
    if (saved === undefined || saved < 0) return
    void options.jumpToOrdinal(saved)
  }

  return {
    positionSaving,
    positionFailure,
    currentLocator,
    jumpToLocator,
    recordPosition,
    jumpToPosition,
  }
}
