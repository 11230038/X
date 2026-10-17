<script setup lang="ts">
/**
 * 审计事件。
 *
 * 这里只有"谁、什么时候、对什么、做了什么、结果如何"。对象是筛选与跳转
 * 入口，不展开被审计的内容 —— 审计记录解释的是动作，不是业务数据的副本。
 *
 * 后端没有 offset / 游标，只有排他的 `until`：向后翻页时把 `until` 设成当前
 * 最老一条的 `created_at`。同一时刻有多条时，边界那一组可能被整组排除。
 * 因此"请求没带回新行"不能被说成"到底了"，页面会明写这个限制。
 */

import { computed, onMounted, ref } from 'vue'

import type { ApiFailure } from '../../api/client'
import {
  listAudit,
  type AuditEvent,
  type AuditFilters,
  type AuditResult,
  type AuditTargetKind,
} from '../../api/audit'
import EmptyState from '../../components/common/EmptyState.vue'
import FailureState from '../../components/common/FailureState.vue'
import LoadMore from '../../components/common/LoadMore.vue'
import SkeletonBlock from '../../components/common/SkeletonBlock.vue'
import { ADMIN, AUDIT_ACTIONS } from '../../copy/admin'
import { formatDateTime } from '../../shared/format'
import { toFailure } from '../../shared/resource'

const TARGET_KINDS: AuditTargetKind[] = [
  'account',
  'grant',
  'guardian_link',
  'guardian_permission',
  'session',
  'library_file',
  'workspace_job',
  'surface',
]

const TARGET_LABELS: Record<AuditTargetKind, string> = {
  account: '账号',
  grant: '单独授权',
  guardian_link: '监护关系',
  guardian_permission: '监护权限',
  session: '会话',
  library_file: '文件',
  workspace_job: '工作区任务',
  surface: '导航面',
}

const RESULT_LABELS: Record<AuditResult, string> = {
  ok: ADMIN.resultOk,
  denied: ADMIN.resultDenied,
  failed: ADMIN.resultFailed,
}

const ACTION_OPTIONS = Object.entries(AUDIT_ACTIONS).map(([value, label]) => ({
  value,
  label,
}))

const events = ref<AuditEvent[]>([])
const status = ref<'loading' | 'ready' | 'failed'>('loading')
const failure = ref<ApiFailure | null>(null)
const loadingMore = ref(false)
const moreFailure = ref<ApiFailure | null>(null)
const exhausted = ref(false)
const stuck = ref(false)
const paged = ref(false)
const maxPageSize = ref(0)

const actorId = ref('')
const subjectId = ref('')
const action = ref('')
const targetKind = ref('')
const targetId = ref('')
const since = ref('')
const until = ref('')
const limit = ref(50)
const filterError = ref('')

const hasFilters = computed(
  () =>
    actorId.value.trim() !== '' ||
    subjectId.value.trim() !== '' ||
    action.value !== '' ||
    targetKind.value !== '' ||
    targetId.value.trim() !== '' ||
    since.value !== '' ||
    until.value !== '',
)

const oldest = computed(() => events.value[events.value.length - 1] ?? null)

const limitOptions = computed(() => {
  const ceiling = maxPageSize.value > 0 ? maxPageSize.value : 100
  return [20, 50, 100].filter((value) => value <= ceiling)
})

function parseOptionalId(value: string): number | undefined {
  const text = value.trim()
  if (text === '') return undefined
  return Number(text)
}

function toRfc3339(value: string): string | undefined {
  if (value === '') return undefined
  return new Date(value).toISOString()
}

function validateFilters(): boolean {
  filterError.value = ''
  for (const [label, value] of [
    [ADMIN.actor, actorId.value],
    ['学习者编号', subjectId.value],
  ] as const) {
    const text = value.trim()
    if (text !== '' && (!/^\d+$/.test(text) || Number(text) <= 0)) {
      filterError.value = `${label}要填正整数编号。`
      return false
    }
  }
  if (since.value !== '' && Number.isNaN(new Date(since.value).getTime())) {
    filterError.value = '开始时间格式不对。'
    return false
  }
  if (until.value !== '' && Number.isNaN(new Date(until.value).getTime())) {
    filterError.value = '结束时间格式不对。'
    return false
  }
  if (since.value !== '' && until.value !== '') {
    const start = new Date(since.value).getTime()
    const end = new Date(until.value).getTime()
    if (end <= start) {
      filterError.value = '结束时间要晚于开始时间。'
      return false
    }
  }
  return true
}

