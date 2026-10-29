<script setup lang="ts">
/**
 * 一条掌握路径的右半页：档位、租约、下一步、模块与知识点、证据。
 *
 * 四种状态是四个不同的东西 —— `unknown` / `weak` / `learning` / `mastered`
 * 分别画，不合并成一个灰标签。"下一步"必须同时带理由与证据；证据 id 不在
 * 这次返回的列表里时明说，不造一行占位。
 */

import { computed } from 'vue'

import type { ApiFailure } from '../../../api/client'
import type {
  EvidenceResult,
  LeaseState,
  MasteryInsight,
  MasteryLevel,
  MasteryModule,
  MasteryPath,
  StepKind,
} from '../../../api/mastery'
import FailureState from '../../../components/common/FailureState.vue'
import PartialBanner from '../../../components/common/PartialBanner.vue'
import SkeletonBlock from '../../../components/common/SkeletonBlock.vue'
import { MASTERY } from '../../../copy/mastery'
import { formatDateTime } from '../../../shared/format'

type Status = 'idle' | 'loading' | 'ready' | 'failed'

const props = defineProps<{
  path: MasteryPath | null
  pathStatus: Status
  pathFailure: ApiFailure | null
  insight: MasteryInsight | null
  insightStatus: Status
  insightFailure: ApiFailure | null
  lease: LeaseState | null
  leaseStatus: Status
  leaseFailure: ApiFailure | null
  releasing: boolean
  selectedPointId: string
}>()

const emit = defineEmits<{
  (event: 'retry-path'): void
  (event: 'retry-insight'): void
  (event: 'retry-lease'): void
  (event: 'release'): void
  (event: 'select-point', pointId: string): void
  (event: 'go-chat'): void
  (event: 'go-practice'): void
}>()

const LEVEL_LABELS: Record<MasteryLevel, string> = {
  unknown: MASTERY.levelUnknown,
  weak: MASTERY.levelWeak,
  learning: MASTERY.levelLearning,
  mastered: MASTERY.levelMastered,
}

const STEP_LABELS: Record<StepKind, string> = {
  map: MASTERY.nextMap,
  learn: MASTERY.nextLearn,
  review: MASTERY.nextReview,
  done: MASTERY.nextDone,
}

const RESULT_LABELS: Record<EvidenceResult, string> = {
  correct: MASTERY.resultCorrect,
  incorrect: MASTERY.resultIncorrect,
  partial: MASTERY.resultPartial,
}

/** 四态按这个次序画：从"没接触"到"已掌握"是读者能理解的顺序。 */
const LEVEL_ORDER: MasteryLevel[] = ['unknown', 'weak', 'learning', 'mastered']

interface PointRow {
  id: string
  name: string
  moduleId: string
  description: string
  level: MasteryLevel | null
  observed: boolean
}

const pointRows = computed<PointRow[]>(() => {
  const rows: PointRow[] = []
  for (const module of props.path?.tree.modules ?? []) {
    for (const point of module.knowledge_points ?? []) {
      const observed = props.insight?.points.find(
        (entry) => entry.knowledge_point_id === point.id,
      )
      rows.push({
        id: point.id,
        name: point.name,
        moduleId: module.id,
        description: point.description ?? '',
        level: observed?.level ?? point.mastery ?? null,
        observed: observed !== undefined,
      })
    }
  }
  return rows
})

const modules = computed(() => props.path?.tree.modules ?? [])

const levelCounts = computed(() => {
  const counts: Record<MasteryLevel, number> = {
    unknown: 0,
    weak: 0,
    learning: 0,
    mastered: 0,
  }
  for (const point of props.insight?.points ?? []) {
    counts[point.level] += 1
  }
  return counts
})

const totalPoints = computed(() => pointRows.value.length)

/** 进度按"已掌握的知识点占全部知识点"算 —— 与路径摘要里的 `mastered` 同一口径。 */
const progressPercent = computed(() =>
  totalPoints.value === 0
    ? 0
    : Math.round((levelCounts.value.mastered / totalPoints.value) * 100),
)

type ModuleState = 'done' | 'active' | 'todo'

