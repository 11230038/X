<script setup lang="ts">
/**
 * 工作区导出。
 *
 * 选择对象 → 生成任务 → **带凭据取回 blob 再交给浏览器**。不能把 URL 直接
 * 交给 `window.open`，因为 token 在 `Authorization` 头里。
 */

import { computed, onMounted, ref } from 'vue'

import type { ApiFailure } from '../../../api/client'
import { listFiles } from '../../../api/files'
import { listKnowledgeBases } from '../../../api/knowledgeBases'
import { listQuestions } from '../../../api/questions'
import { listSessions } from '../../../api/sessions'
import {
  createExport,
  downloadExport,
  listExports,
  type WorkspaceJob,
  type WorkspaceRef,
} from '../../../api/workspace'
import PartialBanner from '../../../components/common/PartialBanner.vue'
import SkeletonBlock from '../../../components/common/SkeletonBlock.vue'
import { CHAT } from '../../../copy/chat'
import { SETTINGS } from '../../../copy/settings'
import { formatDateTime, truncate } from '../../../shared/format'
import { toFailure } from '../../../shared/resource'
import { notify } from '../../../shared/toast'

import { jobStateLabel } from './labels'
import AppIcon from '../../../components/common/AppIcon.vue'

interface PickItem {
  ref: WorkspaceRef
  label: string
  meta: string
}

type LoadState = 'loading' | 'ready' | 'failed'

const sessions = ref<PickItem[]>([])
const files = ref<PickItem[]>([])
const questions = ref<PickItem[]>([])
const knowledgeBases = ref<PickItem[]>([])

const sessionsState = ref<LoadState>('loading')
const filesState = ref<LoadState>('loading')
const questionsState = ref<LoadState>('loading')
const knowledgeBasesState = ref<LoadState>('loading')

const sessionsFailure = ref<ApiFailure | null>(null)
const filesFailure = ref<ApiFailure | null>(null)
const questionsFailure = ref<ApiFailure | null>(null)
const knowledgeBasesFailure = ref<ApiFailure | null>(null)

const selected = ref<WorkspaceRef[]>([])
const exportBusy = ref(false)
const exportFailure = ref<ApiFailure | null>(null)
const exportJob = ref<WorkspaceJob | null>(null)
const downloadBusy = ref(false)
const history = ref<WorkspaceJob[]>([])
const historyFailure = ref<ApiFailure | null>(null)

const selectedCount = computed(() => selected.value.length)

function refKey(ref: WorkspaceRef): string {
  return `${ref.kind}:${ref.id}`
}

async function loadSessions(): Promise<void> {
  sessionsState.value = 'loading'
  sessionsFailure.value = null
  try {
    // 导出要的是全部会话：缺省的 `chat` 会把写作和路径会话漏掉。
    const result = await listSessions({ kind: 'all', limit: 50 })
    sessions.value = result.sessions.map((session) => ({
      ref: { kind: 'session', id: session.session_id },
      label: session.title || session.session_id,
      meta: [
        session.kind === 'writing'
          ? CHAT.kindWriting
          : session.kind === 'mastery'
            ? CHAT.kindMastery
            : session.kind === 'question_bank'
              ? CHAT.kindQuestionBank
              : '',
        session.updated_at ? formatDateTime(session.updated_at) : '',
      ]
        .filter(Boolean)
        .join(' · '),
    }))
    sessionsState.value = 'ready'
  } catch (cause) {
    sessionsFailure.value = toFailure(cause)
    sessionsState.value = 'failed'
  }
}

async function loadFiles(): Promise<void> {
  filesState.value = 'loading'
  filesFailure.value = null
  try {
    const result = await listFiles({ limit: 50 })
    files.value = result.files.map((file) => ({
      ref: { kind: 'material', id: file.id },
      label: file.filename,
      meta: file.mime_type,
    }))
    filesState.value = 'ready'
  } catch (cause) {
    filesFailure.value = toFailure(cause)
    filesState.value = 'failed'
  }
}

async function loadQuestions(): Promise<void> {
  questionsState.value = 'loading'
  questionsFailure.value = null
  try {
    const result = await listQuestions({ limit: 50 })
    questions.value = result.questions.map((entry) => ({
      ref: { kind: 'question', id: entry.question.question_id },
      label: truncate(entry.question.text, 48),
      meta: entry.question.question_type,
    }))
    questionsState.value = 'ready'
  } catch (cause) {
    questionsFailure.value = toFailure(cause)
    questionsState.value = 'failed'
  }
}

