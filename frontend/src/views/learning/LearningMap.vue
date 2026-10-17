<script setup lang="ts">
/**
 * 学习地图。
 *
 * 一条掌握路径的总体地图：模块是区域，知识点是四态节点。数据只来自
 * `GET /mastery/paths` 与 `GET /mastery/paths/{id}/insight`，不新增接口。
 *
 * 三条规则：
 *
 * 1. **四态只认 insight。** 树上的 `mastery` 只是缓存，权威值是 insight 按
 *    证据重算出来的 `level`；两者不一致时以后者为准。
 * 2. **不画知识点之间的连线。** 模块分组与顺序是真实数据；依赖与前置关系
 *    没有数据模型，地图不发明它们。
 * 3. **切路径先清旧图。** 每次请求带一个 token，响应回来时 token 与当前
 *    路径都要对得上，旧响应不能覆盖新地图。
 */

import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import type { ApiFailure } from '../../api/client'
import {
  getMasteryInsight,
  listMasteryPaths,
  type MasteryInsight,
  type MasteryLevel,
  type MasterySummary,
} from '../../api/mastery'
import { canSurface } from '../../auth/permissions'
import EmptyState from '../../components/common/EmptyState.vue'
import FailureState from '../../components/common/FailureState.vue'
import SkeletonBlock from '../../components/common/SkeletonBlock.vue'
import SurfaceGate from '../../components/common/SurfaceGate.vue'
import PageHeader from '../../components/shell/PageHeader.vue'
import { LEVEL_LABELS, LEVEL_ORDER, LEARNING_MAP, STEP_LABELS } from '../../copy/learningMap'
import { MASTERY } from '../../copy/mastery'
import { capabilityStatus, hasCapability, refreshCapabilities } from '../../shared/capabilities'
import { formatDateTime } from '../../shared/format'
import { toFailure } from '../../shared/resource'

type Status = 'idle' | 'loading' | 'ready' | 'failed'

interface MapNode {
  id: string
  name: string
  moduleId: string
  description: string
  level: MasteryLevel
  total: number
  correct: number
  incorrect: number
  partial: number
  lastObservedAt: string
  dueAt: string
}

interface MapModule {
  id: string
  name: string
  description: string
  nodes: MapNode[]
}

const route = useRoute()
const router = useRouter()

/* ── 路径列表 ────────────────────────────────────────────────────────── */

const paths = ref<MasterySummary[]>([])
const pathsStatus = ref<Status>('idle')
const pathsFailure = ref<ApiFailure | null>(null)

/* ── 当前路径的地图 ──────────────────────────────────────────────────── */

const currentId = ref('')
const insight = ref<MasteryInsight | null>(null)
const insightStatus = ref<Status>('idle')
const insightFailure = ref<ApiFailure | null>(null)
const selectedPointId = ref('')
let insightToken = 0

function replaceRoute(pathId: string, pointId: string): void {
  const query: Record<string, string> = {}
  if (pathId !== '') query.path = pathId
  if (pointId !== '') query.point = pointId
  void router.replace({ name: 'learning-map', query })
}

function chooseInitialPoint(loaded: MasteryInsight, hint: string): string {
  const known = new Set(loaded.points.map((point) => point.knowledge_point_id))
  if (hint !== '' && known.has(hint)) return hint
  const suggested = loaded.next.knowledge_point_id
  if (suggested !== undefined && suggested !== '' && known.has(suggested)) return suggested
  return loaded.points[0]?.knowledge_point_id ?? ''
}

async function loadInsight(pathId: string, pointHint: string): Promise<void> {
  if (pathId === '') return
  const token = ++insightToken
  insight.value = null
  selectedPointId.value = ''
  insightStatus.value = 'loading'
  insightFailure.value = null

  try {
    const loaded = await getMasteryInsight(pathId)
    if (token !== insightToken || currentId.value !== pathId) return
    insight.value = loaded
    insightStatus.value = 'ready'
    const chosen = chooseInitialPoint(loaded, pointHint)
    selectedPointId.value = chosen
    const urlPoint = typeof route.query.point === 'string' ? route.query.point : ''
    if (route.query.path !== pathId || urlPoint !== chosen) replaceRoute(pathId, chosen)
  } catch (cause) {
    if (token !== insightToken || currentId.value !== pathId) return
    insightFailure.value = toFailure(cause)
    insightStatus.value = 'failed'
  }
}

