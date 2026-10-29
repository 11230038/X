<script setup lang="ts">
/**
 * 沉浸阅读。
 *
 * 三件事在这里必须保持同一套坐标：
 *
 * 1. **正文按单元懒加载**，不整篇拉取；引用深链只加载它落到的那个单元。
 * 2. **选区 = `{material_id, ordinal, char_start, char_end}`**，批注、位置、
 *    扩展与对话里的 `selection` 全用这一套，不另造格式。
 * 3. **批注与位置必须带 `session_id`**。从导航进入时先让用户选一个阅读会话；
 *    没有会话就不放行这两个写操作，而不是写一个无主的批注。
 *
 * 这个文件只做编排：材料目录、正文、面板与两个对话框各在自己的组件里，
 * 会话、大纲、选区、位置、批注、配对、扩展的状态各在自己的 composable 里。
 */

import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import { describeFailure } from '../../api/errors'
import { canSurface } from '../../auth/permissions'
import EmptyState from '../../components/common/EmptyState.vue'
import FailureState from '../../components/common/FailureState.vue'
import SkeletonBlock from '../../components/common/SkeletonBlock.vue'
import SurfaceGate from '../../components/common/SurfaceGate.vue'
import { READING } from '../../copy/reading'

import ReadingAssistant from './reading/ReadingAssistant.vue'
import ReadingFileDialog from './reading/ReadingFileDialog.vue'
import ReadingMaterials from './reading/ReadingMaterials.vue'
import ReadingNoteDialog from './reading/ReadingNoteDialog.vue'
import ReadingSelectionBar from './reading/ReadingSelectionBar.vue'
import ReadingUnits from './reading/ReadingUnits.vue'
import { useReadingAssistant } from './reading/useReadingAssistant'
import { useReadingFilePicker } from './reading/useReadingFilePicker'
import { useReadingMarks } from './reading/useReadingMarks'
import { useReadingMaterials } from './reading/useReadingMaterials'
import { useReadingOutline } from './reading/useReadingOutline'
import { useReadingPosition } from './reading/useReadingPosition'
import { useReadingSelection } from './reading/useReadingSelection'
import { useReadingSessions } from './reading/useReadingSessions'

const route = useRoute()
const router = useRouter()

const currentId = ref('')

/* ── 会话 ────────────────────────────────────────────────────────────── */

function reloadOutline(): Promise<void> {
  return loadOutline()
}

const { sessionId, loadSessions, needSession, setSession } = useReadingSessions(route)

/* ── 大纲与单元 ──────────────────────────────────────────────────────── */

const {
  outline,
  outlineStatus,
  outlineFailure,
  unitsWithState,
  highlight,
  bindUnit,
  unitAt,
  loadUnit,
  loadOutline,
  jumpToOrdinal,
  focusRange,
  clear: clearOutline,
} = useReadingOutline({
  route,
  currentId,
  sessionId,
  onReset: () => {
    resetMarks()
    resetAssistant()
    selection.value = null
  },
  afterReady: () => {
    void loadMarks()
  },
})

/* ── 材料目录 ────────────────────────────────────────────────────────── */

const {
  materials,
  materialsStatus,
  materialsFailure,
  loadMaterials,
  replaceRoute,
  selectMaterial,
  clearMaterial,
} = useReadingMaterials(
  route,
  router,
  currentId,
  sessionId,
  reloadOutline,
  clearOutline,
)

/* ── 选区与位置 ──────────────────────────────────────────────────────── */

const {
  selection,
  captureSelection,
  clearSelection,
  copySelection,
  askInChat,
} = useReadingSelection(router, currentId, sessionId, unitAt)

const {
  positionSaving,
  positionFailure,
  currentLocator,
  jumpToLocator,
  recordPosition,
  jumpToPosition,
} = useReadingPosition({
  currentId,
  sessionId,
  needSession,
  selection,
  outline,
  unitsWithState,
  jumpToOrdinal,
  focusRange,
})

/* ── 批注 ────────────────────────────────────────────────────────────── */

const {
  marks,
  marksFailure,
  markFailure,
  markSaving,
  loadMarks,
  addMark,
  addHighlight,
  addBookmark,
  resetMarks,
} = useReadingMarks({
  currentId,
  sessionId,
  needSession,
  currentLocator,
  clearSelection,
  jumpToLocator,
})

/* ── 文档问答助手 ────────────────────────────────────────────────────── */

