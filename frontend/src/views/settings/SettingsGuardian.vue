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
import FailureState from '../../components/common/FailureState.vue'
import SkeletonBlock from '../../components/common/SkeletonBlock.vue'
import { SETTINGS } from '../../copy/settings'
import { formatDateTime } from '../../shared/format'
import { toFailure } from '../../shared/resource'
import AppIcon from '../../components/common/AppIcon.vue'

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

    <!-- 没有监护关系时也给一张卡：这里没有本页能做的动作，但要说清该去哪儿。 -->
    <section v-else-if="learners.length === 0" class="set-panel">
      <div class="set-panel__head">
        <span class="set-panel__icon" aria-hidden="true"><AppIcon name="heart" /></span>
        <h2 class="set-panel__title">{{ SETTINGS.learners }}</h2>
      </div>
      <p class="hint">
        当前账号还没有登记监护关系。监护关系由管理员在
        <RouterLink class="link" :to="{ name: 'profile-users' }">用户管理</RouterLink>
        的用户详情里建立。
      </p>
    </section>

    <template v-else>
      <p class="label">{{ SETTINGS.learners }}</p>
      <ul class="learners">
        <li v-for="link in learners" :key="link.link_id">
          <button
            type="button"
            class="learner"
            :class="{ 'learner--active': link.learner_id === selectedId }"
            @click="select(link)"
          >
            <span class="learner__icon" aria-hidden="true"><AppIcon name="user" /></span>
            <span class="learner__text">
              <span class="learner__name">学习者 #{{ link.learner_id }}</span>
              <span class="learner__permissions">{{ permissionLabels(link) }}</span>
            </span>
          </button>
        </li>
      </ul>

      <section v-if="current" class="set-panel">
        <div class="set-panel__head">
          <span class="set-panel__icon" aria-hidden="true"><AppIcon name="space" /></span>
          <h2 class="set-panel__title">{{ SETTINGS.reports }}</h2>
          <div class="set-panel__actions">
            <button
              v-if="canViewSelected"
              type="button"
              class="set-btn set-btn--ghost set-btn--sm"
              :disabled="reportStatus === 'loading'"
              @click="reloadSelectedReport"
            >
              {{ reportStatus === 'loading' ? '正在读取…' : '重新读取' }}
            </button>
          </div>
        </div>

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
  gap: 16px;
}

/* 分区小标题：比正文小一档、比说明重一档。 */
.label {
  margin: 0;
  color: var(--text-faint);
  font-size: 13px;
  font-weight: 700;
  letter-spacing: 0.02em;
}

/* 学习者是一排可选的卡片：图标块 + 名字 + 权限。 */
.learners {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.learner {
  display: flex;
  gap: 12px;
  align-items: center;
  min-width: 240px;
  padding: 12px 20px 12px 12px;
  border: 1px solid rgb(255 255 255 / 70%);
  border-radius: 16px;
  background: rgb(255 255 255 / 55%);
  text-align: left;
  cursor: pointer;
  transition: background 0.2s ease, box-shadow 0.2s ease, border-color 0.2s ease;
}

.learner:hover {
  background: #fff;
}

.learner--active {
  border-color: rgb(177 140 255 / 45%);
  background: #fff;
  box-shadow: 0 8px 20px -10px rgb(177 140 255 / 60%);
}

.learner__icon {
  display: grid;
  flex: none;
  place-items: center;
  width: 40px;
  height: 40px;
  border-radius: 12px;
  background: linear-gradient(135deg, rgb(177 140 255 / 18%), rgb(102 210 245 / 18%));
  font-size: 19px;
  line-height: 1;
}

.learner__text {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.learner__name {
  color: var(--text);
  font-size: 15px;
  font-weight: 700;
}

.learner__permissions {
  color: var(--text-muted);
  font-size: 13px;
}

.report__meta {
  margin: 0 0 14px;
  color: var(--text-faint);
  font-size: 13px;
}

.missing {
  margin: 0;
  padding: 12px 14px;
  border-radius: 12px;
  background: var(--warning-soft);
  color: var(--text);
  font-size: 14px;
  line-height: 1.7;
}

/* 报告里的四块：第一条不画分隔线，其余每条一条浅线。 */
.group {
  margin-top: 16px;
  padding-top: 14px;
  border-top: 1px solid rgb(0 0 0 / 5%);
}

.group__title {
  margin: 0 0 6px;
  color: var(--text-muted);
  font-size: 13px;
  font-weight: 700;
  letter-spacing: 0.02em;
}

.entries {
  display: flex;
  flex-direction: column;
  gap: 6px;
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
  font-size: 14px;
  text-decoration: none;
}

a.entry {
  color: var(--accent-ink);
  font-weight: 600;
}

a.entry:hover {
  text-decoration: underline;
  text-underline-offset: 2px;
}

.entry__reason,
.entry__meta {
  color: var(--text-faint);
  font-size: 13px;
}

.empty {
  margin: 0;
  color: var(--text-faint);
  font-size: 14px;
}

.hint {
  margin: 0;
  color: var(--text-muted);
  font-size: 14px;
  line-height: 1.7;
}

.link {
  color: var(--accent-ink);
  font-weight: 600;
  text-decoration: none;
}

.link:hover {
  text-decoration: underline;
  text-underline-offset: 2px;
}
</style>
