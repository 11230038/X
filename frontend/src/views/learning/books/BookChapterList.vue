<script setup lang="ts">
/**
 * 章节目录。
 *
 * 每一行说三件事：第几章、叫什么、有没有正文。状态用文字而不是只用颜色
 * （`设计文档` §6.2 硬规则 3），所以徽标里永远有"未生成 / 已生成 / 生成失败"。
 */

import type { BookOutlineItem } from '../../../api/books'
import StatusBadge from '../../../components/common/StatusBadge.vue'
import { BOOKS } from '../../../copy/books'

defineProps<{
  outline: BookOutlineItem[]
  selected: number
  /** 有生成在跑：不让切换章节，避免把正在看的那一章换掉。 */
  busy: boolean
  activeOrdinal: number
}>()

defineEmits<{
  select: [ordinal: number]
}>()
</script>

<template>
  <section class="chapters">
    <h2 class="chapters__title">{{ BOOKS.chaptersTitle }}</h2>

    <ol class="chapters__list">
      <li v-for="item in outline" :key="item.ordinal">
        <button
          type="button"
          class="chapter"
          :class="{ 'chapter--active': item.ordinal === selected }"
          :aria-current="item.ordinal === selected ? 'true' : undefined"
          :disabled="busy && item.ordinal !== activeOrdinal"
          @click="$emit('select', item.ordinal)"
        >
          <span class="chapter__ordinal">{{ item.ordinal }}</span>
          <span class="chapter__body">
            <span class="chapter__title">{{ item.title }}</span>
            <span v-if="item.summary" class="chapter__summary">{{ item.summary }}</span>
          </span>
          <StatusBadge
            :label="item.ordinal === activeOrdinal ? BOOKS.chapterGenerating : BOOKS.statusLabel(item.status)"
            :tone="item.ordinal === activeOrdinal ? 'accent' : BOOKS.statusTone(item.status)"
            small
          />
        </button>
      </li>
    </ol>
  </section>
</template>

<style scoped src="./books.css"></style>
<style scoped>
.chapters {
  display: flex;
  flex-direction: column;
  gap: 8px;
  min-width: 0;
  padding: 18px;
  border: 1px solid var(--glass-border);
  border-radius: var(--radius-card);
  background: var(--glass-bg);
  box-shadow: var(--glass-shadow), var(--inset-light);
  backdrop-filter: blur(24px);
  -webkit-backdrop-filter: blur(24px);
}

.chapters__title {
  margin: 0;
  font-size: 15px;
  font-weight: 800;
}

.chapters__list {
  display: flex;
  flex-direction: column;
  gap: 3px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.chapter {
  display: flex;
  gap: 8px;
  align-items: flex-start;
  width: 100%;
  padding: 9px 10px;
  border: 1px solid transparent;
  border-radius: 14px;
  background: transparent;
  text-align: left;
  cursor: pointer;
  transition: all 0.2s ease;
}

.chapter:hover {
  border-color: rgb(255 255 255 / 70%);
  background: rgb(255 255 255 / 58%);
}

.chapter:disabled {
  cursor: default;
  opacity: 0.5;
}

.chapter--active {
  border-color: rgb(177 140 255 / 30%);
  background: #fff;
  box-shadow: 0 4px 14px rgb(51 68 85 / 6%);
}

.chapter__ordinal {
  flex: none;
  width: 18px;
  color: var(--text-faint);
  font-family: var(--font-mono);
  font-size: 14px;
  line-height: 1.7;
  text-align: right;
}

.chapter__body {
  display: flex;
  flex: 1;
  flex-direction: column;
  gap: 1px;
  min-width: 0;
}

.chapter__title {
  overflow: hidden;
  color: var(--text);
  font-size: 14px;
  line-height: 1.5;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.chapter__summary {
  overflow: hidden;
  color: var(--text-faint);
  font-size: 14px;
  line-height: 1.4;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.chapter > :deep(.badge) {
  flex: none;
  margin-top: 2px;
}
</style>
