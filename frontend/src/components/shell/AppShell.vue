<script setup lang="ts">
/**
 * 四段式骨架。`设计说明书.md` §2.1 的那张图，除登录/注册外所有页面共用。
 *
 * ```
 * ┌──────────┬───────────┬──────────────────────┬──────────┐
 * │ 一级导航 │  列表栏    │  主内容区             │ 侧面板    │
 * └──────────┴───────────┴──────────────────────┴──────────┘
 * ```
 *
 * 收合状态放在这一层，不放在 `PrimaryNav` / `SidePanel` 里：**谁决定宽度谁持
 * 有状态**。两个子组件只发出"我要切换"的意图 —— 它们不知道自己的宽度，也不
 * 该知道，那是栅格的事。
 *
 * 基准画布 1440 × 900，内容区最小 1280（§2.1）。窗口更窄时由 `body` 上的
 * `min-width` 出横向滚动条，不在这里把四段挤变形。
 */

import { ref } from 'vue'

import PrimaryNav from './PrimaryNav.vue'

defineProps<{
  /** 侧面板收合时那个信号点的状态。 */
  busy?: boolean
  /** 收合后那条细栏上的一行字。 */
  panelStatus?: string
}>()

const navCollapsed = ref(false)
const panelCollapsed = ref(false)
</script>

<template>
  <div
    class="shell"
    :class="{
      'shell--nav-collapsed': navCollapsed,
      'shell--panel-collapsed': panelCollapsed,
    }"
  >
    <PrimaryNav :collapsed="navCollapsed" @toggle="navCollapsed = !navCollapsed" />

    <slot name="list" />

    <main class="shell__main">
      <slot />
    </main>

    <div class="shell__panel-wrap">
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
  grid-template-columns:
    var(--nav-width)
    var(--list-width)
    minmax(0, 1fr)
    var(--panel-width);
  height: 100vh;
  overflow: hidden;
}

.shell--nav-collapsed {
  grid-template-columns:
    var(--nav-width-collapsed)
    var(--list-width)
    minmax(0, 1fr)
    var(--panel-width);
}

.shell--panel-collapsed {
  grid-template-columns:
    var(--nav-width)
    var(--list-width)
    minmax(0, 1fr)
    40px;
}

.shell--nav-collapsed.shell--panel-collapsed {
  grid-template-columns:
    var(--nav-width-collapsed)
    var(--list-width)
    minmax(0, 1fr)
    40px;
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
