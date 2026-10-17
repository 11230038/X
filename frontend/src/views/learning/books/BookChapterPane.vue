<script setup lang="ts">
/**
 * 一章正文。
 *
 * 三种状态各自长什么样：
 * - `ready`：直接画 Markdown；正文一次性渲染，不做流式（生成是整章返回的）；
 * - `pending`：说明还没有正文，并给"生成本章"；
 * - `failed`：说明上一次没成功，并给"重试本章"。
 *
 * 生成中的这一章不显示旧正文，而是显示进度说明 —— 让人看着一段马上要被
 * 替换的旧文字，比看一句"正在生成"更让人以为是卡住了。
 */

import { computed } from 'vue'

import type { ApiFailure } from '../../../api/client'
import type { BookChapter, BookOutlineItem } from '../../../api/books'
import { describeFailure } from '../../../api/errors'
import EmptyState from '../../../components/common/EmptyState.vue'
import MarkdownBody from '../../../components/common/MarkdownBody.vue'
import SkeletonBlock from '../../../components/common/SkeletonBlock.vue'
import StatusBadge from '../../../components/common/StatusBadge.vue'
import { BOOKS } from '../../../copy/books'

const props = defineProps<{
  item: BookOutlineItem | null
  chapter: BookChapter | null
  status: 'idle' | 'loading' | 'ready' | 'failed'
  loadFailure: ApiFailure | null
  generating: boolean
}>()

defineEmits<{
  generate: []
  retryLoad: []
}>()

const title = computed(() => props.item?.title ?? '')
const hasBody = computed(
  () => props.chapter !== null && props.chapter.status === 'ready' && props.chapter.content !== '',
)
const failed = computed(() => props.item?.status === 'failed')
</script>

<template>
  <section class="pane">
    <EmptyState
      v-if="!item"
      :title="BOOKS.selectChapter"
      :hint="BOOKS.selectChapterHint"
    />

    <template v-else>
      <header class="pane__head">
        <div class="pane__heading">
          <span class="pane__ordinal">{{ BOOKS.chapterNumber(item.ordinal) }}</span>
          <h2 class="pane__title">{{ title }}</h2>
        </div>
        <div class="book-actions">
          <StatusBadge
            :label="generating ? BOOKS.chapterGenerating : BOOKS.statusLabel(item.status)"
            :tone="generating ? 'accent' : BOOKS.statusTone(item.status)"
            small
          />
          <button
            v-if="hasBody && !generating"
            type="button"
            class="book-ghost"
            @click="$emit('generate')"
          >
            {{ BOOKS.retryChapter }}
          </button>
        </div>
      </header>

      <p v-if="item.summary && !hasBody" class="pane__summary">{{ item.summary }}</p>

      <p v-if="generating" class="pane__hint">
        {{ BOOKS.generatingChapter(item.ordinal) }}
      </p>
      <SkeletonBlock v-if="generating" :lines="8" :line-height="32" />

      <template v-else-if="status === 'loading'">
        <SkeletonBlock :lines="8" :line-height="32" />
      </template>

      <div v-else-if="status === 'failed' && loadFailure" class="book-error">
        <span>
          {{ BOOKS.chapterLoadFailed }}：{{ describeFailure(loadFailure) }}
          <code v-if="loadFailure.requestId" class="book-error__req">
            {{ loadFailure.requestId }}
          </code>
        </span>
        <button type="button" class="book-ghost" @click="$emit('retryLoad')">重试</button>
      </div>

      <template v-else-if="hasBody && chapter">
        <MarkdownBody :text="chapter.content" complete />
      </template>

      <template v-else-if="failed">
        <p class="book-inline-warn">{{ BOOKS.chapterFailedHint }}</p>
        <button type="button" class="book-primary" @click="$emit('generate')">
          {{ BOOKS.retryChapter }}
        </button>
      </template>

      <template v-else>
        <p class="book-inline-warn">{{ BOOKS.chapterPendingHint }}</p>
        <button type="button" class="book-primary" @click="$emit('generate')">
          {{ BOOKS.generateChapter }}
        </button>
      </template>
    </template>
  </section>
</template>

<style scoped src="./books.css"></style>
<style scoped>
.pane {
  display: flex;
  flex-direction: column;
  gap: 12px;
  min-width: 0;
  padding: 18px 20px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--surface);
}

.pane__head {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  align-items: flex-start;
  justify-content: space-between;
  padding-bottom: 10px;
  border-bottom: 1px solid var(--border);
}

.pane__heading {
  min-width: 0;
}

.pane__ordinal {
  color: var(--text-faint);
  font-size: 11px;
}

.pane__title {
  margin: 2px 0 0;
  color: var(--text);
  font-size: 17px;
  font-weight: 650;
  line-height: 1.4;
}

.pane__summary {
  margin: 0;
  color: var(--text-muted);
  font-size: 12px;
  line-height: 1.6;
}

.pane__hint {
  margin: 0;
  color: var(--text-muted);
  font-size: 12px;
}
</style>
