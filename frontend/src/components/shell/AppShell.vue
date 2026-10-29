<script setup lang="ts">
/**
 * 四段式骨架。`设计说明书.md` §2.1 的那张图，尺寸照演示稿：
 *
 * ```
 * ┌────┬──────────┬──────────────────────┬──────────┐
 * │72px│  240px   │      主内容区         │  320px   │
 * │导航│ 二级侧栏  │                      │ 上下文面板│
 * └────┴──────────┴──────────────────────┴──────────┘
 * ```
 *
 * 第二列有两种来源：页面自己的列表（`#list` 插槽 —— 会话、知识库），或者按
 * 当前空间自动出现的二级侧栏（`SectionSidebar`）。**页面给了列表就用列表**，
 * 两者不会同时出现。没有列表、路由也没有分组时那一列不占宽度，而不是留一条
 * 空栅格。
 *
 * 两来源只在一种情况下宽度不同：窗口窄于设计基准 1440 时，二级侧栏收成
 * `--rail-width`（80px 图标栏，见 `SectionSidebar` 的媒体查询），页面自己的
 * 列表仍占 `--list-width`（280px）—— 列表的内容就是文字，收成图标没有意义。
 *
 * 上下文面板的收合状态放在这一层，不放在 `SidePanel` 里：**谁决定宽度谁持有
 * 状态**，子组件只发出"我要切换"的意图。
 *
 * 基准画布 1440 × 900，内容区最小 1280（§2.1）。窗口更窄时由 `body` 上的
 * `min-width` 出横向滚动条，不在这里把四段挤变形。
 */

import { computed, ref, useSlots } from 'vue'
import { useRoute } from 'vue-router'

import PrimaryNav from './PrimaryNav.vue'
import SectionSidebar from './SectionSidebar.vue'
import { sectionFor } from './sections'

defineProps<{
  /** 侧面板收合时那个信号点的状态。 */
  busy?: boolean
  /** 收合后那条细栏上的一行字。 */
  panelStatus?: string
}>()

const slots = useSlots()
const route = useRoute()
const panelCollapsed = ref(false)

const hasList = computed(() => Boolean(slots.list))
const hasSection = computed(() => !hasList.value && sectionFor(route) !== null)

const columns = computed(() => {
  const sidebar = hasList.value
    ? ' var(--list-width)'
    : hasSection.value
      ? ' var(--section-width)'
      : ''
  const main = ' minmax(0, 1fr)'
  const panel = slots.panel
    ? panelCollapsed.value
      ? ' 40px'
      : ' var(--panel-width)'
    : ''
  return `var(--nav-width)${sidebar}${main}${panel}`
})
</script>

<template>
  <div class="shell" :style="{ gridTemplateColumns: columns }">
    <PrimaryNav />

    <slot v-if="hasList" name="list" />
    <SectionSidebar v-else-if="hasSection" />

    <main class="shell__main">
      <slot />
    </main>

    <div v-if="$slots.panel" class="shell__panel-wrap">
      <slot
        name="panel"
        :collapsed="panelCollapsed"
        :toggle="() => (panelCollapsed = !panelCollapsed)"
      />
    </div>
  </div>
</template>

<style scoped>
/*
 * 三块玻璃（导航、侧栏、上下文面板）浮在极光底上，中间那一列是**裸的**：演示
 * 稿的 `.main-container` 只有 `flex: 1`，没有底色、没有描边、没有圆角，页面
 * 内容直接落在极光上，靠自己的卡片与留白成形。这里照它，别给正文加边框 ——
 * 加了之后正文就成了"四块玻璃"里的第四块，和演示稿对不上。
 */
.shell {
  /* 二级侧栏当前的宽度。默认就是整条侧栏；窗口窄于设计基准时换成图标栏的
     宽度 —— 换的是这一条自定义属性，`gridTemplateColumns` 那一行不用改。 */
  --section-width: var(--list-width);

  display: grid;
  height: 100vh;
  /* 基准画布 1440 × 900。窗口比 900 还矮时，骨架就按 900 排版，让窗口出
     纵向滚动条 —— 四栏内部的滚动照旧，整页滚动只负责把下面那截拖出来。 */
  min-height: 900px;
  padding: var(--shell-pad);
  gap: var(--shell-gap);
  overflow: hidden;
}

@media (max-width: 1439.98px) {
  .shell {
    --section-width: var(--rail-width);
  }
}

.shell__main {
  /* `minmax(0, 1fr)` 上面那一格 + 这里的 `min-width: 0` 是一件事：没有它们，
     内容区里一条不换行的长内容会把整格撑宽，四段式栅格会被撑破。 */
  min-width: 0;
  overflow: hidden;
}

.shell__panel-wrap {
  min-width: 0;
  overflow: hidden;
}
</style>
