<script setup lang="ts">
/**
 * 题库。
 *
 * 三个容易画错的地方：
 * 1. **掌握状态是三选一**（未处理过 / 未掌握 / 已掌握），不是一个开关。后端
 *    的 `resolved` 是 `null | false | true`，三态在接口上就是分开的。
 * 2. **导入分两步**：同一段原文先 `dry_run` 预览逐行结果，确认之后才写入。
 *    预览之后改了文本，旧预览立即作废 —— 否则确认写入的是另一段原文。
 * 3. **解析随题目一起给出**：同一个 `questionView` 在列表、详情、复习队列里
 *    是同一张脸，这里不另请求一次详情。
 *
 * 「来源已删除」这个状态后端没有字段（`实现规划` 记为缺口），这里不伪造，
 * 只把后端给的 `source` 原样显示。
 */

import { computed, onMounted, ref, watch } from 'vue'

import type { ApiFailure } from '../api/client'
import { describeFailure } from '../api/errors'
import {
  listQuestionCategories,
  listQuestions,
  patchQuestion,
  type QuestionCategory,
  type QuestionEntry,
  type QuestionType,
} from '../api/questions'
import { canSurface } from '../auth/permissions'
import EmptyState from '../components/common/EmptyState.vue'
import FailureState from '../components/common/FailureState.vue'
import LoadMore from '../components/common/LoadMore.vue'
import SkeletonBlock from '../components/common/SkeletonBlock.vue'
import StatusBadge from '../components/common/StatusBadge.vue'
import SurfaceGate from '../components/common/SurfaceGate.vue'
import AppShell from '../components/shell/AppShell.vue'
import PageHeader from '../components/shell/PageHeader.vue'
import { QUESTIONS } from '../copy/questions'
import { toFailure } from '../shared/resource'
import { notify } from '../shared/toast'

import QuestionDetail from './questions/QuestionDetail.vue'
import QuestionImportDialog from './questions/QuestionImportDialog.vue'
import { resolvedLabel, resolvedTone, typeLabel } from './questions/labels'

const PAGE_SIZE = 30

type LoadStatus = 'idle' | 'loading' | 'ready' | 'failed'
type ResolvedFilter = '' | 'untouched' | 'open' | 'done'

// ── 列表与筛选 ────────────────────────────────────────────────────────────

const searchInput = ref('')
const searchTerm = ref('')
const resolvedFilter = ref<ResolvedFilter>('')
const typeFilter = ref<'' | QuestionType>('')
const categoryFilter = ref('')

const questions = ref<QuestionEntry[]>([])
const status = ref<LoadStatus>('idle')
const failure = ref<ApiFailure | null>(null)
const staleFailure = ref<ApiFailure | null>(null)
const moreFailure = ref<ApiFailure | null>(null)
const loadingMore = ref(false)
const exhausted = ref(false)

const categories = ref<QuestionCategory[]>([])

const selectedId = ref<string | null>(null)
const busyId = ref('')
const actionError = ref<string | null>(null)

const hasFilters = computed(
  () =>
    searchTerm.value !== '' ||
    resolvedFilter.value !== '' ||
    typeFilter.value !== '' ||
    categoryFilter.value !== '',
)

const selected = computed(
  () => questions.value.find((entry) => entry.question.question_id === selectedId.value) ?? null,
)

let searchTimer: number | null = null

watch(searchInput, (value) => {
  if (searchTimer !== null) window.clearTimeout(searchTimer)
  searchTimer = window.setTimeout(() => {
    searchTerm.value = value.trim()
    void loadList()
  }, 300)
})

function describe(cause: unknown): string {
  return describeFailure(toFailure(cause))
}

async function loadList(): Promise<void> {
  const reset = questions.value.length === 0
  if (reset) {
    status.value = 'loading'
    failure.value = null
  }
  staleFailure.value = null
  moreFailure.value = null

  try {
    const result = await listQuestions({
      q: searchTerm.value || undefined,
      resolved: resolvedFilter.value || undefined,
      question_type: typeFilter.value || undefined,
      category: categoryFilter.value || undefined,
      limit: PAGE_SIZE,
      offset: 0,
    })
    questions.value = result.questions
    exhausted.value = result.questions.length < PAGE_SIZE
    status.value = 'ready'
    failure.value = null
    reconcileSelection()
  } catch (cause) {
    const next = toFailure(cause)
    if (questions.value.length === 0) {
      failure.value = next
      status.value = 'failed'
    } else {
      staleFailure.value = next
      status.value = 'ready'
    }
  }
}

