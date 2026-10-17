<script setup lang="ts">
/**
 * 用量统计。
 *
 * `unfinished`（没有回来的调用）是**单独一个数**，不并进 `error`：没能结束
 * 不等于失败，它可能是进程被杀、连接断了、或者还在跑。
 *
 * 不显示供应商地址 —— 这里只有谁/哪个模型/哪个来源的计数。
 */

import { onMounted, ref } from 'vue'

import type { ApiFailure } from '../../api/client'
import {
  getUsage,
  type UsageGroupBy,
  type UsageReport,
} from '../../api/usage'
import FailureState from '../../components/common/FailureState.vue'
import PartialBanner from '../../components/common/PartialBanner.vue'
import SkeletonBlock from '../../components/common/SkeletonBlock.vue'
import { SETTINGS } from '../../copy/settings'
import {
  formatDurationMs,
  formatNumber,
} from '../../shared/format'
import { toFailure } from '../../shared/resource'

const GROUP_OPTIONS: Array<{ value: UsageGroupBy; label: string }> = [
  { value: 'day', label: SETTINGS.days },
  { value: 'session', label: SETTINGS.sessions },
  { value: 'source', label: SETTINGS.sources },
  { value: 'model', label: SETTINGS.models },
  { value: 'provider', label: SETTINGS.providers },
]

function dateInput(date: Date): string {
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

const now = new Date()
const since = ref(dateInput(new Date(now.getTime() - 6 * 24 * 60 * 60 * 1000)))
const until = ref(dateInput(now))
const groupBy = ref<UsageGroupBy>('day')
const model = ref('')
const provider = ref('')
const source = ref('')
const sessionId = ref('')

const report = ref<UsageReport | null>(null)
const status = ref<'loading' | 'ready' | 'failed'>('loading')
const failure = ref<ApiFailure | null>(null)
const refreshFailure = ref<ApiFailure | null>(null)

function startOfDay(value: string): string | undefined {
  if (!value) return undefined
  const date = new Date(`${value}T00:00:00`)
  return Number.isNaN(date.getTime()) ? undefined : date.toISOString()
}

function endOfDay(value: string): string | undefined {
  if (!value) return undefined
  const date = new Date(`${value}T23:59:59.999`)
  return Number.isNaN(date.getTime()) ? undefined : date.toISOString()
}

const rangeInvalid = ref('')

async function load(): Promise<void> {
  rangeInvalid.value = ''
  const sinceValue = startOfDay(since.value)
  const untilValue = endOfDay(until.value)
  if (sinceValue && untilValue && sinceValue > untilValue) {
    rangeInvalid.value = '开始日期不能晚于结束日期'
    return
  }

  const hadData = report.value !== null
  status.value = hadData ? status.value : 'loading'
  if (!hadData) failure.value = null
  refreshFailure.value = null

  try {
    report.value = await getUsage({
      group_by: groupBy.value,
      ...(sinceValue ? { since: sinceValue } : {}),
      ...(untilValue ? { until: untilValue } : {}),
      ...(model.value.trim() ? { model: model.value.trim() } : {}),
      ...(provider.value.trim() ? { provider: provider.value.trim() } : {}),
      ...(source.value.trim() ? { source: source.value.trim() } : {}),
      ...(sessionId.value.trim() ? { session_id: sessionId.value.trim() } : {}),
    })
    status.value = 'ready'
  } catch (cause) {
    const next = toFailure(cause)
    if (hadData) {
      refreshFailure.value = next
      status.value = 'ready'
    } else {
      failure.value = next
      status.value = 'failed'
    }
  }
}

onMounted(() => {
  void load()
})
</script>

<template>
  <div class="usage">
    <div class="filters">
      <label class="field">
        <span class="field__label">{{ SETTINGS.usageWindow }}（开始）</span>
        <input v-model="since" type="date" class="field__input" />
      </label>
      <label class="field">
        <span class="field__label">{{ SETTINGS.usageWindow }}（结束）</span>
        <input v-model="until" type="date" class="field__input" />
      </label>
      <label class="field">
        <span class="field__label">{{ SETTINGS.groupBy }}</span>
        <select v-model="groupBy" class="field__input">
          <option v-for="option in GROUP_OPTIONS" :key="option.value" :value="option.value">
            {{ option.label }}
          </option>
        </select>
      </label>
      <label class="field">
        <span class="field__label">模型（可选）</span>
        <input v-model="model" type="text" class="field__input" />
      </label>
      <label class="field">
        <span class="field__label">供应商（可选）</span>
        <input v-model="provider" type="text" class="field__input" />
      </label>
      <label class="field">
        <span class="field__label">来源（可选）</span>
        <input v-model="source" type="text" class="field__input" />
      </label>
      <label class="field">
        <span class="field__label">会话（可选）</span>
        <input v-model="sessionId" type="text" class="field__input" />
      </label>
      <button type="button" class="primary" @click="load">查询</button>
    </div>

    <p v-if="rangeInvalid" class="error">{{ rangeInvalid }}</p>

    <PartialBanner
      v-if="refreshFailure"
      :failure="refreshFailure"
      text="这次查询没有成功，下面仍是上一次的结果"
      @retry="load"
    />

    <SkeletonBlock v-if="status === 'loading'" :lines="7" :line-height="28" />

    <FailureState
      v-else-if="status === 'failed' && failure"
      :failure="failure"
      title="用量没能读出来"
      @retry="load"
    />

    <template v-else-if="report">
      <section class="totals">
        <div class="totals__item">
          <span class="totals__label">{{ SETTINGS.calls }}</span>
          <span class="totals__value">{{ formatNumber(report.finished.calls) }}</span>
        </div>
        <div class="totals__item">
          <span class="totals__label">{{ SETTINGS.ok }}</span>
          <span class="totals__value">{{ formatNumber(report.finished.ok) }}</span>
        </div>
        <div class="totals__item">
          <span class="totals__label">{{ SETTINGS.errors }}</span>
          <span class="totals__value">{{ formatNumber(report.finished.error) }}</span>
        </div>
        <div class="totals__item">
          <span class="totals__label">{{ SETTINGS.cancelled }}</span>
          <span class="totals__value">{{ formatNumber(report.finished.cancelled) }}</span>
        </div>
        <div class="totals__item totals__item--warn">
          <span class="totals__label">{{ SETTINGS.unfinished }}</span>
          <span class="totals__value">{{ formatNumber(report.unfinished) }}</span>
        </div>
        <div class="totals__item">
          <span class="totals__label">{{ SETTINGS.tokens }}</span>
          <span class="totals__value">{{ formatNumber(report.finished.total_tokens) }}</span>
        </div>
        <div class="totals__item">
          <span class="totals__label">{{ SETTINGS.duration }}</span>
          <span class="totals__value">{{ formatDurationMs(report.finished.duration_ms) }}</span>
        </div>
        <div class="totals__item">
          <span class="totals__label">{{ SETTINGS.ttft }}</span>
          <span class="totals__value">{{ formatDurationMs(report.finished.ttft_ms) }}</span>
        </div>
      </section>

      <p class="hint">
        「未完成的调用」不是失败：它可能是进程退出、连接断开，或者还在进行。
      </p>

      <section class="groups">
        <h2 class="groups__title">按{{ GROUP_OPTIONS.find((item) => item.value === groupBy)?.label }}分组</h2>
        <p v-if="report.groups.length === 0" class="empty">这个时间范围内没有调用</p>
        <table v-else class="table">
          <thead>
            <tr>
              <th>分组</th>
              <th>{{ SETTINGS.calls }}</th>
              <th>{{ SETTINGS.ok }}</th>
              <th>{{ SETTINGS.errors }}</th>
              <th>{{ SETTINGS.cancelled }}</th>
              <th>{{ SETTINGS.unfinished }}</th>
              <th>{{ SETTINGS.tokens }}</th>
              <th>{{ SETTINGS.duration }}</th>
              <th>{{ SETTINGS.ttft }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="group in report.groups" :key="group.key">
              <td class="key" :title="group.key">{{ group.key || '—' }}</td>
              <td>{{ formatNumber(group.finished.calls) }}</td>
              <td>{{ formatNumber(group.finished.ok) }}</td>
              <td>{{ formatNumber(group.finished.error) }}</td>
              <td>{{ formatNumber(group.finished.cancelled) }}</td>
              <td>{{ formatNumber(group.unfinished) }}</td>
              <td>{{ formatNumber(group.finished.total_tokens) }}</td>
              <td>{{ formatDurationMs(group.finished.duration_ms) }}</td>
              <td>{{ formatDurationMs(group.finished.ttft_ms) }}</td>
            </tr>
          </tbody>
        </table>
      </section>
    </template>
  </div>
</template>

<style scoped>
.usage {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.filters {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  align-items: flex-end;
  padding: 12px 14px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--surface);
}

.field {
  display: flex;
  flex-direction: column;
  gap: 3px;
}

.field__label {
  color: var(--text-faint);
  font-size: 10px;
}

.field__input {
  min-width: 110px;
  padding: 5px 8px;
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-sm);
  background: var(--surface);
  font-size: 12px;
}

.totals {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(110px, 1fr));
  gap: 8px;
}

