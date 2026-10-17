<script setup lang="ts">
/**
 * 文件库。列表栏 + 主内容区。
 *
 * 两件容易画错的事：
 * 1. `document: null` 不是失败 —— 文件已经存下来了，只是还没解析。页面画
 *    "已上传，尚未解析"，并给"加入知识库"这个真正能改变它的动作；
 * 2. 回收站与在用库是两个列表，不是同一个列表加一个标记。永久删除只在回收站
 *    里出现；在用文件只能先移进回收站。
 *
 * 列表、详情/文本、上传与动作各在自己的 composable 里，这里只做组装。
 */

import { onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'

import EmptyState from '../components/common/EmptyState.vue'
import FailureState from '../components/common/FailureState.vue'
import LoadMore from '../components/common/LoadMore.vue'
import PartialBanner from '../components/common/PartialBanner.vue'
import SkeletonBlock from '../components/common/SkeletonBlock.vue'
import StatusBadge from '../components/common/StatusBadge.vue'
import SurfaceGate from '../components/common/SurfaceGate.vue'
import AppShell from '../components/shell/AppShell.vue'
import PageHeader from '../components/shell/PageHeader.vue'
import { FILES, REFERENCE_KIND_LABELS } from '../copy/files'
import { canSurface } from '../auth/permissions'
import { formatBytes, formatDateTime } from '../shared/format'

import FileDocumentPanel from './files/FileDocumentPanel.vue'
import { useFileActions } from './files/useFileActions'
import { useFileDetail } from './files/useFileDetail'
import { useFileList } from './files/useFileList'
import { useFileUpload } from './files/useFileUpload'

const route = useRoute()

/**
 * 从对话页的引用面板过来时带两个参数：`file` 指定打开哪一份，`text=1` 表示
 * 直接把文本抽屉展开（引用指向的是片段，用户不该再自己去找那个按钮）。
 */
function requestedFileId(): string | null {
  const value = route.query.file
  return typeof value === 'string' && value !== '' ? value : null
}

function shouldOpenText(fileId: string): boolean {
  return requestedFileId() === fileId && route.query.text === '1'
}

const detailApi = useFileDetail({
  shouldOpenText,
  onClearExtra: () => {
    actions.resetKbPicker()
    actions.resetPurge()
  },
})

const list = useFileList({
  reconcileSelection,
  clearSelection: detailApi.clearSelection,
})

const actions = useFileActions({ list, detail: detailApi })
const fileInput = ref<HTMLInputElement | null>(null)
const upload = useFileUpload({ list, detail: detailApi, fileInput })

const {
  mode,
  searchInput,
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
  switchMode,
  applySearch,
  clearSearch,
} = list

const {
  selectedId,
  detailStatus,
  detailFailure,
  actionBusy,
  showText,
  text,
  textStatus,
  textFailure,
  textLoadingMore,
  selectedFile,
  selectedDocument,
  selectFile,
  retryDetail,
  loadText,
  toggleText,
} = detailApi

const {
  kbPickerOpen,
  kbOptions,
  kbOptionsFailure,
  kbTargetId,
  kbBusy,
  openKbPicker,
  addToKnowledgeBase,
  purgeRefusal,
  purgeReferencesList,
  downloadSelected,
  removeSelected,
  restoreSelected,
  purgeSelected,
  referenceName,
} = actions

const {
  uploading,
  uploadName,
  uploadPercent,
  uploadError,
  chooseFile,
  onPicked,
  cancelUpload,
  progressLabel,
} = upload

/**
 * 列表刷新后决定选中哪一份：保留仍然存在的选中项；从引用面板深链进来时先
 * 打开它点名的那一份；否则退回第一行。
 */
function reconcileSelection(): void {
  const current = files.value.find((file) => file.id === selectedId.value)
  if (current) return
  // 从引用面板深链进来时，先打开它点名的那一份 —— 不要先闪一下列表里第一行。
  const requested = requestedFileId()
  if (requested !== null && selectedId.value === null) {
    void selectFile(requested)
    return
  }
  const first = files.value[0]
  if (first) {
    void selectFile(first.id)
  } else {
    detailApi.clearSelection()
  }
}

onMounted(() => {
  // 权限明确为 false 时不发请求；`SurfaceGate` 会画拒绝页。
  if (canSurface('library') === false) return
  void loadList()
})
</script>
<template>
  <SurfaceGate surface="library">
    <AppShell>
      <template #list>
        <aside class="files-pane">
          <div class="files-pane__head">
            <div class="mode-tabs" role="tablist" aria-label="文件状态">
              <button
                type="button"
                role="tab"
                class="mode-tab"
                :class="{ 'mode-tab--active': mode === 'active' }"
                :aria-selected="mode === 'active'"
                @click="switchMode('active')"
              >
                {{ FILES.active }}
              </button>
              <button
                type="button"
                role="tab"
                class="mode-tab"
                :class="{ 'mode-tab--active': mode === 'deleted' }"
                :aria-selected="mode === 'deleted'"
                @click="switchMode('deleted')"
              >
                {{ FILES.deleted }}
              </button>
            </div>

            <button
              v-if="mode === 'active'"
              type="button"
              class="upload-button"
              :disabled="uploading"
              @click="chooseFile"
            >
              {{ FILES.upload }}
            </button>
            <input
              ref="fileInput"
              class="upload-input"
              type="file"
              tabindex="-1"
              aria-hidden="true"
              @change="onPicked"
            />
            <p class="upload-note">{{ FILES.uploadOneAtATime }}</p>

            <form class="search" @submit.prevent="applySearch">
              <input
                v-model="searchInput"
                class="search__input"
                type="search"
                :placeholder="FILES.searchPlaceholder"
                :aria-label="FILES.searchPlaceholder"
              />
              <button type="submit" class="search__button">{{ FILES.search }}</button>
            </form>
          </div>

          <div v-if="uploading" class="upload-state">
            <div class="upload-state__row">
              <span class="upload-state__name" :title="uploadName">{{ uploadName }}</span>
              <span class="upload-state__percent">{{ progressLabel }}</span>
            </div>
            <progress
              class="upload-state__bar"
              :class="{ 'upload-state__bar--indeterminate': uploadPercent === null }"
              :value="uploadPercent ?? undefined"
              max="100"
            ></progress>
            <div class="upload-state__actions">
              <button type="button" class="link-button" @click="cancelUpload">
                {{ FILES.cancelUpload }}
              </button>
            </div>
          </div>

          <p v-if="uploadError" class="pane-error" role="alert">{{ uploadError }}</p>

          <div class="files-pane__scroll">
            <SkeletonBlock v-if="listStatus === 'loading'" :lines="6" :line-height="16" />
            <FailureState
              v-else-if="listStatus === 'failed' && listFailure"
              title="文件列表没能加载出来"
              :failure="listFailure"
              @retry="loadList"
            />
            <EmptyState
              v-else-if="files.length === 0 && isSearching"
              :title="FILES.emptySearch"
              :hint="FILES.emptySearchHint"
              :action-label="FILES.clearSearch"
              @action="clearSearch"
            />
            <EmptyState
              v-else-if="files.length === 0"
              :title="mode === 'active' ? FILES.emptyActive : FILES.emptyDeleted"
              :hint="mode === 'active' ? FILES.emptyActiveHint : FILES.emptyDeletedHint"
              :action-label="mode === 'active' ? FILES.upload : ''"
              @action="chooseFile"
            />
            <ul v-else class="file-list">
              <li v-for="file in files" :key="file.id">
                <button
                  type="button"
                  class="file-row"
                  :class="{ 'file-row--active': file.id === selectedId }"
                  @click="selectFile(file.id)"
                >
                  <span class="file-row__name" :title="file.filename">{{ file.filename }}</span>
                  <span class="file-row__meta">
                    {{ formatBytes(file.size_bytes) }} · {{ formatDateTime(file.created_at) }}
                  </span>
                  <StatusBadge
                    v-if="file.deleted_at"
                    class="file-row__badge"
                    :label="FILES.deleted"
                    small
                  />
                </button>
              </li>
            </ul>
            <LoadMore
              v-if="files.length > 0"
              :loading="loadingMore"
              :exhausted="exhausted"
              :failure="moreFailure"
              @load="loadMore"
            />
          </div>
        </aside>
      </template>

      <div class="page">
        <div class="page__inner">
          <PartialBanner
            v-if="staleFailure"
            :failure="staleFailure"
            text="列表没有刷新成功，下面是上一次的结果"
            @retry="loadList"
          />
          <PageHeader :title="FILES.title" :description="FILES.description" />

          <SkeletonBlock v-if="detailStatus === 'loading'" :lines="7" :line-height="16" />
          <FailureState
            v-else-if="detailStatus === 'failed' && detailFailure"
            title="这个文件没能加载出来"
            :failure="detailFailure"
            @retry="retryDetail"
          />
          <EmptyState
            v-else-if="!selectedFile"
            :title="FILES.selectFile"
            :hint="FILES.selectFileHint"
          />

          <article v-else class="detail">
            <header class="detail__head">
              <div class="detail__title-wrap">
                <h2 class="detail__title">{{ selectedFile.filename }}</h2>
                <p class="detail__sub">
                  {{ formatBytes(selectedFile.size_bytes) }} ·
                  {{ selectedFile.mime_type }} · {{ formatDateTime(selectedFile.created_at) }}
                </p>
              </div>
              <div class="detail__actions">
                <button
                  v-if="mode === 'active'"
                  type="button"
                  class="button"
                  @click="downloadSelected"
                >
                  {{ FILES.download }}
                </button>
                <button
                  v-if="mode === 'active'"
                  type="button"
                  class="button"
                  :disabled="actionBusy"
                  @click="removeSelected"
                >
                  {{ FILES.remove }}
                </button>
                <template v-else>
                  <button
                    type="button"
                    class="button"
                    :disabled="actionBusy"
                    @click="restoreSelected"
                  >
                    {{ FILES.restore }}
                  </button>
                  <button
                    type="button"
                    class="button button--danger"
                    :disabled="actionBusy"
                    @click="purgeSelected"
                  >
                    {{ FILES.purge }}
                  </button>
                </template>
              </div>
            </header>

            <div v-if="purgeRefusal" class="in-use" role="alert">
              <p class="in-use__title">{{ purgeRefusal }}</p>
              <p class="in-use__hint">{{ FILES.purgeInUseHint }}</p>
              <ul class="in-use__list">
                <li v-for="reference in purgeReferencesList" :key="reference.ID">
                  <span class="in-use__kind">
                    {{ REFERENCE_KIND_LABELS[reference.Kind] ?? reference.Kind }}
                  </span>
                  <span>{{ referenceName(reference) }}</span>
                </li>
              </ul>
            </div>

            <dl class="facts">
              <div class="fact">
                <dt>{{ FILES.size }}</dt>
                <dd>{{ formatBytes(selectedFile.size_bytes) }}</dd>
              </div>
              <div class="fact">
                <dt>{{ FILES.type }}</dt>
                <dd>{{ selectedFile.mime_type }}</dd>
              </div>
              <div class="fact">
                <dt>{{ FILES.category }}</dt>
                <dd>{{ selectedFile.category }}</dd>
              </div>
              <div class="fact">
                <dt>{{ FILES.uploadedAt }}</dt>
                <dd>{{ formatDateTime(selectedFile.created_at) }}</dd>
              </div>
              <div class="fact fact--wide">
                <dt>{{ FILES.checksum }}</dt>
                <dd class="fact__mono">{{ selectedFile.sha256 }}</dd>
              </div>
            </dl>

            <FileDocumentPanel
              :document="selectedDocument"
              :mode="mode"
              :show-text="showText"
              :text-status="textStatus"
              :text-failure="textFailure"
              :text="text"
              :text-loading-more="textLoadingMore"
              :kb-picker-open="kbPickerOpen"
              :kb-options="kbOptions"
              :kb-options-failure="kbOptionsFailure"
              :kb-target-id="kbTargetId"
              :kb-busy="kbBusy"
              @toggle-text="toggleText"
              @load-text="loadText"
              @open-kb-picker="openKbPicker"
              @add-to-knowledge-base="addToKnowledgeBase"
              @update:kb-target-id="kbTargetId = $event"
              @update:kb-picker-open="kbPickerOpen = $event"
            />
          </article>
        </div>
      </div>
    </AppShell>
  </SurfaceGate>
</template>

<style scoped src="./Files.css"></style>
