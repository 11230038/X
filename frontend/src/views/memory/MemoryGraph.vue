<script setup lang="ts">
/**
 * 归属分组图。
 *
 * 后端没有"事实之间的关系"接口，所以这张图**只画归属**：一条事实属于哪个
 * 归属面、属于哪个 scope。层级线是可点击的事实与两个分组之间的包含关系，
 * 不是概念之间的边。
 *
 * 数据来自 L3 综合（每次 GET 现算），因此这里显示 `computed_at`，且不缓存。
 */

import { computed, onMounted, ref } from 'vue'

import type { ApiFailure } from '../../api/client'
import {
  getSynthesis,
  type FactView,
  type MemorySurface,
  type SynthesisView,
} from '../../api/memory'
import EmptyState from '../../components/common/EmptyState.vue'
import FailureState from '../../components/common/FailureState.vue'
import SkeletonBlock from '../../components/common/SkeletonBlock.vue'
import FactDetailDialog from '../../components/memory/FactDetailDialog.vue'
import { MEMORY } from '../../copy/memory'
import { formatDateTime } from '../../shared/format'
import { toFailure } from '../../shared/resource'

interface ScopeBranch {
  scope: string
  facts: FactView[]
}

interface SurfaceGroup {
  surface: MemorySurface
  facts: FactView[]
  truncated: number
  scopes: ScopeBranch[]
  unscoped: FactView[]
}

const surfaceOrder: MemorySurface[] = ['mastery', 'reading', 'practice', 'user']

const synthesis = ref<SynthesisView | null>(null)
const status = ref<'loading' | 'ready' | 'failed'>('loading')
const failure = ref<ApiFailure | null>(null)
const openFactId = ref('')

async function load(): Promise<void> {
  status.value = 'loading'
  failure.value = null
  try {
    synthesis.value = await getSynthesis()
    status.value = 'ready'
  } catch (cause) {
    failure.value = toFailure(cause)
    status.value = 'failed'
  }
}

/**
 * 综合结果里 recent / profile / scopes / preferences 会重复同一批事实。
 * 按 fact_id 去重，保留第一次出现的顺序。
 */
function dedupe(facts: FactView[]): FactView[] {
  const seen = new Set<string>()
  const result: FactView[] = []
  for (const fact of facts) {
    if (seen.has(fact.fact_id)) continue
    seen.add(fact.fact_id)
    result.push(fact)
  }
  return result
}

const groups = computed<SurfaceGroup[]>(() => {
  const data = synthesis.value
  if (!data) return []

  const truncatedBySurface = new Map<string, number>()
  const collected: FactView[] = [...data.recent]
  for (const profile of data.profile) {
    collected.push(...profile.facts)
    truncatedBySurface.set(profile.surface, profile.truncated)
  }
  for (const scopeGroup of data.scopes) collected.push(...scopeGroup.facts)
  collected.push(...data.preferences)
  const all = dedupe(collected)

  const bySurface = new Map<MemorySurface, FactView[]>()
  for (const fact of all) {
    const list = bySurface.get(fact.surface) ?? []
    list.push(fact)
    bySurface.set(fact.surface, list)
  }

  const ordered: MemorySurface[] = [
    ...surfaceOrder.filter((surface) => bySurface.has(surface)),
    ...[...bySurface.keys()].filter((surface) => !surfaceOrder.includes(surface)),
  ]

  return ordered.map((surface) => {
    const facts = bySurface.get(surface) ?? []
    const scopes: ScopeBranch[] = []
    const unscoped: FactView[] = []

    for (const fact of facts) {
      if (!fact.scope) {
        unscoped.push(fact)
        continue
      }
      let branch = scopes.find((item) => item.scope === fact.scope)
      if (!branch) {
        branch = { scope: fact.scope, facts: [] }
        scopes.push(branch)
      }
      branch.facts.push(fact)
    }

    scopes.sort((left, right) => left.scope.localeCompare(right.scope, 'zh-Hans-CN'))

    return {
      surface,
      facts,
      truncated: truncatedBySurface.get(surface) ?? 0,
      scopes,
      unscoped,
    }
  })
})

function surfaceLabel(surface: string): string {
  return MEMORY.surfaces[surface] ?? surface
}

function factTitle(statement: string): string {
  return statement.length > 80 ? `${statement.slice(0, 80)}…` : statement
}

onMounted(() => {
  void load()
})
</script>