.totals__item {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 9px 11px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--surface);
}

.totals__item--warn {
  border-color: var(--warning);
  background: var(--warning-soft);
}

.totals__label {
  color: var(--text-faint);
  font-size: 10px;
}

.totals__value {
  color: var(--text);
  font-family: var(--font-mono);
  font-size: 14px;
}

.hint {
  margin: 0;
  color: var(--text-muted);
  font-size: 11px;
}

.groups {
  padding: 14px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--surface);
}

.groups__title {
  margin: 0 0 9px;
  font-size: 13px;
  font-weight: 650;
}

.table {
  width: 100%;
  border-collapse: collapse;
  font-size: 11px;
}

.table th,
.table td {
  padding: 5px 7px;
  border-bottom: 1px solid var(--border);
  text-align: right;
  white-space: nowrap;
}

.table th:first-child,
.table td:first-child {
  text-align: left;
}

.table th {
  color: var(--text-faint);
  font-weight: 500;
}

.key {
  max-width: 220px;
  overflow: hidden;
  font-family: var(--font-mono);
  text-overflow: ellipsis;
}

.empty {
  margin: 0;
  color: var(--text-muted);
  font-size: 12px;
}

.error {
  margin: 0;
  color: var(--danger);
  font-size: 11px;
}

.primary {
  padding: 5px 13px;
  border: 1px solid var(--accent);
  border-radius: var(--radius-sm);
  background: var(--accent);
  color: #fff;
  font-size: 12px;
  cursor: pointer;
}
</style>