const {
  exchanges,
  asking,
  ask: askAssistant,
  reset: resetAssistant,
} = useReadingAssistant(currentId, () => outline.value?.material.filename ?? '')

function explainSelection(): void {
  if (!selection.value) return
  void askAssistant(selection.value.text, selection.value.locator)
}

/* ── 上次阅读位置 ────────────────────────────────────────────────────── */

/**
 * 打开材料不替人翻页，只在角落放一会儿"上次读到哪"：点它就跳过去，不点就
 * 自己退场。位置是会话的，换了会话自然就没有这一条。
 */
const resumeVisible = ref(false)
const resumeOrdinal = computed(() => outline.value?.position?.locator.ordinal ?? null)

/**
 * 浮条跟着「哪份材料、读到第几段」走，不只看段号：两份材料都停在第 3 段
 * 时，单看段号会觉得位置没变，浮条就不会再出现。
 */
const resumeKey = computed(() => {
  const position = outline.value?.position
  if (!position) return ''
  return `${outline.value?.material.material_id ?? ''}:${position.locator.ordinal}`
})

let resumeTimer: ReturnType<typeof setTimeout> | null = null

function hideResume(): void {
  resumeVisible.value = false
  if (resumeTimer !== null) {
    clearTimeout(resumeTimer)
    resumeTimer = null
  }
}

function resumeReading(): void {
  hideResume()
  jumpToPosition()
}

watch(resumeKey, (key) => {
  hideResume()
  if (key === '') return
  resumeVisible.value = true
  resumeTimer = setTimeout(hideResume, 8000)
})

onBeforeUnmount(hideResume)

/* ── 写批注对话框 ────────────────────────────────────────────────────── */

const noteBody = ref('')
const noteDialog = ref<InstanceType<typeof ReadingNoteDialog> | null>(null)

function openNote(): void {
  if (!needSession() || !selection.value) return
  noteBody.value = ''
  markFailure.value = null
  noteDialog.value?.show()
}

async function submitNote(): Promise<void> {
  if (noteBody.value.trim() === '') return
  const saved = await addMark('note', noteBody.value.trim())
  if (saved) noteDialog.value?.hide()
}

/* ── 从文件打开 ──────────────────────────────────────────────────────── */

const fileDialog = ref<InstanceType<typeof ReadingFileDialog> | null>(null)

const {
  files,
  filesStatus,
  filesFailure,
  openingFileId,
  fileErrors,
  loadFiles,
  chooseFile,
} = useReadingFilePicker({
  currentId,
  materials,
  replaceRoute,
  reloadOutline,
  closeDialog: () => fileDialog.value?.hide(),
})

async function openFilePicker(): Promise<void> {
  fileDialog.value?.show()
  await loadFiles()
}

/* ── 生命周期 ────────────────────────────────────────────────────────── */

watch(
  () => route.query.material,
  (value) => {
    if (typeof value !== 'string' || value === '' || value === currentId.value) return
    currentId.value = value
    void loadOutline()
  },
)

watch(
  () => route.query.session,
  (value) => {
    const next = typeof value === 'string' ? value : ''
    if (next === sessionId.value) return
    setSession(next)
    if (currentId.value) void loadOutline()
  },
)

const showReader = computed(() => outlineStatus.value === 'ready' && outline.value !== null)

/**
 * 换材料的间隙：新材料还没换上来，屏幕上的正文仍属于上一份。这段时间里
 * 正文不可点，以免把旧材料上的选区与位置记到新材料名下。
 */
const switching = computed(() => {
  const open = outline.value?.material.material_id ?? ''
  return open !== '' && currentId.value !== '' && open !== currentId.value
})

/**
 * 正文的大标题是材料名。扩展名只对文件系统有意义，读的时候去掉 ——
 * 完整的文件名在标题下面的来源行里。
 */
const materialTitle = computed(() => {
  const filename = outline.value?.material.filename ?? ''
  return filename.replace(/\.[^./\\]+$/, '') || filename
})

/**
 * 材料按 markdown 排版。判断看 `source_type` 与扩展名两处，因为有的来源只
 * 带其中之一。其余材料照旧按纯文本读，不做任何标记猜测。
 */
const markdownMode = computed(() => {
  const material = outline.value?.material
  if (!material) return false
  const type = (material.source_type ?? '').toLowerCase()
  const name = material.filename.toLowerCase()
  return type === 'md' || type === 'markdown' || name.endsWith('.md') || name.endsWith('.markdown')
})

