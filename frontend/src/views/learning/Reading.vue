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

import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import { describeFailure } from '../../api/errors'
import { canSurface } from '../../auth/permissions'
import EmptyState from '../../components/common/EmptyState.vue'
import FailureState from '../../components/common/FailureState.vue'
import SkeletonBlock from '../../components/common/SkeletonBlock.vue'
import SurfaceGate from '../../components/common/SurfaceGate.vue'
import PageHeader from '../../components/shell/PageHeader.vue'
import { READING } from '../../copy/reading'

import ReadingFileDialog from './reading/ReadingFileDialog.vue'
import ReadingExtensions from './reading/ReadingExtensions.vue'
import ReadingMarks from './reading/ReadingMarks.vue'
import ReadingMaterials from './reading/ReadingMaterials.vue'
import ReadingNoteDialog from './reading/ReadingNoteDialog.vue'
import ReadingPairing from './reading/ReadingPairing.vue'
import ReadingSelectionBar from './reading/ReadingSelectionBar.vue'
import ReadingSessionBar from './reading/ReadingSessionBar.vue'
import ReadingUnits from './reading/ReadingUnits.vue'
import { useReadingExtensions } from './reading/useReadingExtensions'
import { useReadingFilePicker } from './reading/useReadingFilePicker'
import { useReadingMarks } from './reading/useReadingMarks'
import { useReadingMaterials } from './reading/useReadingMaterials'
import { useReadingOutline } from './reading/useReadingOutline'
import { useReadingPairing } from './reading/useReadingPairing'
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

const {
  sessions,
  sessionsStatus,
  sessionId,
  pendingSession,
  sessionHint,
  sessionTitle,
  sessionMissing,
  loadSessions,
  applySession,
  clearSession,
  needSession,
  setSession,
} = useReadingSessions(route, router, currentId, reloadOutline)

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
    resetExtensions()
    resetPairing()
    selection.value = null
  },
  afterReady: () => {
    void Promise.all([loadMarks(), loadPairings(), loadExtensions()])
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

/* ── 批注、配对、扩展 ────────────────────────────────────────────────── */

const {
  marks,
  marksStatus,
  marksFailure,
  markFailure,
  markSaving,
  loadMarks,
  addMark,
  addHighlight,
  addBookmark,
  jumpToMark,
  resetMarks,
} = useReadingMarks({
  currentId,
  sessionId,
  needSession,
  currentLocator,
  clearSelection,
  jumpToLocator,
})

const {
  bilingual,
  candidates,
  pairingStatus,
  pairingFailure,
  pairingActionFailure,
  pairChoice,
  pairingBusy,
  loadPairings,
  confirmSelectedPairing,
  cancelCurrentPairing,
  resetPairing,
} = useReadingPairing(currentId)

const {
  extensions,
  extensionsStatus,
  extensionsFailure,
  extensionResults,
  extensionRunning,
  extensionFailure,
  loadExtensions,
  runOne,
  resetExtensions,
} = useReadingExtensions(currentId, selection)

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

onMounted(() => {
  if (canSurface('reading') === false) return
  void Promise.all([loadMaterials(), loadSessions()])
})
</script>

<template>
  <SurfaceGate surface="reading">
    <PageHeader :title="READING.title" :description="READING.description" />

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

      <section class="reader">
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
          <ReadingSessionBar
            :session-id="sessionId"
            :session-title="sessionTitle"
            :sessions="sessions"
            :sessions-status="sessionsStatus"
            :session-missing="sessionMissing"
            :session-hint="sessionHint"
            :pending-session="pendingSession"
            :position-ordinal="outline.position?.locator.ordinal ?? null"
            @update:pending-session="pendingSession = $event"
            @apply="applySession"
            @clear="clearSession"
            @reload-sessions="loadSessions"
            @jump-position="jumpToPosition"
          />

          <ReadingSelectionBar
            v-if="selection"
            :text="selection.text"
            :saving="markSaving"
            @copy="copySelection"
            @highlight="addHighlight"
            @note="openNote"
            @ask="askInChat"
            @clear="clearSelection"
          />

          <p v-if="markFailure" class="inline-error">
            批注没能保存：{{ describeFailure(markFailure) }}
            <code v-if="markFailure.requestId" class="req">{{ markFailure.requestId }}</code>
          </p>

          <div class="meta">{{ READING.textChars(outline.text_chars) }}</div>

          <ReadingUnits
            :entries="unitsWithState"
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

      <aside class="panel">
        <ReadingMarks
          :marks="marks"
          :status="marksStatus"
          :failure="marksFailure"
          :saving="markSaving"
          @retry="loadMarks"
          @bookmark="addBookmark"
          @jump="jumpToMark"
        />

        <ReadingPairing
          :choice="pairChoice"
          :bilingual="bilingual"
          :candidates="candidates"
          :status="pairingStatus"
          :failure="pairingFailure"
          :action-failure="pairingActionFailure"
          :busy="pairingBusy"
          @retry="loadPairings"
          @confirm="confirmSelectedPairing"
          @cancel="cancelCurrentPairing"
          @update:choice="pairChoice = $event"
        />

        <ReadingExtensions
          :extensions="extensions"
          :status="extensionsStatus"
          :failure="extensionsFailure"
          :results="extensionResults"
          :running="extensionRunning"
          :action-failure="extensionFailure"
          :has-selection="selection !== null"
          @retry="loadExtensions"
          @run="runOne"
        />
      </aside>
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
