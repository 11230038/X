<script setup lang="ts">
/**
 * 引用面板里点开一条来源之后的样子。
 *
 * 阅读页与文件库是两条不同的出口：前者能定位到同一套字符坐标，后者只能打开
 * 整份文件。材料已经没有了的时候，阅读页那条出口不出现（而不是出现一个点了
 * 报错的按钮），文件文本那条仍在。
 */

import type { SourceItem } from '../../ws/blocks'
import { CHAT } from '../../copy/chat'
import EmptyState from '../common/EmptyState.vue'

defineProps<{
  source: SourceItem | null
  /** 这条来源能对应上的阅读材料；空串表示对不上。 */
  materialId: string
  resolving: boolean
}>()

const emit = defineEmits<{
  (event: 'open-reading'): void
  (event: 'open-file'): void
  (event: 'clear'): void
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
    v-if="!source"
    title="还没有打开引用"
    hint="在引用标签里点一条来源，这里会显示原文片段与打开方式。"
  />

  <div v-else class="preview">
    <header class="preview__head">
      <span class="preview__index">{{ CHAT.sourceIndex(source.index) }}</span>
      <button type="button" class="preview__back" @click="emit('clear')">返回列表</button>
    </header>

    <h3 class="preview__title">{{ source.title }}</h3>
    <p class="preview__locator">{{ locatorText(source) }}</p>
    <pre class="preview__snippet">{{ source.snippet }}</pre>

    <div class="preview__actions">
      <button
        type="button"
        class="preview__action"
        :disabled="resolving || materialId === ''"
        :title="materialId === '' ? CHAT.sourceUnavailable : ''"
        @click="emit('open-reading')"
      >
        {{ resolving ? '正在定位…' : CHAT.sourceOpenReading }}
      </button>
      <button type="button" class="preview__secondary" @click="emit('open-file')">
        {{ CHAT.sourceOpenFile }}
      </button>
    </div>
  </div>
</template>

<style scoped>
.preview {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.preview__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.preview__index {
  color: var(--text-faint);
  font-size: 11px;
}

.preview__back {
  padding: 2px 7px;
  border: 1px solid var(--border);
  border-radius: 4px;
  background: transparent;
  color: var(--text-muted);
  font-size: 11px;
  cursor: pointer;
}

.preview__title {
  margin: 0;
  font-size: 13px;
  font-weight: 650;
  overflow-wrap: anywhere;
}

.preview__locator {
  margin: 0;
  color: var(--text-faint);
  font-size: 11px;
}

.preview__snippet {
  max-height: 320px;
  margin: 0;
  padding: 10px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--surface-sunken);
  overflow: auto;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  color: var(--text);
  font-family: inherit;
  font-size: 12px;
  line-height: 1.65;
}

.preview__actions {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.preview__action {
  padding: 6px 10px;
  border: 1px solid var(--accent);
  border-radius: var(--radius-sm);
  background: var(--accent);
  color: #fff;
  font-size: 12px;
  cursor: pointer;
}

.preview__action:disabled {
  border-color: var(--border);
  background: var(--surface-sunken);
  color: var(--text-faint);
  cursor: default;
}

.preview__secondary {
  padding: 6px 10px;
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--text-muted);
  font-size: 12px;
  cursor: pointer;
}
</style>