async function loadKnowledgeBases(): Promise<void> {
  knowledgeBasesState.value = 'loading'
  knowledgeBasesFailure.value = null
  try {
    const result = await listKnowledgeBases()
    knowledgeBases.value = result.knowledge_bases.map((kb) => ({
      ref: { kind: 'knowledge_base', id: kb.kb_id },
      label: kb.name,
      meta: `${kb.documents} 份文档`,
    }))
    knowledgeBasesState.value = 'ready'
  } catch (cause) {
    knowledgeBasesFailure.value = toFailure(cause)
    knowledgeBasesState.value = 'failed'
  }
}

function isSelected(ref: WorkspaceRef): boolean {
  return selected.value.some((item) => item.kind === ref.kind && item.id === ref.id)
}

function toggle(ref: WorkspaceRef): void {
  const key = refKey(ref)
  selected.value = isSelected(ref)
    ? selected.value.filter((item) => refKey(item) !== key)
    : [...selected.value, ref]
}

function selectAll(items: PickItem[]): void {
  const keys = new Set(selected.value.map(refKey))
  for (const item of items) {
    if (!keys.has(refKey(item.ref))) {
      selected.value = [...selected.value, item.ref]
    }
  }
}

async function loadHistory(): Promise<void> {
  historyFailure.value = null
  try {
    const result = await listExports(20)
    history.value = result.jobs
  } catch (cause) {
    historyFailure.value = toFailure(cause)
  }
}

async function runExport(): Promise<void> {
  if (selected.value.length === 0) return
  exportBusy.value = true
  exportFailure.value = null
  exportJob.value = null
  try {
    const result = await createExport(selected.value)
    exportJob.value = result.job
    await loadHistory()
    notify(SETTINGS.exportReady, 'success')
  } catch (cause) {
    exportFailure.value = toFailure(cause)
  } finally {
    exportBusy.value = false
  }
}

function saveBlob(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = filename
  link.click()
  URL.revokeObjectURL(url)
}

async function download(job: WorkspaceJob): Promise<void> {
  downloadBusy.value = true
  try {
    const result = await downloadExport(job.job_id)
    saveBlob(result.blob, result.filename)
  } catch (cause) {
    notify(`下载没能完成：${toFailure(cause).message}`, 'error')
  } finally {
    downloadBusy.value = false
  }
}

onMounted(() => {
  void Promise.allSettled([
    loadSessions(),
    loadFiles(),
    loadQuestions(),
    loadKnowledgeBases(),
    loadHistory(),
  ])
})
</script>