async function loadMore(): Promise<void> {
  if (loadingMore.value || exhausted.value) return
  loadingMore.value = true
  moreFailure.value = null
  try {
    const result = await listQuestions({
      q: searchTerm.value || undefined,
      resolved: resolvedFilter.value || undefined,
      question_type: typeFilter.value || undefined,
      category: categoryFilter.value || undefined,
      limit: PAGE_SIZE,
      offset: questions.value.length,
    })
    questions.value = [...questions.value, ...result.questions]
    exhausted.value = result.questions.length < PAGE_SIZE
  } catch (cause) {
    moreFailure.value = toFailure(cause)
  } finally {
    loadingMore.value = false
  }
}

function reconcileSelection(): void {
  const existing = questions.value.find(
    (entry) => entry.question.question_id === selectedId.value,
  )
  if (existing) return
  selectedId.value = questions.value[0]?.question.question_id ?? null
}

function clearFilters(): void {
  searchInput.value = ''
  searchTerm.value = ''
  resolvedFilter.value = ''
  typeFilter.value = ''
  categoryFilter.value = ''
  void loadList()
}

function refreshForFilter(): void {
  selectedId.value = null
  void loadList()
}

async function loadCategories(): Promise<void> {
  try {
    categories.value = (await listQuestionCategories()).categories
  } catch {
    // 分类筛选是辅助，不因为它失败就把整个题库画成错误。
  }
}

// ── 单题标记 ──────────────────────────────────────────────────────────────

function replaceEntry(updated: QuestionEntry): void {
  questions.value = questions.value.map((entry) =>
    entry.question.question_id === updated.question.question_id ? updated : entry,
  )
}

async function toggleBookmark(entry: QuestionEntry): Promise<void> {
  if (busyId.value !== '') return
  busyId.value = entry.question.question_id
  actionError.value = null
  try {
    replaceEntry(
      await patchQuestion(entry.question.question_id, {
        bookmarked: !entry.answer.bookmarked,
      }),
    )
  } catch (cause) {
    actionError.value = describe(cause)
  } finally {
    busyId.value = ''
  }
}

/**
 * 标记是两态覆盖，没有"回到未处理过"的接口 —— 所以这里只提供"标为已掌握"与
 * "标为未掌握"两个方向，不画一个做不到的第三态按钮。
 */
async function markResolved(entry: QuestionEntry): Promise<void> {
  if (busyId.value !== '') return
  const next = entry.answer.resolved !== true
  busyId.value = entry.question.question_id
  actionError.value = null
  try {
    replaceEntry(
      await patchQuestion(entry.question.question_id, { resolved: next }),
    )
    notify(next ? QUESTIONS.resolvedMastered : QUESTIONS.resolvedOpen, 'success')
  } catch (cause) {
    actionError.value = describe(cause)
  } finally {
    busyId.value = ''
  }
}

// ── 导入 ──────────────────────────────────────────────────────────────────

const importDialog = ref<InstanceType<typeof QuestionImportDialog> | null>(null)

function openImport(): void {
  void importDialog.value?.open()
}

function onImported(): void {
  void loadList()
  void loadCategories()
}

// ── 生命周期 ──────────────────────────────────────────────────────────────

onMounted(() => {
  if (canSurface('notebook') === false) {
    status.value = 'ready'
    return
  }
  void Promise.all([loadList(), loadCategories()])
})
</script>


