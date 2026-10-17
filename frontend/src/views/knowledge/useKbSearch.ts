/**
 * 检索测试。
 *
 * 向量还没就绪时全文检索仍然可用 —— 空结果不是失败，提问太短与冲突码各自
 * 说自己的话。
 */

import { ref, type ComputedRef } from 'vue'

import { isApiFailure } from '../../api/client'
import { describeFailure } from '../../api/errors'
import { searchKnowledgeBase, type KbSource } from '../../api/knowledgeBases'
import { KNOWLEDGE } from '../../copy/knowledge'

export function useKbSearch(selectedId: ComputedRef<string | null>) {
  const searchQuery = ref('')
  const searchBusy = ref(false)
  const searchResults = ref<KbSource[] | null>(null)
  const searchFailure = ref<string | null>(null)
  const searchedFor = ref('')

  function resetSearch(): void {
    searchQuery.value = ''
    searchBusy.value = false
    searchResults.value = null
    searchFailure.value = null
    searchedFor.value = ''
  }

  async function runSearch(): Promise<void> {
    const kbId = selectedId.value
    const query = searchQuery.value.trim()
    if (!kbId || searchBusy.value) return
    if (!query) {
      searchFailure.value = '写一个检索词再试'
      return
    }

    searchBusy.value = true
    searchFailure.value = null
    try {
      const result = await searchKnowledgeBase(kbId, query)
      if (kbId !== selectedId.value) return
      searchResults.value = result.sources
      searchedFor.value = query
    } catch (cause) {
      if (kbId !== selectedId.value) return
      if (isApiFailure(cause) && cause.failure.code === 'query_too_short') {
        searchFailure.value = KNOWLEDGE.errorQueryTooShort
      } else if (isApiFailure(cause) && cause.failure.code === 'conflict') {
        searchFailure.value = KNOWLEDGE.errorConflict
      } else {
        searchFailure.value = isApiFailure(cause)
          ? describeFailure(cause.failure)
          : KNOWLEDGE.searchFailed
      }
      searchResults.value = null
    } finally {
      searchBusy.value = false
    }
  }

  return {
    searchQuery,
    searchBusy,
    searchResults,
    searchFailure,
    searchedFor,
    resetSearch,
    runSearch,
  }
}
