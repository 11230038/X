<script setup lang="ts">
/**
 * 右侧面板的「活动时间线」。`设计说明书.md` §3.5 第 1 条：AI 正在做什么 ——
 * 思考、调用工具、查到什么、走到阶段几步之几。**这是产品的透明度所在。**
 *
 * 它是会话流的**一个视图**，不是第二份状态：事件只到一次，会话流按"回答"的
 * 顺序画，这里按"动作"的顺序画。两处都从同一份 `Block[]` 来，所以不会出现
 * 面板说走到了第 2 步而流里还停在第 1 步。
 *
 * §3.5 特别提醒"走到几步之几"有**两种来源，不要混**：一种是阶段（能力的阶段
 * 表，数量与名字随能力变），一种是同一阶段内部的多步进度（`progress` 事件）。
 * 两者在这里是两种不同的行：阶段是一枚实心点，进度带自己的 `步/总`。
 */

import { computed } from 'vue'

import type { Block } from '../../ws/blocks'
import { CHAT } from '../../copy/chat'

const props = defineProps<{ blocks: readonly Block[] }>()

/** 只有这几类进得了时间线。回答与用户消息是"内容"，不是"动作"。 */
const entries = computed(() =>
  props.blocks.filter(
    (block) =>
      block.kind === 'stage' ||
      block.kind === 'tool' ||
      block.kind === 'error' ||
      block.kind === 'thinking' ||
      block.kind === 'observation' ||
      block.kind === 'progress' ||
      block.kind === 'sources',
  ),
)
</script>

<template>
  <div v-if="entries.length === 0" class="timeline__empty">
    <!--
      §7.1 第 2 条：空状态给动作，不给解释。这里的动作就是下面那个输入框，
      所以这一格不放任何文字 —— 放一句话解释"这里将来会显示什么"正是被禁的
      那类小字。
    -->
  </div>

  <ol v-else class="timeline">
    <li v-for="(entry, index) in entries" :key="index" class="timeline__item">
      <template v-if="entry.kind === 'stage'">
        <span class="timeline__dot" :class="{ 'timeline__dot--done': entry.done }" aria-hidden="true" />
        <span class="timeline__label">
          {{ entry.name }}
          <span v-if="entry.index > 0" class="timeline__count">{{ entry.index }}/{{ entry.total }}</span>
        </span>
      </template>

      <template v-else-if="entry.kind === 'tool'">
        <span class="timeline__dot timeline__dot--tool" aria-hidden="true" />
        <span class="timeline__label">
          <span class="timeline__tool">{{ entry.name }}</span>
          <span v-if="entry.ok === false" class="timeline__fail">失败</span>
        </span>
      </template>

      <template v-else-if="entry.kind === 'thinking'">
        <span class="timeline__dot timeline__dot--quiet" aria-hidden="true" />
        <span class="timeline__label timeline__label--quiet">{{ CHAT.thinking }}</span>
      </template>

      <template v-else-if="entry.kind === 'observation'">
        <span class="timeline__dot timeline__dot--quiet" aria-hidden="true" />
        <span class="timeline__label">{{ CHAT.observation }} · {{ entry.text }}</span>
      </template>

      <template v-else-if="entry.kind === 'progress'">
        <span class="timeline__dot timeline__dot--progress" aria-hidden="true" />
        <span class="timeline__label">
          {{ entry.label || CHAT.progress }}
          <span v-if="entry.total > 0" class="timeline__count">
            {{ entry.step }}/{{ entry.total }}
          </span>
        </span>
      </template>

      <template v-else-if="entry.kind === 'sources'">
        <span class="timeline__dot timeline__dot--quiet" aria-hidden="true" />
        <span class="timeline__label">
          {{ CHAT.sourceTitle }} · {{ entry.sources.length }}
        </span>
      </template>

      <template v-else>
        <span class="timeline__dot timeline__dot--error" aria-hidden="true" />
        <span class="timeline__label">{{ entry.text }}</span>
      </template>
    </li>
  </ol>
</template>

<style scoped>
.timeline {
  display: flex;
  flex-direction: column;
  gap: 2px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.timeline__item {
  display: flex;
  gap: 9px;
  align-items: baseline;
  padding: 5px 2px;
  font-size: 13px;
}

.timeline__dot {
  flex: none;
  width: 6px;
  height: 6px;
  border: 1px solid var(--border-strong);
  border-radius: 50%;
  background: var(--surface);
}

.timeline__dot--done {
  border-color: var(--success);
  background: var(--success);
}

.timeline__dot--tool {
  border-radius: 1px;
  border-color: var(--accent);
  background: var(--accent);
}

.timeline__dot--error {
  border-color: var(--danger);
  background: var(--danger);
}

.timeline__dot--quiet {
  border-color: var(--border);
}

.timeline__dot--progress {
  border-color: var(--warning);
  background: var(--warning);
}

.timeline__label--quiet {
  color: var(--text-faint);
}

.timeline__label {
  color: var(--text-muted);
  overflow-wrap: anywhere;
}

.timeline__count {
  margin-left: 6px;
  color: var(--text-faint);
  font-size: 12px;
}

.timeline__tool {
  font-family: var(--font-mono);
  font-size: 12px;
}

.timeline__fail {
  margin-left: 6px;
  color: var(--danger);
}
</style>
