<script setup lang="ts">
/**
 * 监护：按学习者出报告。
 *
 * 报告接口要求这条关系上持有 `view_reports`。没有这项权限时**不画空报告**，
 * 也不发请求 —— 空报告会被读成"孩子没有学习"，而事实是"你没被允许看"。
 */

import { computed, onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'

import type { ApiFailure } from '../../api/client'
import type { AdminGuardianLink, GuardianPermission } from '../../api/admin'
import { getLearnerReport, listLearners } from '../../api/guardian'
import type { DashboardView } from '../../api/dashboard'
import EmptyState from '../../components/common/EmptyState.vue'
import FailureState from '../../components/common/FailureState.vue'
import SkeletonBlock from '../../components/common/SkeletonBlock.vue'
import { SETTINGS } from '../../copy/settings'
import { formatDateTime } from '../../shared/format'
import { toFailure } from '../../shared/resource'

const PERMISSION_LABELS: Record<GuardianPermission, string> = {
  view_reports: '查看学习报告',
  assign_materials: '分配材料',
  manage_restrictions: '管理限制',
  reset_credentials: '重置凭据',
}

const learners = ref<AdminGuardianLink[]>([])
const status = ref<'loading' | 'ready' | 'failed'>('loading')
const failure = ref<ApiFailure | null>(null)

const selectedId = ref<number | null>(null)
const report = ref<DashboardView | null>(null)
const reportStatus = ref<'idle' | 'loading' | 'ready' | 'failed'>('idle')
const reportFailure = ref<ApiFailure | null>(null)

const current = computed(
  () => learners.value.find((item) => item.learner_id === selectedId.value) ?? null,
)
const canViewSelected = computed(
  () => current.value !== null && canViewReports(current.value),
)
const selectedPermissionText = computed(() =>
  current.value ? permissionLabels(current.value) : '',
)

function canViewReports(link: AdminGuardianLink): boolean {
  return link.permissions.includes('view_reports')
}

async function load(): Promise<void> {
  status.value = 'loading'
  failure.value = null
  try {
    const result = await listLearners()
    learners.value = result.learners
    status.value = 'ready'
  } catch (cause) {
    failure.value = toFailure(cause)
    status.value = 'failed'
  }
}

function select(link: AdminGuardianLink): void {
  selectedId.value = link.learner_id
  report.value = null
  reportFailure.value = null
  reportStatus.value = 'idle'
  if (canViewReports(link)) {
    void loadReport(link)
  }
}

function reloadSelectedReport(): void {
  if (current.value) void loadReport(current.value)
}

async function loadReport(link: AdminGuardianLink): Promise<void> {
  if (!canViewReports(link)) return
  reportStatus.value = 'loading'
  reportFailure.value = null
  try {
    report.value = await getLearnerReport(link.learner_id)
    reportStatus.value = 'ready'
  } catch (cause) {
    reportFailure.value = toFailure(cause)
    reportStatus.value = 'failed'
  }
}

function permissionLabels(link: AdminGuardianLink): string {
  if (link.permissions.length === 0) return SETTINGS.permissionNoEntry
  return link.permissions.map((permission) => PERMISSION_LABELS[permission]).join('、')
}

onMounted(() => {
  void load()
})
</script>

<template>
  <div class="guardian">
    <SkeletonBlock v-if="status === 'loading'" :lines="4" :line-height="34" />

    <FailureState
      v-else-if="status === 'failed' && failure"
      :failure="failure"
      title="监护关系没能读出来"
      @retry="load"
    />

    <EmptyState
      v-else-if="learners.length === 0"
      :title="SETTINGS.learners"
      hint="当前账号还没有登记监护关系。"
    />

    <template v-else>
      <p class="hint">{{ SETTINGS.learners }}</p>
      <ul class="learners">
        <li v-for="link in learners" :key="link.link_id">
          <button
            type="button"
            class="learner"
            :class="{ 'learner--active': link.learner_id === selectedId }"
            @click="select(link)"
          >
            <span class="learner__name">学习者 #{{ link.learner_id }}</span>
            <span class="learner__permissions">{{ permissionLabels(link) }}</span>
          </button>
        </li>
      </ul>

      <section v-if="current" class="report">
        <header class="report__head">
          <h2 class="report__title">{{ SETTINGS.reports }}</h2>
          <button
            v-if="canViewSelected"
            type="button"
            class="ghost"
            :disabled="reportStatus === 'loading'"
            @click="reloadSelectedReport"
          >
            {{ reportStatus === 'loading' ? '正在读取…' : '重新读取' }}
          </button>
        </header>

        <p v-if="!canViewSelected" class="missing">
          {{ SETTINGS.viewReportsMissing }}：这条监护关系没有 `view_reports`，所以这里
          不显示报告。报告为空会被误读成"没有学习"，它不是一回事。
        </p>

        <template v-else>
          <SkeletonBlock v-if="reportStatus === 'loading'" :lines="5" :line-height="26" />

          <FailureState
            v-else-if="reportStatus === 'failed' && reportFailure"
            :failure="reportFailure"
            title="报告没能读出来"
            @retry="reloadSelectedReport"
          />

          <template v-else-if="report">
            <p class="report__meta">
              {{ SETTINGS.guardianPermissions }}：{{ selectedPermissionText }}
            </p>

            <section class="group">
              <h3 class="group__title">继续对话</h3>
              <p v-if="report.sessions.length === 0" class="empty">没有会话</p>
              <ul v-else class="entries">
                <li v-for="entry in report.sessions" :key="entry.id">
                  <RouterLink class="entry" :to="{ name: 'conversation', params: { sessionId: entry.id } }">
                    {{ entry.title }}
                  </RouterLink>
                  <span v-if="entry.updated_at" class="entry__meta">
                    {{ formatDateTime(entry.updated_at) }}
                  </span>
                </li>
              </ul>
            </section>

            <section class="group">
              <h3 class="group__title">掌握路径</h3>
              <p v-if="report.paths.length === 0" class="empty">没有掌握路径</p>
              <ul v-else class="entries">
                <li v-for="entry in report.paths" :key="entry.id">
                  <RouterLink
                    class="entry"
                    :to="{ name: 'mastery', query: { path: entry.id } }"
                  >
                    {{ entry.title }}
                  </RouterLink>
                </li>
              </ul>
            </section>

            <section class="group">
              <h3 class="group__title">待复习</h3>
              <p v-if="report.reviews.length === 0" class="empty">没有到期的复习</p>
              <ul v-else class="entries">
                <li v-for="entry in report.reviews" :key="entry.id">
                  <span class="entry">{{ entry.title }}</span>
                </li>
              </ul>
            </section>

            <section class="group">
              <h3 class="group__title">下一步</h3>
              <p v-if="report.next.length === 0" class="empty">没有建议</p>
              <ul v-else class="entries">
                <li v-for="item in report.next" :key="`${item.path_id}:${item.knowledge_point_id ?? ''}`">
                  <RouterLink
                    class="entry"
                    :to="{
                      name: 'mastery',
                      query: {
                        path: item.path_id,
                        ...(item.knowledge_point_id ? { point: item.knowledge_point_id } : {}),
                      },
                    }"
                  >
                    {{ item.title }}
                  </RouterLink>
                  <span class="entry__reason">{{ item.reason }}</span>
                  <span class="entry__meta">证据 {{ item.evidence_ids.length }} 条</span>
                </li>
              </ul>
            </section>
          </template>
        </template>
      </section>
    </template>
  </div>
