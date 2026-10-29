<script setup lang="ts">
/**
 * 大纲草稿编辑器。
 *
 * 这里改的是**目录**，不是正文：标题、说明、顺序、增删。字数上限与后端
 * 一致（标题 120、说明 500、1–12 章），在提交前就拦住，因为一次往返换来
 * 的 `invalid_outline` 只会让人重改一遍。
 *
 * 草稿以数组顺序为章节顺序，不上传 `ordinal` —— 上移、下移就是数组换位，
 * 没有第三种排序规则可以和它分叉。
 */

import { computed, ref, watch } from 'vue'

import type { ApiFailure } from '../../../api/client'
import type { BookOutlineItem } from '../../../api/books'
import { describeFailure } from '../../../api/errors'
import { BOOK_ERRORS, BOOKS } from '../../../copy/books'
import { confirmAction } from '../../../shared/confirm'

const props = defineProps<{
  /** 服务端当前的大纲；每次刷新都会重新灌进草稿。 */
  outline: BookOutlineItem[]
  busy: boolean
  failure: ApiFailure | null
}>()

const emit = defineEmits<{
  save: [payload: { chapters: Array<{ title: string; summary: string }>; confirmed: boolean }]
  regenerate: []
}>()

const TITLE_MAX = 120
const SUMMARY_MAX = 500
const MAX_CHAPTERS = 12

interface Draft {
  key: number
  title: string
  summary: string
}

const drafts = ref<Draft[]>([])
let nextKey = 1

watch(
  () => props.outline,
  (items) => {
    drafts.value = items.map((item) => ({
      key: nextKey++,
      title: item.title,
      summary: item.summary,
    }))
  },
  { immediate: true },
)

const problem = computed(() => {
  if (drafts.value.length === 0) return BOOKS.outlineTooFew
  if (drafts.value.length > MAX_CHAPTERS) return BOOKS.outlineTooMany
  const seen = new Set<string>()
  for (const draft of drafts.value) {
    const title = draft.title.trim()
    if (title === '') return BOOKS.outlineTitleEmpty
    if ([...title].length > TITLE_MAX) return BOOKS.outlineTitleLong
    if ([...draft.summary].length > SUMMARY_MAX) return BOOKS.outlineSummaryLong
    if (seen.has(title)) return BOOKS.outlineDuplicate(title)
    seen.add(title)
  }
  return ''
})

const failureText = computed(() => {
  if (!props.failure) return ''
  return BOOK_ERRORS[props.failure.code] ?? describeFailure(props.failure)
})

function addChapter(): void {
  if (drafts.value.length >= MAX_CHAPTERS) return
  drafts.value = [...drafts.value, { key: nextKey++, title: '', summary: '' }]
}

function removeChapter(index: number): void {
  if (drafts.value.length <= 1) return
  drafts.value = drafts.value.filter((_, at) => at !== index)
}

function move(index: number, delta: number): void {
  const target = index + delta
  if (target < 0 || target >= drafts.value.length) return
  const next = [...drafts.value]
  const [item] = next.splice(index, 1)
  next.splice(target, 0, item)
  drafts.value = next
}

function payload(confirmed: boolean): { chapters: Array<{ title: string; summary: string }>; confirmed: boolean } {
  return {
    chapters: drafts.value.map((draft) => ({
      title: draft.title.trim(),
      summary: draft.summary.trim(),
    })),
    confirmed,
  }
}

function save(confirmed: boolean): void {
  if (problem.value !== '' || props.busy) return
  emit('save', payload(confirmed))
}

async function regenerate(): Promise<void> {
  if (props.busy) return
  const confirmed = await confirmAction({
    title: BOOKS.regenerateTitle,
    body: BOOKS.regenerateBody,
    confirmLabel: BOOKS.regenerateConfirm,
    danger: true,
  })
  if (confirmed) emit('regenerate')
}

function titleCount(draft: Draft): string {
  return `${[...draft.title].length}/${TITLE_MAX}`
}
</script>

