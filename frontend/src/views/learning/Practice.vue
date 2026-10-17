<script setup lang="ts">
/**
 * 练习。复习队列一次一题，提交后立刻看到判分。
 *
 * 三条守着后端语义的规则：
 *
 * 1. **判分有两种性质。** `objective` 是"判定结果"，`model_judgment` 是
 *    **"AI 的判断"** —— 后者要带置信度、评分标准、反馈、建议与边界。把模型
 *    判断画成客观对错，是在制造一个不存在的权威。
 * 2. **判分失败不是判分结果。** `judge_unavailable`(503) 与
 *    `question_not_judgeable`(422) 都保留作答，前者可以原样重试（同一个
 *    `request_id`），后者不能判、可以跳过。
 * 3. **`request_id` 是一份作答的身份。** 失败重试复用它；`stale_review` 带回
 *    最新 `state`，要显式确认后才用新版本重新提交；`conflicting_request_id`
 *    说明这个编号被别的内容用过，换一个新的再交。
 */

import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'

import type { ApiFailure } from '../../api/client'
import { describeFailure } from '../../api/errors'
import {
  getPracticeQueue,
  submitReview,
  type Judgment,
  type Marker,
  type QueueItem,
  type ReviewResult,
  type ReviewState,
} from '../../api/practice'
import type { QuestionType } from '../../api/questions'
import { canSurface } from '../../auth/permissions'
import EmptyState from '../../components/common/EmptyState.vue'
import FailureState from '../../components/common/FailureState.vue'
import QuotaState from '../../components/common/QuotaState.vue'
import SkeletonBlock from '../../components/common/SkeletonBlock.vue'
import SurfaceGate from '../../components/common/SurfaceGate.vue'
import PageHeader from '../../components/shell/PageHeader.vue'
import { PRACTICE } from '../../copy/practice'
import { QUESTIONS } from '../../copy/questions'
import { formatDateTime, formatPercent } from '../../shared/format'
import { toFailure } from '../../shared/resource'

import PracticeStats from './practice/PracticeStats.vue'

const router = useRouter()

type LoadStatus = 'idle' | 'loading' | 'ready' | 'failed'
type Phase =
  | 'answering'
  | 'submitting'
  | 'judged'
  | 'skipped'
  | 'failed'
  | 'not_judgeable'
  | 'stale'
  | 'conflict'
  | 'quota'

const TYPE_LABELS: Record<QuestionType, string> = {
  choice: QUESTIONS.typeChoice,
  fill_blank: QUESTIONS.typeFillBlank,
  short_answer: QUESTIONS.typeShortAnswer,
}

// ── 队列 ──────────────────────────────────────────────────────────────────

const queue = ref<QueueItem[]>([])
const queueStatus = ref<LoadStatus>('idle')
const queueFailure = ref<ApiFailure | null>(null)
const index = ref(0)
const finished = ref(false)

const current = computed<QueueItem | null>(() => queue.value[index.value] ?? null)

const phase = ref<Phase>('answering')
const answerText = ref('')
const choice = ref('')
const requestId = ref('')
const stateVersion = ref<number | null>(null)
const result = ref<ReviewResult | null>(null)
const submitFailure = ref<ApiFailure | null>(null)
const staleState = ref<ReviewState | null>(null)
/** 模型判断失败时那句话是"可以重试"还是"换个动作"靠它分。 */
const failureCode = computed(() => submitFailure.value?.code ?? '')

function newRequestId(): string {
  if (typeof crypto.randomUUID === 'function') return crypto.randomUUID()
  return `r-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`
}

function prepare(): void {
  phase.value = 'answering'
  answerText.value = ''
  choice.value = ''
  requestId.value = newRequestId()
  stateVersion.value = current.value?.state?.version ?? null
  result.value = null
  submitFailure.value = null
  staleState.value = null
}

async function loadQueue(): Promise<void> {
  queueStatus.value = 'loading'
  queueFailure.value = null
  finished.value = false
  try {
    const loaded = await getPracticeQueue()
    queue.value = loaded.queue
    index.value = 0
    queueStatus.value = 'ready'
    if (queue.value.length === 0) finished.value = false
    else prepare()
  } catch (cause) {
    queueFailure.value = toFailure(cause)
    queueStatus.value = 'failed'
  }
}