</template>

<style scoped>
.guardian {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.hint {
  margin: 0;
  color: var(--text-muted);
  font-size: 11px;
}

.learners {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.learner {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  align-items: baseline;
  width: 100%;
  padding: 9px 11px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--surface);
  text-align: left;
  cursor: pointer;
}

.learner--active {
  border-color: var(--accent);
  background: var(--accent-soft);
}

.learner__name {
  font-size: 12px;
  font-weight: 600;
}

.learner__permissions {
  color: var(--text-muted);
  font-size: 10px;
}

.report {
  padding: 14px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--surface);
}

.report__head {
  display: flex;
  gap: 10px;
  align-items: baseline;
  justify-content: space-between;
  margin-bottom: 8px;
}

.report__title {
  margin: 0;
  font-size: 13px;
  font-weight: 650;
}

.report__meta {
  margin: 0 0 10px;
  color: var(--text-faint);
  font-size: 10px;
}

.missing {
  margin: 0;
  padding: 10px 12px;
  border: 1px solid var(--warning);
  border-radius: var(--radius-sm);
  background: var(--warning-soft);
  color: var(--text);
  font-size: 12px;
  line-height: 1.7;
}

.group {
  margin-top: 12px;
}

.group__title {
  margin: 0 0 5px;
  font-size: 12px;
  font-weight: 600;
}

.entries {
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.entries li {
  display: flex;
  flex-wrap: wrap;
  gap: 6px 10px;
  align-items: baseline;
}

.entry {
  color: var(--text);
  font-size: 12px;
  text-decoration: none;
}

a.entry:hover {
  color: var(--accent);
}

.entry__reason,
.entry__meta {
  color: var(--text-faint);
  font-size: 10px;
}

.empty {
  margin: 0;
  color: var(--text-faint);
  font-size: 11px;
}

.ghost {
  padding: 4px 10px;
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-sm);
  background: var(--surface);
  color: var(--text-muted);
  font-size: 11px;
  cursor: pointer;
}

.ghost:disabled {
  cursor: default;
  opacity: 0.55;
}
</style>
