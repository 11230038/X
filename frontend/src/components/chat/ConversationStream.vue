<script setup lang="ts">
/**
 * 会话流。`设计说明书.md` §4.2 把"消息类型"逐条列了出来，**每一种都要单独
 * 设计**，所以这里是一串具名的分支，不是一个统一的"消息卡片"。
 *
 * 三件事写清楚，免得被读成做漏了：
 *
 * - **回答按纯文本渲染**，`white-space: pre-wrap` 保留换行。Markdown、公式、
 *   代码高亮、表格、Mermaid、图片都**不在 stage 0** —— 那是要引解析器和一处
 *   HTML 注入面（`session.ts` 记着这一版不引 `v-html`）的一件独立的事，不是
 *   顺手能加的一行。
 * - **阶段名与总数来自能力**，由 `socket.ts` 算好放在块里。这里不做任何"如果
 *   没有阶段就当成三段"的兜底。
 * - **工具调用可折叠**，默认收起：参数和结果可能很长，但它必须在那儿。
 */

import type { Block } from '../../ws/blocks'

defineProps<{ blocks: readonly Block[] }>()

/** `done` 的终态。认不出来的原样显示，不猜一个近义词。 */
const OUTCOME_LABEL: Record<string, string> = {
  completed: '本轮结束',
  cancelled: '已停止',
  failed: '本轮失败',
}

function outcomeLabel(status: string): string {
  return OUTCOME_LABEL[status] ?? (status === '' ? '本轮结束' : status)
}
</script>

<template>
  <ol class="stream">
    <li v-for="(block, index) in blocks" :key="index" class="stream__row" :class="`stream__row--${block.kind}`">
      <!-- 用户消息 -->
      <p v-if="block.kind === 'user'" class="bubble">{{ block.text }}</p>

      <!-- AI 回答。`complete` 为假表示还在流式生成。 -->
      <div v-else-if="block.kind === 'answer'" class="answer">
        <p class="answer__text">{{ block.text }}</p>
        <!--
          生成中的标记：三个点。它**不是**不确定进度的替代品（§6.2 硬规则 1）
          —— 这里表达的是"还有字要来"，而进度条表达的是"走到哪了"，两者在
          这个界面上同时存在且长得不一样。
        -->
        <span v-if="!block.complete" class="answer__typing" aria-label="正在生成">···</span>
      </div>

      <!-- 阶段指示。"第 3/5 步"里的两个数字都来自能力的阶段表。 -->
      <div v-else-if="block.kind === 'stage'" class="stage" :class="{ 'stage--done': block.done }">
        <span class="stage__mark" aria-hidden="true">{{ block.done ? '✓' : '●' }}</span>
        <span class="stage__name">{{ block.name }}</span>
        <span v-if="block.index > 0" class="stage__count">第 {{ block.index }}/{{ block.total }} 步</span>
      </div>

      <!-- 工具调用。可折叠，默认收起。 -->
      <details v-else-if="block.kind === 'tool'" class="tool">
        <summary class="tool__summary">
          <span class="tool__name">{{ block.name }}</span>
          <span v-if="block.ok === false" class="tool__fail">调用失败</span>
        </summary>
        <div class="tool__body">
          <p v-if="block.args" class="tool__line"><span class="tool__key">参数</span>{{ block.args }}</p>
          <p v-if="block.result" class="tool__line"><span class="tool__key">结果</span>{{ block.result }}</p>
          <p v-else class="tool__line tool__line--pending">还没有结果</p>
        </div>
      </details>

      <!-- 澄清提问：整轮暂停，等一个回答。 -->
      <div v-else-if="block.kind === 'question'" class="question">
        <p class="question__text">{{ block.text }}</p>
      </div>

      <!--
        错误。结构化：是哪一类（`code`）与能不能重试，两者都显示。§6.2 硬规则
        2 要求每个错误有出口 —— 出口是输入区：能重试时它就在下面那一条里。
      -->
      <div v-else-if="block.kind === 'error'" class="error">
        <p class="error__text">{{ block.text }}</p>
        <p class="error__meta">
          <span class="error__code">{{ block.code }}</span>
          <span>{{ block.retryable ? '可以重试' : '重试也不会成功' }}</span>
        </p>
      </div>

      <!-- 终态。 -->
      <div v-else-if="block.kind === 'done'" class="done">
        <span class="done__label">{{ outcomeLabel(block.status) }}</span>
        <span v-if="block.status === 'failed' && block.partial" class="done__partial">
          上面的回答没有写完，不完整
        </span>
      </div>
    </li>
  </ol>
</template>

<style scoped>
.stream {
  display: flex;
  flex-direction: column;
  gap: 14px;
  margin: 0;
  padding: 20px 24px 8px;
  list-style: none;
}

.stream__row {
  display: flex;
}

.stream__row--user {
  justify-content: flex-end;
}

.bubble {
  max-width: 72%;
  padding: 9px 13px;
  border-radius: var(--radius) var(--radius) 2px var(--radius);
  background: var(--accent-soft);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.answer {
  max-width: 100%;
}

/* `pre-wrap` 是这一版唯一的排版：保留用户与模型给的换行，不做 Markdown。 */
.answer__text {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  line-height: 1.7;
}

.answer__typing {
  color: var(--text-faint);
  letter-spacing: 0.15em;
}

.stage {
  display: flex;
  gap: 8px;
  align-items: center;
  padding: 5px 10px;
  border: 1px solid var(--border);
  border-radius: 999px;
  background: var(--surface);
  color: var(--text-muted);
  font-size: 13px;
}

.stage--done {
  color: var(--success);
  border-color: #bfe0cd;
}

.stage__mark {
  font-size: 11px;
}

.stage__count {
  color: var(--text-faint);
}

.tool {
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--surface);
  font-size: 13px;
}

.tool__summary {
  display: flex;
  gap: 8px;
  align-items: center;
  padding: 7px 10px;
  cursor: pointer;
}

.tool__name {
  font-family: var(--font-mono);
  font-size: 12px;
}

.tool__fail {
  color: var(--danger);
}

.tool__body {
  padding: 0 10px 9px;
  border-top: 1px solid var(--border);
}

.tool__line {
  margin-top: 7px;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  color: var(--text-muted);
}

.tool__line--pending {
  color: var(--text-faint);
}

.tool__key {
  margin-right: 8px;
  color: var(--text-faint);
  font-size: 12px;
}

.question {
  padding: 11px 13px;
  border: 1px solid #f0dca8;
  border-left: 3px solid var(--warning);
  border-radius: var(--radius-sm);
  background: var(--warning-soft);
}

.question__text {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.error {
  padding: 11px 13px;
  border: 1px solid #f3c9c3;
  border-left: 3px solid var(--danger);
  border-radius: var(--radius-sm);
  background: var(--danger-soft);
}

.error__text {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.error__meta {
  display: flex;
  gap: 8px;
  margin-top: 6px;
  color: var(--text-muted);
  font-size: 12px;
}

.error__code {
  font-family: var(--font-mono);
}

.done {
  display: flex;
  gap: 10px;
  align-items: center;
  color: var(--text-faint);
  font-size: 12px;
}

.done__partial {
  padding: 2px 7px;
  border-radius: 999px;
  background: var(--warning-soft);
  color: var(--warning);
}
</style>
