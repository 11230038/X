<script setup lang="ts">
/**
 * 引用面板。来源是服务端从自己的行里读出来的：页码、字符区间、片段都不经过
 * 模型，所以这里不补、不改、不排序 —— 只按服务端给的顺序铺开。
 *
 * 一个来源可以在阅读页打开（材料还在），也可以退回文件文本（材料没了但文件
 * 在）。两样都不行时明说不可用，不给一个点了没反应的按钮。
 */

import type { SourceItem } from '../../ws/blocks'
import { CHAT } from '../../copy/chat'
import EmptyState from '../common/EmptyState.vue'

defineProps<{
  sources: readonly SourceItem[]
}>()

const emit = defineEmits<{
  (event: 'open', source: SourceItem): void
  (event: 'go-files'): void
}>()

function locatorText(source: SourceItem): string {
  const parts: string[] = []
  if (typeof source.locator.page === 'number') {
    parts.push(CHAT.sourcePage(source.locator.page))
  }
  parts.push(CHAT.sourceChars(source.locator.char_start, source.locator.char_end))
  return parts.join(' · ')
}
</script>

<template>
  <EmptyState
    v-if="sources.length === 0"
    :title="CHAT.sourceEmpty"
    action-label="去文件库"
    @action="emit('go-files')"
  />

  <ol v-else class="sources">
    <li v-for="source in sources" :key="`${source.index}-${source.document_id}`" class="sources__item">
      <button type="button" class="sources__open" @click="emit('open', source)">
        <span class="sources__index">{{ source.index }}</span>
        <span class="sources__body">
          <span class="sources__title">{{ source.title || CHAT.sourceIndex(source.index) }}</span>
          <span class="sources__locator">{{ locatorText(source) }}</span>
          <span class="sources__snippet">{{ source.snippet }}</span>
        </span>
      </button>
    </li>
  </ol>
</template>

<style scoped>
.sources {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.sources__open {
  display: flex;
  gap: 9px;
  width: 100%;
  padding: 9px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--surface);
  text-align: left;
  cursor: pointer;
}

.sources__open:hover {
  border-color: var(--accent);
}

.sources__index {
  flex: none;
  display: grid;
  place-items: center;
  width: 18px;
  height: 18px;
  border-radius: 9px;
  background: var(--accent-soft);
  color: var(--accent);
  font-family: var(--font-mono);
  font-size: 11px;
}

.sources__body {
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.sources__title {
  overflow: hidden;
  font-size: 12px;
  font-weight: 600;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.sources__locator {
  color: var(--text-faint);
  font-size: 11px;
}

.sources__snippet {
  display: -webkit-box;
  overflow: hidden;
  color: var(--text-muted);
  font-size: 12px;
  line-height: 1.55;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 3;
}
</style>