const MODULE_LABELS: Record<ModuleState, string> = {
  done: MASTERY.moduleDone,
  active: MASTERY.moduleActive,
  todo: MASTERY.moduleTodo,
}

/** 模块的状态由它自己的知识点推出来，不额外存一份。 */
function moduleState(module: MasteryModule): ModuleState {
  const levels = (module.knowledge_points ?? []).map(
    (point) => rowOf(point.id)?.level ?? null,
  )
  if (levels.length === 0) return 'todo'
  if (levels.every((level) => level === 'mastered')) return 'done'
  if (levels.some((level) => level !== null)) return 'active'
  return 'todo'
}

function moduleLabel(module: MasteryModule): string {
  return MODULE_LABELS[moduleState(module)]
}

const selectedPoint = computed(
  () => pointRows.value.find((row) => row.id === props.selectedPointId) ?? null,
)

const selectedInsight = computed(
  () =>
    props.insight?.points.find(
      (point) => point.knowledge_point_id === props.selectedPointId,
    ) ?? null,
)

const selectedEvidence = computed(() =>
  (props.insight?.evidence ?? []).filter(
    (evidence) => evidence.knowledge_point_id === props.selectedPointId,
  ),
)

const next = computed(() => props.insight?.next ?? null)

const nextEvidenceRows = computed(() => {
  const step = next.value
  if (!step) return []
  const all = props.insight?.evidence ?? []
  return (step.evidence_ids ?? []).map((id) => ({
    id,
    evidence: all.find((evidence) => evidence.evidence_id === id) ?? null,
  }))
})

const missingEvidenceCount = computed(
  () => nextEvidenceRows.value.filter((row) => row.evidence === null).length,
)

function rowOf(pointId: string): PointRow | null {
  return pointRows.value.find((row) => row.id === pointId) ?? null
}

function levelKeyOf(pointId: string): string {
  return rowOf(pointId)?.level ?? 'unknown'
}

function levelTextOf(pointId: string): string {
  const row = rowOf(pointId)
  return row ? (row.level ? LEVEL_LABELS[row.level] : MASTERY.levelUnknown) : MASTERY.levelUnknown
}

function evidenceResultLabel(result: EvidenceResult): string {
  return RESULT_LABELS[result] ?? result
}

function detailText(detail: unknown): string {
  if (detail === undefined || detail === null) return ''
  if (typeof detail === 'string') return detail
  try {
    return JSON.stringify(detail, null, 2)
  } catch {
    return String(detail)
  }
}

function selectPoint(pointId: string): void {
  emit('select-point', pointId)
}

function actOnNext(): void {
  if (!next.value) return
  if (next.value.kind === 'review' || next.value.kind === 'done') emit('go-practice')
  else emit('go-chat')
}
</script>

