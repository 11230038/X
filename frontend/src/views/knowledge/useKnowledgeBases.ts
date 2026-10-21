/**
 * 知识库页的组装层。
 *
 * 页面本身只剩模板：列表/详情、新建、文档动作、加入文档、检索与预览各自在
 * 自己的 composable 里，这里把它们接成一张表，并处理它们之间的三处耦合：
 *
 * 1. 换库时清掉只属于上一份详情的东西（检索结果、加入面板、预览）；
 * 2. 文档动作依赖详情的轮询开关；
 * 3. 新建成功后先重读列表，再打开新库。
 *
 * 预览弹窗的 `ref` 也放在这里：打开是"先落状态、下一帧再 showModal"，两步
 * 必须在同一处，否则关掉再开同一份文档会读到上一份的正文。
 */

import { nextTick, onMounted, type ComputedRef, type Ref } from 'vue'
import type { Router } from 'vue-router'

import type { KbDocument, KbSource } from '../../api/knowledgeBases'
import { canSurface } from '../../auth/permissions'
import { VECTOR_STATE_LABELS, VECTOR_STATE_TONES } from '../../copy/files'

import KbDocumentPreviewDialog from './KbDocumentPreviewDialog.vue'
import { useKbAdd } from './useKbAdd'
import { useKbCreate } from './useKbCreate'
import { useKbDetail } from './useKbDetail'
import { useKbDocuments } from './useKbDocuments'
import { useKbPreview } from './useKbPreview'
import { useKbSearch } from './useKbSearch'

export function useKnowledgeBases(
  selectedId: ComputedRef<string | null>,
  router: Router,
  previewDialog: Ref<InstanceType<typeof KbDocumentPreviewDialog> | null>,
) {
  /**
   * 换库时要清掉面板状态。详情层的 immediate 首次加载发生在下面这些面板创建
   * 之前，此时它们本来就是空的；之后的每次换库都会调到真正的重置。
   */
  let resetPanels: () => void = () => {}

  const detailApi = useKbDetail({
    selectedId,
    router,
    onResetPanels: () => resetPanels(),
  })

  const searchApi = useKbSearch(selectedId)

  const addApi = useKbAdd({
    selectedId,
    detail: detailApi.detail,
    loadDetail: detailApi.loadDetail,
    loadList: detailApi.loadList,
  })

  const previewApi = useKbPreview(selectedId)

  /** 打开预览：状态先落位，弹窗在下一帧拿到它之后再 showModal。 */
  function openPreview(document: KbDocument): void {
    void previewApi.open(document)
    void nextTick(() => previewDialog.value?.show())
  }

  /** 关弹窗还要停掉未完成的读取；换库时走的也是这一条。 */
  function closePreview(): void {
    previewDialog.value?.hide()
    previewApi.close()
  }

  resetPanels = () => {
    searchApi.resetSearch()
    addApi.resetAdd()
    closePreview()
  }

  const actionsApi = useKbDocuments({
    selectedId,
    router,
    detail: detailApi.detail,
    loadList: detailApi.loadList,
    loadDetail: detailApi.loadDetail,
    clearPoll: detailApi.clearPoll,
    schedulePoll: detailApi.schedulePoll,
  })

  const createApi = useKbCreate({
    onCreated: async (kbId) => {
      await detailApi.loadList()
      detailApi.openBase(kbId)
    },
  })

  // ── 展示辅助 ────────────────────────────────────────────────────────────
  function vectorLabel(state: string): string {
    return VECTOR_STATE_LABELS[state] ?? state
  }

  function vectorTone(state: string): 'neutral' | 'accent' | 'success' | 'warning' | 'danger' {
    return VECTOR_STATE_TONES[state] ?? 'neutral'
  }

  function locatorLabel(source: KbSource): string {
    const parts: string[] = [source.locator.kind]
    if (source.locator.page !== null && source.locator.page !== undefined) {
      parts.push(`第 ${source.locator.page} 页`)
    }
    parts.push(`第 ${source.locator.char_start}–${source.locator.char_end} 字`)
    return parts.join(' · ')
  }

  onMounted(() => {
    if (canSurface('knowledge_base') === false) return
    void detailApi.loadList()
  })

  return {
    // 列表与详情
    bases: detailApi.bases,
    listStatus: detailApi.listStatus,
    listFailure: detailApi.listFailure,
    loadList: detailApi.loadList,
    openBase: detailApi.openBase,
    detailStatus: detailApi.detailStatus,
    detailFailure: detailApi.detailFailure,
    pollFailure: detailApi.pollFailure,
    retryDetail: detailApi.retryDetail,
    refreshDetail: detailApi.refreshDetail,
    currentBase: detailApi.currentBase,
    currentDocuments: detailApi.currentDocuments,
    partialVectors: detailApi.partialVectors,
    // 新建
    createOpen: createApi.createOpen,
    createName: createApi.createName,
    createDescription: createApi.createDescription,
    createBusy: createApi.createBusy,
    createError: createApi.createError,
    openCreate: createApi.openCreate,
    closeCreate: createApi.closeCreate,
    submitCreate: createApi.submitCreate,
    // 文档动作
    actionBusy: actionsApi.actionBusy,
    isRebuilding: actionsApi.isRebuilding,
    setDefault: actionsApi.setDefault,
    deleteBase: actionsApi.deleteBase,
    rebuild: actionsApi.rebuild,
    removeDocument: actionsApi.removeDocument,
    // 预览
    previewDocument: previewApi.previewDocument,
    previewText: previewApi.text,
    previewStatus: previewApi.status,
    previewFailure: previewApi.failure,
    previewLoadingMore: previewApi.loadingMore,
    previewRetry: previewApi.retry,
    previewLoadMore: previewApi.loadMore,
    openPreview,
    closePreview,
    // 加入文档
    addOpen: addApi.addOpen,
    fileOptions: addApi.fileOptions,
    addFileId: addApi.addFileId,
    addBusy: addApi.addBusy,
    addFailure: addApi.addFailure,
    resetAdd: addApi.resetAdd,
    openAdd: addApi.openAdd,
    submitAdd: addApi.submitAdd,
    // 检索
    searchQuery: searchApi.searchQuery,
    searchBusy: searchApi.searchBusy,
    searchResults: searchApi.searchResults,
    searchFailure: searchApi.searchFailure,
    searchedFor: searchApi.searchedFor,
    runSearch: searchApi.runSearch,
    // 展示辅助
    vectorLabel,
    vectorTone,
    locatorLabel,
  }
}
