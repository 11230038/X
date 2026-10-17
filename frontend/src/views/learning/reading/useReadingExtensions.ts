/**
 * 阅读扩展。
 *
 * 可用性由服务端逐项给出；不可用的扩展**留着并写明原因**，不悄悄从列表里
 * 消失。被跳过的结果同理，带原因保留。
 */

import { ref, type Ref } from 'vue'

import type { ApiFailure } from '../../../api/client'
import {
  listExtensions,
  runExtension,
  type ExtensionOutcome,
  type ReadingCard,
  type ReadingMoment,
  type ReadingQuizQuestion,
} from '../../../api/reading'
import { READING } from '../../../copy/reading'
import type { ReadingSelection } from './useReadingSelection'
import { toFailure } from '../../../shared/resource'
import { notify } from '../../../shared/toast'

type Status = 'idle' | 'loading' | 'ready' | 'failed'

export interface ExtensionManifest {
  name: string
  schema: string
  available: boolean
  reason?: string
}

export function useReadingExtensions(
  currentId: Ref<string>,
  selection: Ref<ReadingSelection | null>,
) {
  const extensions = ref<ExtensionManifest[]>([])
  const extensionsStatus = ref<Status>('idle')
  const extensionsFailure = ref<ApiFailure | null>(null)
  const extensionResults = ref<ExtensionOutcome[]>([])
  const extensionRunning = ref('')
  const extensionFailure = ref<ApiFailure | null>(null)

  async function loadExtensions(): Promise<void> {
    const materialId = currentId.value
    extensionsStatus.value = 'loading'
    try {
      const loaded = await listExtensions(materialId)
      if (currentId.value !== materialId) return
      extensions.value = loaded.extensions
      extensionsStatus.value = 'ready'
      extensionsFailure.value = null
    } catch (cause) {
      if (currentId.value !== materialId) return
      extensionsFailure.value = toFailure(cause)
      extensionsStatus.value = 'failed'
    }
  }

  async function runOne(name: string): Promise<void> {
    if (!selection.value) {
      notify(READING.extensionNeedsSelection, 'error')
      return
    }
    extensionRunning.value = name
    extensionFailure.value = null
    try {
      const loaded = await runExtension(currentId.value, name, selection.value.locator)
      extensionResults.value = [loaded.extension, ...extensionResults.value]
    } catch (cause) {
      extensionFailure.value = toFailure(cause)
    } finally {
      extensionRunning.value = ''
    }
  }

  function resetExtensions(): void {
    extensions.value = []
    extensionsStatus.value = 'idle'
    extensionsFailure.value = null
    extensionResults.value = []
    extensionRunning.value = ''
    extensionFailure.value = null
  }

  return {
    extensions,
    extensionsStatus,
    extensionsFailure,
    extensionResults,
    extensionRunning,
    extensionFailure,
    loadExtensions,
    runOne,
    resetExtensions,
  }
}

export function asCards(result: unknown): ReadingCard[] {
  return Array.isArray(result) ? (result as ReadingCard[]) : []
}

export function asMoments(result: unknown): ReadingMoment[] {
  return Array.isArray(result) ? (result as ReadingMoment[]) : []
}

export function asQuiz(result: unknown): ReadingQuizQuestion[] {
  return Array.isArray(result) ? (result as ReadingQuizQuestion[]) : []
}

export function audioUrl(result: unknown): string {
  if (result && typeof result === 'object' && 'url' in result) {
    const value = (result as { url?: unknown }).url
    return typeof value === 'string' ? value : ''
  }
  return ''
}
