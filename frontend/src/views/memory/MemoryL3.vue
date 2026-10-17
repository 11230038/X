<script setup lang="ts">
/**
 * L3 综合。
 *
 * 这个页面**不缓存**服务端结果，也不提供"刷新"按钮：`GET /memory/synthesis`
 * 每次现算，打开一次就是一次新的计算，`computed_at` 是这一次的时间。
 *
 * `truncated` 是服务端明确告诉我们的"还有多少条没放进来"。页面照实写出来，
 * 不把它当成 0，也不偷偷省略。
 */

import { onMounted, ref } from 'vue'

import type { ApiFailure } from '../../api/client'
import {
  getSynthesis,
  type FactView,
  type SynthesisView,
} from '../../api/memory'
import FactCard from '../../components/memory/FactCard.vue'
import FactDetailDialog from '../../components/memory/FactDetailDialog.vue'
import EmptyState from '../../components/common/EmptyState.vue'
import FailureState from '../../components/common/FailureState.vue'
import SkeletonBlock from '../../components/common/SkeletonBlock.vue'
import { MEMORY } from '../../copy/memory'
import { formatDateTime } from '../../shared/format'
import { toFailure } from '../../shared/resource'

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

function surfaceLabel(surface: string): string {
  return MEMORY.surfaces[surface] ?? surface
}

function factCount(): number {
  if (!synthesis.value) return 0
  const seen = new Set<string>()
  const collect = (facts: FactView[]): void => {
    for (const fact of facts) seen.add(fact.fact_id)
  }
  collect(synthesis.value.recent)
  for (const group of synthesis.value.profile) collect(group.facts)
  for (const group of synthesis.value.scopes) collect(group.facts)
  collect(synthesis.value.preferences)
  return seen.size
}

onMounted(() => {
  void load()
})
</script>

<template>
  <div class="l3">
    <header class="head">
      <div>
        <h2 class="head__title">{{ MEMORY.l3Title }}</h2>
        <p class="head__description">{{ MEMORY.l3Description }}</p>
      </div>
    </header>

    <SkeletonBlock v-if="status === 'loading'" :lines="9" :line-height="34" />

    <FailureState
      v-else-if="status === 'failed' && failure"
      :failure="failure"
      :title="MEMORY.l3Failed"
      @retry="load"
    />

    <template v-else-if="synthesis">
      <p class="stamp">
        {{ MEMORY.l3ComputedAt }} {{ formatDateTime(synthesis.computed_at) }} ·
        {{ MEMORY.l3Window(synthesis.window_days) }}
        <span v-if="factCount() > 0"> · {{ MEMORY.l2TotalShown(factCount()) }}</span>
      </p>

      <EmptyState
        v-if="factCount() === 0"
        :title="MEMORY.l3Empty"
        :hint="MEMORY.l3EmptyHint"
        :action-label="MEMORY.goChat"
        @action="$router.push({ name: 'chat' })"
      />

      <template v-else>
        <!-- 最近 -->
        <section v-if="synthesis.recent.length > 0" class="section">
          <h3 class="section__title">{{ MEMORY.l3Recent }}</h3>
          <div class="facts">
            <FactCard
              v-for="fact in synthesis.recent"
              :key="fact.fact_id"
              :fact="fact"
              show-surface
              @open="openFactId = fact.fact_id"
              @edit="openFactId = fact.fact_id"
            />
          </div>
        </section>

        <!-- 画像 -->
        <section v-if="synthesis.profile.length > 0" class="section">
          <h3 class="section__title">{{ MEMORY.l3Profile }}</h3>
          <div class="groups">
            <article v-for="group in synthesis.profile" :key="group.surface" class="group">
              <header class="group__head">
                <h4 class="group__title">{{ surfaceLabel(group.surface) }}</h4>
                <span class="group__count">{{ MEMORY.graphFactCount(group.facts.length) }}</span>
              </header>
              <p v-if="group.facts.length === 0" class="group__empty">
                {{ MEMORY.l3SurfaceEmpty }}
              </p>
              <div v-else class="facts">
                <FactCard
                  v-for="fact in group.facts"
                  :key="fact.fact_id"
                  :fact="fact"
                  @open="openFactId = fact.fact_id"
                  @edit="openFactId = fact.fact_id"
                />
              </div>
              <p v-if="group.truncated > 0" class="group__truncated">
                {{ MEMORY.l3Truncated(group.truncated) }}
              </p>
            </article>
          </div>
        </section>

        <!-- scope -->
        <section class="section">
          <h3 class="section__title">{{ MEMORY.l3Scopes }}</h3>
          <p v-if="synthesis.scopes.length === 0" class="note">{{ MEMORY.l3NoScopes }}</p>
          <div v-else class="groups">
            <article v-for="group in synthesis.scopes" :key="group.scope" class="group">
              <header class="group__head">
                <h4 class="group__title">{{ group.scope }}</h4>
                <span class="group__count">{{ MEMORY.graphFactCount(group.facts.length) }}</span>
              </header>
              <div class="facts">
                <FactCard
                  v-for="fact in group.facts"
                  :key="fact.fact_id"
                  :fact="fact"
                  show-surface
                  @open="openFactId = fact.fact_id"
                  @edit="openFactId = fact.fact_id"
                />
              </div>
            </article>
          </div>
        </section>

        <!-- 偏好 -->
        <section class="section">
          <h3 class="section__title">{{ MEMORY.l3Preferences }}</h3>
          <p v-if="synthesis.preferences.length === 0" class="note">
            {{ MEMORY.l3NoPreferences }}
          </p>
          <div v-else class="facts">
            <FactCard
              v-for="fact in synthesis.preferences"
              :key="fact.fact_id"
              :fact="fact"
              show-surface
              @open="openFactId = fact.fact_id"
              @edit="openFactId = fact.fact_id"
            />
          </div>
        </section>
      </template>

      <FactDetailDialog
        v-if="openFactId"
        :fact-id="openFactId"
        @close="openFactId = ''"
        @changed="load"
      />
    </template>
  </div>
</template>

<style scoped>
.l3 {
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

.stamp {
  margin: 0;
  color: var(--text-faint);
  font-family: var(--font-mono);
  font-size: 10px;
}

.section {
  padding: 14px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--surface);
}

.section__title {
  margin: 0 0 9px;
  font-size: 13px;
  font-weight: 650;
}

.groups {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
  gap: 10px;
  align-items: start;
}

.group {
  padding: 10px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--surface-sunken);
}

.group__head {
  display: flex;
  gap: 8px;
  align-items: baseline;
  justify-content: space-between;
  margin-bottom: 6px;
}

.group__title {
  margin: 0;
  font-size: 12px;
  font-weight: 600;
}

.group__count,
.group__truncated {
  color: var(--text-faint);
  font-size: 10px;
}

.group__empty {
  margin: 0;
  color: var(--text-muted);
  font-size: 11px;
}

.group__truncated {
  margin: 7px 0 0;
}

.facts {
  display: flex;
  flex-direction: column;
  gap: 7px;
}

.note {
  margin: 0;
  color: var(--text-muted);
  font-size: 12px;
}
</style>
