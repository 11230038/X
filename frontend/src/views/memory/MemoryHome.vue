<script setup lang="ts">
/**
 * 记忆工作台。
 *
 * 三层各自独立取数，所以各自独立失败：一段读不出来，另外两段照常可用。
 * 这里不显示"总数"——`/memory/traces` 只有 limit，facts 有游标但工作台只取
 * 最近几条；把"最近 5 条"写成"共 5 条"会是错的。
 */

import { onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'

import type { ApiFailure } from '../../api/client'
import {
  exportMemory,
  listFacts,
  listMemoryAudit,
  listTraces,
  type FactView,
  type MemoryAuditView,
  type TraceView,
} from '../../api/memory'
import FactCard from '../../components/memory/FactCard.vue'
import FactDetailDialog from '../../components/memory/FactDetailDialog.vue'
import PartialBanner from '../../components/common/PartialBanner.vue'
import SkeletonBlock from '../../components/common/SkeletonBlock.vue'
import { MEMORY } from '../../copy/memory'
import { formatDateTime } from '../../shared/format'
import { toFailure } from '../../shared/resource'
import { notify } from '../../shared/toast'

type Status = 'loading' | 'ready' | 'failed'

const traceLimit = 5
const factLimit = 5
const auditLimit = 5

const traces = ref<TraceView[]>([])
const tracesStatus = ref<Status>('loading')
const tracesFailure = ref<ApiFailure | null>(null)

const facts = ref<FactView[]>([])
const factsStatus = ref<Status>('loading')
const factsFailure = ref<ApiFailure | null>(null)

const audits = ref<MemoryAuditView[]>([])
const auditsStatus = ref<Status>('loading')
const auditsFailure = ref<ApiFailure | null>(null)

const openFactId = ref('')
const exporting = ref(false)

async function loadTraces(): Promise<void> {
  tracesStatus.value = 'loading'
  tracesFailure.value = null
  try {
    traces.value = await listTraces({ limit: traceLimit })
    tracesStatus.value = 'ready'
  } catch (cause) {
    tracesFailure.value = toFailure(cause)
    tracesStatus.value = 'failed'
  }
}

async function loadFacts(): Promise<void> {
  factsStatus.value = 'loading'
  factsFailure.value = null
  try {
    facts.value = await listFacts({ limit: factLimit, status: ['active'] })
    factsStatus.value = 'ready'
  } catch (cause) {
    factsFailure.value = toFailure(cause)
    factsStatus.value = 'failed'
  }
}

async function loadAudits(): Promise<void> {
  auditsStatus.value = 'loading'
  auditsFailure.value = null
  try {
    audits.value = await listMemoryAudit({ limit: auditLimit })
    auditsStatus.value = 'ready'
  } catch (cause) {
    auditsFailure.value = toFailure(cause)
    auditsStatus.value = 'failed'
  }
}

function surfaceLabel(surface: string): string {
  return MEMORY.surfaces[surface] ?? surface
}

function actionLabel(action: string): string {
  return MEMORY.actionLabels[action] ?? action
}

function auditDetail(detail: unknown): string {
  if (detail === undefined || detail === null) return ''
  if (typeof detail === 'string') return detail
  try {
    return JSON.stringify(detail)
  } catch {
    return String(detail)
  }
}

async function exportAll(): Promise<void> {
  exporting.value = true
  try {
    const exported = await exportMemory()
    const blob = new Blob([JSON.stringify(exported, null, 2)], {
      type: 'application/json;charset=utf-8',
    })
    const url = URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = `memory-${new Date().toISOString().slice(0, 10)}.json`
    link.click()
    URL.revokeObjectURL(url)
    notify(MEMORY.homeExported, 'success')
  } catch (cause) {
    notify(`${MEMORY.homeExportFailed}：${MEMORY.actionFailed}`, 'error')
    void cause
  } finally {
    exporting.value = false
  }
}

onMounted(() => {
  void Promise.all([loadTraces(), loadFacts(), loadAudits()])
})
</script>

<template>
  <div class="home">
    <nav class="layers" aria-label="记忆分层">
      <RouterLink class="layer" :to="{ name: 'memory-l1' }">
        <span class="layer__name">L1</span>
        <span class="layer__title">{{ MEMORY.l1Title }}</span>
        <span class="layer__hint">{{ MEMORY.l1Description }}</span>
      </RouterLink>
      <RouterLink class="layer" :to="{ name: 'memory-l2' }">
        <span class="layer__name">L2</span>
        <span class="layer__title">{{ MEMORY.l2Title }}</span>
        <span class="layer__hint">{{ MEMORY.l2Description }}</span>
      </RouterLink>
      <RouterLink class="layer" :to="{ name: 'memory-l3' }">
        <span class="layer__name">L3</span>
        <span class="layer__title">{{ MEMORY.l3Title }}</span>
        <span class="layer__hint">{{ MEMORY.l3Description }}</span>
      </RouterLink>
      <RouterLink class="layer" :to="{ name: 'memory-graph' }">
        <span class="layer__name">◎</span>
        <span class="layer__title">{{ MEMORY.homeGraph }}</span>
        <span class="layer__hint">{{ MEMORY.homeGraphHint }}</span>
      </RouterLink>
      <RouterLink class="layer" :to="{ name: 'memory-resolve' }">
        <span class="layer__name">⇄</span>
        <span class="layer__title">{{ MEMORY.homeResolve }}</span>
        <span class="layer__hint">{{ MEMORY.homeResolveHint }}</span>
      </RouterLink>
    </nav>

    <div class="toolbar">
      <button type="button" class="primary" :disabled="exporting" @click="exportAll">
        {{ exporting ? MEMORY.homeExporting : MEMORY.homeExport }}
      </button>
    </div>

    <div class="columns">
      <!-- 最近观察 -->
      <section class="column">
        <header class="column__head">
          <h2 class="column__title">{{ MEMORY.homeRecentTraces }}</h2>
          <RouterLink class="link" :to="{ name: 'memory-l1' }">
            {{ MEMORY.homeTracesMore }}
          </RouterLink>
        </header>
        <p class="column__note">{{ MEMORY.homeRecent(traceLimit) }}</p>

        <SkeletonBlock v-if="tracesStatus === 'loading'" :lines="4" :line-height="30" />

        <PartialBanner
          v-else-if="tracesStatus === 'failed' && tracesFailure"
          :failure="tracesFailure"
          :text="MEMORY.homeSectionFailed"
          @retry="loadTraces"
        />

        <p v-else-if="traces.length === 0" class="empty">{{ MEMORY.homeTracesEmpty }}</p>

        <ul v-else class="traces">
          <li v-for="trace in traces" :key="trace.trace_id" class="trace">
            <div class="trace__head">
              <span class="chip">{{ surfaceLabel(trace.surface) }}</span>
              <span class="trace__kind">{{ trace.kind }}</span>
              <span class="trace__time">{{ formatDateTime(trace.created_at) }}</span>
            </div>
            <p class="trace__statement">{{ trace.statement }}</p>
          </li>
        </ul>
      </section>

      <!-- 最近事实 -->
      <section class="column">
        <header class="column__head">
          <h2 class="column__title">{{ MEMORY.homeRecentFacts }}</h2>
          <RouterLink class="link" :to="{ name: 'memory-l2' }">
            {{ MEMORY.homeFactsMore }}
          </RouterLink>
        </header>
        <p class="column__note">{{ MEMORY.homeRecent(factLimit) }}</p>

        <SkeletonBlock v-if="factsStatus === 'loading'" :lines="4" :line-height="30" />

        <PartialBanner
          v-else-if="factsStatus === 'failed' && factsFailure"
          :failure="factsFailure"
          :text="MEMORY.homeSectionFailed"
          @retry="loadFacts"
        />

        <p v-else-if="facts.length === 0" class="empty">{{ MEMORY.homeFactsEmpty }}</p>

        <div v-else class="facts">
          <FactCard
            v-for="fact in facts"
            :key="fact.fact_id"
            :fact="fact"
            @open="openFactId = fact.fact_id"
            @edit="openFactId = fact.fact_id"
          />
        </div>
      </section>

      <!-- 最近动作 -->
      <section class="column">
        <header class="column__head">
          <h2 class="column__title">{{ MEMORY.homeRecentActions }}</h2>
          <RouterLink class="link" :to="{ name: 'memory-resolve' }">
            {{ MEMORY.homeResolve }}
          </RouterLink>
        </header>
        <p class="column__note">{{ MEMORY.homeRecent(auditLimit) }}</p>

        <SkeletonBlock v-if="auditsStatus === 'loading'" :lines="4" :line-height="30" />

        <PartialBanner
          v-else-if="auditsStatus === 'failed' && auditsFailure"
          :failure="auditsFailure"
          :text="MEMORY.homeSectionFailed"
          @retry="loadAudits"
        />

        <p v-else-if="audits.length === 0" class="empty">{{ MEMORY.homeActionsEmpty }}</p>

        <ul v-else class="audits">
          <li v-for="audit in audits" :key="audit.audit_id" class="audit">
            <div class="audit__head">
              <span class="chip">{{ actionLabel(audit.action) }}</span>
              <span v-if="audit.result === 'partial'" class="audit__partial">
                {{ MEMORY.resultPartial }}
              </span>
              <span class="audit__time">{{ formatDateTime(audit.created_at) }}</span>
            </div>
            <p v-if="audit.target_id" class="audit__target">
              {{ audit.target_kind }} {{ audit.target_id }}
            </p>
            <p v-if="auditDetail(audit.detail)" class="audit__detail">{{ auditDetail(audit.detail) }}</p>
          </li>
        </ul>
      </section>
    </div>

    <FactDetailDialog
      v-if="openFactId"
      :fact-id="openFactId"
      @close="openFactId = ''"
      @changed="loadFacts"
    />
  </div>
</template>

<style scoped>
.home {
  display: flex;
  flex-direction: column;
  gap: 20px;
}

.layers {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(190px, 1fr));
  gap: 9px;
}

