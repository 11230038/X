<script setup lang="ts">
/**
 * 掌握路径。
 *
 * 左栏是路径列表，右栏交给 `MasteryDetail`。租约是"另一个会话正在写"的提
 * 示：可以等待，也可以让它停下，但**停下不等于拿到**。拿到写权只能靠一次
 * `mastery_path` 对话，页面不假装有这个按钮。
 */

import { onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import type { ApiFailure } from '../../api/client'
import {
  getMasteryInsight,
  getMasteryLease,
  getMasteryPath,
  listMasteryPaths,
  releaseMasteryLease,
  type LeaseState,
  type MasteryInsight,
  type MasteryPath,
  type MasterySummary,
} from '../../api/mastery'
import { canSurface } from '../../auth/permissions'
import EmptyState from '../../components/common/EmptyState.vue'
import FailureState from '../../components/common/FailureState.vue'
import SkeletonBlock from '../../components/common/SkeletonBlock.vue'
import SurfaceGate from '../../components/common/SurfaceGate.vue'
import PageHeader from '../../components/shell/PageHeader.vue'
import { MASTERY } from '../../copy/mastery'
import { confirmAction } from '../../shared/confirm'
import { capabilityStatus, hasCapability, refreshCapabilities } from '../../shared/capabilities'
import { toFailure } from '../../shared/resource'
import { notify } from '../../shared/toast'

import MasteryDetail from './mastery/MasteryDetail.vue'

const route = useRoute()
const router = useRouter()

type Status = 'idle' | 'loading' | 'ready' | 'failed'

/* ── 路径列表 ────────────────────────────────────────────────────────── */

const paths = ref<MasterySummary[]>([])
const pathsStatus = ref<Status>('idle')
const pathsFailure = ref<ApiFailure | null>(null)
const currentId = ref('')

async function loadPaths(): Promise<void> {
  pathsStatus.value = 'loading'
  pathsFailure.value = null
  try {
    const loaded = await listMasteryPaths()
    paths.value = loaded.paths
    pathsStatus.value = 'ready'
    const requested = typeof route.query.path === 'string' ? route.query.path : ''
    const chosen = requested || paths.value[0]?.path_id || ''
    if (chosen) {
      currentId.value = chosen
      if (route.query.path !== chosen) replaceRoute(chosen)
      await loadPath()
    }
  } catch (cause) {
    pathsFailure.value = toFailure(cause)
    pathsStatus.value = 'failed'
  }
}

function replaceRoute(pathId: string): void {
  void router.replace({ name: 'mastery', query: { path: pathId } })
}

function selectPath(pathId: string): void {
  if (pathId === currentId.value) return
  currentId.value = pathId
  replaceRoute(pathId)
  void loadPath()
}

/* ── 路径详情 ────────────────────────────────────────────────────────── */

const path = ref<MasteryPath | null>(null)
const pathStatus = ref<Status>('idle')
const pathFailure = ref<ApiFailure | null>(null)

const insight = ref<MasteryInsight | null>(null)
const insightStatus = ref<Status>('idle')
const insightFailure = ref<ApiFailure | null>(null)

const lease = ref<LeaseState | null>(null)
const leaseStatus = ref<Status>('idle')
const leaseFailure = ref<ApiFailure | null>(null)
const releasing = ref(false)
const selectedPointId = ref('')

async function loadPath(): Promise<void> {
  const pathId = currentId.value
  if (!pathId) return
  pathStatus.value = 'loading'
  pathFailure.value = null
  path.value = null
  insight.value = null
  insightStatus.value = 'loading'
  insightFailure.value = null
  selectedPointId.value = ''

  try {
    const loadedPath = await getMasteryPath(pathId)
    if (currentId.value !== pathId) return
    path.value = loadedPath
    pathStatus.value = 'ready'
  } catch (cause) {
    if (currentId.value !== pathId) return
    pathFailure.value = toFailure(cause)
    pathStatus.value = 'failed'
    return
  }

  void Promise.all([loadInsight(pathId), loadLease(pathId)])
}

async function loadInsight(pathId = currentId.value): Promise<void> {
  if (!pathId) return
  insightStatus.value = 'loading'
  insightFailure.value = null
  try {
    const loaded = await getMasteryInsight(pathId)
    if (currentId.value !== pathId) return
    insight.value = loaded
    insightStatus.value = 'ready'
    if (selectedPointId.value === '') {
      const suggested = loaded.next.knowledge_point_id
      const exists = loaded.points.some((point) => point.knowledge_point_id === suggested)
      selectedPointId.value = exists ? (suggested ?? '') : (loaded.points[0]?.knowledge_point_id ?? '')
    }
  } catch (cause) {
    if (currentId.value !== pathId) return
    insightFailure.value = toFailure(cause)
    insightStatus.value = 'failed'
  }
}

async function loadLease(pathId = currentId.value): Promise<void> {
  if (!pathId) return
  leaseStatus.value = 'loading'
  leaseFailure.value = null
  try {
    const loaded = await getMasteryLease(pathId)
    if (currentId.value !== pathId) return
    lease.value = loaded
    leaseStatus.value = 'ready'
  } catch (cause) {
    if (currentId.value !== pathId) return
    leaseFailure.value = toFailure(cause)
    leaseStatus.value = 'failed'
  }
}

async function releaseLease(): Promise<void> {
  if (!lease.value?.lease) return
  const confirmed = await confirmAction({
    title: MASTERY.leaseConfirmTitle,
    body: MASTERY.leaseConfirmBody,
    note: MASTERY.leaseConfirmNote,
    confirmLabel: MASTERY.leaseRelease,
    danger: true,
  })
  if (!confirmed) return

  releasing.value = true
  leaseFailure.value = null
  try {
    const next = await releaseMasteryLease(currentId.value)
    lease.value = next
    notify(MASTERY.leaseReleased, 'success')
  } catch (cause) {
    leaseFailure.value = toFailure(cause)
    notify(MASTERY.leaseReleaseFailed, 'error')
  } finally {
    releasing.value = false
  }
}

/* ── 跳转 ────────────────────────────────────────────────────────────── */

function canCreate(): boolean {
  return hasCapability('mastery_path')
}

function goChat(): void {
  void router.push({ name: 'mastery-chat' })
}

function goPractice(): void {
  void router.push({ name: 'practice' })
}

/* ── 生命周期 ────────────────────────────────────────────────────────── */

watch(
  () => route.query.path,
  (value) => {
    if (typeof value !== 'string' || value === '' || value === currentId.value) return
    currentId.value = value
    void loadPath()
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
    <PageHeader :title="MASTERY.title" :description="MASTERY.description" />

    <FailureState
      v-if="pathsStatus === 'failed' && pathsFailure"
      :failure="pathsFailure"
      :title="MASTERY.pathsFailed"
      @retry="loadPaths"
    />

    <div v-else class="workspace">
      <!-- 左：路径列表 -->
      <aside class="side">
        <div class="side__head">
          <h2 class="side__title">{{ MASTERY.paths }}</h2>
          <button v-if="canCreate()" type="button" class="link" @click="goChat">
            {{ MASTERY.goChat }}
          </button>
        </div>

        <SkeletonBlock v-if="pathsStatus === 'loading'" :lines="5" :line-height="32" />

        <template v-else-if="paths.length === 0">
          <p class="side__empty">{{ MASTERY.pathsEmpty }}</p>
          <EmptyState
            :title="MASTERY.pathsEmpty"
            :hint="canCreate() ? MASTERY.goChatHint : MASTERY.capabilityMissing"
            :action-label="MASTERY.goChat"
            @action="goChat"
          />
        </template>

        <ul v-else class="paths">
          <li v-for="item in paths" :key="item.path_id">
            <button
              type="button"
              class="path"
              :class="{ 'path--on': item.path_id === currentId }"
              @click="selectPath(item.path_id)"
            >
              <span class="path__goal">{{ item.emoji }} {{ item.goal }}</span>
              <span class="path__meta">
                {{ MASTERY.masteredCount(item.mastered, item.knowledge_points) }}
              </span>
            </button>
          </li>
        </ul>
      </aside>

      <!-- 右：详情 -->
      <MasteryDetail
        :path="path"
        :path-status="pathStatus"
        :path-failure="pathFailure"
        :insight="insight"
        :insight-status="insightStatus"
        :insight-failure="insightFailure"
        :lease="lease"
        :lease-status="leaseStatus"
        :lease-failure="leaseFailure"
        :releasing="releasing"
        :selected-point-id="selectedPointId"
        :paths-ready="pathsStatus === 'ready'"
        :has-current="currentId !== ''"
        @retry-path="loadPath"
        @retry-insight="loadInsight()"
        @retry-lease="loadLease()"
        @release="releaseLease"
        @select-point="selectedPointId = $event"
        @go-chat="goChat"
        @go-practice="goPractice"
      />
    </div>
  </SurfaceGate>
</template>

<style scoped src="./Mastery.css"></style>
