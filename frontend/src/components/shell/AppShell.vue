<script setup lang="ts">
/**
 * 四段式骨架。`设计说明书.md` §2.1 的那张图。
 *
 * ```
 * ┌──────────┬───────────┬──────────────────────┬──────────┐
 * │ 一级导航 │  列表栏    │  主内容区             │ 侧面板    │
 * └──────────┴───────────┴──────────────────────┴──────────┘
 * ```
 *
 * 不是每一页都需要列表栏与侧面板：只有对话、文件库这类"选了左边看右边"的
 * 页面有列表，只有对话有侧面板。**没有对应 slot 时那一列不占宽度**，而不是
 * 留一条空栅格 —— 否则一个纯阅读页会凭空少掉 600px。
 *
 * 收合状态放在这一层，不放在 `PrimaryNav` / `SidePanel` 里：**谁决定宽度谁持
 * 有状态**。两个子组件只发出"我要切换"的意图。
 *
 * 基准画布 1440 × 900，内容区最小 1280（§2.1）。窗口更窄时由 `body` 上的
 * `min-width` 出横向滚动条，不在这里把四段挤变形。
 */

import { computed, ref, useSlots } from 'vue'

import PrimaryNav from './PrimaryNav.vue'

defineProps<{
  /** 侧面板收合时那个信号点的状态。 */
  busy?: boolean
  /** 收合后那条细栏上的一行字。 */
  panelStatus?: string
}>()

const slots = useSlots()
const navCollapsed = ref(false)
const panelCollapsed = ref(false)

const hasList = computed(() => Boolean(slots.list))
const hasPanel = computed(() => Boolean(slots.panel))

const columns = computed(() => {
  const nav = navCollapsed.value ? 'var(--nav-width-collapsed)' : 'var(--nav-width)'
  const list = hasList.value ? ' var(--list-width)' : ''
  const main = ' minmax(0, 1fr)'
  const panel = hasPanel.value
    ? panelCollapsed.value
      ? ' 40px'
      : ' var(--panel-width)'
    : ''
  return `${nav}${list}${main}${panel}`
})
</script>

<template>
  <div class="shell" :style="{ gridTemplateColumns: columns }">
    <PrimaryNav :collapsed="navCollapsed" @toggle="navCollapsed = !navCollapsed" />

    <slot name="list" />

    <main class="shell__main">
      <slot />
    </main>

    <div v-if="hasPanel" class="shell__panel-wrap">
      <slot
        name="panel"
        :collapsed="panelCollapsed"
        :toggle="() => (panelCollapsed = !panelCollapsed)"
      />
    </div>
  </div>
</template>

<style scoped>
.shell {
  display: grid;
  height: 100vh;
  overflow: hidden;
}

.shell__main {
  /* `minmax(0, 1fr)` 上面那一格 + 这里的 `min-width: 0` 是一件事：没有它们，
     内容区里一条不换行的长内容会把整格撑宽，四段式栅格会被撑破。 */
  min-width: 0;
  overflow: hidden;
  background: var(--surface-sunken);
}

.shell__panel-wrap {
  min-width: 0;
  overflow: hidden;
}
</style>