onMounted(() => {
  if (canSurface('reading') === false) return
  // 先定会话再读大纲：大纲要带着 session_id 去问，才知道上次读到哪。
  void loadSessions().then(() => loadMaterials())
})
</script>

<template>
  <SurfaceGate surface="reading">
    <FailureState
      v-if="materialsStatus === 'failed' && materialsFailure"
      :failure="materialsFailure"
      :title="READING.materialListFailed"
      @retry="loadMaterials"
    />

    <div v-else class="workspace">
      <ReadingMaterials
        :materials="materials"
        :current-id="currentId"
        :loading="materialsStatus === 'loading'"
        @select="selectMaterial"
        @open-file="openFilePicker"
      />

      <section class="reader" :class="{ 'reader--switching': switching }">
        <EmptyState
          v-if="currentId === '' && materialsStatus === 'ready'"
          :title="READING.selectMaterial"
          :hint="READING.selectMaterialHint"
          :action-label="READING.openFromFile"
          @action="openFilePicker"
        />

        <SkeletonBlock v-else-if="outlineStatus === 'loading'" :lines="10" :line-height="44" />

        <FailureState
          v-else-if="outlineStatus === 'failed' && outlineFailure"
          :failure="outlineFailure"
          :title="READING.outlineFailed"
          :exit-label="READING.goReadingList"
          @retry="loadOutline"
          @exit="clearMaterial"
        />

        <template v-else-if="showReader && outline">
          <h1 class="reader-title">{{ materialTitle }}</h1>
          <p class="reader-meta">
            <span>{{ READING.source }}：{{ outline.material.filename }}</span>
            <span>{{ READING.textChars(outline.text_chars) }}</span>
          </p>

          <ReadingSelectionBar
            v-if="selection && !switching"
            :text="selection.text"
            :saving="markSaving"
            :explaining="asking"
            @copy="copySelection"
            @highlight="addHighlight"
            @favorite="addBookmark"
            @note="openNote"
            @explain="explainSelection"
            @ask="askInChat"
            @clear="clearSelection"
          />

          <p v-if="markFailure" class="inline-error">
            批注没能保存：{{ describeFailure(markFailure) }}
            <code v-if="markFailure.requestId" class="req">{{ markFailure.requestId }}</code>
          </p>

          <p v-if="marksFailure" class="inline-warn">
            {{ READING.marksFailed }}
            <button type="button" class="link" @click="loadMarks">{{ READING.retry }}</button>
          </p>

          <ReadingUnits
            :entries="unitsWithState"
            :markdown="markdownMode"
            :marks="marks"
            :highlight="highlight"
            :bind-unit="bindUnit"
            @capture="captureSelection"
            @load="loadUnit"
          />

          <button
            type="button"
            class="primary position"
            :disabled="positionSaving"
            @click="recordPosition"
          >
            {{ positionSaving ? READING.positionSaving : READING.positionSave }}
          </button>

          <p v-if="positionFailure" class="inline-error">
            {{ positionFailure.code === 'stale_position' ? READING.positionStale : READING.positionFailed }}
            ：{{ describeFailure(positionFailure) }}
            <button type="button" class="link" @click="loadOutline">
              {{ READING.positionReload }}
            </button>
          </p>
        </template>
      </section>

      <ReadingAssistant
        :filename="outline?.material.filename ?? ''"
        :exchanges="exchanges"
      />
    </div>

    <div
      v-if="showReader && !switching && resumeVisible && resumeOrdinal !== null"
      class="resume"
    >
      <span class="resume__text">{{ READING.resumeAt(resumeOrdinal + 1) }}</span>
      <button type="button" class="resume__go" @click="resumeReading">
        {{ READING.jumpToPosition }}
      </button>
      <button
        type="button"
        class="resume__close"
        :aria-label="READING.dismiss"
        @click="hideResume"
      >
        ×
      </button>
    </div>

    <ReadingNoteDialog
      ref="noteDialog"
      :body="noteBody"
      :saving="markSaving"
      :failure="markFailure"
      @update:body="noteBody = $event"
      @submit="submitNote"
    />

    <ReadingFileDialog
      ref="fileDialog"
      :status="filesStatus"
      :failure="filesFailure"
      :files="files"
      :errors="fileErrors"
      :opening-id="openingFileId"
      @retry="openFilePicker"
      @choose="chooseFile"
      @go-files="router.push({ name: 'files' })"
    />
  </SurfaceGate>
</template>

<style scoped src="./reading/controls.css"></style>
<style scoped src="./Reading.css"></style>