.layer {
  display: flex;
  flex-direction: column;
  gap: 3px;
  padding: 16px 18px;
  border: 1px solid var(--glass-border);
  border-radius: var(--radius);
  background: var(--glass-bg);
  box-shadow: var(--glass-shadow), var(--inset-light);
  text-decoration: none;
  transition: all 0.2s ease;
}

.layer:hover {
  border-color: rgb(177 140 255 / 40%);
  background: rgb(255 255 255 / 72%);
  transform: translateY(-2px);
}

.layer__name {
  color: var(--accent-ink);
  font-family: var(--font-mono);
  font-size: 14px;
  font-weight: 700;
}

.layer__title {
  color: var(--text);
  font-size: 15px;
  font-weight: 600;
}

.layer__hint {
  color: var(--text-muted);
  font-size: 14px;
  line-height: 1.5;
}

.toolbar {
  display: flex;
  justify-content: flex-end;
}

.columns {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(280px, 1fr));
  gap: 18px;
  align-items: start;
}

.column {
  padding: 18px;
  border: 1px solid var(--glass-border);
  border-radius: var(--radius-card);
  background: var(--glass-bg);
  box-shadow: var(--glass-shadow), var(--inset-light);
  backdrop-filter: blur(20px);
  -webkit-backdrop-filter: blur(20px);
}

