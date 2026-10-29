<script setup lang="ts">
/**
 * 新建活书。
 *
 * 四选一来源，但四个来源的"选"不是同一件事：
 * - 对话、知识库、文件要选一个具体的行；
 * - 题库不用选 —— 它整份题库就是来源，提交空 `source_id`。
 *
 * 文件还多一步：已上传不等于能读。`document === null` 的文件提交上去只会
 * 换来一个 409，所以在这里先查一次，把"尚未解析"说在提交之前。
 *
 * 来源在创建时被快照，之后来源被删改不会影响这本书；对话框的说明里写了
 * 这一点，因为它是这个功能最容易被误解的地方。
 */

import { computed, ref, watch } from 'vue'
import { useRouter } from 'vue-router'

import type { ApiFailure } from '../../../api/client'
import {
  createBook,
  type BookDetail,
  type BookSourceKind,
} from '../../../api/books'
import { describeFailure } from '../../../api/errors'
import { getFile, listFiles, type FileSummary } from '../../../api/files'
import { listKnowledgeBases, type KnowledgeBase } from '../../../api/knowledgeBases'
import { listSessions, type SessionSummary } from '../../../api/sessions'
import { BOOK_ERRORS, BOOKS } from '../../../copy/books'
import { toFailure } from '../../../shared/resource'

const emit = defineEmits<{
  created: [book: BookDetail]
}>()

const router = useRouter()

const dialog = ref<HTMLDialogElement | null>(null)

const kinds: ReadonlyArray<BookSourceKind> = [
  'session',
  'knowledge_base',
  'file',
  'question_bank',
]

type ListStatus = 'idle' | 'loading' | 'ready' | 'failed'
type FileCheck = 'idle' | 'checking' | 'parsed' | 'unparsed' | 'failed'

const kind = ref<BookSourceKind>('session')
const title = ref('')
const sessionId = ref('')
const kbId = ref('')
const fileId = ref('')

const sessions = ref<SessionSummary[]>([])
const knowledgeBases = ref<KnowledgeBase[]>([])
const files = ref<FileSummary[]>([])

const sessionsStatus = ref<ListStatus>('idle')
const sessionsFailure = ref<ApiFailure | null>(null)
const knowledgeStatus = ref<ListStatus>('idle')
const knowledgeFailure = ref<ApiFailure | null>(null)
const filesStatus = ref<ListStatus>('idle')
const filesFailure = ref<ApiFailure | null>(null)

const fileCheck = ref<FileCheck>('idle')

const submitting = ref(false)
const failure = ref<ApiFailure | null>(null)

const ready = computed(() => {
  switch (kind.value) {
    case 'session':
      return sessionId.value !== ''
    case 'knowledge_base':
      return kbId.value !== ''
    case 'file':
      return fileId.value !== '' && fileCheck.value === 'parsed'
    case 'question_bank':
      return true
    default:
      return false
  }
})

const failureText = computed(() => {
  const failed = failure.value
  if (!failed) return ''
  return BOOK_ERRORS[failed.code] ?? describeFailure(failed)
})

async function loadSessions(): Promise<void> {
  if (sessionsStatus.value === 'loading') return
  sessionsStatus.value = 'loading'
  sessionsFailure.value = null
  try {
    sessions.value = (await listSessions({ limit: 50 })).sessions
    sessionsStatus.value = 'ready'
  } catch (cause) {
    sessionsFailure.value = toFailure(cause)
    sessionsStatus.value = 'failed'
  }
}

async function loadKnowledgeBases(): Promise<void> {
  if (knowledgeStatus.value === 'loading') return
  knowledgeStatus.value = 'loading'
  knowledgeFailure.value = null
  try {
    knowledgeBases.value = (await listKnowledgeBases()).knowledge_bases
    knowledgeStatus.value = 'ready'
  } catch (cause) {
    knowledgeFailure.value = toFailure(cause)
    knowledgeStatus.value = 'failed'
  }
}

async function loadFiles(): Promise<void> {
  if (filesStatus.value === 'loading') return
  filesStatus.value = 'loading'
  filesFailure.value = null
  try {
    files.value = (await listFiles({ limit: 50 })).files
    filesStatus.value = 'ready'
  } catch (cause) {
    filesFailure.value = toFailure(cause)
    filesStatus.value = 'failed'
  }
}