/** `untilOverride` 只用于翻页；筛选栏里的 `until` 在首页生效。 */
function buildFilters(untilOverride?: string): AuditFilters {
  const filters: AuditFilters = { limit: limit.value }
  const actor = parseOptionalId(actorId.value)
  const subject = parseOptionalId(subjectId.value)
  if (actor !== undefined) filters.actor_id = actor
  if (subject !== undefined) filters.subject_id = subject
  if (action.value !== '') filters.action = action.value
  if (targetKind.value !== '') filters.target_kind = targetKind.value
  if (targetId.value.trim() !== '') filters.target_id = targetId.value.trim()
  const start = toRfc3339(since.value)
  if (start !== undefined) filters.since = start
  if (untilOverride !== undefined) {
    filters.until = untilOverride
  } else {
    const end = toRfc3339(until.value)
    if (end !== undefined) filters.until = end
  }
  return filters
}

function mergeEvents(incoming: AuditEvent[]): number {
  const seen = new Set(events.value.map((event) => event.audit_id))
  let added = 0
  for (const event of incoming) {
    if (seen.has(event.audit_id)) continue
    seen.add(event.audit_id)
    events.value.push(event)
    added += 1
  }
  return added
}

async function loadFirst(): Promise<void> {
  if (!validateFilters()) return
  status.value = 'loading'
  failure.value = null
  moreFailure.value = null
  stuck.value = false
  exhausted.value = false
  paged.value = false
  try {
    const result = await listAudit(buildFilters())
    events.value = result.events
    maxPageSize.value = result.max_page_size
    status.value = 'ready'
    exhausted.value = result.events.length < limit.value
  } catch (cause) {
    failure.value = toFailure(cause)
    status.value = 'failed'
  }
}

async function loadOlder(): Promise<void> {
  const boundary = oldest.value
  if (!boundary || loadingMore.value) return
  loadingMore.value = true
  moreFailure.value = null
  stuck.value = false
  try {
    const result = await listAudit(buildFilters(boundary.created_at))
    paged.value = true
    const added = mergeEvents(result.events)
    if (added === 0) {
      // 边界那一秒的事件可能全部被排他条件挡掉了 —— 不能说自己到底了。
      stuck.value = true
    } else if (result.events.length < limit.value) {
      exhausted.value = true
    }
  } catch (cause) {
    moreFailure.value = toFailure(cause)
  } finally {
    loadingMore.value = false
  }
}

function clearFilters(): void {
  actorId.value = ''
  subjectId.value = ''
  action.value = ''
  targetKind.value = ''
  targetId.value = ''
  since.value = ''
  until.value = ''
  filterError.value = ''
  void loadFirst()
}

/** 把某个事件的对象变成一次筛选，而不是假装能打开详情。 */
function filterByTarget(event: AuditEvent): void {
  targetKind.value = event.target_kind
  targetId.value = event.target_id
  void loadFirst()
}

function actionLabel(value: string): string {
  return AUDIT_ACTIONS[value] ?? value
}

function resultLabel(value: AuditResult): string {
  return RESULT_LABELS[value] ?? value
}

function targetKindLabel(value: AuditTargetKind): string {
  return TARGET_LABELS[value] ?? value
}

onMounted(() => {
  void loadFirst()
})
</script>

<style scoped src="./AdminAudit.css"></style>