<template>
  <div class="graph-page">
    <header class="head">
      <h2 class="head__title">{{ MEMORY.graphTitle }}</h2>
      <p class="head__description">{{ MEMORY.graphDescription }}</p>
    </header>

    <SkeletonBlock v-if="status === 'loading'" :lines="8" :line-height="44" />

    <FailureState
      v-else-if="status === 'failed' && failure"
      :failure="failure"
      :title="MEMORY.graphFailed"
      @retry="load"
    />

    <EmptyState
      v-else-if="groups.length === 0"
      :title="MEMORY.graphEmpty"
      :hint="MEMORY.graphEmptyHint"
      :action-label="MEMORY.goChat"
      @action="$router.push({ name: 'chat' })"
    />

    <template v-else-if="synthesis">
      <p class="stamp">
        {{ MEMORY.l3ComputedAt }} {{ formatDateTime(synthesis.computed_at) }} ·
        {{ MEMORY.l3Window(synthesis.window_days) }}
      </p>
      <p class="legend">
        <span class="legend__line"></span>
        {{ MEMORY.graphLegend }}
      </p>

      <div class="tree">
        <section v-for="group in groups" :key="group.surface" class="surface">
          <header class="surface__head">
            <span class="node__dot"></span>
            <span class="surface__name">{{ surfaceLabel(group.surface) }}</span>
            <span class="count">{{ MEMORY.graphFactCount(group.facts.length) }}</span>
            <span v-if="group.truncated > 0" class="count">
              {{ MEMORY.l3Truncated(group.truncated) }}
            </span>
          </header>

          <div class="branches">
            <div v-if="group.scopes.length > 0" class="branch-group">
              <p class="branch-group__label">{{ MEMORY.graphScopes }}</p>
              <div v-for="branch in group.scopes" :key="branch.scope" class="branch">
                <p class="branch__head">
                  <span class="node__dot node__dot--small"></span>
                  <span class="branch__name">{{ branch.scope }}</span>
                  <span class="count">{{ MEMORY.graphFactCount(branch.facts.length) }}</span>
                </p>
                <ul class="facts">
                  <li v-for="fact in branch.facts" :key="fact.fact_id">
                    <button
                      type="button"
                      class="fact"
                      :title="fact.statement"
                      @click="openFactId = fact.fact_id"
                    >
                      {{ factTitle(fact.statement) }}
                    </button>
                  </li>
                </ul>
              </div>
            </div>

            <div v-if="group.unscoped.length > 0" class="branch-group">
              <p class="branch-group__label">{{ MEMORY.graphUnscoped }}</p>
              <ul class="facts facts--unscoped">
                <li v-for="fact in group.unscoped" :key="fact.fact_id">
                  <button
                    type="button"
                    class="fact"
                    :title="fact.statement"
                    @click="openFactId = fact.fact_id"
                  >
                    {{ factTitle(fact.statement) }}
                  </button>
                </li>
              </ul>
            </div>
          </div>
        </section>
      </div>
    </template>

    <FactDetailDialog
      v-if="openFactId"
      :fact-id="openFactId"
      @close="openFactId = ''"
      @changed="load"
    />
  </div>
</template>

<style scoped>
.graph-page {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.head__title {
  margin: 0;
  font-size: 15px;
  font-weight: 650;
}

.head__description {
  margin: 4px 0 0;
  color: var(--text-muted);
  font-size: 14px;
  line-height: 1.6;
}

.stamp {
  margin: 0;
  color: var(--text-faint);
  font-family: var(--font-mono);
  font-size: 14px;
}

.legend {
  display: flex;
  gap: 7px;
  align-items: center;
  margin: 0;
  color: var(--text-muted);
  font-size: 14px;
}

.legend__line {
  width: 22px;
  height: 0;
  border-top: 1px dashed var(--border-strong);
}

.tree {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.surface {
  padding: 16px 18px 16px 22px;
  border: 1px solid var(--glass-border);
  border-left: 2px solid var(--accent);
  border-radius: var(--radius);
  background: var(--glass-bg);
  box-shadow: var(--glass-shadow);
}

.surface__head {
  display: flex;
  flex-wrap: wrap;
  gap: 7px;
  align-items: center;
}

.node__dot {
  width: 7px;
  height: 7px;
  flex: none;
  border-radius: 50%;
  background: var(--accent);
}

.node__dot--small {
  width: 5px;
  height: 5px;
  background: var(--text-faint);
}

.surface__name {
  font-size: 15px;
  font-weight: 650;
}

.count {
  color: var(--text-faint);
  font-size: 14px;
}

.branches {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin-top: 9px;
  padding-left: 14px;
  border-left: 1px dashed var(--border-strong);
}

.branch-group__label {
  margin: 0 0 5px;
  color: var(--text-faint);
  font-size: 14px;
}

.branch {
  padding-left: 10px;
  border-left: 1px dashed var(--border);
}

.branch-group + .branch-group {
  margin-top: 4px;
}

.branch__head {
  display: flex;
  gap: 6px;
  align-items: center;
  margin: 0 0 4px;
}

.branch__name {
  color: var(--text-muted);
  font-family: var(--font-mono);
  font-size: 14px;
}

.facts {
  display: flex;
  flex-direction: column;
  gap: 3px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.facts--unscoped {
  padding-left: 10px;
  border-left: 1px dashed var(--border);
}

.fact {
  width: 100%;
  padding: 3px 6px;
  border: 1px solid transparent;
  border-radius: 4px;
  background: transparent;
  color: var(--text);
  font-size: 14px;
  line-height: 1.5;
  text-align: left;
  cursor: pointer;
}

.fact:hover {
  border-color: var(--border-strong);
  background: var(--surface-sunken);
}
</style>