<template>
  <SurfaceGate surface="notebook">
    <AppShell>
      <template #list>
        <aside class="bank">
          <div class="bank__head">
            <input
              v-model="searchInput"
              class="bank__search"
              type="search"
              :placeholder="QUESTIONS.searchPlaceholder"
              :aria-label="QUESTIONS.searchPlaceholder"
            />
          </div>

          <div class="bank__filters">
            <select
              v-model="resolvedFilter"
              class="bank__select"
              :aria-label="QUESTIONS.filterResolved"
              @change="refreshForFilter"
            >
              <option value="">{{ QUESTIONS.filterResolved }}：{{ QUESTIONS.all }}</option>
              <option value="untouched">{{ QUESTIONS.resolvedUntouched }}</option>
              <option value="open">{{ QUESTIONS.resolvedOpen }}</option>
              <option value="done">{{ QUESTIONS.resolvedDone }}</option>
            </select>

            <select
              v-model="typeFilter"
              class="bank__select"
              :aria-label="QUESTIONS.filterType"
              @change="refreshForFilter"
            >
              <option value="">{{ QUESTIONS.filterType }}：{{ QUESTIONS.all }}</option>
              <option value="choice">{{ QUESTIONS.typeChoice }}</option>
              <option value="fill_blank">{{ QUESTIONS.typeFillBlank }}</option>
              <option value="short_answer">{{ QUESTIONS.typeShortAnswer }}</option>
            </select>

            <select
              v-model="categoryFilter"
              class="bank__select"
              :aria-label="QUESTIONS.filterCategory"
              @change="refreshForFilter"
            >
              <option value="">{{ QUESTIONS.filterCategory }}：{{ QUESTIONS.all }}</option>
              <option v-for="category in categories" :key="category.name" :value="category.name">
                {{ category.name }}（{{ category.count }}）
              </option>
            </select>
          </div>

          <div class="bank__body">
            <SkeletonBlock v-if="status === 'loading'" :lines="8" :line-height="38" />

            <FailureState
              v-else-if="status === 'failed' && failure"
              :failure="failure"
              :title="QUESTIONS.loadFailed"
              @retry="loadList"
            />

            <EmptyState
              v-else-if="questions.length === 0 && hasFilters"
              :title="QUESTIONS.emptyFiltered"
              :hint="QUESTIONS.emptyFilteredHint"
              :action-label="QUESTIONS.clearFilters"
              @action="clearFilters"
            />

            <EmptyState
              v-else-if="questions.length === 0"
              :title="QUESTIONS.empty"
              :hint="QUESTIONS.emptyHint"
              :action-label="QUESTIONS.importAction"
              @action="openImport"
            />

            <template v-else>
              <p v-if="staleFailure" class="bank__stale">
                {{ QUESTIONS.loadFailed }}
                <button type="button" class="bank__retry" @click="loadList">重试</button>
              </p>

              <ul class="bank__list">
                <li v-for="entry in questions" :key="entry.question.question_id">
                  <button
                    type="button"
                    class="qrow"
                    :class="{ 'qrow--active': entry.question.question_id === selectedId }"
                    @click="selectedId = entry.question.question_id"
                  >
                    <span class="qrow__text">{{ entry.question.text }}</span>
                    <span class="qrow__meta">
                      <StatusBadge :label="typeLabel(entry.question.question_type)" small />
                      <StatusBadge
                        :label="resolvedLabel(entry)"
                        :tone="resolvedTone(entry)"
                        small
                      />
                      <span v-if="entry.answer.bookmarked" class="qrow__star" aria-hidden="true">★</span>
                    </span>
                  </button>
                </li>
              </ul>

              <LoadMore
                :loading="loadingMore"
                :exhausted="exhausted"
                :failure="moreFailure"
                @load="loadMore"
              />
            </template>
          </div>
        </aside>
      </template>

      <div class="page">
        <div class="page__inner">
          <PageHeader :title="QUESTIONS.title" :description="QUESTIONS.description">
            <template #actions>
              <button type="button" class="primary" @click="openImport">
                {{ QUESTIONS.importAction }}
              </button>
            </template>
          </PageHeader>

          <p v-if="actionError" class="action-error">{{ actionError }}</p>

          <QuestionDetail
            v-if="selected"
            :entry="selected"
            :busy="busyId !== ''"
            @bookmark="toggleBookmark(selected)"
            @resolve="markResolved(selected)"
          />

          <EmptyState
            v-else
            :title="hasFilters ? QUESTIONS.emptyFiltered : QUESTIONS.selectQuestion"
            :hint="hasFilters ? QUESTIONS.emptyFilteredHint : QUESTIONS.selectQuestionHint"
            :action-label="hasFilters ? QUESTIONS.clearFilters : ''"
            @action="clearFilters"
          />
        </div>
      </div>
    </AppShell>

    <QuestionImportDialog ref="importDialog" @imported="onImported" />
  </SurfaceGate>
</template>

<style scoped src="./Questions.css"></style>
