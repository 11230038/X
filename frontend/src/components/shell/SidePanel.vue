<script setup lang="ts">
/**
 * 右侧面板。`设计说明书.md` §3.5：**同一个面板，三种内容，用标签页切换** ——
 * 活动时间线 / 引用与来源 / 文件预览。同一时刻只显示一件事。
 *
 * 三个标签页这里都渲染，哪怕 stage 0 只填得满第一页：标签是面板形态的一部分
 * （用户要知道这里有另外两样东西），而"只有一个标签的面板"与"有三个、其中
 * 两个还空着"是两个不同的界面。空着的标签页给出**动作**不给解释（§7.1）。
 *
 * 收合之后留一个常驻的细条（§3.5 末句）：用户要能随时看出"AI 还在干活 / 干
 * 完了"，所以那个细条上带着一个状态点，不是一个纯粹的展开箭头。
 */

import { ref } from 'vue'

export type PanelTab = 'activity' | 'sources' | 'preview'

const props = defineProps<{
  collapsed: boolean
  /** 这一轮是不是还在跑。收合时那个状态点靠它 —— 它就是 §3.5 要的那个信号。 */
  busy: boolean
  /** 收合后仍要可见的一句话，通常是在跑的那个阶段。 */
  status: string
}>()

const emit = defineEmits<{ (event: 'toggle'): void }>()

const tab = ref<PanelTab>('activity')

const TABS: readonly { key: PanelTab; label: string }[] = [
  { key: 'activity', label: '活动' },
  { key: 'sources', label: '引用' },
  { key: 'preview', label: '预览' },
]
</script>

<template>
  <!--
    收合态与展开态是**两个不同的元素**，不是一个元素改了尺寸：收合之后要留下
    的是"一个能看见的信号 + 一个展开入口"，与展开态那个有标签页标题的头部并
    不是同一块东西。用一个元素硬凹会把两者都做不好。
  -->
  <button
    v-if="props.collapsed"
    type="button"
    class="panel-rail"
    aria-label="展开右侧面板"
    @click="emit('toggle')"
  >
    <span class="panel-rail__dot" :class="{ 'panel-rail__dot--busy': props.busy }" aria-hidden="true" />
    <span class="panel-rail__text">{{ props.status }}</span>
  </button>

  <aside v-else class="panel" aria-label="右侧面板">
    <header class="panel__head">
      <div class="panel__tabs" role="tablist">
        <button
          v-for="entry in TABS"
          :key="entry.key"
          type="button"
          role="tab"
          class="panel__tab"
          :class="{ 'panel__tab--active': tab === entry.key }"
          :aria-selected="tab === entry.key"
          @click="tab = entry.key"
        >
          {{ entry.label }}
        </button>
      </div>
      <button type="button" class="panel__collapse" aria-label="收合右侧面板" @click="emit('toggle')">
        <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M10 6l6 6-6 6" /></svg>
      </button>
    </header>

    <div class="panel__body" role="tabpanel">
      <slot v-if="tab === 'activity'" name="activity" />
      <slot v-else-if="tab === 'sources'" name="sources" />
      <slot v-else name="preview" />
    </div>
  </aside>
</template>

<style scoped>
.panel {
  display: flex;
  flex-direction: column;
  height: 100%;
  border-left: 1px solid var(--border);
  background: var(--surface);
}

.panel__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 10px 8px 8px 12px;
  border-bottom: 1px solid var(--border);
}

.panel__tabs {
  display: flex;
  gap: 2px;
}

.panel__tab {
  padding: 5px 10px;
  border: 0;
  border-radius: var(--radius-sm);
  background: none;
  color: var(--text-muted);
  font-size: 13px;
  cursor: pointer;
}

.panel__tab:hover {
  background: var(--surface-sunken);
}

.panel__tab--active {
  background: var(--accent-soft);
  color: var(--accent);
  font-weight: 600;
}

.panel__collapse {
  display: grid;
  place-items: center;
  padding: 5px;
  border: 0;
  border-radius: var(--radius-sm);
  background: none;
  color: var(--text-faint);
  cursor: pointer;
}

.panel__collapse:hover {
  background: var(--surface-sunken);
  color: var(--text);
}

.panel__collapse svg {
  width: 16px;
  height: 16px;
  fill: none;
  stroke: currentcolor;
  stroke-width: 1.8;
  stroke-linecap: round;
  stroke-linejoin: round;
}

.panel__body {
  flex: 1;
  overflow-y: auto;
  padding: 12px;
}

.panel-rail {
  display: flex;
  flex-direction: column;
  gap: 10px;
  align-items: center;
  width: 40px;
  height: 100%;
  padding: 14px 0;
  border: 0;
  border-left: 1px solid var(--border);
  background: var(--surface);
  cursor: pointer;
}

.panel-rail:hover {
  background: var(--surface-sunken);
}

.panel-rail__dot {
  flex: none;
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--border-strong);
}

/* 只靠颜色不够（§6.2）：跑的时候这个点还会呼吸，静止时不动。 */
.panel-rail__dot--busy {
  background: var(--accent);
  animation: panel-pulse 1.2s ease-in-out infinite;
}

@keyframes panel-pulse {
  0%,
  100% {
    opacity: 1;
  }

  50% {
    opacity: 0.3;
  }
}

.panel-rail__text {
  color: var(--text-muted);
  font-size: 12px;
  letter-spacing: 0.08em;
  writing-mode: vertical-rl;
}

@media (prefers-reduced-motion: reduce) {
  .panel-rail__dot--busy {
    animation: none;
  }
}
</style>
