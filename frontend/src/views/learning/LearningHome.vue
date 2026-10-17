<script setup lang="ts">
/**
 * 学习首页。
 *
 * 四段来自同一个 `GET /dashboard`，所以只有"整页首次失败"与"整页成功"两种
 * 形态，没有段级的部分失败。全空是合法状态：每一段都给一个**真实动作**，
 * 不用解释性小字填空。
 *
 * "下一步"必须同时给出**理由**与**证据入口**。只给一句"建议复习"而不说是
 * 哪条路径、哪条证据，等于要求用户相信一个看不见的判断。
 */

import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'

import type { ApiFailure } from '../../api/client'
import { getDashboard, type DashboardView } from '../../api/dashboard'
import { canSurface } from '../../auth/permissions'
import FailureState from '../../components/common/FailureState.vue'
import SkeletonBlock from '../../components/common/SkeletonBlock.vue'
import SurfaceGate from '../../components/common/SurfaceGate.vue'
import PageHeader from '../../components/shell/PageHeader.vue'
import { LEARNING } from '../../copy/learning'
import { formatDateTime } from '../../shared/format'
import { toFailure } from '../../shared/resource'

const router = useRouter()

type Status = 'idle' | 'loading' | 'ready' | 'failed'

const status = ref<Status>('idle')
const failure = ref<ApiFailure | null>(null)
const dashboard = ref<DashboardView | null>(null)

async function load(): Promise<void> {
  if (!dashboard.value) status.value = 'loading'
  failure.value = null
  try {
    dashboard.value = await getDashboard()
    status.value = 'ready'
  } catch (cause) {
    failure.value = toFailure(cause)
    // 已有内容时保留它，不把页面换成一整块错误。
    status.value = dashboard.value ? 'ready' : 'failed'
  }
}

function openSession(id: string): void {
  void router.push({ name: 'conversation', params: { sessionId: id } })
}

function openPath(pathId: string): void {
  void router.push({ name: 'mastery', query: { path: pathId } })
}

function openEvidence(item: { path_id: string; knowledge_point_id?: string }): void {
  void router.push({
    name: 'mastery',
    query: {
      path: item.path_id,
      ...(item.knowledge_point_id ? { point: item.knowledge_point_id } : {}),
    },
  })
}

function goPractice(): void {
  void router.push({ name: 'practice' })
}

function goChat(): void {
  void router.push({ name: 'chat' })
}

function goMastery(): void {
  void router.push({ name: 'mastery' })
}

function nextKindLabel(kind: 'learn' | 'review' | 'done'): string {
  return kind === 'learn' ? LEARNING.nextLearn : kind === 'review' ? LEARNING.nextReview : LEARNING.nextDone
}

onMounted(() => {
  if (canSurface('dashboard') === false) return
  void load()
})
</script>

<template>
  <SurfaceGate surface="dashboard">
    <PageHeader :title="LEARNING.title" :description="LEARNING.description" />

    <SkeletonBlock v-if="status === 'loading'" :lines="10" :line-height="46" />

    <FailureState
      v-else-if="status === 'failed' && failure"
      :failure="failure"
      :title="LEARNING.loadFailed"
      @retry="load"
    />

    <template v-else-if="dashboard">
      <div class="grid">
        <!-- 继续对话 -->
        <section class="card">
          <h2 class="card__title">{{ LEARNING.sessions }}</h2>
          <p v-if="dashboard.sessions.length === 0" class="card__empty">
            {{ LEARNING.sessionsEmpty }}
          </p>
          <ul v-else class="entries">
            <li v-for="entry in dashboard.sessions" :key="entry.id">
              <button type="button" class="entry" @click="openSession(entry.id)">
                <span class="entry__title">{{ entry.title }}</span>
                <span v-if="entry.context" class="entry__context">{{ entry.context }}</span>
                <span v-if="entry.updated_at" class="entry__meta">
                  {{ LEARNING.updatedAt }} {{ formatDateTime(entry.updated_at) }}
                </span>
              </button>
            </li>
          </ul>
          <button type="button" class="card__action" @click="goChat">
            {{ LEARNING.sessionsAction }}
          </button>
        </section>

        <!-- 掌握路径 -->
        <section class="card">
          <h2 class="card__title">{{ LEARNING.paths }}</h2>
          <p v-if="dashboard.paths.length === 0" class="card__empty">
            {{ LEARNING.pathsEmpty }}
          </p>
          <ul v-else class="entries">
            <li v-for="entry in dashboard.paths" :key="entry.id">
              <button type="button" class="entry" @click="openPath(entry.id)">
                <span class="entry__title">{{ entry.title }}</span>
                <span v-if="entry.context" class="entry__context">{{ entry.context }}</span>
                <span v-if="entry.updated_at" class="entry__meta">
                  {{ LEARNING.updatedAt }} {{ formatDateTime(entry.updated_at) }}
                </span>
              </button>
            </li>
          </ul>
          <p v-if="dashboard.paths.length === 0" class="card__hint">{{ LEARNING.pathsHint }}</p>
          <button type="button" class="card__action" @click="goMastery">
            {{ LEARNING.pathsAction }}
          </button>
        </section>

        <!-- 待复习 -->
        <section class="card">
          <h2 class="card__title">{{ LEARNING.reviews }}</h2>
          <p v-if="dashboard.reviews.length === 0" class="card__empty">
            {{ LEARNING.reviewsEmpty }}
          </p>
          <ul v-else class="entries">
            <li v-for="entry in dashboard.reviews" :key="entry.id">
              <span class="entry entry--static">
                <span class="entry__title">{{ entry.title }}</span>
                <span v-if="entry.context" class="entry__context">{{ entry.context }}</span>
              </span>
            </li>
          </ul>
          <button type="button" class="card__action" @click="goPractice">
            {{ LEARNING.reviewsAction }}
          </button>
        </section>
      </div>

      <!-- 下一步 -->
      <section class="next">
        <h2 class="next__title">{{ LEARNING.next }}</h2>
        <p v-if="dashboard.next.length === 0" class="card__empty">
          {{ LEARNING.nextEmpty }}
        </p>
        <p v-if="dashboard.next.length === 0" class="card__hint">
          {{ LEARNING.nextEmptyHint }}
        </p>
        <ol v-else class="next__list">
          <li v-for="(item, i) in dashboard.next" :key="`${item.path_id}-${i}`" class="next__item">
            <div class="next__body">
              <span class="next__kind">{{ nextKindLabel(item.kind) }}</span>
              <span class="next__name">{{ item.title }}</span>
              <p class="next__reason">{{ item.reason }}</p>
            </div>
            <div class="next__actions">
              <button
                v-if="item.evidence_ids.length > 0"
                type="button"
                class="next__evidence"
                @click="openEvidence(item)"
              >
                {{ LEARNING.nextEvidence(item.evidence_ids.length) }}
              </button>
              <button
                v-if="item.kind === 'review'"
                type="button"
                class="next__go"
                @click="goPractice"
              >
                {{ LEARNING.nextReview }}
              </button>
              <button
                v-else-if="item.kind === 'learn'"
                type="button"
                class="next__go"
                @click="openEvidence(item)"
              >
                {{ LEARNING.nextLearn }}
              </button>
            </div>
          </li>
        </ol>
        <p v-if="dashboard.next.length > 0" class="next__hint">
          {{ LEARNING.nextEvidenceHint }}
        </p>
      </section>

      <p v-if="failure" class="stale">
        这一次刷新没有成功，下面显示的是上一次读到的内容。
        <button type="button" class="stale__retry" @click="load">{{ LEARNING.retry }}</button>
      </p>

    </template>
  </SurfaceGate>
