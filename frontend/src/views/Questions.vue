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
 *
 * 「掌握路径」是一个下拉筛选：选中一条路径，列表就只请求这条路径出的题。
 * 筛选在服务端做（`mastery_path_id`），不是把已经取回来的那几页过滤一遍 ——
 * 否则翻页会在筛选后漏题。路径名从 `/mastery/paths` 取，只用于画选项。
 */

import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import type { ApiFailure } from '../api/client'
import { describeFailure } from '../api/errors'
import { listMasteryPaths, type MasterySummary } from '../api/mastery'
import {
  getQuestion,
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
import SurfaceGate from '../components/common/SurfaceGate.vue'
import AppShell from '../components/shell/AppShell.vue'
import PageHeader from '../components/shell/PageHeader.vue'
import { QUESTIONS } from '../copy/questions'
import { toFailure } from '../shared/resource'
import { notify } from '../shared/toast'

import QuestionDetail from './questions/QuestionDetail.vue'
import QuestionImportDialog from './questions/QuestionImportDialog.vue'
import { resolvedLabel, resolvedTone, typeLabel } from './questions/labels'
import AppIcon from '../components/common/AppIcon.vue'

const PAGE_SIZE = 30
const router = useRouter()
const route = useRoute()

type LoadStatus = 'idle' | 'loading' | 'ready' | 'failed'
type ResolvedFilter = '' | 'untouched' | 'open' | 'done'

// ── 列表与筛选 ────────────────────────────────────────────────────────────

const searchInput = ref('')
const searchTerm = ref('')
const resolvedFilter = ref<ResolvedFilter>('')
const typeFilter = ref<'' | QuestionType>('')
const categoryFilter = ref('')
const pathFilter = ref('')

const questions = ref<QuestionEntry[]>([])
const status = ref<LoadStatus>('idle')
const failure = ref<ApiFailure | null>(null)
const staleFailure = ref<ApiFailure | null>(null)
const moreFailure = ref<ApiFailure | null>(null)
const loadingMore = ref(false)
const exhausted = ref(false)

const categories = ref<QuestionCategory[]>([])
/** 下拉里的掌握路径。读不到也不影响读题，只是少了这一个筛选项。 */
const paths = ref<MasterySummary[]>([])

const selectedId = ref<string | null>(null)
const busyId = ref('')
const actionError = ref<string | null>(null)

const hasFilters = computed(
  () =>
    searchTerm.value !== '' ||
    resolvedFilter.value !== '' ||
    typeFilter.value !== '' ||
    categoryFilter.value !== '' ||
    pathFilter.value !== '',
)

const selected = computed(
  () => questions.value.find((entry) => entry.question.question_id === selectedId.value) ?? null,
)

/** 下拉里显示的名字。路径名读不到时退回 id —— 选项仍然能选，只是不好认。 */
function pathLabel(path: MasterySummary): string {
  return path.goal || QUESTIONS.pathFallback(path.path_id)
}

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

/** 掌握状态对应的标签底色。灰底（neutral）不给修饰类，就是 `.m-tag` 本身。 */
const RESOLVED_TAG_CLASS: Record<string, string> = {
  success: 'm-tag--success',
  warning: 'm-tag--warning',
}

function resolvedTagClass(entry: QuestionEntry): string {
  return RESOLVED_TAG_CLASS[resolvedTone(entry)] ?? ''
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
      mastery_path_id: pathFilter.value || undefined,
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
      mastery_path_id: pathFilter.value || undefined,
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

/**
 * 列表刷新后决定打开哪一道。
 *
 * 页面是"选择题目 → 点开一道 → 返回列表"，所以刷新**不会**替用户挑一道：
 * 只有正在看的那道从列表里消失了（被删、换了筛选条件）才退回列表。
 */
function reconcileSelection(): void {
  if (selectedId.value === null) return
  const existing = questions.value.find(
    (entry) => entry.question.question_id === selectedId.value,
  )
  if (!existing) selectedId.value = null
}

function clearFilters(): void {
  searchInput.value = ''
  searchTerm.value = ''
  resolvedFilter.value = ''
  typeFilter.value = ''
  categoryFilter.value = ''
  pathFilter.value = ''
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

/**
 * 路径列表只用来画下拉选项。读不到（例如权限快照还没到）时没有这一项，
 * 题目照常按其余筛选条件列出。
 */
async function loadPaths(): Promise<void> {
  try {
    paths.value = (await listMasteryPaths()).paths
  } catch {
    // 同上：选项是辅助信息。
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

/**
 * 就这道题提问：跳到题目问答并直接进对话，第一句由那边用题干拼好发出去。
 * 题库页自己不读题、也不建会话——会话属于 `question_bank` 分区，页面只有一处
 * 该知道怎么开它。
 */
function askAbout(entry: QuestionEntry): void {
  void router.push({
    name: 'question-chat',
    query: { question: entry.question.question_id, send: '1' },
  })
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

/**
 * 地址上带着 `?question=<id>` 时直接打开那一道。收藏夹的题目卡片这样进来。
 *
 * 列表只取第一页，收藏的题可能比第一页更早，所以列表里没有时按 id 单独取
 * 一道，插到最前面 —— 这样"返回列表"仍然回到一个能看见它的列表。取不到
 * （题被删了、没权限）就留在列表，不把整页画成错误。
 */
async function openFromQuery(): Promise<void> {
  const id = route.query.question
  if (typeof id !== 'string' || id === '') return
  if (questions.value.some((entry) => entry.question.question_id === id)) {
    selectedId.value = id
    return
  }
  try {
    const entry = await getQuestion(id)
    questions.value = [entry, ...questions.value]
    selectedId.value = id
  } catch {
    // 打开失败只是没打开，列表照常可用。
  }
}

onMounted(async () => {
  if (canSurface('notebook') === false) {
    status.value = 'ready'
    return
  }
  await Promise.all([loadList(), loadCategories(), loadPaths()])
  await openFromQuery()
})

// 已经在题库页时从收藏夹再点一道，页面不会重建，靠这个 watch 换选中的题。
watch(
  () => route.query.question,
  (id) => {
    if (typeof id === 'string' && id !== '') void openFromQuery()
  },
)
</script>


<template>
  <SurfaceGate surface="notebook">
    <AppShell>
      <div class="page">
        <div class="page__inner">
          <PageHeader :title="QUESTIONS.title" :description="QUESTIONS.description" serif>
            <template #actions>
              <button type="button" class="primary" @click="openImport">
                {{ QUESTIONS.importAction }}
              </button>
            </template>
          </PageHeader>

          <p v-if="actionError" class="action-error">{{ actionError }}</p>

          <!--
            列表态：一页"选择题目"。工具行在顶上，下面一排题目；点一道就换成
            详情。题库页不再占左侧那一栏，所以学习空间的侧边栏一直都在。
          -->
          <template v-if="selectedId === null">
            <div class="feature-pills">
              <span v-for="pill in QUESTIONS.featurePills" :key="pill.label" class="f-pill">
                <span aria-hidden="true"><AppIcon :name="pill.icon" /></span>
                {{ pill.label }}
              </span>
            </div>

            <!-- 搜索占一整行，四个筛选条件排在它下面一行。 -->
            <div class="m-tools">
              <input
                v-model="searchInput"
                class="m-input"
                type="search"
                :placeholder="QUESTIONS.searchPlaceholder"
                :aria-label="QUESTIONS.searchPlaceholder"
              />

              <div class="m-filters">
                <select
                  v-model="resolvedFilter"
                  class="m-select"
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
                  class="m-select"
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
                  class="m-select"
                  :aria-label="QUESTIONS.filterCategory"
                  @change="refreshForFilter"
                >
                  <option value="">{{ QUESTIONS.filterCategory }}：{{ QUESTIONS.all }}</option>
                  <option
                    v-for="category in categories"
                    :key="category.name"
                    :value="category.name"
                  >
                    {{ category.name }}（{{ category.count }}）
                  </option>
                </select>

                <select
                  v-model="pathFilter"
                  class="m-select"
                  :aria-label="QUESTIONS.filterPath"
                  @change="refreshForFilter"
                >
                  <option value="">{{ QUESTIONS.filterPath }}：{{ QUESTIONS.all }}</option>
                  <option v-for="path in paths" :key="path.path_id" :value="path.path_id">
                    {{ pathLabel(path) }}
                  </option>
                </select>
              </div>
            </div>

            <p v-if="staleFailure" class="question-stale">
              {{ QUESTIONS.loadFailed }}
              <button type="button" class="question-retry" @click="loadList">重试</button>
            </p>

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
              <ul class="m-list">
                <li v-for="entry in questions" :key="entry.question.question_id">
                  <button
                    type="button"
                    class="m-card"
                    @click="selectedId = entry.question.question_id"
                  >
                    <span class="m-card__main">
                      <span class="m-card__text">{{ entry.question.text }}</span>
                      <span class="m-card__tags">
                        <span class="m-tag">{{ typeLabel(entry.question.question_type) }}</span>
                        <span class="m-tag" :class="resolvedTagClass(entry)">
                          {{ resolvedLabel(entry) }}
                        </span>
                        <span v-if="entry.answer.bookmarked" class="m-tag m-tag--accent">
                          <AppIcon name="star" /> {{ QUESTIONS.bookmarked }}
                        </span>
                        <span v-for="name in entry.question.categories" :key="name" class="m-tag">
                          {{ name }}
                        </span>
                      </span>
                    </span>
                    <span class="m-card__go" aria-hidden="true">›</span>
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
          </template>

          <!-- 详情态：点开的那一道。返回就回到列表，筛选条件还在。 -->
          <template v-else>
            <button type="button" class="back" @click="selectedId = null">
              ← 返回{{ QUESTIONS.selectTitle }}
            </button>

            <QuestionDetail
              v-if="selected"
              :entry="selected"
              :busy="busyId !== ''"
              @bookmark="toggleBookmark(selected)"
              @resolve="markResolved(selected)"
              @ask="askAbout(selected)"
            />
          </template>
        </div>
      </div>
    </AppShell>

    <QuestionImportDialog ref="importDialog" @imported="onImported" />
  </SurfaceGate>
</template>

<style scoped src="./Questions.css"></style>
