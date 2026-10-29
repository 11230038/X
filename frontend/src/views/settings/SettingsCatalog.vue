<script setup lang="ts">
/**
 * 设置下的技能 / 工具目录。
 *
 * 「技能」是服务端注册的能力（`/capabilities`），「工具」是这些能力声明的
 * 工具名。两者不是两张表：工具由能力声明，所以工具这一块只是把同一份清单
 * 反过来按工具名归组，不额外请求。原来的「扩展」一页拆成两个入口，共用这
 * 一个组件，由路由的 `view` 决定画哪一块。
 *
 * 这一块**只读**：后端没有安装、启用、停用的接口，所以这里不画开关，也不
 * 画看起来能点却永远失败的按钮。阶段表照 `shared/capabilities.ts` 的归一化
 * 结果画，空数组显示成一句明确的话而不是留白。
 */

import { computed, onMounted } from 'vue'

import EmptyState from '../../components/common/EmptyState.vue'
import FailureState from '../../components/common/FailureState.vue'
import PartialBanner from '../../components/common/PartialBanner.vue'
import SkeletonBlock from '../../components/common/SkeletonBlock.vue'
import { CATALOG } from '../../copy/catalog'
import {
  capabilityFailure,
  capabilityList,
  capabilityStatus,
  refreshCapabilities,
  toolDescriptions,
} from '../../shared/capabilities'

interface ToolRow {
  name: string
  /** 工具自己给出的那句描述；清单里没有这一项时为空。 */
  description: string
  /** 声明了这个工具的能力名，按清单顺序。 */
  capabilities: string[]
}

const props = defineProps<{ view: 'skills' | 'tools' }>()

const isSkills = computed(() => props.view === 'skills')

const tools = computed<ToolRow[]>(() => {
  const byName = new Map<string, string[]>()
  for (const capability of capabilityList.value) {
    for (const tool of capability.tools) {
      const owners = byName.get(tool)
      if (owners) owners.push(capability.name)
      else byName.set(tool, [capability.name])
    }
  }
  const descriptions = toolDescriptions.value
  return [...byName.entries()]
    .map(([name, capabilities]) => ({
      name,
      description: descriptions.get(name) ?? '',
      capabilities,
    }))
    .sort((left, right) => left.name.localeCompare(right.name))
})

const count = computed(() =>
  isSkills.value
    ? CATALOG.skillsCount(capabilityList.value.length)
    : CATALOG.toolsCount(tools.value.length),
)

function load(): void {
  void refreshCapabilities()
}

onMounted(() => {
  if (capabilityStatus.value !== 'ready') load()
})
</script>

<template>
  <section class="catalog">
    <!-- 页面标题已经写着"技能 / 工具"，这里只留条数与动作，不再重复一次名字。 -->
    <header class="catalog__head">
      <span v-if="capabilityList.length > 0" class="set-chip">{{ count }}</span>
      <button
        type="button"
        class="set-btn set-btn--ghost set-btn--sm"
        :disabled="capabilityStatus === 'loading'"
        @click="load"
      >
        {{ capabilityStatus === 'loading' ? CATALOG.refreshing : CATALOG.refresh }}
      </button>
    </header>

    <p v-if="isSkills" class="note">{{ CATALOG.stageNote }}</p>

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

    <p v-else-if="!isSkills && tools.length === 0" class="note">{{ CATALOG.toolsEmpty }}</p>

    <ul v-else-if="isSkills" class="cards">
      <li v-for="capability in capabilityList" :key="capability.name" class="card">
        <header class="card__head">
          <code class="card__name">{{ capability.name }}</code>
          <span class="card__description">{{ capability.description }}</span>
        </header>

        <div class="row">
          <span class="row__label">{{ CATALOG.stages }}</span>
          <template v-if="capability.stages.length > 0">
            <span v-for="(stage, index) in capability.stages" :key="stage" class="set-chip">
              {{ index + 1 }}. {{ stage }}
            </span>
          </template>
          <span v-else class="row__note">{{ CATALOG.noStages }}</span>
        </div>

        <div class="row">
          <span class="row__label">{{ CATALOG.toolsTitle }}</span>
          <template v-if="capability.tools.length > 0">
            <code v-for="tool in capability.tools" :key="tool" class="set-chip set-chip--mono">
              {{ tool }}
            </code>
          </template>
          <span v-else class="row__note">{{ CATALOG.noTools }}</span>
        </div>
      </li>
    </ul>

    <ul v-else class="tools">
      <li v-for="tool in tools" :key="tool.name" class="tool">
        <div class="tool__head">
          <code class="tool__name">{{ tool.name }}</code>
          <p v-if="tool.description" class="tool__description">{{ tool.description }}</p>
        </div>
        <div class="tool__owners">
          <span class="row__label">{{ CATALOG.usedBy }}</span>
          <code v-for="owner in tool.capabilities" :key="owner" class="set-chip set-chip--mono">
            {{ owner }}
          </code>
        </div>
      </li>
    </ul>
  </section>
</template>

<style scoped src="./SettingsCatalog.css"></style>
