<script setup lang="ts">
/**
 * 练习页的右侧面板：复习轨迹 / 错题解析。
 *
 * 与知识库的 `KbOperationsPanel` 同一套壳：展开时是一块玻璃面板（顶部两个
 * Tab + 收合按钮 + 滚动正文），收合后留一条 40px 竖条，图标与竖排标题都在，
 * 随时能展开。
 *
 * 两个 Tab 各自读失败只影响自己那一栏：统计读不出来不妨碍解析，解析出不来也
 * 不妨碍做题。
 */

import { computed, ref, watch } from 'vue'

import { canSurface } from '../../../auth/permissions'
import { PRACTICE } from '../../../copy/practice'

import PracticeAnalysis from './PracticeAnalysis.vue'
import PracticeStats from './PracticeStats.vue'
import AppIcon from '../../../components/common/AppIcon.vue'

defineProps<{
  collapsed: boolean
}>()

const emit = defineEmits<{
  toggle: []
}>()

type Tab = 'tracker' | 'analysis'

const tab = ref<Tab>('tracker')

/**
 * 解析走的是 `/chat/complete`，那条路由挂在 `chat` 面上 —— 只有练习面、没有
 * 对话面的账号会被后端拒。权限明确为 false 时干脆不画这一栏；`'unknown'`
 * 照常出现，后端每个请求还会再判一次。
 */
const analysisAllowed = computed(() => canSurface('chat') !== false)

watch(analysisAllowed, (allowed) => {
  if (!allowed) tab.value = 'tracker'
})
</script>

<template>
  <button
    v-if="collapsed"
    type="button"
    class="stats-rail"
    aria-label="展开练习统计"
    @click="emit('toggle')"
  >
    <span class="stats-rail__icon" aria-hidden="true"><AppIcon name="chart" /></span>
    <span class="stats-rail__text">{{ PRACTICE.statsTitle }}</span>
  </button>

  <aside v-else class="stats-panel" aria-label="练习统计">
    <header class="stats-panel__head">
      <div class="tracker-tabs" role="tablist" aria-label="练习统计">
        <button
          type="button"
          role="tab"
          class="tracker-tab"
          :class="{ 'tracker-tab--on': tab === 'tracker' }"
          :aria-selected="tab === 'tracker'"
          @click="tab = 'tracker'"
        >
          {{ PRACTICE.tabTracker }}
        </button>
        <button
          v-if="analysisAllowed"
          type="button"
          role="tab"
          class="tracker-tab"
          :class="{ 'tracker-tab--on': tab === 'analysis' }"
          :aria-selected="tab === 'analysis'"
          @click="tab = 'analysis'"
        >
          {{ PRACTICE.tabAnalysis }}
        </button>
      </div>

      <button
        type="button"
        class="stats-panel__collapse"
        aria-label="收合右侧面板"
        @click="emit('toggle')"
      >
        <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M10 6l6 6-6 6" /></svg>
      </button>
    </header>

    <div class="stats-panel__body">
      <!-- 两栏都留在 DOM 里：切回轨迹不该再打一次 `/practice/stats`。 -->
      <PracticeStats v-show="tab === 'tracker'" />
      <PracticeAnalysis v-show="tab === 'analysis'" />
    </div>
  </aside>
</template>

<style scoped src="../Practice.css"></style>