function next(): void {
  if (index.value + 1 >= queue.value.length) {
    finished.value = true
    return
  }
  index.value += 1
  prepare()
}

// ── 提交 ──────────────────────────────────────────────────────────────────

const answerValue = computed(() => {
  const item = current.value
  if (!item) return ''
  return item.question.question_type === 'choice' ? choice.value : answerText.value.trim()
})

const canSubmit = computed(
  () => current.value !== null && answerValue.value !== '' && phase.value !== 'submitting',
)

async function submit(marker: Marker = ''): Promise<void> {
  const item = current.value
  if (!item || phase.value === 'submitting') return
  if (marker === '' && answerValue.value === '') return

  phase.value = 'submitting'
  submitFailure.value = null
  try {
    const submitted = await submitReview({
      request_id: requestId.value,
      question_id: item.question.question_id,
      user_answer: marker === 'skipped' ? '' : answerValue.value,
      marker,
      rating: null,
      version: stateVersion.value,
    })
    result.value = submitted
    if (submitted.judgment === null) {
      phase.value = 'skipped'
    } else {
      phase.value = 'judged'
    }
  } catch (cause) {
    const failure = toFailure(cause)
    submitFailure.value = failure
    if (failure.kind === 'quota') {
      phase.value = 'quota'
      return
    }
    switch (failure.code) {
      case 'stale_review': {
        const latest = stateFromDetails(failure.details)
        staleState.value = latest
        phase.value = 'stale'
        return
      }
      case 'conflicting_request_id':
        // 这个编号已经被另一份内容占用，原样重试必然再失败。
        requestId.value = newRequestId()
        phase.value = 'conflict'
        return
      case 'question_not_judgeable':
        phase.value = 'not_judgeable'
        return
      default:
        phase.value = 'failed'
        return
    }
  }
}

/** `stale_review` 的 details 里带着最新状态；没有就只给一句"重新读一次"。 */
function stateFromDetails(details: unknown): ReviewState | null {
  if (!details || typeof details !== 'object') return null
  const state = (details as { state?: unknown }).state
  if (!state || typeof state !== 'object') return null
  const value = state as Partial<ReviewState>
  if (typeof value.version !== 'number') return null
  return {
    due_time: typeof value.due_time === 'string' ? value.due_time : '',
    streak: typeof value.streak === 'number' ? value.streak : 0,
    lapses: typeof value.lapses === 'number' ? value.lapses : 0,
    review_count: typeof value.review_count === 'number' ? value.review_count : 0,
    ease: typeof value.ease === 'number' ? value.ease : 0,
    is_mistake: value.is_mistake === true,
    version: value.version,
  }
}

/**
 * 版本冲突之后不自动重发：拿回最新版本，让用户看过那句话再决定。确认时用
 * **新的 `request_id`**，因为这一次与上一次不是同一份意图。
 */
function adoptLatest(): void {
  const item = current.value
  if (!item) return
  if (staleState.value) {
    item.state = staleState.value
    stateVersion.value = staleState.value.version
  }
  requestId.value = newRequestId()
  phase.value = 'answering'
  void submit()
}

function retryFailed(): void {
  // 同一份作答、同一个 request_id：服务端只记一次。
  void submit()
}

function skip(): void {
  void submit('skipped')
}

// ── 展示 ──────────────────────────────────────────────────────────────────

function typeLabel(type: string): string {
  return TYPE_LABELS[type as QuestionType] ?? type
}

function reasonLabel(item: QueueItem): string {
  return item.reason === 'new' ? PRACTICE.reasonNew : PRACTICE.reasonDue
}

function confidenceText(judgment: Judgment): string {
  return judgment.confidence === null ? '' : formatPercent(judgment.confidence, 0)
}

const quotaResetAt = computed(() => {
  const details = submitFailure.value?.details
  if (!details || typeof details !== 'object') return ''
  const value = (details as { resets_at?: unknown }).resets_at
  return typeof value === 'string' ? value : ''
})

onMounted(() => {
  if (canSurface('practice') === false) return
  void loadQueue()
})
</script>

