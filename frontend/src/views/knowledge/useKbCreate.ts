/**
 * 新建知识库。只填名字与描述 —— 解析器与向量模型不在这张表上。
 */

import { ref } from 'vue'

import { isApiFailure } from '../../api/client'
import { describeFailure } from '../../api/errors'
import { createKnowledgeBase } from '../../api/knowledgeBases'
import { KNOWLEDGE } from '../../copy/knowledge'
import { notify } from '../../shared/toast'

export function useKbCreate(deps: { onCreated: (kbId: string) => Promise<void> }) {
  const createOpen = ref(false)
  const createName = ref('')
  const createDescription = ref('')
  const createBusy = ref(false)
  const createError = ref<string | null>(null)

  function openCreate(): void {
    createOpen.value = true
    createError.value = null
  }

  function closeCreate(): void {
    createOpen.value = false
    createName.value = ''
    createDescription.value = ''
    createError.value = null
  }

  async function submitCreate(): Promise<void> {
    const name = createName.value.trim()
    if (!name) {
      createError.value = '名称不能是空的'
      return
    }
    if (createBusy.value) return
    createBusy.value = true
    createError.value = null
    try {
      const result = await createKnowledgeBase(name, createDescription.value.trim())
      notify(KNOWLEDGE.createSucceeded, 'success')
      closeCreate()
      await deps.onCreated(result.knowledge_base.kb_id)
    } catch (cause) {
      createError.value = isApiFailure(cause)
        ? describeFailure(cause.failure)
        : '新建没能完成，请重试'
    } finally {
      createBusy.value = false
    }
  }

  return {
    createOpen,
    createName,
    createDescription,
    createBusy,
    createError,
    openCreate,
    closeCreate,
    submitCreate,
  }
}
