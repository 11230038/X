<script setup lang="ts">
/**
 * 技能与工具目录。
 *
 * 「技能」是服务端注册的能力（`/capabilities`），「工具」是这些能力声明的
 * 工具名。两者不是两张表：工具由能力声明，所以工具那一节只是把同一份清单
 * 反过来按工具名归组，不额外请求。
 *
 * 这一页**只读**：后端没有安装、启用、停用的接口，所以这里不画开关，也不
 * 画看起来能点却永远失败的按钮。阶段表照 `shared/capabilities.ts` 的归一化
 * 结果画，空数组显示成一句明确的话而不是留白。
 */

import { computed, onMounted } from 'vue'

import EmptyState from '../../components/common/EmptyState.vue'
import FailureState from '../../components/common/FailureState.vue'
import PartialBanner from '../../components/common/PartialBanner.vue'
import SkeletonBlock from '../../components/common/SkeletonBlock.vue'
import AppShell from '../../components/shell/AppShell.vue'
import PageHeader from '../../components/shell/PageHeader.vue'
import { CATALOG } from '../../copy/catalog'
import {
  capabilityFailure,
  capabilityList,
  capabilityStatus,
  refreshCapabilities,
} from '../../shared/capabilities'

interface ToolRow {
  name: string
  /** 声明了这个工具的能力名，按清单顺序。 */
  capabilities: string[]
}

const tools = computed<ToolRow[]>(() => {
  const byName = new Map<string, string[]>()
  for (const capability of capabilityList.value) {
    for (const tool of capability.tools) {
      const owners = byName.get(tool)
      if (owners) owners.push(capability.name)
      else byName.set(tool, [capability.name])
    }
  }
  return [...byName.entries()]
    .map(([name, capabilities]) => ({ name, capabilities }))
    .sort((left, right) => left.name.localeCompare(right.name))
})

function load(): void {
  void refreshCapabilities()
}

onMounted(() => {
  if (capabilityStatus.value !== 'ready') load()
})
</script>

<template>
  <AppShell>
    <div class="page">
      <div class="page__inner">
        <PageHeader :title="CATALOG.title" :description="CATALOG.description">
          <template #actions>
            <button
              type="button"
              class="ghost"
              :disabled="capabilityStatus === 'loading'"
              @click="load"
            >
              {{ capabilityStatus === 'loading' ? CATALOG.refreshing : CATALOG.refresh }}
            </button>
          </template>
        </PageHeader>

        <PartialBanner
          v-if="capabilityFailure && capabilityList.length > 0"
          :failure="capabilityFailure"
          :text="CATALOG.refreshFailedKept"
          @retry="load"
        />

        <SkeletonBlock
          v-if="capabilityStatus === 'loading' && capabilityList.length === 0"
          :lines="6"
          :line-height="44"
        />

        <FailureState
          v-else-if="capabilityStatus === 'failed' && capabilityFailure && capabilityList.length === 0"
          :failure="capabilityFailure"
          :title="CATALOG.loadFailed"
          @retry="load"
        />

        <EmptyState
          v-else-if="capabilityList.length === 0"
          :title="CATALOG.emptyTitle"
          :hint="CATALOG.emptyHint"
          :action-label="CATALOG.refresh"
          @action="load"
        />

        <template v-else>
          <section class="section">
            <header class="section__head">
              <h2 class="section__title">{{ CATALOG.skillsTitle }}</h2>
              <span class="section__count">{{ CATALOG.skillsCount(capabilityList.length) }}</span>
            </header>

            <ul class="cards">
              <li v-for="capability in capabilityList" :key="capability.name" class="card">
                <header class="card__head">
                  <code class="card__name">{{ capability.name }}</code>
                  <span class="card__description">{{ capability.description }}</span>
                </header>

                <div class="row">
                  <span class="row__label">{{ CATALOG.stages }}</span>
                  <template v-if="capability.stages.length > 0">
                    <span
                      v-for="(stage, index) in capability.stages"
                      :key="stage"
                      class="chip"
                    >
                      {{ index + 1 }}. {{ stage }}
                    </span>
                  </template>
                  <span v-else class="row__note">{{ CATALOG.noStages }}</span>
                </div>

                <div class="row">
                  <span class="row__label">{{ CATALOG.toolsTitle }}</span>
                  <template v-if="capability.tools.length > 0">
                    <code v-for="tool in capability.tools" :key="tool" class="chip chip--mono">
                      {{ tool }}
                    </code>
                  </template>
                  <span v-else class="row__note">{{ CATALOG.noTools }}</span>
                </div>
              </li>
            </ul>

            <p class="note">{{ CATALOG.stageNote }}</p>
          </section>

          <section class="section">
            <header class="section__head">
              <h2 class="section__title">{{ CATALOG.toolsTitle }}</h2>
              <span class="section__count">{{ CATALOG.toolsCount(tools.length) }}</span>
            </header>
            <p class="note">{{ CATALOG.toolsScope }}</p>

            <p v-if="tools.length === 0" class="note">{{ CATALOG.toolsEmpty }}</p>
            <ul v-else class="tools">
              <li v-for="tool in tools" :key="tool.name" class="tool">
                <code class="tool__name">{{ tool.name }}</code>
                <div class="tool__owners">
                  <span class="row__label">{{ CATALOG.usedBy }}</span>
                  <code v-for="owner in tool.capabilities" :key="owner" class="chip chip--mono">
                    {{ owner }}
                  </code>
                </div>
              </li>
            </ul>
          </section>
        </template>
      </div>
    </div>
  </AppShell>
</template>

<style scoped>
.page {
  display: flex;
  flex: 1;
  min-width: 0;
  overflow: auto;
}

.page__inner {
  display: flex;
  flex: 1;
  flex-direction: column;
  gap: 16px;
  width: 100%;
  max-width: 920px;
  min-width: 0;
  margin: 0 auto;
  padding: 24px;
}

.section {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.section__head {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 8px;
  padding-bottom: 6px;
  border-bottom: 1px solid var(--border);
}

.section__title {
  margin: 0;
  color: var(--text);
  font-size: 15px;
  font-weight: 650;
}

.section__count {
  color: var(--text-faint);
  font-size: 11px;
}

.cards,
.tools {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.card,
.tool {
  padding: 12px 14px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--surface);
}

.card__head {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 8px;
}

.card__name,
.tool__name {
  color: var(--text);
  font-family: var(--font-mono);
  font-size: 12px;
  font-weight: 600;
}

.card__description {
  color: var(--text-muted);
  font-size: 12px;
}

.row {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 5px;
  margin-top: 8px;
}

.row__label {
  color: var(--text-faint);
  font-size: 10px;
}

.row__note,
.note {
  margin: 0;
  color: var(--text-faint);
  font-size: 10px;
  line-height: 1.6;
}

.row .row__note {
  margin-top: 0;
}

.chip {
  padding: 0 7px;
  border: 1px solid var(--border);
  border-radius: 9px;
  color: var(--text-muted);
  font-size: 10px;
  line-height: 18px;
}

.chip--mono {
  font-family: var(--font-mono);
}

.tool {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  justify-content: space-between;
  gap: 8px;
}

.tool__owners {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 5px;
}

.ghost {
  padding: 4px 11px;
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
