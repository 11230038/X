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

import { onMounted } from 'vue'
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
import type { FileSummary } from '../api/files'
import { FILES, REFERENCE_KIND_LABELS } from '../copy/files'
import { canSurface } from '../auth/permissions'
import { formatBytes, formatDateTime } from '../shared/format'

import FileDocumentPanel from './files/FileDocumentPanel.vue'
import UploadDialog from './files/UploadDialog.vue'
import { useFileActions } from './files/useFileActions'
import { useFileDetail } from './files/useFileDetail'
import { useFileList } from './files/useFileList'
import { useFileUpload } from './files/useFileUpload'
import AppIcon, { type AppIconName } from '../components/common/AppIcon.vue'

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
const upload = useFileUpload({ list, detail: detailApi })

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
  clearSelection,
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
  dialogOpen,
  uploading,
  uploadName,
  uploadPercent,
  uploadError,
  openDialog,
  closeDialog,
  startUpload,
  cancelUpload,
  progressLabel,
} = upload

/**
 * 卡片的封面：按后端分好的类（`file.category`）给一处渐变色和一枚图标，不在
 * 前端再猜一遍 MIME。认不出来的分类用中性灰和文件夹，不编一个图标。
 */
const COVERS: Record<string, { icon: AppIconName; background: string }> = {
  pdf: { icon: 'document', background: 'linear-gradient(135deg, #FFE9E9, #FFB4B4)' },
  doc: { icon: 'documentText', background: 'linear-gradient(135deg, #E8FAFF, #66D2F5)' },
  ppt: { icon: 'presentation', background: 'linear-gradient(135deg, #FFF1E6, #FFB07C)' },
  md: { icon: 'documentText', background: 'linear-gradient(135deg, #FFFBF2, #FEDD76)' },
  images: { icon: 'image', background: 'linear-gradient(135deg, #FFF0FE, #F7A9EF)' },
  mp3: { icon: 'music', background: 'linear-gradient(135deg, #EAFBF3, #A7F3D0)' },
}

const FALLBACK_COVER = { icon: 'folder', background: 'linear-gradient(135deg, #F4F7F9, #E2E8F0)' }

function coverFor(file: FileSummary): { icon: AppIconName; background: string } {
  return COVERS[file.category] ?? FALLBACK_COVER
}

/**
 * 列表刷新后决定选中哪一份。
 *
 * 页面是"网格 → 点开一份 → 返回网格"，所以刷新**不会**替用户挑一份：只有
 * 从引用面板深链进来时才直接打开它点名的那一份，其余时候停在网格上。深链只
 * 认一次 —— 之后的重载（删掉、切模式）不该再把用户拽回那份文件。
 */
let deepLinkConsumed = false

function reconcileSelection(): void {
  if (selectedId.value !== null) {
    // 选中的那一份还在列表里就保持住；不在了（被删、换了模式）就回网格。
    if (!files.value.some((file) => file.id === selectedId.value)) clearSelection()
    return
  }
  if (deepLinkConsumed) return
  deepLinkConsumed = true
  const requested = requestedFileId()
  if (requested !== null) void selectFile(requested)
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
      <div class="page">
        <div class="page__inner">
          <PartialBanner
            v-if="staleFailure"
            :failure="staleFailure"
            text="列表没有刷新成功，下面是上一次的结果"
            @retry="loadList"
          />
          <PageHeader :title="FILES.title" :description="FILES.description">
            <template v-if="mode === 'active'" #actions>
              <button
                type="button"
                class="upload-button"
                :disabled="uploading"
                @click="openDialog"
              >
                {{ FILES.upload }}
              </button>
            </template>
          </PageHeader>

          <!--
            网格态：工具行 + 卡片。上传走弹窗（拖入或点选），点了卡片就换成下面
            那段详情，不再同时并排两栏 —— 演示稿的资料市场就是一页卡片。
          -->
          <template v-if="selectedId === null">
            <div class="market-tools">
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
              @action="openDialog"
            />
            <template v-else>
              <ul class="market-grid">
                <li v-for="file in files" :key="file.id">
                  <button type="button" class="market-card" @click="selectFile(file.id)">
                    <span
                      class="market-card__cover"
                      :style="{ background: coverFor(file).background }"
                      aria-hidden="true"
                    >
                      <AppIcon :name="coverFor(file).icon" />
                    </span>
                    <span class="market-card__name" :title="file.filename">{{ file.filename }}</span>
                    <span class="market-card__meta">
                      {{ formatBytes(file.size_bytes) }} · {{ formatDateTime(file.created_at) }}
                    </span>
                    <span class="market-card__foot">
                      <span class="market-card__action">{{ FILES.cardAction }}</span>
                      <StatusBadge v-if="file.deleted_at" :label="FILES.deleted" small />
                    </span>
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
            </template>
          </template>

          <!-- 详情态：点开的一份文件。返回就回到网格，不丢工具行上的选择。 -->
          <template v-else>
            <button type="button" class="back" @click="clearSelection">
              ← 返回{{ FILES.title }}
            </button>

            <SkeletonBlock v-if="detailStatus === 'loading'" :lines="7" :line-height="16" />
            <FailureState
              v-else-if="detailStatus === 'failed' && detailFailure"
              title="这个文件没能加载出来"
              :failure="detailFailure"
              @retry="retryDetail"
            />

            <article v-else-if="selectedFile" class="detail">
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
          </template>
        </div>
      </div>

      <UploadDialog
        :open="dialogOpen"
        :uploading="uploading"
        :name="uploadName"
        :percent="uploadPercent"
        :progress-label="progressLabel"
        :error="uploadError"
        @close="closeDialog"
        @pick="startUpload"
        @cancel="cancelUpload"
      />
    </AppShell>
  </SurfaceGate>
</template>

<style scoped src="./Files.css"></style>