async function loadPaths(): Promise<void> {
  pathsStatus.value = 'loading'
  pathsFailure.value = null
  try {
    const loaded = await listMasteryPaths()
    paths.value = loaded.paths
    pathsStatus.value = 'ready'
    if (paths.value.length === 0) {
      currentId.value = ''
      insight.value = null
      insightStatus.value = 'idle'
      selectedPointId.value = ''
      if (route.query.path !== undefined || route.query.point !== undefined) replaceRoute('', '')
      return
    }

    const requested = typeof route.query.path === 'string' ? route.query.path : ''
    const chosen = paths.value.some((item) => item.path_id === requested)
      ? requested
      : (paths.value[0]?.path_id ?? '')
    currentId.value = chosen
    const hint = typeof route.query.point === 'string' ? route.query.point : ''
    await loadInsight(chosen, hint)
  } catch (cause) {
    pathsFailure.value = toFailure(cause)
    pathsStatus.value = 'failed'
  }
}

function retryInsight(): void {
  const hint = typeof route.query.point === 'string' ? route.query.point : ''
  void loadInsight(currentId.value, hint)
}

/* ── 选择 ────────────────────────────────────────────────────────────── */

function selectPath(pathId: string): void {
  if (pathId === '' || pathId === currentId.value) return
  currentId.value = pathId
  selectedPointId.value = ''
  insight.value = null
  insightStatus.value = 'loading'
  replaceRoute(pathId, '')
  void loadInsight(pathId, '')
}

function onPathChange(event: Event): void {
  const target = event.target
  if (target instanceof HTMLSelectElement) selectPath(target.value)
}

function selectPoint(pointId: string): void {
  if (pointId === '' || pointId === selectedPointId.value) return
  selectedPointId.value = pointId
  replaceRoute(currentId.value, pointId)
}

/* ── 合并树与 insight ────────────────────────────────────────────────── */

const modules = computed<MapModule[]>(() => {
  const current = insight.value
  if (!current) return []
  const observed = new Map(current.points.map((point) => [point.knowledge_point_id, point] as const))

  return (current.path.tree.modules ?? []).map((module) => ({
    id: module.id,
    name: module.name,
    description: module.description ?? '',
    nodes: (module.knowledge_points ?? []).map((point) => {
      const row = observed.get(point.id)
      return {
        id: point.id,
        name: point.name,
        moduleId: module.id,
        description: point.description ?? '',
        level: row?.level ?? 'unknown',
        total: row?.total ?? 0,
        correct: row?.correct ?? 0,
        incorrect: row?.incorrect ?? 0,
        partial: row?.partial ?? 0,
        lastObservedAt: row?.last_observed_at ?? '',
        dueAt: row?.due_at ?? '',
      }
    }),
  }))
})

const allNodes = computed(() => modules.value.flatMap((module) => module.nodes))

const currentSummary = computed(
  () => paths.value.find((item) => item.path_id === currentId.value) ?? null,
)

const selectedNode = computed(
  () => allNodes.value.find((node) => node.id === selectedPointId.value) ?? null,
)

const selectedModule = computed(() => {
  const id = selectedPointId.value
  return modules.value.find((module) => module.nodes.some((node) => node.id === id)) ?? null
})

const next = computed(() => insight.value?.next ?? null)

/** 只有下一步确实指向当前选中的点时，详情里才再画一张卡片。 */
const nextApplies = computed(
  () => selectedPointId.value !== '' && next.value?.knowledge_point_id === selectedPointId.value,
)

const nextEvidenceCount = computed(() => next.value?.evidence_ids?.length ?? 0)

const nextActionLabel = computed(() => {
  if (!next.value) return ''
  if (next.value.kind === 'review' || next.value.kind === 'done') return MASTERY.nextGoPractice
  if (next.value.kind === 'learn') return MASTERY.nextGoChat
  return ''
})

const counts = computed(() => {
  const result: Record<MasteryLevel, number> = {
    unknown: 0,
    weak: 0,
    learning: 0,
    mastered: 0,
  }
  for (const node of allNodes.value) result[node.level] += 1
  return result
})