/** 切换来源时只加载它自己的列表；加载过就不重复要。 */
function ensureSourceList(next: BookSourceKind): void {
  if (next === 'session' && sessionsStatus.value === 'idle') void loadSessions()
  if (next === 'knowledge_base' && knowledgeStatus.value === 'idle') void loadKnowledgeBases()
  if (next === 'file' && filesStatus.value === 'idle') void loadFiles()
}

watch(kind, (next) => {
  sessionId.value = ''
  kbId.value = ''
  fileId.value = ''
  fileCheck.value = 'idle'
  failure.value = null
  ensureSourceList(next)
})

/** 选中的文件能不能读，只有 `getFile` 的回答算数。 */
watch(fileId, async (id) => {
  fileCheck.value = 'idle'
  failure.value = null
  if (id === '') return
  fileCheck.value = 'checking'
  try {
    const view = await getFile(id)
    // 换了一个文件时，这份回答已经过时了：不拿它覆盖新的检查。
    if (fileId.value !== id) return
    fileCheck.value = view.document ? 'parsed' : 'unparsed'
  } catch (cause) {
    if (fileId.value !== id) return
    fileCheck.value = 'failed'
    failure.value = toFailure(cause)
  }
})

function show(): void {
  title.value = ''
  kind.value = 'session'
  sessionId.value = ''
  kbId.value = ''
  fileId.value = ''
  fileCheck.value = 'idle'
  failure.value = null
  submitting.value = false
  if (!dialog.value?.open) dialog.value?.showModal()
  ensureSourceList('session')
}

function hide(): void {
  if (dialog.value?.open) dialog.value.close()
}

function sourceId(): string {
  if (kind.value === 'session') return sessionId.value
  if (kind.value === 'knowledge_base') return kbId.value
  if (kind.value === 'file') return fileId.value
  return ''
}

async function submit(): Promise<void> {
  if (!ready.value || submitting.value) return
  submitting.value = true
  failure.value = null
  try {
    const created = await createBook({
      title: title.value.trim(),
      source_kind: kind.value,
      source_id: sourceId(),
    })
    hide()
    emit('created', created)
  } catch (cause) {
    failure.value = toFailure(cause)
  } finally {
    submitting.value = false
  }
}

function go(name: 'chat' | 'knowledge-bases' | 'files'): void {
  hide()
  void router.push({ name })
}

defineExpose({ show, hide })
</script>