.column__head {
  display: flex;
  gap: 8px;
  align-items: baseline;
  justify-content: space-between;
}

.column__title {
  margin: 0;
  font-size: 15px;
  font-weight: 650;
}

.column__note {
  margin: 3px 0 8px;
  color: var(--text-faint);
  font-size: 14px;
}

.empty {
  margin: 8px 0;
  color: var(--text-muted);
  font-size: 14px;
}

.traces,
.audits {
  display: flex;
  flex-direction: column;
  gap: 7px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.trace,
.audit {
  padding: 8px 9px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--surface-sunken);
}

.trace__head,
.audit__head {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  align-items: baseline;
}

.chip {
  padding: 0 6px;
  border: 1px solid var(--border);
  border-radius: 8px;
  color: var(--text-muted);
  font-size: 14px;
  line-height: 16px;
}

.trace__kind {
  color: var(--text-muted);
  font-family: var(--font-mono);
  font-size: 14px;
}

.trace__time,
.audit__time {
  margin-left: auto;
  color: var(--text-faint);
  font-size: 14px;
}

.trace__statement,
.audit__target,
.audit__detail {
  margin: 4px 0 0;
  font-size: 14px;
  line-height: 1.6;
  word-break: break-word;
}

.audit__target,
.audit__detail {
  color: var(--text-faint);
  font-family: var(--font-mono);
  font-size: 14px;
}

.audit__partial {
  color: var(--warning);
  font-size: 14px;
}

.facts {
  display: flex;
  flex-direction: column;
  gap: 7px;
}

.link {
  color: var(--accent);
  font-size: 14px;
  text-decoration: none;
}

.primary {
  padding: 5px 13px;
  border: 1px solid var(--accent);
  border-radius: var(--radius-sm);
  background: var(--accent);
  color: #fff;
  font-size: 14px;
  cursor: pointer;
}

.primary:disabled {
  cursor: default;
  opacity: 0.55;
}
</style>
