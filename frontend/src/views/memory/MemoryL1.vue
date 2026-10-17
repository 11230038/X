<script setup lang="ts">
/**
 * L1 原始轨迹。
 *
 * 这条路由只有 `limit`，没有游标。页面照实说"最近 N 条"，不画一个假的
 * "下一页"。`payload` 是任意 JSON，只用等宽文本展示，不当结构解释。
 */

import { onBeforeUnmount, onMounted, ref, watch } from 'vue'

import type { ApiFailure } from '../../api/client'
import {
  listTraces,
  type MemorySurface,
  type TraceView,
} from '../../api/memory'
import EmptyState from '../../components/common/EmptyState.vue'
import FailureState from '../../components/common/FailureState.vue'
import SkeletonBlock from '../../components/common/SkeletonBlock.vue'
import { MEMORY } from '../../copy/memory'
import { formatDateTime } from '../../shared/format'
import { toFailure } from '../../shared/resource'
import { RouterLink } from 'vue-router'

const surfaces: MemorySurface[] = ['mastery', 'reading', 'practice', 'user']
const limits = [20, 50, 100]

const traces = ref<TraceView[]>([])
const status = ref<'loading' | 'ready' | 'failed'>('loading')
const failure = ref<ApiFailure | null>(null)
const surface = ref<'' | MemorySurface>('')
const kind = ref('')
const limit = ref(50)

let debounce: ReturnType<typeof setTimeout> | null = null

async function load(): Promise<void> {
  status.value = 'loading'
  failure.value = null
  try {
    traces.value = await listTraces({
      limit: limit.value,
      ...(surface.value ? { surface: surface.value } : {}),
      ...(kind.value.trim() ? { kind: kind.value.trim() } : {}),
    })
    status.value = 'ready'
  } catch (cause) {
    failure.value = toFailure(cause)
    status.value = 'failed'
  }
}

watch([surface, limit], () => {
  void load()
})

watch(kind, () => {
  if (debounce) clearTimeout(debounce)
  debounce = setTimeout(() => {
    debounce = null
    void load()
  }, 300)
})

function surfaceLabel(value: string): string {
  return MEMORY.surfaces[value] ?? value
}

function payloadText(payload: unknown): string {
  if (payload === undefined || payload === null) return ''
  if (typeof payload === 'string') return payload
  try {
    return JSON.stringify(payload, null, 2)
  } catch {
    return String(payload)
  }
}

onMounted(() => {
  void load()
})

onBeforeUnmount(() => {
  if (debounce) clearTimeout(debounce)
})
</script>

<template>
  <div class="l1">
    <header class="head">
      <div>
        <h2 class="head__title">{{ MEMORY.l1Title }}</h2>
        <p class="head__description">{{ MEMORY.l1Description }}</p>
      </div>
    </header>

    <div class="filters">
      <label class="field">
        <span class="field__label">{{ MEMORY.surface }}</span>
        <select v-model="surface" class="field__input">
          <option value="">{{ MEMORY.allSurfaces }}</option>
          <option v-for="value in surfaces" :key="value" :value="value">
            {{ surfaceLabel(value) }}
          </option>
        </select>
      </label>

      <label class="field">
        <span class="field__label">{{ MEMORY.l1Kind }}</span>
        <input
          v-model="kind"
          class="field__input"
          type="text"
          :placeholder="MEMORY.l1KindPlaceholder"
        />
      </label>

      <label class="field">
        <span class="field__label">{{ MEMORY.l1Limit }}</span>
        <select v-model.number="limit" class="field__input">
          <option v-for="count in limits" :key="count" :value="count">{{ count }}</option>
        </select>
      </label>
    </div>

    <SkeletonBlock v-if="status === 'loading'" :lines="8" :line-height="38" />

    <FailureState
      v-else-if="status === 'failed' && failure"
      :failure="failure"
      :title="MEMORY.l1Failed"
      @retry="load"
    />

    <EmptyState
      v-else-if="traces.length === 0"
      :title="MEMORY.l1Empty"
      :hint="MEMORY.l1EmptyHint"
      :action-label="MEMORY.goChat"
      @action="$router.push({ name: 'chat' })"
    />

    <template v-else>
      <p class="note">{{ MEMORY.l1Recent(traces.length) }}</p>

      <ul class="traces">
        <li v-for="trace in traces" :key="trace.trace_id" class="trace">
          <header class="trace__head">
            <span class="chip">{{ surfaceLabel(trace.surface) }}</span>
            <span class="chip chip--kind">{{ trace.kind }}</span>
            <span class="trace__time">{{ formatDateTime(trace.created_at) }}</span>
          </header>

          <p class="trace__statement">{{ trace.statement }}</p>

          <p v-if="trace.session_id" class="trace__ids">
            {{ MEMORY.traceSession }}
            <RouterLink
              class="ids__link"
              :to="{ name: 'conversation', params: { sessionId: trace.session_id } }"
            >
              {{ trace.session_id }}
            </RouterLink>
            <template v-if="trace.turn_id"> · {{ MEMORY.traceTurn }} {{ trace.turn_id }}</template>
          </p>

          <details v-if="payloadText(trace.payload)" class="payload">
            <summary class="payload__toggle">{{ MEMORY.traceOpen }}</summary>
            <pre class="payload__body">{{ payloadText(trace.payload) }}</pre>
          </details>
          <p v-else class="trace__empty">{{ MEMORY.traceNoPayload }}</p>
        </li>
      </ul>
    </template>
  </div>
</template>

<style scoped>
.l1 {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.head__title {
  margin: 0;
  font-size: 15px;
  font-weight: 650;
}

.head__description {
  margin: 4px 0 0;
  color: var(--text-muted);
  font-size: 12px;
  line-height: 1.6;
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
  min-width: 120px;
  padding: 5px 8px;
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-sm);
  background: var(--surface);
  font-size: 12px;
}

.note {
  margin: 0;
  color: var(--text-faint);
  font-size: 11px;
}

.traces {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.trace {
  padding: 11px 13px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--surface);
}

.trace__head {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  align-items: baseline;
}

.chip {
  padding: 0 7px;
  border: 1px solid var(--border);
  border-radius: 9px;
  color: var(--text-muted);
  font-size: 9px;
  line-height: 17px;
}

.chip--kind {
  font-family: var(--font-mono);
}

.trace__time {
  margin-left: auto;
  color: var(--text-faint);
  font-size: 10px;
}

.trace__statement {
  margin: 6px 0 0;
  font-size: 13px;
  line-height: 1.7;
  white-space: pre-wrap;
  word-break: break-word;
}

.trace__ids {
  margin: 5px 0 0;
  color: var(--text-faint);
  font-family: var(--font-mono);
  font-size: 10px;
  word-break: break-all;
}

.ids__link {
  color: var(--accent);
  text-decoration: none;
}

.trace__empty {
  margin: 6px 0 0;
  color: var(--text-faint);
  font-size: 10px;
}

.payload {
  margin-top: 7px;
}

.payload__toggle {
  color: var(--text-muted);
  font-size: 10px;
  cursor: pointer;
}

.payload__body {
  margin: 6px 0 0;
  padding: 8px;
  overflow-x: auto;
  border: 1px solid var(--border);
  border-radius: 4px;
  background: var(--surface-sunken);
  color: var(--text-muted);
  font-family: var(--font-mono);
  font-size: 10px;
  line-height: 1.5;
  white-space: pre-wrap;
  word-break: break-word;
}
</style>