const summaryText = computed(() =>
  LEARNING_MAP.summary(
    allNodes.value.length,
    counts.value.mastered,
    counts.value.learning,
    counts.value.weak,
    counts.value.unknown,
  ),
)

/* ── 动作 ────────────────────────────────────────────────────────────── */

function canCreate(): boolean {
  return hasCapability('mastery_path')
}

function goChat(): void {
  void router.push({ name: 'chat' })
}

function goPractice(): void {
  void router.push({ name: 'practice' })
}

function goNext(): void {
  const step = next.value
  if (!step) return
  if (step.kind === 'review' || step.kind === 'done') {
    goPractice()
    return
  }
  if (step.kind === 'learn') {
    goChat()
    return
  }
  void router.push({ name: 'mastery', query: { path: currentId.value } })
}

/** Go 的零值时间会编码成 0001-01-01，不能直接交给日期格式化。 */
function isRealTime(value: string | null | undefined): boolean {
  if (!value) return false
  const date = new Date(value)
  return !Number.isNaN(date.getTime()) && date.getUTCFullYear() > 1
}

/* ── 生命周期 ────────────────────────────────────────────────────────── */

watch(
  () => route.query.path,
  (value) => {
    if (typeof value !== 'string' || value === '' || value === currentId.value) return
    if (!paths.value.some((item) => item.path_id === value)) return
    // 外部链接可能同时带 `?point=`；先把它当提示，加载后再校验，别在这里抹掉。
    const hint = typeof route.query.point === 'string' ? route.query.point : ''
    currentId.value = value
    selectedPointId.value = ''
    insight.value = null
    insightStatus.value = 'loading'
    void loadInsight(value, hint)
  },
)

watch(
  () => route.query.point,
  (value) => {
    if (typeof value !== 'string' || value === '' || value === selectedPointId.value) return
    if (!insight.value?.points.some((point) => point.knowledge_point_id === value)) return
    selectedPointId.value = value
  },
)

onMounted(() => {
  if (canSurface('mastery') === false) return
  if (capabilityStatus.value === 'idle') void refreshCapabilities()
  void loadPaths()
})
</script>

