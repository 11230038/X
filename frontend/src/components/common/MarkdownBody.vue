<script setup lang="ts">
/**
 * 全应用**唯一**允许 `v-html` 的组件。
 *
 * HTML 只来自 `src/markdown/render.ts`（markdown-it + DOMPurify），这里不
 * 拼接任何字符串。流式回答每来一段就重渲染一次代价太高，所以按约 100ms
 * 节流；`complete` 一翻真就立即落到权威全文，不再等节流窗口。
 *
 * 引用按钮用事件委托处理：HTML 是字符串，挂不了 Vue 事件。
 */

import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { renderMarkdown } from '../../markdown/render'

const props = withDefaults(
  defineProps<{
    text: string
    /** 流式结束。true 时立刻渲染 `text` 的最后版本。 */
    complete?: boolean
    /** 能对上的引用编号；对不上的编号按普通文本画。 */
    citations?: ReadonlySet<number>
  }>(),
  {
    complete: false,
    citations: () => new Set<number>(),
  },
)

const emit = defineEmits<{
  citation: [index: number]
}>()

const RENDER_INTERVAL_MS = 100

const shown = ref(props.text)
let timer: number | null = null
let lastRender = 0

function clearTimer(): void {
  if (timer !== null) {
    window.clearTimeout(timer)
    timer = null
  }
}

function commit(next: string): void {
  clearTimer()
  shown.value = next
  lastRender = performance.now()
}

watch(
  () => props.text,
  (next) => {
    if (props.complete) {
      commit(next)
      return
    }
    const wait = RENDER_INTERVAL_MS - (performance.now() - lastRender)
    if (wait <= 0) {
      commit(next)
      return
    }
    if (timer === null) {
      timer = window.setTimeout(() => {
        timer = null
        commit(props.text)
      }, wait)
    }
  },
)

watch(
  () => props.complete,
  (done) => {
    if (done) commit(props.text)
  },
)

onBeforeUnmount(clearTimer)

const html = computed(() => renderMarkdown(shown.value, { citations: props.citations }))

function handleClick(event: MouseEvent): void {
  const target = event.target
  if (!(target instanceof Element)) return
  const button = target.closest('button[data-citation]')
  if (!(button instanceof HTMLElement)) return
  const index = Number(button.dataset.citation)
  if (!Number.isFinite(index)) return
  event.preventDefault()
  emit('citation', index)
}
</script>

<template>
  <div class="markdown" @click="handleClick" v-html="html"></div>
</template>

<style scoped>
/* 演示稿的正文是 16px（`.bubble-*` 继承 body、`.reader-text p` 明写 16）。
   这一层同时供对话气泡、书籍章节与阅读助手用，所以按正文尺度定；现在整体
   抬到 18px —— 1440 宽的玻璃卡里 16px 偏细。对话页的回答在
   `ConversationStream` 里再大一档（20px）。 */
.markdown {
  font-size: 18px;
  line-height: 1.8;
  color: var(--text);
  overflow-wrap: anywhere;
}

.markdown :deep(> :first-child) {
  margin-top: 0;
}

.markdown :deep(> :last-child) {
  margin-bottom: 0;
}

.markdown :deep(p) {
  margin: 0 0 0.8em;
}

.markdown :deep(h1),
.markdown :deep(h2),
.markdown :deep(h3),
.markdown :deep(h4),
.markdown :deep(h5),
.markdown :deep(h6) {
  margin: 1.1em 0 0.5em;
  line-height: 1.35;
  font-weight: 600;
}

.markdown :deep(h1) {
  font-size: 1.45em;
}
.markdown :deep(h2) {
  font-size: 1.28em;
}
.markdown :deep(h3) {
  font-size: 1.14em;
}
.markdown :deep(h4),
.markdown :deep(h5),
.markdown :deep(h6) {
  font-size: 1em;
}

.markdown :deep(ul),
.markdown :deep(ol) {
  margin: 0 0 0.8em;
  padding-left: 1.5em;
}

.markdown :deep(li) {
  margin: 0.18em 0;
}

.markdown :deep(blockquote) {
  margin: 0 0 0.7em;
  padding: 0.2em 0 0.2em 0.9em;
  border-left: 3px solid var(--border-strong);
  color: var(--text-muted);
}

.markdown :deep(hr) {
  margin: 1.2em 0;
  border: 0;
  border-top: 1px solid var(--border);
}

.markdown :deep(a) {
  color: var(--accent-ink);
  text-decoration: underline;
  text-underline-offset: 2px;
}

.markdown :deep(code) {
  font-family: var(--font-mono);
  font-size: 0.9em;
}

.markdown :deep(:not(pre) > code) {
  padding: 1px 4px;
  border: 1px solid var(--border);
  border-radius: 4px;
  background: var(--surface-sunken);
}

.markdown :deep(pre) {
  margin: 0 0 0.8em;
  padding: 14px 16px;
  border: 1px solid rgb(255 255 255 / 72%);
  border-radius: var(--radius-sm);
  background: rgb(255 255 255 / 52%);
  overflow-x: auto;
  line-height: 1.55;
}

.markdown :deep(pre code) {
  padding: 0;
  border: 0;
  background: transparent;
}

.markdown :deep(table) {
  display: block;
  width: max-content;
  max-width: 100%;
  margin: 0 0 0.8em;
  border-collapse: collapse;
  overflow-x: auto;
}

.markdown :deep(th),
.markdown :deep(td) {
  padding: 5px 9px;
  border: 1px solid var(--border);
  text-align: left;
}

.markdown :deep(th) {
  background: var(--surface-sunken);
  font-weight: 600;
}

.markdown :deep(.math-block) {
  margin: 0.6em 0;
  overflow-x: auto;
}

.markdown :deep(.citation) {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 18px;
  height: 18px;
  margin: 0 2px;
  padding: 0 4px;
  border: 1px solid var(--accent);
  border-radius: 9px;
  background: var(--accent-soft);
  color: var(--accent);
  font-family: var(--font-mono);
  font-size: 14px;
  line-height: 1;
  vertical-align: 1px;
  cursor: pointer;
}

.markdown :deep(.citation:hover) {
  background: var(--accent);
  color: #fff;
}
</style>