<template>
  <section class="editor">
    <header class="editor__head">
      <div>
        <h2 class="editor__title">{{ BOOKS.outlineDraft }}</h2>
        <p class="editor__hint">{{ BOOKS.outlineDraftHint }}</p>
      </div>
      <div class="book-actions">
        <button type="button" class="book-ghost" :disabled="busy" @click="regenerate">
          {{ BOOKS.regenerateOutline }}
        </button>
        <button
          type="button"
          class="book-ghost"
          :disabled="busy || problem !== ''"
          @click="save(false)"
        >
          {{ busy ? BOOKS.savingOutline : BOOKS.saveOutline }}
        </button>
        <button
          type="button"
          class="book-primary"
          :disabled="busy || problem !== ''"
          @click="save(true)"
        >
          {{ busy ? BOOKS.savingOutline : BOOKS.saveAndConfirm }}
        </button>
      </div>
    </header>

    <p v-if="failure" class="book-inline-error">
      {{ failureText }}
      <code v-if="failure.requestId" class="book-error__req">{{ failure.requestId }}</code>
    </p>

    <ol class="rows">
      <li v-for="(draft, index) in drafts" :key="draft.key" class="row">
        <div class="row__side">
          <span class="row__ordinal">{{ BOOKS.chapterNumber(index + 1) }}</span>
          <div class="row__moves">
            <button
              type="button"
              class="row__move"
              :disabled="busy || index === 0"
              :aria-label="BOOKS.moveUp"
              @click="move(index, -1)"
            >
              ↑
            </button>
            <button
              type="button"
              class="row__move"
              :disabled="busy || index === drafts.length - 1"
              :aria-label="BOOKS.moveDown"
              @click="move(index, 1)"
            >
              ↓
            </button>
          </div>
        </div>

        <div class="row__fields">
          <div class="book-field">
            <label class="book-field__label">
              {{ BOOKS.chapterTitle }}
              <span class="row__count">{{ titleCount(draft) }}</span>
            </label>
            <input
              v-model="draft.title"
              type="text"
              :maxlength="TITLE_MAX"
              :placeholder="BOOKS.chapterTitle"
              :disabled="busy"
            />
          </div>
          <div class="book-field">
            <label class="book-field__label">{{ BOOKS.chapterSummary }}</label>
            <input
              v-model="draft.summary"
              type="text"
              :maxlength="SUMMARY_MAX"
              :placeholder="BOOKS.chapterSummary"
              :disabled="busy"
            />
          </div>
        </div>

        <button
          type="button"
          class="book-danger row__remove"
          :disabled="busy || drafts.length <= 1"
          @click="removeChapter(index)"
        >
          {{ BOOKS.removeChapter }}
        </button>
      </li>
    </ol>

    <button
      type="button"
      class="book-ghost add"
      :disabled="busy || drafts.length >= MAX_CHAPTERS"
      @click="addChapter"
    >
      {{ BOOKS.addChapter }}
    </button>

    <p v-if="problem" class="book-inline-warn">{{ problem }}</p>
  </section>
</template>

<style scoped src="./books.css"></style>
<style scoped>
.editor {
  display: flex;
  flex-direction: column;
  gap: 14px;
  padding: 20px;
  border: 1px solid var(--glass-border);
  border-radius: var(--radius-card);
  background: var(--glass-bg);
  box-shadow: var(--glass-shadow), var(--inset-light);
  backdrop-filter: blur(24px);
  -webkit-backdrop-filter: blur(24px);
}

.editor__head {
  display: flex;
  flex-wrap: wrap;
  gap: 14px;
  align-items: flex-start;
  justify-content: space-between;
}

.editor__title {
  margin: 0;
  font-size: 15px;
  font-weight: 650;
}

.editor__hint {
  margin: 3px 0 0;
  color: var(--text-muted);
  font-size: 14px;
}

.rows {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.row {
  display: flex;
  gap: 14px;
  align-items: flex-start;
  padding: 10px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--surface-sunken);
}

.row__side {
  display: flex;
  flex: none;
  flex-direction: column;
  gap: 6px;
  align-items: center;
  width: 64px;
}

.row__ordinal {
  color: var(--text-muted);
  font-size: 14px;
  white-space: nowrap;
}

.row__moves {
  display: flex;
  gap: 3px;
}

.row__move {
  width: 24px;
  height: 22px;
  padding: 0;
  border: 1px solid rgb(0 0 0 / 8%);
  border-radius: 8px;
  background: rgb(255 255 255 / 78%);
  color: var(--text-muted);
  font-size: 14px;
  cursor: pointer;
}

.row__move:hover:not(:disabled) {
  border-color: var(--accent);
  background: #fff;
  color: var(--text);
}

.row__move:disabled {
  cursor: default;
  opacity: 0.4;
}

.row__fields {
  flex: 1;
  min-width: 0;
}

.row__fields .book-field:first-child {
  margin-top: 0;
}

.row__fields .book-field + .book-field {
  margin-top: 6px;
}

.row__count {
  margin-left: 6px;
  color: var(--text-faint);
  font-size: 14px;
}

.row__remove {
  flex: none;
  align-self: center;
}

.add {
  align-self: flex-start;
}
</style>