<template>
  <dialog ref="dialog" class="book-dialog" @cancel.prevent="hide">
    <h2 class="book-dialog__title">{{ BOOKS.createTitle }}</h2>
    <p class="book-dialog__hint">{{ BOOKS.createHint }}</p>

    <div class="kinds" role="radiogroup" :aria-label="BOOKS.sourceLabel(kind)">
      <button
        v-for="option in kinds"
        :key="option"
        type="button"
        class="kind"
        :class="{ 'kind--active': kind === option }"
        role="radio"
        :aria-checked="kind === option"
        :disabled="submitting"
        @click="kind = option"
      >
        {{ BOOKS.sourceLabel(option) }}
      </button>
    </div>

    <!-- 对话 -->
    <div v-if="kind === 'session'" class="book-field">
      <label class="book-field__label" for="book-source-session">
        {{ BOOKS.chooseSession }}
      </label>
      <select
        id="book-source-session"
        v-model="sessionId"
        :disabled="submitting || sessionsStatus === 'loading'"
      >
        <option value="">{{ BOOKS.chooseSession }}</option>
        <option v-for="session in sessions" :key="session.session_id" :value="session.session_id">
          {{ session.title }}
        </option>
      </select>
      <p v-if="sessionsStatus === 'failed'" class="book-inline-error">
        {{ BOOKS.sourceListFailed }}：{{ sessionsFailure ? describeFailure(sessionsFailure) : '' }}
        <button type="button" class="book-link" @click="loadSessions">重试</button>
      </p>
      <p v-else-if="sessionsStatus === 'ready' && sessions.length === 0" class="book-inline-warn">
        {{ BOOKS.noSessionOption }}
        <button type="button" class="book-link" @click="go('chat')">{{ BOOKS.goChat }}</button>
      </p>
    </div>

    <!-- 知识库 -->
    <div v-else-if="kind === 'knowledge_base'" class="book-field">
      <label class="book-field__label" for="book-source-kb">
        {{ BOOKS.chooseKnowledgeBase }}
      </label>
      <select
        id="book-source-kb"
        v-model="kbId"
        :disabled="submitting || knowledgeStatus === 'loading'"
      >
        <option value="">{{ BOOKS.chooseKnowledgeBase }}</option>
        <option v-for="base in knowledgeBases" :key="base.kb_id" :value="base.kb_id">
          {{ base.name }}
        </option>
      </select>
      <p v-if="knowledgeStatus === 'failed'" class="book-inline-error">
        {{ BOOKS.sourceListFailed }}：{{ knowledgeFailure ? describeFailure(knowledgeFailure) : '' }}
        <button type="button" class="book-link" @click="loadKnowledgeBases">重试</button>
      </p>
      <p
        v-else-if="knowledgeStatus === 'ready' && knowledgeBases.length === 0"
        class="book-inline-warn"
      >
        {{ BOOKS.noKnowledgeBaseOption }}
        <button type="button" class="book-link" @click="go('knowledge-bases')">
          {{ BOOKS.goKnowledge }}
        </button>
      </p>
    </div>

    <!-- 文件 -->
    <div v-else-if="kind === 'file'" class="book-field">
      <label class="book-field__label" for="book-source-file">
        {{ BOOKS.chooseFile }}
      </label>
      <select
        id="book-source-file"
        v-model="fileId"
        :disabled="submitting || filesStatus === 'loading'"
      >
        <option value="">{{ BOOKS.chooseFile }}</option>
        <option v-for="file in files" :key="file.id" :value="file.id">
          {{ file.filename }}
        </option>
      </select>
      <p v-if="fileCheck === 'checking'" class="book-inline-warn">{{ BOOKS.loadingFile }}</p>
      <p v-else-if="fileCheck === 'unparsed'" class="book-inline-warn">
        {{ BOOKS.unparsedFile }}
        <button type="button" class="book-link" @click="go('files')">{{ BOOKS.goFiles }}</button>
      </p>
      <p v-else-if="fileCheck === 'failed' && failure" class="book-inline-error">
        {{ failureText }}
      </p>
      <p v-if="filesStatus === 'failed'" class="book-inline-error">
        {{ BOOKS.sourceListFailed }}：{{ filesFailure ? describeFailure(filesFailure) : '' }}
        <button type="button" class="book-link" @click="loadFiles">重试</button>
      </p>
      <p v-else-if="filesStatus === 'ready' && files.length === 0" class="book-inline-warn">
        {{ BOOKS.noFileOption }}
        <button type="button" class="book-link" @click="go('files')">{{ BOOKS.goFiles }}</button>
      </p>
    </div>

    <!-- 题库 -->
    <p v-else class="book-inline-warn">{{ BOOKS.questionBankNote }}</p>

    <div class="book-field">
      <label class="book-field__label" for="book-title">{{ BOOKS.titleLabel }}</label>
      <input
        id="book-title"
        v-model="title"
        type="text"
        maxlength="200"
        :placeholder="BOOKS.titlePlaceholder"
        :disabled="submitting"
        @keydown.enter.prevent="submit"
      />
    </div>

    <p v-if="failure && fileCheck !== 'failed'" class="book-inline-error">
      {{ failureText }}
      <code v-if="failure.requestId" class="book-error__req">{{ failure.requestId }}</code>
    </p>

    <div class="book-dialog__actions">
      <button type="button" class="book-ghost" :disabled="submitting" @click="hide">
        {{ BOOKS.cancel }}
      </button>
      <button
        type="button"
        class="book-primary"
        :disabled="!ready || submitting"
        @click="submit"
      >
        {{ submitting ? BOOKS.creating : BOOKS.create }}
      </button>
    </div>
  </dialog>
</template>

<style scoped src="./books.css"></style>
<style scoped>
.kinds {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin: 14px 0 2px;
}

.kind {
  padding: 6px 14px;
  border: 1px solid rgb(0 0 0 / 8%);
  border-radius: 999px;
  background: rgb(255 255 255 / 78%);
  color: var(--text-muted);
  font-size: 14px;
  font-weight: 600;
  cursor: pointer;
  transition: all 0.2s ease;
}

.kind:hover {
  border-color: var(--accent);
  background: #fff;
  color: var(--text);
}

.kind--active {
  border-color: transparent;
  background: var(--accent-gradient);
  color: #fff;
  box-shadow: 0 4px 12px rgb(177 140 255 / 30%);
}
</style>