<template>
  <SurfaceGate surface="practice">
    <PageHeader :title="PRACTICE.title" :description="PRACTICE.description" />

    <!-- 队列本身 -->
    <SkeletonBlock v-if="queueStatus === 'loading'" :lines="8" :line-height="42" />

    <FailureState
      v-else-if="queueStatus === 'failed' && queueFailure"
      :failure="queueFailure"
      :title="'复习队列没有读出来'"
      @retry="loadQueue"
    />

    <EmptyState
      v-else-if="queue.length === 0"
      :title="PRACTICE.emptyQueue"
      :hint="PRACTICE.emptyQueueHint"
      :action-label="PRACTICE.goQuestions"
      secondary-label="回到学习首页"
      @action="router.push({ name: 'questions' })"
      @secondary="router.push({ name: 'learning' })"
    />

    <EmptyState
      v-else-if="finished"
      title="这一轮做完了"
      hint="可以重新抽一轮队列，或者回学习首页看看下一步。"
      action-label="再看一次队列"
      secondary-label="回到学习首页"
      @action="loadQueue"
      @secondary="router.push({ name: 'learning' })"
    />

    <section v-else-if="current" class="card">
      <header class="card__head">
        <div class="card__badges">
          <span class="chip">{{ typeLabel(current.question.question_type) }}</span>
          <span class="chip">{{ reasonLabel(current) }}</span>
          <span v-if="current.is_mistake" class="chip chip--danger">{{ PRACTICE.mistake }}</span>
        </div>
        <span class="card__progress">{{ PRACTICE.queueProgress(index + 1, queue.length) }}</span>
      </header>

      <p class="card__question">{{ current.question.text }}</p>

      <p v-if="current.answer.attempt_count > 0" class="card__prior">
        {{ QUESTIONS.attempts(current.answer.attempt_count) }}
        <span v-if="current.answer.updated_at">
          · {{ QUESTIONS.lastAnswered }}：{{ formatDateTime(current.answer.updated_at) }}
        </span>
        <span v-if="current.state?.version !== undefined" class="card__version">
          · 状态版本 {{ current.state?.version }}
        </span>
      </p>

      <!-- 作答区。判分之后只读，但内容留着。 -->
      <fieldset
        v-if="current.question.question_type === 'choice'"
        class="choices"
        :disabled="phase !== 'answering'"
      >
        <legend class="visually-hidden">{{ PRACTICE.choiceHint }}</legend>
        <label
          v-for="option in current.question.options"
          :key="option.key"
          class="choice"
          :class="{ 'choice--on': choice === option.key }"
        >
          <input v-model="choice" type="radio" :value="option.key" />
          <span class="choice__key">{{ option.key }}</span>
          <span class="choice__text">{{ option.text }}</span>
        </label>
      </fieldset>

      <textarea
        v-else
        v-model="answerText"
        class="answer"
        rows="4"
        :placeholder="PRACTICE.answerPlaceholder"
        :disabled="phase !== 'answering'"
        :aria-label="PRACTICE.answerPlaceholder"
      ></textarea>

      <div class="actions">
        <button
          type="button"
          class="primary"
          :disabled="!canSubmit"
          @click="submit()"
        >
          {{ phase === 'submitting' ? PRACTICE.submitting : PRACTICE.submit }}
        </button>
        <button
          type="button"
          class="ghost"
          :disabled="phase === 'submitting'"
          @click="skip"
        >
          {{ phase === 'submitting' ? PRACTICE.skipping : PRACTICE.skip }}
        </button>
      </div>

      <!-- 判分结果 -->
      <section v-if="phase === 'judged' && result?.judgment" class="judgment">
        <header class="judgment__head">
          <h2 class="judgment__title">
            {{ result.judgment.kind === 'objective' ? PRACTICE.objectiveTitle : PRACTICE.modelTitle }}
          </h2>
          <span
            class="judgment__verdict"
            :class="{ 'judgment__verdict--ok': result.judgment.is_correct }"
          >
            {{ result.judgment.is_correct ? PRACTICE.correct : PRACTICE.incorrect }}
          </span>
          <span v-if="result.judgment.kind === 'model_judgment'" class="judgment__kind">
            {{ PRACTICE.modelTitle }}
          </span>
        </header>

        <p v-if="result.replayed" class="judgment__replayed">{{ PRACTICE.replayed }}</p>

        <p v-if="result.judgment.reason" class="judgment__reason">{{ result.judgment.reason }}</p>

        <dl class="judgment__meta">
          <template v-if="confidenceText(result.judgment)">
            <dt>{{ PRACTICE.confidence }}</dt>
            <dd>{{ confidenceText(result.judgment) }}</dd>
          </template>
          <template v-if="result.judgment.model">
            <dt>{{ PRACTICE.model }}</dt>
            <dd>{{ result.judgment.model }}</dd>
          </template>
        </dl>

        <section v-if="result.judgment.rubric.length > 0" class="judgment__block">
          <h3 class="judgment__label">{{ PRACTICE.rubric }}</h3>
          <ul class="judgment__list">
            <li v-for="(line, i) in result.judgment.rubric" :key="i">{{ line }}</li>
          </ul>
        </section>

        <section v-if="result.judgment.feedback" class="judgment__block">
          <h3 class="judgment__label">{{ PRACTICE.feedback }}</h3>
          <p class="judgment__text">{{ result.judgment.feedback }}</p>
        </section>

        <section v-if="result.judgment.suggestion" class="judgment__block">
          <h3 class="judgment__label">{{ PRACTICE.suggestion }}</h3>
          <p class="judgment__text">{{ result.judgment.suggestion }}</p>
        </section>

        <section v-if="result.judgment.boundary" class="judgment__block">
          <h3 class="judgment__label">{{ PRACTICE.boundary }}</h3>
          <p class="judgment__text">{{ result.judgment.boundary }}</p>
        </section>

        <button type="button" class="primary" @click="next">{{ PRACTICE.next }}</button>
      </section>

      <!-- 跳过 -->
      <section v-else-if="phase === 'skipped'" class="judgment">
        <h2 class="judgment__title">{{ PRACTICE.skippedTitle }}</h2>
        <p class="judgment__text">{{ PRACTICE.skippedHint }}</p>
        <button type="button" class="primary" @click="next">{{ PRACTICE.next }}</button>
      </section>

      <!-- 判分服务不可用：作答与 request_id 都还在。 -->
      <section v-else-if="phase === 'failed'" class="judgment judgment--failed">
        <h2 class="judgment__title">{{ PRACTICE.failed }}</h2>
        <p class="judgment__text">
          {{ failureCode === 'judge_unavailable' ? PRACTICE.judgeUnavailable : submitFailure ? describeFailure(submitFailure) : PRACTICE.failed }}
        </p>
        <p v-if="submitFailure?.requestId" class="judgment__request">
          请求编号：{{ submitFailure.requestId }}
        </p>
        <button type="button" class="primary" @click="retryFailed">{{ PRACTICE.retry }}</button>
      </section>

      <section v-else-if="phase === 'not_judgeable'" class="judgment judgment--failed">
        <h2 class="judgment__title">{{ PRACTICE.notJudgeable }}</h2>
        <p class="judgment__text">{{ submitFailure?.message }}</p>
        <button type="button" class="primary" @click="skip">{{ PRACTICE.skip }}</button>
      </section>

      <section v-else-if="phase === 'stale'" class="judgment judgment--warning">
        <h2 class="judgment__title">{{ PRACTICE.staleReview }}</h2>
        <button type="button" class="primary" @click="adoptLatest">
          {{ PRACTICE.adoptLatest }}
        </button>
      </section>

      <section v-else-if="phase === 'conflict'" class="judgment judgment--warning">
        <h2 class="judgment__title">{{ PRACTICE.conflictingRequest }}</h2>
        <button type="button" class="primary" @click="retryFailed">{{ PRACTICE.submit }}</button>
      </section>

      <QuotaState
        v-else-if="phase === 'quota'"
        :reset-at="quotaResetAt"
        @back="router.push({ name: 'learning' })"
      />
    </section>

    <!-- 统计。它自己的失败只影响这一段。 -->
    <PracticeStats />
  </SurfaceGate>
</template>

<style scoped src="./Practice.css"></style>