<template>
  <SurfaceGate surface="mastery">
    <PageHeader :title="LEARNING_MAP.title" :description="LEARNING_MAP.description" />

    <FailureState
      v-if="pathsStatus === 'failed' && pathsFailure"
      :failure="pathsFailure"
      :title="MASTERY.pathsFailed"
      @retry="loadPaths"
    />

    <template v-else>
      <SkeletonBlock v-if="pathsStatus !== 'ready'" :lines="5" :line-height="32" />

      <EmptyState
        v-else-if="paths.length === 0"
        :title="MASTERY.pathsEmpty"
        :hint="canCreate() ? MASTERY.goChatHint : MASTERY.capabilityMissing"
        :action-label="MASTERY.goChat"
        @action="goChat"
      />

      <template v-else>
        <section class="toolbar">
          <span class="toolbar__label">{{ MASTERY.paths }}</span>
          <select
            v-if="paths.length > 1"
            class="toolbar__select"
            :value="currentId"
            :aria-label="MASTERY.paths"
            @change="onPathChange"
          >
            <option v-for="item in paths" :key="item.path_id" :value="item.path_id">
              {{ item.emoji }} {{ item.goal }}
            </option>
          </select>
          <p v-else-if="currentSummary" class="toolbar__goal">
            {{ currentSummary.emoji }} {{ currentSummary.goal }}
          </p>
        </section>

        <FailureState
          v-if="insightStatus === 'failed' && insightFailure"
          :failure="insightFailure"
          :title="MASTERY.insightFailed"
          @retry="retryInsight"
        />

        <SkeletonBlock v-else-if="insightStatus !== 'ready'" :lines="8" :line-height="44" />

        <EmptyState
          v-else-if="insight && modules.length === 0"
          :title="MASTERY.moduleEmpty"
        />

        <template v-else-if="insight">
          <section class="summary">
            <p class="summary__counts">{{ summaryText }}</p>
            <p class="summary__next">
              <span class="summary__kind">{{ STEP_LABELS[insight.next.kind] }}</span>
              <span class="summary__reason">{{ insight.next.reason }}</span>
            </p>
          </section>

          <div class="legend" role="group" :aria-label="LEARNING_MAP.legend">
            <span class="legend__label">{{ LEARNING_MAP.legend }}</span>
            <span v-for="level in LEVEL_ORDER" :key="level" class="legend__item">
              <span
                class="legend__swatch"
                :class="`legend__swatch--${level}`"
                aria-hidden="true"
              ></span>
              {{ LEVEL_LABELS[level] }}
            </span>
          </div>

          <div class="board">
            <div class="map">
              <section v-for="module in modules" :key="module.id" class="region">
                <header class="region__head">
                  <h3 class="region__name">{{ module.name }}</h3>
                  <span class="region__count">{{ MASTERY.points(module.nodes.length) }}</span>
                </header>
                <p v-if="module.description" class="region__description">
                  {{ module.description }}
                </p>

                <ul class="nodes">
                  <li v-for="node in module.nodes" :key="node.id">
                    <button
                      type="button"
                      class="node"
                      :class="[`node--${node.level}`, { 'node--on': node.id === selectedPointId }]"
                      :aria-pressed="node.id === selectedPointId"
                      aria-controls="learning-map-detail"
                      @click="selectPoint(node.id)"
                    >
                      <span class="node__name">{{ node.name }}</span>
                      <span class="node__badge">{{ LEVEL_LABELS[node.level] }}</span>
                    </button>
                  </li>
                </ul>
              </section>
            </div>

            <aside id="learning-map-detail" class="detail" aria-live="polite">
              <template v-if="selectedNode">
                <header class="detail__head">
                  <h2 class="detail__title">{{ selectedNode.name }}</h2>
                  <p v-if="selectedModule" class="detail__module">
                    {{ LEARNING_MAP.module }}：{{ selectedModule.name }}
                  </p>
                </header>

                <p v-if="selectedNode.description" class="detail__description">
                  {{ selectedNode.description }}
                </p>

                <p class="detail__level">
                  <span class="detail__level-label">{{ MASTERY.levelLabel }}</span>
                  <span class="detail__badge" :class="`detail__badge--${selectedNode.level}`">
                    {{ LEVEL_LABELS[selectedNode.level] }}
                  </span>
                </p>

                <template v-if="selectedNode.total > 0">
                  <div class="detail__stats">
                    <span>{{ MASTERY.totalObservations(selectedNode.total) }}</span>
                    <span>{{ MASTERY.correctCount(selectedNode.correct) }}</span>
                    <span>{{ MASTERY.incorrectCount(selectedNode.incorrect) }}</span>
                    <span>{{ MASTERY.partialCount(selectedNode.partial) }}</span>
                  </div>
                  <p v-if="isRealTime(selectedNode.lastObservedAt)" class="detail__meta">
                    {{ MASTERY.lastObserved }}：{{ formatDateTime(selectedNode.lastObservedAt) }}
                  </p>
                  <p v-if="isRealTime(selectedNode.dueAt)" class="detail__meta">
                    {{ MASTERY.dueAt }}：{{ formatDateTime(selectedNode.dueAt) }}
                  </p>
                </template>
                <p v-else class="detail__empty">{{ MASTERY.noObservation }}</p>

                <section v-if="nextApplies && next" class="detail__next">
                  <header class="detail__next-head">
                    <span class="detail__next-kind">{{ STEP_LABELS[next.kind] }}</span>
                    <h3 class="detail__next-title">{{ MASTERY.nextTitle }}</h3>
                  </header>
                  <p class="detail__next-reason">{{ next.reason }}</p>
                  <p v-if="nextEvidenceCount > 0" class="detail__next-meta">
                    {{ MASTERY.nextEvidence(nextEvidenceCount) }}
                  </p>
                  <button
                    v-if="nextActionLabel"
                    type="button"
                    class="detail__next-go"
                    @click="goNext"
                  >
                    {{ nextActionLabel }}
                  </button>
                </section>
              </template>

              <p v-else class="detail__empty">{{ LEARNING_MAP.pointEmpty }}</p>
            </aside>
          </div>
        </template>
      </template>
    </template>
  </SurfaceGate>
</template>

<style scoped src="./LearningMap.css"></style>
