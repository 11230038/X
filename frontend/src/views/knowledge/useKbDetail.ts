/**
 * 知识库列表与详情。
 *
 * `vector_state` 是逐文档的：只要还有 pending/running，就每 4 秒重读一次详情；
 * 连续三次失败才把轮询失败挂出来。一份文档在准备向量，不代表整页不可用。
 */

import { computed, onBeforeUnmount, ref, watch, type ComputedRef } from 'vue'
import type { Router } from 'vue-router'

import type { ApiFailure } from '../../api/client'
import {
  getKnowledgeBase,
  listKnowledgeBases,
  type KbDocument,
  type KnowledgeBase,
} from '../../api/knowledgeBases'
import { canSurface } from '../../auth/permissions'
import { toFailure } from '../../shared/resource'

export interface KbDetailView {
  knowledge_base: KnowledgeBase
  documents: KbDocument[]
}

type LoadStatus = 'idle' | 'loading' | 'ready' | 'failed'

export function useKbDetail(deps: {
  selectedId: ComputedRef<string | null>
  router: Router
  /** 换库时清掉只属于上一份详情的面板状态（加入文档、检索结果）。 */
  onResetPanels: () => void
}) {
  // ── 列表 ────────────────────────────────────────────────────────────────
  const bases = ref<KnowledgeBase[]>([])
  const listStatus = ref<LoadStatus>('idle')
  const listFailure = ref<ApiFailure | null>(null)

  async function loadList(): Promise<void> {
    if (canSurface('knowledge_base') === false) return
    if (bases.value.length === 0) listStatus.value = 'loading'
    try {
      const result = await listKnowledgeBases()
      bases.value = result.knowledge_bases
      listStatus.value = 'ready'
      listFailure.value = null
    } catch (cause) {
      if (bases.value.length === 0) {
        listStatus.value = 'failed'
        listFailure.value = toFailure(cause)
      }
    }
  }

  function openBase(kbId: string): void {
    void deps.router.push({ name: 'knowledge-base', params: { kbId } })
  }

  // ── 详情 ────────────────────────────────────────────────────────────────
  const detail = ref<KbDetailView | null>(null)
  const detailStatus = ref<LoadStatus>('idle')
  const detailFailure = ref<ApiFailure | null>(null)
  const pollFailure = ref<ApiFailure | null>(null)

  let detailSeq = 0
  let pollTimer: number | null = null
  let pollFailures = 0

  function clearPoll(): void {
    if (pollTimer !== null) {
      window.clearTimeout(pollTimer)
      pollTimer = null
    }
  }

  function hasPendingVectors(view: KbDetailView | null): boolean {
    if (!view) return false
    return view.documents.some(
      (document) => document.vector_state === 'pending' || document.vector_state === 'running',
    )
  }

  function schedulePoll(): void {
    clearPoll()
    if (!hasPendingVectors(detail.value)) return

    pollTimer = window.setTimeout(async () => {
      const kbId = deps.selectedId.value
      if (!kbId) return
      try {
        const result = await getKnowledgeBase(kbId)
        if (kbId !== deps.selectedId.value) return
        detail.value = result
        pollFailures = 0
        pollFailure.value = null
        schedulePoll()
      } catch (cause) {
        if (kbId !== deps.selectedId.value) return
        pollFailures += 1
        if (pollFailures >= 3) {
          pollFailure.value = toFailure(cause)
        } else {
          schedulePoll()
        }
      }
    }, 4000)
  }

  async function loadDetail(kbId: string): Promise<void> {
    if (canSurface('knowledge_base') === false) return
    const seq = ++detailSeq
    clearPoll()
    if (detail.value?.knowledge_base.kb_id !== kbId) {
      detail.value = null
    }
    detailStatus.value = 'loading'
    detailFailure.value = null
    pollFailure.value = null
    pollFailures = 0
    deps.onResetPanels()

    try {
      const result = await getKnowledgeBase(kbId)
      if (seq !== detailSeq) return
      detail.value = result
      detailStatus.value = 'ready'
      schedulePoll()
    } catch (cause) {
      if (seq !== detailSeq) return
      detailStatus.value = 'failed'
      detailFailure.value = toFailure(cause)
    }
  }

  function retryDetail(): void {
    if (deps.selectedId.value) void loadDetail(deps.selectedId.value)
  }

  function refreshDetail(): Promise<void> {
    return deps.selectedId.value ? loadDetail(deps.selectedId.value) : Promise.resolve()
  }

  watch(
    () => deps.selectedId.value,
    (kbId) => {
      if (kbId) {
        void loadDetail(kbId)
      } else {
        detailSeq += 1
        clearPoll()
        detail.value = null
        detailStatus.value = 'idle'
        detailFailure.value = null
      }
    },
    { immediate: true },
  )

  onBeforeUnmount(() => {
    clearPoll()
    detailSeq += 1
  })

  const currentBase = computed(() => detail.value?.knowledge_base ?? null)
  const currentDocuments = computed(() => detail.value?.documents ?? [])
  const partialVectors = computed(() => hasPendingVectors(detail.value))

  return {
    bases,
    listStatus,
    listFailure,
    loadList,
    openBase,
    detail,
    detailStatus,
    detailFailure,
    pollFailure,
    clearPoll,
    schedulePoll,
    loadDetail,
    retryDetail,
    refreshDetail,
    currentBase,
    currentDocuments,
    partialVectors,
  }
}