<template>
  <section class="detail">
    <SkeletonBlock v-if="pathStatus === 'loading'" :lines="9" :line-height="42" />

    <FailureState
      v-else-if="pathStatus === 'failed' && pathFailure"
      :failure="pathFailure"
      :title="MASTERY.detailFailed"
      @retry="emit('retry-path')"
    />

    <template v-else-if="path">
      <p class="head__meta">
        {{ MASTERY.status }}：{{ path.status }} · {{ MASTERY.version }} {{ path.version }} ·
        {{ MASTERY.updatedAt }} {{ formatDateTime(path.updated_at) }} ·
        {{ MASTERY.modules(modules.length) }} · {{ MASTERY.points(totalPoints) }}
      </p>

      <!-- 进度卡与四态计数，照演示稿的 `.mastery-progress-card`。 -->
      <section class="progress-card">
        <span class="progress-card__label">{{ MASTERY.progress(progressPercent) }}</span>
        <div
          class="progress-card__bar"
          role="progressbar"
          aria-valuemin="0"
          aria-valuemax="100"
          :aria-valuenow="progressPercent"
        >
          <div class="progress-card__fill" :style="{ width: `${progressPercent}%` }"></div>
        </div>
        <span class="progress-card__count">
          {{ MASTERY.masteredCount(levelCounts.mastered, totalPoints) }}
        </span>

        <!-- 四态分别画 -->
        <div v-if="insight" class="levels">
          <span v-for="level in LEVEL_ORDER" :key="level" class="level" :class="`level--${level}`">
            <span class="level__dot" aria-hidden="true"></span>
            {{ LEVEL_LABELS[level] }} {{ levelCounts[level] }}
          </span>
        </div>
      </section>

      <PartialBanner
        v-if="insightStatus === 'failed' && insightFailure"
        :failure="insightFailure"
        :text="MASTERY.insightFailed"
        @retry="emit('retry-insight')"
      />

      <!-- 租约：只有别的会话真的持着写权时才画。没有就整块不出现 ——
           一张"现在没人改"的卡只会占地方。读失败时仍要露面，免得把
           "读不到"当成"没人改"。 -->
      <section
        v-if="lease?.lease || leaseStatus === 'failed'"
        class="lease"
        :class="{ 'lease--held': Boolean(lease?.lease) }"
      >
        <div class="lease__body">
          <h3 class="lease__title">{{ MASTERY.leaseTitle }}</h3>
          <PartialBanner
            v-if="leaseStatus === 'failed' && leaseFailure"
            :failure="leaseFailure"
            :text="MASTERY.leaseFailed"
            @retry="emit('retry-lease')"
          />
          <template v-else-if="lease?.lease">
            <p class="lease__state">{{ MASTERY.leaseHeld }}</p>
            <p class="lease__holder">
              {{ MASTERY.leaseHolder(lease.lease.session_id) }} ·
              {{ MASTERY.leaseTurn(lease.lease.turn_id) }} ·
              {{ MASTERY.leaseAcquired }} {{ formatDateTime(lease.lease.acquired_at) }}
            </p>
            <p class="lease__explain">{{ MASTERY.leaseExplanation }}</p>
          </template>
        </div>
        <div v-if="lease?.lease" class="lease__actions">
          <button type="button" class="ghost" @click="emit('retry-lease')">
            {{ MASTERY.leaseWait }}
          </button>
          <button type="button" class="danger" :disabled="releasing" @click="emit('release')">
            {{ releasing ? MASTERY.leaseReleasing : MASTERY.leaseRelease }}
          </button>
        </div>
      </section>

      <!-- 下一步 -->
      <section v-if="next" class="next">
        <div class="next__head">
          <span class="next__kind">{{ STEP_LABELS[next.kind] }}</span>
          <h3 class="next__title">{{ MASTERY.nextTitle }}</h3>
        </div>
        <p class="next__reason">{{ next.reason }}</p>

        <div v-if="nextEvidenceRows.length > 0" class="next__evidence">
          <span class="next__evidence-label">
            {{ MASTERY.nextEvidence(nextEvidenceRows.length) }}
          </span>
          <button
            v-for="row in nextEvidenceRows.filter((item) => item.evidence !== null)"
            :key="row.id"
            type="button"
            class="evidence-chip"
            @click="selectPoint(row.evidence?.knowledge_point_id ?? '')"
          >
            {{ row.evidence ? evidenceResultLabel(row.evidence.result) : row.id }}
          </button>
          <span
            v-for="row in nextEvidenceRows.filter((item) => item.evidence === null)"
            :key="`missing-${row.id}`"
            class="evidence-missing"
          >
            {{ MASTERY.nextEvidenceMissing(row.id) }}
          </span>
        </div>
        <p v-else class="next__no-evidence">{{ MASTERY.nextNoEvidence }}</p>
        <p v-if="missingEvidenceCount > 0" class="next__missing">
          {{ MASTERY.nextMissingEvidence(missingEvidenceCount) }}
        </p>

        <div class="next__actions">
          <button type="button" class="primary" @click="actOnNext">
            {{ next.kind === 'review' || next.kind === 'done' ? MASTERY.nextGoPractice : MASTERY.nextGoChat }}
          </button>
          <button
            v-if="next.knowledge_point_id"
            type="button"
            class="ghost"
            @click="selectPoint(next.knowledge_point_id ?? '')"
          >
            看这个知识点
          </button>
        </div>
      </section>

      <!-- 模块。演示稿的时间线：每个模块一个节点，知识点在模块里面。 -->
      <section class="timeline-section">
        <h3 class="section__title">{{ MASTERY.modulesTitle }}</h3>
        <p v-if="modules.length === 0" class="side__empty">{{ MASTERY.moduleEmpty }}</p>
        <ol v-else class="timeline">
          <li
            v-for="module in modules"
            :key="module.id"
            class="module"
            :class="`module--${moduleState(module)}`"
          >
            <span class="m-node" aria-hidden="true"></span>
            <div class="m-info">
              <header class="module__head">
                <h4 class="module__name">{{ module.name }}</h4>
                <span class="m-tag" :class="`m-tag--${moduleState(module)}`">
                  {{ moduleLabel(module) }}
                </span>
              </header>
              <p v-if="module.description" class="module__description">{{ module.description }}</p>
              <ul class="points">
                <li v-for="point in module.knowledge_points ?? []" :key="point.id">
                  <button
                    type="button"
                    class="point"
                    :class="{ 'point--on': point.id === selectedPointId }"
                    @click="selectPoint(point.id)"
                  >
                    <span class="point__name">{{ point.name }}</span>
                    <span class="chip" :class="`chip--${levelKeyOf(point.id)}`">
                      {{ levelTextOf(point.id) }}
                    </span>
                  </button>
                </li>
              </ul>
            </div>
          </li>
        </ol>
      </section>

      <!-- 选中知识点 -->
      <section class="point-detail">
        <template v-if="selectedPoint">
          <h3 class="section__title">{{ selectedPoint.name }}</h3>
          <p v-if="selectedPoint.description" class="point-detail__description">
            {{ selectedPoint.description }}
          </p>

          <div v-if="selectedInsight" class="stats">
            <span>{{ MASTERY.totalObservations(selectedInsight.total) }}</span>
            <span>{{ MASTERY.correctCount(selectedInsight.correct) }}</span>
            <span>{{ MASTERY.incorrectCount(selectedInsight.incorrect) }}</span>
            <span>{{ MASTERY.partialCount(selectedInsight.partial) }}</span>
            <span>
              {{ MASTERY.lastObserved }}：{{ formatDateTime(selectedInsight.last_observed_at) }}
            </span>
            <span>
              {{ MASTERY.dueAt }}：{{ formatDateTime(selectedInsight.due_at) }}
            </span>
          </div>
          <p v-else class="side__empty">{{ MASTERY.noObservation }}</p>

          <h4 class="point-detail__subtitle">
            {{ MASTERY.evidenceOf(selectedPoint.name) }}
          </h4>
          <p v-if="selectedEvidence.length === 0" class="side__empty">
            {{ MASTERY.evidenceEmpty }}
          </p>
          <ul v-else class="evidence">
            <li v-for="evidence in selectedEvidence" :key="evidence.evidence_id" class="evidence-item">
              <header class="evidence-item__head">
                <span class="chip" :class="`result--${evidence.result}`">
                  {{ evidenceResultLabel(evidence.result) }}
                </span>
                <span class="evidence-item__time">
                  {{ formatDateTime(evidence.created_at) }}
                </span>
              </header>
              <p v-if="evidence.source" class="evidence-item__meta">
                {{ MASTERY.evidenceSource }}：{{ evidence.source }}
              </p>
              <p v-if="evidence.assessment_type" class="evidence-item__meta">
                {{ evidence.assessment_type }}
              </p>
              <p v-if="evidence.quality" class="evidence-item__meta">
                {{ MASTERY.evidenceQuality }}：{{ evidence.quality }}
              </p>
              <pre v-if="detailText(evidence.detail)" class="evidence-item__detail">{{ detailText(evidence.detail) }}</pre>
            </li>
          </ul>
        </template>

        <p v-else class="side__empty">{{ MASTERY.pointEmpty }}</p>
      </section>
    </template>
  </section>
</template>

<style scoped src="../Mastery.css"></style>
<style scoped src="./MasteryDetail.css"></style>
