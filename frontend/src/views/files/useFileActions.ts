/**
 * 文件上的动作：下载、移入回收站、恢复、永久删除、加入知识库。
 *
 * 危险动作统一二次确认。purge 被 `file_in_use` 拒绝时列出引用方，而不是让
 * 用户盲目重试 —— 引用关系不解决，重试多少次都一样。
 */

import { ref } from 'vue'

import { isApiFailure } from '../../api/client'
import {
  deleteFile,
  downloadFileContent,
  purgeFile,
  purgeReferences,
  restoreFile,
  type PurgeReference,
} from '../../api/files'
import {
  addKnowledgeBaseDocument,
  listKnowledgeBases,
  type KnowledgeBase,
} from '../../api/knowledgeBases'
import { describeFailure } from '../../api/errors'
import {
  FILE_ACTION_ERRORS,
  FILES,
} from '../../copy/files'
import { confirmAction } from '../../shared/confirm'
import { notify } from '../../shared/toast'

import type { useFileDetail } from './useFileDetail'
import type { useFileList } from './useFileList'

export function useFileActions(deps: {
  list: ReturnType<typeof useFileList>
  detail: ReturnType<typeof useFileDetail>
}) {
  const { list, detail } = deps

  // ── 知识库归属 ──────────────────────────────────────────────────────────
  const kbPickerOpen = ref(false)
  const kbOptions = ref<KnowledgeBase[] | null>(null)
  const kbOptionsFailure = ref<string | null>(null)
  const kbTargetId = ref('')
  const kbBusy = ref(false)

  function resetKbPicker(): void {
    kbPickerOpen.value = false
    kbOptionsFailure.value = null
  }

  async function openKbPicker(): Promise<void> {
    kbPickerOpen.value = true
    if (kbOptions.value !== null) return
    try {
      const result = await listKnowledgeBases()
      kbOptions.value = result.knowledge_bases
      if (!kbTargetId.value) kbTargetId.value = result.knowledge_bases[0]?.kb_id ?? ''
    } catch (cause) {
      kbOptionsFailure.value = isApiFailure(cause)
        ? describeFailure(cause.failure)
        : '知识库列表没能加载出来'
    }
  }

  async function addToKnowledgeBase(): Promise<void> {
    const fileId = detail.selectedId.value
    const kbId = kbTargetId.value
    if (!fileId || !kbId || kbBusy.value) return
    kbBusy.value = true
    try {
      await addKnowledgeBaseDocument(kbId, fileId)
      notify(FILES.addedToKnowledgeBase, 'success')
      kbPickerOpen.value = false
      await detail.selectFile(fileId, { force: true })
    } catch (cause) {
      notify(
        isApiFailure(cause) ? describeFailure(cause.failure) : '加入知识库没能完成，请重试',
        'error',
      )
    } finally {
      kbBusy.value = false
    }
  }

  // ── 生命周期动作 ────────────────────────────────────────────────────────
  async function downloadSelected(): Promise<void> {
    const fileId = detail.selectedId.value
    if (!fileId) return
    try {
      const { blob, filename } = await downloadFileContent(fileId)
      const url = URL.createObjectURL(blob)
      const link = document.createElement('a')
      link.href = url
      link.download = filename
      document.body.appendChild(link)
      link.click()
      link.remove()
      window.setTimeout(() => URL.revokeObjectURL(url), 0)
    } catch {
      notify(FILES.downloadFailed, 'error')
    }
  }

  async function removeSelected(): Promise<void> {
    const current = detail.detail.value
    if (!current || detail.actionBusy.value) return
    const confirmed = await confirmAction({
      title: FILES.removeTitle,
      body: FILES.removeBody,
      note: FILES.removeNote,
      confirmLabel: FILES.removeConfirm,
    })
    if (!confirmed) return

    detail.actionBusy.value = true
    try {
      await deleteFile(current.file.id)
      notify(FILES.removed, 'success')
      await list.reloadAfterChange()
    } catch (cause) {
      notify(actionError(cause), 'error')
    } finally {
      detail.actionBusy.value = false
    }
  }

  async function restoreSelected(): Promise<void> {
    const current = detail.detail.value
    if (!current || detail.actionBusy.value) return
    detail.actionBusy.value = true
    try {
      await restoreFile(current.file.id)
      notify(FILES.restored, 'success')
      await list.reloadAfterChange()
    } catch (cause) {
      notify(actionError(cause), 'error')
    } finally {
      detail.actionBusy.value = false
    }
  }

  const purgeRefusal = ref<string | null>(null)
  const purgeReferencesList = ref<PurgeReference[]>([])

  function resetPurge(): void {
    purgeRefusal.value = null
    purgeReferencesList.value = []
  }

  async function purgeSelected(): Promise<void> {
    const current = detail.detail.value
    if (!current || detail.actionBusy.value) return
    const confirmed = await confirmAction({
      title: FILES.purgeTitle,
      body: FILES.purgeBody,
      note: FILES.purgeNote,
      confirmLabel: FILES.purgeConfirm,
      danger: true,
    })
    if (!confirmed) return

    detail.actionBusy.value = true
    resetPurge()
    try {
      await purgeFile(current.file.id)
      notify(FILES.purged, 'success')
      await list.reloadAfterChange()
    } catch (cause) {
      if (isApiFailure(cause) && cause.failure.code === 'file_in_use') {
        purgeRefusal.value = FILES.purgeInUseTitle
        purgeReferencesList.value = purgeReferences(cause.failure.details)
      } else {
        notify(actionError(cause), 'error')
      }
    } finally {
      detail.actionBusy.value = false
    }
  }

  function actionError(cause: unknown): string {
    if (isApiFailure(cause)) {
      return FILE_ACTION_ERRORS[cause.failure.code] ?? describeFailure(cause.failure)
    }
    return '这一步没能完成，请重试'
  }

  function referenceName(reference: PurgeReference): string {
    return reference.Name || reference.ID
  }

  return {
    kbPickerOpen,
    kbOptions,
    kbOptionsFailure,
    kbTargetId,
    kbBusy,
    resetKbPicker,
    openKbPicker,
    addToKnowledgeBase,
    purgeRefusal,
    purgeReferencesList,
    resetPurge,
    downloadSelected,
    removeSelected,
    restoreSelected,
    purgeSelected,
    referenceName,
  }
}
