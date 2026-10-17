/**
 * 往知识库加入一份文件。
 *
 * 候选只列还没被删、也还没在这份知识库里的文件；加入成功后重读详情，让向量
 * 状态从 pending 开始按真实进度走。
 */

import { ref, type ComputedRef, type Ref } from 'vue'

import { isApiFailure } from '../../api/client'
import { describeFailure } from '../../api/errors'
import { listFiles, type FileSummary } from '../../api/files'
import { addKnowledgeBaseDocument } from '../../api/knowledgeBases'
import { KNOWLEDGE } from '../../copy/knowledge'
import { notify } from '../../shared/toast'

import type { KbDetailView } from './useKbDetail'

export function useKbAdd(deps: {
  selectedId: ComputedRef<string | null>
  detail: Ref<KbDetailView | null>
  loadDetail: (kbId: string) => Promise<void>
  loadList: () => Promise<void>
}) {
  const addOpen = ref(false)
  const fileOptions = ref<FileSummary[] | null>(null)
  const addFileId = ref('')
  const addBusy = ref(false)
  const addFailure = ref<string | null>(null)

  function resetAdd(): void {
    addOpen.value = false
    fileOptions.value = null
    addFileId.value = ''
    addFailure.value = null
  }

  async function openAdd(): Promise<void> {
    addOpen.value = true
    if (fileOptions.value !== null) return
    try {
      const result = await listFiles({ limit: 100 })
      const used = new Set(deps.detail.value?.documents.map((document) => document.file_id) ?? [])
      fileOptions.value = result.files.filter((file) => !file.deleted_at && !used.has(file.id))
      addFileId.value = fileOptions.value[0]?.id ?? ''
    } catch (cause) {
      addFailure.value = isApiFailure(cause)
        ? describeFailure(cause.failure)
        : '文件列表没能加载出来'
    }
  }

  async function submitAdd(): Promise<void> {
    const kbId = deps.selectedId.value
    const fileId = addFileId.value
    if (!kbId || !fileId || addBusy.value) return
    addBusy.value = true
    addFailure.value = null
    try {
      await addKnowledgeBaseDocument(kbId, fileId)
      notify(KNOWLEDGE.addSucceeded, 'success')
      resetAdd()
      await deps.loadDetail(kbId)
      await deps.loadList()
    } catch (cause) {
      addFailure.value = isApiFailure(cause)
        ? describeFailure(cause.failure)
        : '加入没能完成，请重试'
    } finally {
      addBusy.value = false
    }
  }

  return {
    addOpen,
    fileOptions,
    addFileId,
    addBusy,
    addFailure,
    resetAdd,
    openAdd,
    submitAdd,
  }
}
