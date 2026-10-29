/**
 * 知识库与文档上的动作：设为默认、删除知识库、逐文档重建、移出文档。
 *
 * 重建只从失败的那一份开始，没有全库重建；删除知识库要二次确认并说明原文件
 * 不受影响。
 */

import { reactive, ref, type ComputedRef, type Ref } from 'vue'
import type { Router } from 'vue-router'

import { isApiFailure } from '../../api/client'
import { describeFailure } from '../../api/errors'
import {
  deleteKnowledgeBase,
  rebuildDocument,
  removeKnowledgeBaseDocument,
  setDefaultKnowledgeBase,
  type KbDocument,
} from '../../api/knowledgeBases'
import { createReadingMaterial } from '../../api/reading'
import { KNOWLEDGE } from '../../copy/knowledge'
import { confirmAction } from '../../shared/confirm'
import { notify } from '../../shared/toast'

import type { KbDetailView } from './useKbDetail'

export function useKbDocuments(deps: {
  selectedId: ComputedRef<string | null>
  router: Router
  detail: Ref<KbDetailView | null>
  loadList: () => Promise<void>
  loadDetail: (kbId: string) => Promise<void>
  clearPoll: () => void
  schedulePoll: () => void
}) {
  const actionBusy = ref(false)
  const rebuildBusy = ref<Set<string>>(new Set())

  async function setDefault(): Promise<void> {
    if (!deps.selectedId.value || actionBusy.value) return
    actionBusy.value = true
    try {
      const result = await setDefaultKnowledgeBase(deps.selectedId.value)
      if (deps.detail.value) {
        deps.detail.value = { ...deps.detail.value, knowledge_base: result.knowledge_base }
      }
      await deps.loadList()
      notify(KNOWLEDGE.setDefaultSucceeded, 'success')
    } catch (cause) {
      notify(isApiFailure(cause) ? describeFailure(cause.failure) : '没能设为默认，请重试', 'error')
    } finally {
      actionBusy.value = false
    }
  }

  async function deleteBase(): Promise<void> {
    const current = deps.detail.value?.knowledge_base
    if (!current || actionBusy.value) return
    const confirmed = await confirmAction({
      title: KNOWLEDGE.deleteTitle,
      body: KNOWLEDGE.deleteBody,
      note: KNOWLEDGE.deleteNote,
      confirmLabel: KNOWLEDGE.deleteConfirm,
      danger: true,
    })
    if (!confirmed) return

    actionBusy.value = true
    try {
      await deleteKnowledgeBase(current.kb_id)
      notify(KNOWLEDGE.deleteSucceeded, 'success')
      deps.clearPoll()
      deps.detail.value = null
      await deps.loadList()
      await deps.router.push({ name: 'knowledge-bases' })
    } catch (cause) {
      notify(isApiFailure(cause) ? describeFailure(cause.failure) : '删除没能完成，请重试', 'error')
    } finally {
      actionBusy.value = false
    }
  }

  function isRebuilding(documentId: string): boolean {
    return rebuildBusy.value.has(documentId)
  }

  async function rebuild(document: KbDocument): Promise<void> {
    const kbId = deps.selectedId.value
    if (!kbId || isRebuilding(document.document_id)) return
    rebuildBusy.value = new Set(rebuildBusy.value).add(document.document_id)
    try {
      const result = await rebuildDocument(kbId, document.document_id)
      if (deps.detail.value) {
        deps.detail.value = {
          ...deps.detail.value,
          documents: deps.detail.value.documents.map((item) =>
            item.document_id === document.document_id ? result.document : item,
          ),
        }
      }
      notify(KNOWLEDGE.rebuildSucceeded, 'success')
      deps.schedulePoll()
    } catch (cause) {
      notify(
        isApiFailure(cause) ? describeFailure(cause.failure) : KNOWLEDGE.rebuildFailed,
        'error',
      )
    } finally {
      const next = new Set(rebuildBusy.value)
      next.delete(document.document_id)
      rebuildBusy.value = next
    }
  }

  // ── 从知识库进沉浸阅读 ──────────────────────────────────────────────────
  // 阅读材料建在文档背后的那份文件上；同一份文件已经有材料时，服务端会把旧的
  // 还回来，所以点两次不会多出一个材料。
  const readingBusy = ref<Set<string>>(new Set())
  const readingErrors = reactive<Record<string, string>>({})

  function isEnteringReading(documentId: string): boolean {
    return readingBusy.value.has(documentId)
  }

  function readingError(document: KbDocument): string {
    return readingErrors[document.document_id] ?? ''
  }

  async function enterReading(document: KbDocument): Promise<void> {
    if (isEnteringReading(document.document_id)) return
    delete readingErrors[document.document_id]
    readingBusy.value = new Set(readingBusy.value).add(document.document_id)
    try {
      const created = await createReadingMaterial(document.file_id, 'knowledge_base')
      await deps.router.push({
        name: 'reading',
        query: { material: created.material.material_id },
      })
    } catch (cause) {
      // 失败留在卡片上：这里正盯着这份文档，浮层飘走就找不到是哪一份了。
      readingErrors[document.document_id] = isApiFailure(cause)
        ? describeFailure(cause.failure)
        : KNOWLEDGE.enterReadingFailed
    } finally {
      const next = new Set(readingBusy.value)
      next.delete(document.document_id)
      readingBusy.value = next
    }
  }

  async function removeDocument(document: KbDocument): Promise<void> {
    const kbId = deps.selectedId.value
    if (!kbId || actionBusy.value) return
    const confirmed = await confirmAction({
      title: KNOWLEDGE.removeDocumentTitle,
      body: KNOWLEDGE.removeDocumentBody,
      confirmLabel: KNOWLEDGE.removeDocumentConfirm,
      danger: true,
    })
    if (!confirmed) return

    actionBusy.value = true
    try {
      await removeKnowledgeBaseDocument(kbId, document.document_id)
      notify(KNOWLEDGE.removeSucceeded, 'success')
      await deps.loadDetail(kbId)
      await deps.loadList()
    } catch (cause) {
      notify(isApiFailure(cause) ? describeFailure(cause.failure) : '移出没能完成，请重试', 'error')
    } finally {
      actionBusy.value = false
    }
  }

  return {
    actionBusy,
    isRebuilding,
    setDefault,
    deleteBase,
    rebuild,
    enterReading,
    isEnteringReading,
    readingError,
    removeDocument,
  }
}
