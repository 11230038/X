<script setup lang="ts">
/**
 * 一本书：大纲、章节与阅读。
 *
 * 这一页有两条互斥的路径：
 * - **大纲还没有确认**：页面上只有大纲草稿，没有正文；
 * - **大纲已确认**：左边目录、右边正文，逐章生成。
 *
 * 生成是**同步请求**，一次一章。所以这里的编排只做三件事：把每一章的结果
 * 放进缓存、在章与章之间刷新一次详情、以及把失败留在发生的地方。离开页面
 * 不会取消已经在跑的请求 —— 它仍会落库，回来重新加载就能看到。
 *
 * 阅读进度写的是"最后打开的章"（`ordinal`），不是字符位置：书是模型写的，
 * 章节是它自己的坐标。
 */

import { computed, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'

import type { ApiFailure } from '../../api/client'
import {
  deleteBook,
  exportBook,
  generateBookOutline,
  getBook,
  getBookChapter,
  saveBookOutline,
  saveBookProgress,
  type BookChapter,
  type BookOutlineItem,
} from '../../api/books'
import { describeFailure } from '../../api/errors'
import { canSurface } from '../../auth/permissions'
import EmptyState from '../../components/common/EmptyState.vue'
import FailureState from '../../components/common/FailureState.vue'
import PartialBanner from '../../components/common/PartialBanner.vue'
import SkeletonBlock from '../../components/common/SkeletonBlock.vue'
import SurfaceGate from '../../components/common/SurfaceGate.vue'
import PageHeader from '../../components/shell/PageHeader.vue'
import { BOOK_ERRORS, BOOKS } from '../../copy/books'
import { confirmAction } from '../../shared/confirm'
import { toFailure, useResource } from '../../shared/resource'
import { notify } from '../../shared/toast'

import BookChapterList from './books/BookChapterList.vue'
import BookChapterPane from './books/BookChapterPane.vue'
import BookOutlineEditor from './books/BookOutlineEditor.vue'
import { useBookGeneration } from './books/useBookGeneration'

const props = defineProps<{ bookId: string }>()

const router = useRouter()

const { status, data, failure, stale, load, reset } = useResource(() =>
  getBook(props.bookId),
)

const detail = computed(() => data.value)
const outline = computed<BookOutlineItem[]>(() => data.value?.outline ?? [])
const remaining = computed(() =>
  outline.value.filter((item) => item.status !== 'ready').map((item) => item.ordinal),
)

const description = computed(() => {
  const book = data.value
  if (!book) return ''
  const parts = [
    BOOKS.sourceOf(BOOKS.sourceLabel(book.source_kind), book.source_title),
    BOOKS.sourceChars(book.source_chars),
    BOOKS.sourceSnapshot,
  ]
  if (book.source_truncated) parts.push(BOOKS.sourceTruncated)
  return parts.join(' · ')
})

/* ── 章节缓存与选择 ──────────────────────────────────────────────────── */

const chapters = ref<Record<number, BookChapter>>({})
const selected = ref(0)
const chapterStatus = ref<'idle' | 'loading' | 'ready' | 'failed'>('idle')
const chapterFailure = ref<ApiFailure | null>(null)

const selectedItem = computed<BookOutlineItem | null>(
  () => outline.value.find((item) => item.ordinal === selected.value) ?? null,
)
const selectedChapter = computed<BookChapter | null>(
  () => chapters.value[selected.value] ?? null,
)

function itemAt(ordinal: number): BookOutlineItem | null {
  return outline.value.find((item) => item.ordinal === ordinal) ?? null
}

async function loadChapter(ordinal: number, force = false): Promise<void> {
  if (!force && chapters.value[ordinal]?.status === 'ready') {
    chapterStatus.value = 'ready'
    return
  }
  const forBook = props.bookId
  chapterStatus.value = 'loading'
  chapterFailure.value = null
  try {
    const chapter = await getBookChapter(forBook, ordinal)
    if (props.bookId !== forBook || selected.value !== ordinal) return
    chapters.value = { ...chapters.value, [ordinal]: chapter }
    chapterStatus.value = 'ready'
  } catch (cause) {
    if (props.bookId !== forBook || selected.value !== ordinal) return
    chapterFailure.value = toFailure(cause)
    chapterStatus.value = 'failed'
  }
}

/* ── 阅读进度 ────────────────────────────────────────────────────────── */

const lastSavedOrdinal = ref(0)
const progressFailure = ref<ApiFailure | null>(null)

async function persistProgress(ordinal: number): Promise<void> {
  if (ordinal < 1 || ordinal === lastSavedOrdinal.value) return
  const forBook = props.bookId
  progressFailure.value = null
  try {
    const summary = await saveBookProgress(forBook, ordinal)
    if (props.bookId !== forBook) return
    lastSavedOrdinal.value = ordinal
    if (data.value) data.value = { ...data.value, last_ordinal: summary.last_ordinal }
  } catch (cause) {
    if (props.bookId !== forBook) return
    progressFailure.value = toFailure(cause)
  }
}

function select(ordinal: number): void {
  selected.value = ordinal
  chapterFailure.value = null
  const item = itemAt(ordinal)
  if (item?.status === 'ready') {
    if (chapters.value[ordinal]?.status === 'ready') {
      chapterStatus.value = 'ready'
    } else {
      chapterStatus.value = 'idle'
      void loadChapter(ordinal)
    }
  } else {
    chapterStatus.value = 'idle'
  }
  void persistProgress(ordinal)
}

/** 服务端的 `last_ordinal` 决定第一次落在哪一章。 */
watch(detail, (book) => {
  if (!book || book.outline.length === 0) return
  if (selected.value > 0 && itemAt(selected.value)) return
  const wanted = book.last_ordinal > 0 ? book.last_ordinal : book.outline[0].ordinal
  const ordinal = itemAt(wanted) ? wanted : book.outline[0].ordinal
  selected.value = ordinal
  lastSavedOrdinal.value = book.last_ordinal
  if (itemAt(ordinal)?.status === 'ready') void loadChapter(ordinal)
})

/* ── 大纲 ────────────────────────────────────────────────────────────── */

const outlineBusy = ref(false)
const outlineFailure = ref<ApiFailure | null>(null)

async function generateOutline(): Promise<void> {
  if (outlineBusy.value) return
  outlineBusy.value = true
  outlineFailure.value = null
  try {
    data.value = await generateBookOutline(props.bookId)
    status.value = 'ready'
  } catch (cause) {
    outlineFailure.value = toFailure(cause)
  } finally {
    outlineBusy.value = false
  }
}

async function saveOutline(payload: {
  chapters: Array<{ title: string; summary: string }>
  confirmed: boolean
}): Promise<void> {
  if (outlineBusy.value) return
  outlineBusy.value = true
  outlineFailure.value = null
  try {
    data.value = await saveBookOutline(props.bookId, payload)
    status.value = 'ready'
  } catch (cause) {
    outlineFailure.value = toFailure(cause)
  } finally {
    outlineBusy.value = false
  }
}

/* ── 生成 ────────────────────────────────────────────────────────────── */

const {
  running: generating,
  activeOrdinal,
  failure: generationFailure,
  notice: generationNotice,
  generateOne,
  generateAll,
  stop: stopGeneration,
  reset: resetGeneration,
} = useBookGeneration(() => props.bookId, {
  onChapter: (chapter) => {
    chapters.value = { ...chapters.value, [chapter.ordinal]: chapter }
    selected.value = chapter.ordinal
    chapterStatus.value = 'ready'
    chapterFailure.value = null
    void persistProgress(chapter.ordinal)
  },
  afterEach: () => load(),
})

function generateSelected(): void {
  if (selected.value < 1) return
  void generateOne(selected.value)
}

function generateRemaining(): void {
  if (remaining.value.length === 0) return
  void generateAll(remaining.value)
}

/* ── 导出与删除 ──────────────────────────────────────────────────────── */

const exporting = ref(false)
const exportFailure = ref<ApiFailure | null>(null)

const allReady = computed(
  () => outline.value.length > 0 && remaining.value.length === 0,
)

async function runExport(): Promise<void> {
  if (exporting.value || generating.value) return
  exporting.value = true
  exportFailure.value = null
  try {
    const document = await exportBook(props.bookId)
    const blob = new Blob([document.content], { type: 'text/markdown;charset=utf-8' })
    const url = URL.createObjectURL(blob)
    const anchor = window.document.createElement('a')
    anchor.href = url
    anchor.download = document.filename
    window.document.body.appendChild(anchor)
    anchor.click()
    anchor.remove()
    URL.revokeObjectURL(url)
    notify(BOOKS.exported, 'success')
  } catch (cause) {
    exportFailure.value = toFailure(cause)
  } finally {
    exporting.value = false
  }
}

const deleting = ref(false)
const deleteFailure = ref<ApiFailure | null>(null)

async function remove(): Promise<void> {
  const book = data.value
  if (!book || deleting.value || generating.value) return
  const confirmed = await confirmAction({
    title: BOOKS.deleteTitle(book.title),
    body: BOOKS.deleteBody,
    confirmLabel: BOOKS.deleteConfirm,
    danger: true,
  })
  if (!confirmed) return

  deleting.value = true
  deleteFailure.value = null
  try {
    await deleteBook(book.book_id)
    notify(BOOKS.deleted, 'success')
    void router.replace({ name: 'books' })
  } catch (cause) {
    const failed = toFailure(cause)
    // 404 的意思是它已经不在了；想要的结果已经达成。
    if (failed.status === 404) {
      notify(BOOKS.deleted, 'success')
      void router.replace({ name: 'books' })
    } else {
      deleteFailure.value = failed
    }
  } finally {
    deleting.value = false
  }
}

/* ── 辅助 ────────────────────────────────────────────────────────────── */

function failureText(failed: ApiFailure | null): string {
  if (!failed) return ''
  return BOOK_ERRORS[failed.code] ?? describeFailure(failed)
}

function goShelf(): void {
  void router.push({ name: 'books' })
}

watch(
  () => props.bookId,
  () => {
    reset()
    resetGeneration()
    chapters.value = {}
    selected.value = 0
    chapterStatus.value = 'idle'
    chapterFailure.value = null
    outlineFailure.value = null
    outlineBusy.value = false
    progressFailure.value = null
    deleteFailure.value = null
    exportFailure.value = null
    lastSavedOrdinal.value = 0
    if (canSurface('book') === false) return
    void load()
  },
)

onMounted(() => {
  if (canSurface('book') === false) return
  void load()
})
</script>

<template>
  <SurfaceGate surface="book">
    <PageHeader :title="detail?.title ?? BOOKS.title" :description="description">
      <template #actions>
        <button type="button" class="book-ghost" @click="goShelf">
          {{ BOOKS.backToShelf }}
        </button>
        <button
          type="button"
          class="book-ghost"
          :disabled="!detail || detail.ready_chapters === 0 || exporting || generating"
          :title="detail && detail.ready_chapters === 0 ? BOOKS.exportEmpty : ''"
          @click="runExport"
        >
          {{
            exporting
              ? BOOKS.exporting
              : allReady
                ? BOOKS.exportAll
                : BOOKS.exportChapters
          }}
        </button>
        <button
          type="button"
          class="book-danger"
          :disabled="!detail || deleting || generating"
          @click="remove"
        >
          {{ deleting ? BOOKS.deleting : BOOKS.delete }}
        </button>
      </template>
    </PageHeader>

    <PartialBanner
      v-if="stale && detail"
      :failure="failure"
      :text="BOOKS.detailRefreshFailedKept"
      @retry="load"
    />

    <SkeletonBlock
      v-if="status === 'loading' && !detail"
      :lines="8"
      :line-height="38"
    />

    <FailureState
      v-else-if="status === 'failed' && failure && !detail"
      :failure="failure"
      :title="BOOKS.detailLoadFailed"
      :exit-label="BOOKS.backToShelf"
      @retry="load"
      @exit="goShelf"
    />

    <template v-else-if="detail">
      <p v-if="progressFailure" class="book-inline-warn">
        阅读进度没能保存：{{ failureText(progressFailure) }}
        <button type="button" class="book-link" @click="persistProgress(selected)">
          重试
        </button>
      </p>

      <p v-if="deleteFailure" class="book-inline-error">
        {{ BOOKS.deleteFailed }}：{{ failureText(deleteFailure) }}
      </p>

      <p v-if="exportFailure" class="book-inline-error">
        {{ BOOKS.exportFailed }}：{{ failureText(exportFailure) }}
      </p>

      <!-- 还没有大纲 -->
      <template v-if="outline.length === 0">
        <EmptyState
          :title="BOOKS.outlineEmpty"
          :hint="BOOKS.outlineEmptyHint"
          :action-label="outlineBusy ? BOOKS.generatingOutline : BOOKS.generateOutline"
          @action="generateOutline"
        />
        <p v-if="outlineBusy" class="book-inline-warn">{{ BOOKS.generatingOutline }}</p>
        <p v-if="outlineFailure" class="book-inline-error">
          {{ BOOKS.outlineGenerateFailed }}：{{ failureText(outlineFailure) }}
          <code v-if="outlineFailure.requestId" class="book-error__req">
            {{ outlineFailure.requestId }}
          </code>
        </p>
      </template>

      <!-- 大纲未确认：只有草稿 -->
      <BookOutlineEditor
        v-else-if="!detail.outline_confirmed"
        :outline="outline"
        :busy="outlineBusy"
        :failure="outlineFailure"
        @save="saveOutline"
        @regenerate="generateOutline"
      />

      <!-- 大纲已确认：目录 + 正文 -->
      <template v-else>
        <div class="toolbar">
          <span class="book-meta">
            {{ BOOKS.chapters(detail.ready_chapters, detail.chapter_count) }}
          </span>
          <div class="book-actions">
            <button
              v-if="!generating && remaining.length > 0"
              type="button"
              class="book-primary"
              @click="generateRemaining"
            >
              {{ BOOKS.generateRemaining }}
            </button>
            <button
              v-if="generating"
              type="button"
              class="book-ghost"
              @click="stopGeneration"
            >
              {{ BOOKS.stopGeneration }}
            </button>
          </div>
        </div>

        <p v-if="generationNotice" class="notice">{{ generationNotice }}</p>
        <p v-if="generationFailure?.requestId" class="book-meta">
          <code class="book-error__req">{{ generationFailure.requestId }}</code>
        </p>

        <div class="workspace">
          <BookChapterList
            :outline="outline"
            :selected="selected"
            :busy="generating"
            :active-ordinal="activeOrdinal"
            @select="select"
          />

          <BookChapterPane
            :item="selectedItem"
            :chapter="selectedChapter"
            :status="chapterStatus"
            :load-failure="chapterFailure"
            :generating="activeOrdinal === selected && selected > 0"
            @generate="generateSelected"
            @retry-load="loadChapter(selected, true)"
          />
        </div>
      </template>
    </template>
  </SurfaceGate>
</template>

<style scoped src="./books/books.css"></style>