</template>

<style scoped>
.grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
  gap: 16px;
  margin-top: 18px;
}

.card {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 16px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--surface);
}

.card__title {
  margin: 0;
  font-size: 14px;
  font-weight: 650;
}

.card__empty {
  margin: 0;
  color: var(--text-muted);
  font-size: 13px;
}

.card__hint {
  margin: 0;
  color: var(--text-faint);
  font-size: 11px;
}

.card__action {
  align-self: flex-start;
  margin-top: auto;
  padding: 5px 12px;
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-sm);
  background: var(--surface);
  color: var(--text);
  font-size: 12px;
  cursor: pointer;
}

.card__action:hover {
  border-color: var(--accent);
  color: var(--accent);
}

.entries {
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.entry {
  display: flex;
  flex-direction: column;
  gap: 2px;
  width: 100%;
  padding: 7px 8px;
  border: 1px solid transparent;
  border-radius: var(--radius-sm);
  background: transparent;
  text-align: left;
  cursor: pointer;
}

.entry:hover {
  border-color: var(--border);
  background: var(--surface-sunken);
}

.entry--static {
  cursor: default;
}

.entry--static:hover {
  border-color: transparent;
  background: transparent;
}

.entry__title {
  overflow: hidden;
  color: var(--text);
  font-size: 13px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.entry__context {
  overflow: hidden;
  color: var(--text-muted);
  font-size: 12px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.entry__meta {
  color: var(--text-faint);
  font-size: 10px;
}

/* ── 下一步 ───────────────────────────────────────────────────────────── */

.next {
  margin-top: 20px;
  padding: 16px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--surface);
}

.next__title {
  margin: 0 0 10px;
  font-size: 14px;
  font-weight: 650;
}

.next__list {
  display: flex;
  flex-direction: column;
  gap: 10px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.next__item {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  align-items: flex-start;
  justify-content: space-between;
  padding: 10px 12px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
}

.next__body {
  display: flex;
  flex-direction: column;
  gap: 3px;
  min-width: 0;
  flex: 1;
}

.next__kind {
  align-self: flex-start;
  padding: 0 7px;
  border: 1px solid var(--border-strong);
  border-radius: 9px;
  color: var(--text-muted);
  font-size: 10px;
}

.next__name {
  font-size: 13px;
  font-weight: 600;
}

.next__reason {
  margin: 0;
  color: var(--text-muted);
  font-size: 12px;
  line-height: 1.6;
}

.next__actions {
  display: flex;
  flex: none;
  flex-wrap: wrap;
  gap: 6px;
  align-items: center;
}

.next__evidence {
  padding: 3px 9px;
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--text-muted);
  font-size: 11px;
  cursor: pointer;
}

.next__go {
  padding: 3px 10px;
  border: 1px solid var(--accent);
  border-radius: var(--radius-sm);
  background: var(--accent);
  color: #fff;
  font-size: 11px;
  cursor: pointer;
}

.next__hint {
  margin: 8px 0 0;
  color: var(--text-faint);
  font-size: 11px;
}

.stale {
  margin: 16px 0 0;
  color: var(--text-muted);
  font-size: 12px;
}

.stale__retry {
  margin-left: 6px;
  padding: 1px 7px;
  border: 1px solid var(--border-strong);
  border-radius: 4px;
  background: transparent;
  color: var(--text);
  font-size: 11px;
  cursor: pointer;
}

</style>