<template>
  <section class="card">
    <div class="card__head">
      <span class="card__icon" aria-hidden="true"><AppIcon name="package" /></span>
      <h2 class="card__title">{{ SETTINGS.export }}</h2>
    </div>
    <p class="hint">{{ SETTINGS.exportScope }}</p>

    <div class="pickers">
      <div class="picker">
        <header class="picker__head">
          <h3 class="picker__title">会话</h3>
          <button
            type="button"
            class="link"
            :disabled="sessions.length === 0"
            @click="selectAll(sessions)"
          >
            全选
          </button>
        </header>
        <SkeletonBlock v-if="sessionsState === 'loading'" :lines="3" :line-height="22" />
        <PartialBanner
          v-else-if="sessionsState === 'failed' && sessionsFailure"
          :failure="sessionsFailure"
          text="会话列表没有读出来"
          @retry="loadSessions"
        />
        <p v-else-if="sessions.length === 0" class="empty">没有会话</p>
        <ul v-else class="items">
          <li v-for="item in sessions" :key="item.ref.id">
            <label class="item">
              <input
                type="checkbox"
                :checked="isSelected(item.ref)"
                @change="toggle(item.ref)"
              />
              <span class="item__label">{{ item.label }}</span>
              <span class="item__meta">{{ item.meta }}</span>
            </label>
          </li>
        </ul>
      </div>

      <div class="picker">
        <header class="picker__head">
          <h3 class="picker__title">材料</h3>
          <button type="button" class="link" :disabled="files.length === 0" @click="selectAll(files)">
            全选
          </button>
        </header>
        <SkeletonBlock v-if="filesState === 'loading'" :lines="3" :line-height="22" />
        <PartialBanner
          v-else-if="filesState === 'failed' && filesFailure"
          :failure="filesFailure"
          text="材料列表没有读出来"
          @retry="loadFiles"
        />
        <p v-else-if="files.length === 0" class="empty">没有材料</p>
        <ul v-else class="items">
          <li v-for="item in files" :key="item.ref.id">
            <label class="item">
              <input type="checkbox" :checked="isSelected(item.ref)" @change="toggle(item.ref)" />
              <span class="item__label">{{ item.label }}</span>
              <span class="item__meta">{{ item.meta }}</span>
            </label>
          </li>
        </ul>
      </div>

      <div class="picker">
        <header class="picker__head">
          <h3 class="picker__title">题目</h3>
          <button
            type="button"
            class="link"
            :disabled="questions.length === 0"
            @click="selectAll(questions)"
          >
            全选
          </button>
        </header>
        <SkeletonBlock v-if="questionsState === 'loading'" :lines="3" :line-height="22" />
        <PartialBanner
          v-else-if="questionsState === 'failed' && questionsFailure"
          :failure="questionsFailure"
          text="题目列表没有读出来"
          @retry="loadQuestions"
        />
        <p v-else-if="questions.length === 0" class="empty">没有题目</p>
        <ul v-else class="items">
          <li v-for="item in questions" :key="item.ref.id">
            <label class="item">
              <input type="checkbox" :checked="isSelected(item.ref)" @change="toggle(item.ref)" />
              <span class="item__label">{{ item.label }}</span>
              <span class="item__meta">{{ item.meta }}</span>
            </label>
          </li>
        </ul>
      </div>

      <div class="picker">
        <header class="picker__head">
          <h3 class="picker__title">知识库</h3>
          <button
            type="button"
            class="link"
            :disabled="knowledgeBases.length === 0"
            @click="selectAll(knowledgeBases)"
          >
            全选
          </button>
        </header>
        <SkeletonBlock v-if="knowledgeBasesState === 'loading'" :lines="3" :line-height="22" />
        <PartialBanner
          v-else-if="knowledgeBasesState === 'failed' && knowledgeBasesFailure"
          :failure="knowledgeBasesFailure"
          text="知识库列表没有读出来"
          @retry="loadKnowledgeBases"
        />
        <p v-else-if="knowledgeBases.length === 0" class="empty">没有知识库</p>
        <ul v-else class="items">
          <li v-for="item in knowledgeBases" :key="item.ref.id">
            <label class="item">
              <input type="checkbox" :checked="isSelected(item.ref)" @change="toggle(item.ref)" />
              <span class="item__label">{{ item.label }}</span>
              <span class="item__meta">{{ item.meta }}</span>
            </label>
          </li>
        </ul>
      </div>
    </div>

    <div class="actions">
      <span class="hint">已选 {{ selectedCount }} 个对象</span>
      <button
        type="button"
        class="primary"
        :disabled="exportBusy || selectedCount === 0"
        @click="runExport"
      >
        {{ exportBusy ? '正在生成…' : SETTINGS.export }}
      </button>
    </div>

    <p v-if="exportFailure" class="error">
      导出没能生成：{{ exportFailure.message }}
    </p>

    <div v-if="exportJob" class="job">
      <span class="job__state">{{ SETTINGS.jobState }}：{{ jobStateLabel(exportJob.state) }}</span>
      <span class="job__id">{{ exportJob.job_id }}</span>
      <button
        v-if="exportJob.state !== 'failed'"
        type="button"
        class="ghost"
        :disabled="downloadBusy"
        @click="download(exportJob)"
      >
        {{ SETTINGS.download }}
      </button>
    </div>

    <h3 class="subtitle">最近的导出</h3>
    <PartialBanner
      v-if="historyFailure"
      :failure="historyFailure"
      text="导出记录没有读出来"
      @retry="loadHistory"
    />
    <p v-else-if="history.length === 0" class="empty">还没有导出记录</p>
    <ul v-else class="jobs">
      <li v-for="job in history" :key="job.job_id" class="job">
        <span class="job__state">{{ jobStateLabel(job.state) }}</span>
        <span class="job__time">{{ formatDateTime(job.created_at) }}</span>
        <span class="job__id">{{ job.job_id }}</span>
        <button
          v-if="job.state !== 'failed'"
          type="button"
          class="ghost"
          :disabled="downloadBusy"
          @click="download(job)"
        >
          {{ SETTINGS.download }}
        </button>
      </li>
    </ul>
  </section>
</template>

<style scoped src="../SettingsWorkspace.css"></style>
