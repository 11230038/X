/**
 * 文件列表：在用 / 回收站两个列表，搜索与 `limit+offset` 分页。
 *
 * 刷新失败但已有旧数据时保留旧数据，只把失败放到 `staleFailure`；翻页失败
 * 只影响"加载更多"那一段，不把已经看到的行换掉。
 */

import { computed, ref } from 'vue'

import type { ApiFailure } from '../../api/client'
import { listFiles, type FileSummary } from '../../api/files'
import { toFailure } from '../../shared/resource'

type LoadStatus = 'idle' | 'loading' | 'ready' | 'failed'
export type FileMode = 'active' | 'deleted'

const PAGE_SIZE = 30

export function useFileList(options: {
  reconcileSelection: () => void
  clearSelection: () => void
}) {
  const mode = ref<FileMode>('active')
  const searchInput = ref('')
  const searchTerm = ref('')
  const files = ref<FileSummary[]>([])
  const listStatus = ref<LoadStatus>('idle')
  const listFailure = ref<ApiFailure | null>(null)
  const staleFailure = ref<ApiFailure | null>(null)
  const moreFailure = ref<ApiFailure | null>(null)
  const loadingMore = ref(false)
  const exhausted = ref(false)

  async function loadList(): Promise<void> {
    const reset = files.value.length === 0
    if (reset) {
      listStatus.value = 'loading'
      listFailure.value = null
    }
    staleFailure.value = null
    moreFailure.value = null

    try {
      const result = await listFiles({
        deleted: mode.value === 'deleted',
        filename: searchTerm.value || undefined,
        limit: PAGE_SIZE,
        offset: 0,
      })
      files.value = result.files
      exhausted.value = result.files.length < PAGE_SIZE
      listStatus.value = 'ready'
      listFailure.value = null
      options.reconcileSelection()
    } catch (cause) {
      const failure = toFailure(cause)
      if (files.value.length === 0) {
        listStatus.value = 'failed'
        listFailure.value = failure
      } else {
        // 已有旧数据：保留它，只在顶部说这次刷新没成功。
        staleFailure.value = failure
      }
    }
  }

  async function loadMore(): Promise<void> {
    if (loadingMore.value || exhausted.value) return
    loadingMore.value = true
    moreFailure.value = null
    try {
      const result = await listFiles({
        deleted: mode.value === 'deleted',
        filename: searchTerm.value || undefined,
        limit: PAGE_SIZE,
        offset: files.value.length,
      })
      files.value = [...files.value, ...result.files]
      exhausted.value = result.files.length < PAGE_SIZE
    } catch (cause) {
      moreFailure.value = toFailure(cause)
    } finally {
      loadingMore.value = false
    }
  }

  /** 列表条件变了：清空已选中的行，再按新条件从头拉。 */
  async function reloadAfterChange(): Promise<void> {
    files.value = []
    exhausted.value = false
    options.clearSelection()
    await loadList()
  }

  function reloadList(): void {
    void reloadAfterChange()
  }

  function switchMode(next: FileMode): void {
    if (mode.value === next) return
    mode.value = next
    searchInput.value = ''
    searchTerm.value = ''
    reloadList()
  }

  function applySearch(): void {
    searchTerm.value = searchInput.value.trim()
    reloadList()
  }

  function clearSearch(): void {
    searchInput.value = ''
    if (searchTerm.value !== '') {
      searchTerm.value = ''
      reloadList()
    }
  }

  /** 刚上传完：新文件属于"在用"这一边，搜索条件也要让开，否则它可能被筛掉。 */
  async function resetForNewFile(): Promise<void> {
    mode.value = 'active'
    searchInput.value = ''
    searchTerm.value = ''
    files.value = []
    exhausted.value = false
    await loadList()
  }

  const isSearching = computed(() => searchTerm.value !== '')

  return {
    mode,
    searchInput,
    searchTerm,
    files,
    listStatus,
    listFailure,
    staleFailure,
    moreFailure,
    loadingMore,
    exhausted,
    isSearching,
    loadList,
    loadMore,
    reloadList,
    reloadAfterChange,
    switchMode,
    applySearch,
    clearSearch,
    resetForNewFile,
  }
}