<template>
  <div class="audit">
    <section class="filters">
      <h2 class="filters__title">{{ ADMIN.filters }}</h2>
      <div class="filters__grid">
        <label class="field">
          <span class="field__label">{{ ADMIN.actor }}编号</span>
          <input v-model="actorId" type="text" inputmode="numeric" class="field__input" />
        </label>
        <label class="field">
          <span class="field__label">学习者编号</span>
          <input v-model="subjectId" type="text" inputmode="numeric" class="field__input" />
        </label>
        <label class="field">
          <span class="field__label">{{ ADMIN.actionOf }}</span>
          <select v-model="action" class="field__input">
            <option value="">全部</option>
            <option v-for="option in ACTION_OPTIONS" :key="option.value" :value="option.value">
              {{ option.label }}
            </option>
          </select>
        </label>
        <label class="field">
          <span class="field__label">对象类型</span>
          <select v-model="targetKind" class="field__input">
            <option value="">全部</option>
            <option v-for="kind in TARGET_KINDS" :key="kind" :value="kind">
              {{ targetKindLabel(kind) }}
            </option>
          </select>
        </label>
        <label class="field">
          <span class="field__label">对象编号</span>
          <input v-model="targetId" type="text" class="field__input" />
        </label>
        <label class="field">
          <span class="field__label">开始时间</span>
          <input v-model="since" type="datetime-local" class="field__input" />
        </label>
        <label class="field">
          <span class="field__label">结束时间</span>
          <input v-model="until" type="datetime-local" class="field__input" />
        </label>
        <label class="field">
          <span class="field__label">每页</span>
          <select v-model.number="limit" class="field__input">
            <option v-for="value in limitOptions" :key="value" :value="value">
              {{ value }} 条
            </option>
          </select>
        </label>
      </div>

      <p v-if="filterError" class="filters__error">{{ filterError }}</p>

      <div class="filters__actions">
        <button type="button" class="primary" @click="loadFirst">
          {{ status === 'loading' ? '正在读取…' : '查询' }}
        </button>
        <button type="button" class="ghost" :disabled="!hasFilters" @click="clearFilters">
          清除筛选
        </button>
      </div>
    </section>

    <SkeletonBlock v-if="status === 'loading'" :lines="6" :line-height="34" />

    <FailureState
      v-else-if="status === 'failed' && failure"
      :failure="failure"
      title="审计事件没能读出来"
      @retry="loadFirst"
    />

    <template v-else>
      <EmptyState
        v-if="events.length === 0"
        :title="ADMIN.noEvents"
        :hint="hasFilters ? '当前筛选下没有事件。' : '这个范围里还没有审计记录。'"
        :action-label="hasFilters ? '清除筛选' : ''"
        @action="clearFilters"
      />

      <template v-else>
        <p class="count">
          当前 {{ events.length }} 条{{ hasFilters ? '（已筛选）' : '' }}
        </p>

        <ol class="events">
          <li v-for="event in events" :key="event.audit_id" class="event">
            <div class="event__head">
              <span class="event__action">{{ actionLabel(event.action) }}</span>
              <span class="chip" :class="`chip--${event.result}`">
                {{ resultLabel(event.result) }}
              </span>
              <time class="event__time">{{ formatDateTime(event.created_at) }}</time>
            </div>

            <div class="event__meta">
              <span>
                {{ ADMIN.actor }}：{{ event.actor_name }}
                <template v-if="event.actor_id">（#{{ event.actor_id }}）</template>
                · {{ event.actor_role }}
              </span>
              <span v-if="event.subject_id">学习者：#{{ event.subject_id }}</span>
              <span>
                {{ ADMIN.target }}：
                <button type="button" class="target" @click="filterByTarget(event)">
                  {{ targetKindLabel(event.target_kind) }}
                  {{ event.target_id || '（整个类型）' }}
                </button>
              </span>
              <code v-if="event.request_id" class="event__request" :title="event.request_id">
                {{ event.request_id }}
              </code>
            </div>
          </li>
        </ol>

        <p class="hint">{{ ADMIN.objectIsLink }}</p>

        <div v-if="stuck" class="stuck">
          <p class="stuck__text">
            这一页没有带回新事件。审计接口按排他时间向后翻，同一时刻的事件可能被跳过，
            所以这里不能当作已经到底。
          </p>
          <div class="stuck__actions">
            <button type="button" class="ghost" @click="loadFirst">从头刷新</button>
            <button type="button" class="ghost" @click="clearFilters">清除筛选</button>
          </div>
        </div>

        <LoadMore
          v-else
          label="加载更早"
          :loading="loadingMore"
          :exhausted="exhausted"
          :failure="moreFailure"
          @load="loadOlder"
        />

        <p v-if="paged && exhausted && !stuck" class="hint">
          向后翻页是排他的时间条件。即使这里显示"没有更多了"，同一时刻的事件仍可能不在结果里。
        </p>
      </template>
    </template>
  </div>
</template>
