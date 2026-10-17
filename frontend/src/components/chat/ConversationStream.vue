<script setup lang="ts">
/**
 * 会话流。`设计说明书.md` §4.2 把"消息类型"逐条列了出来，**每一种都要单独
 * 设计**，所以这里是一串具名的分支，不是一个统一的"消息卡片"。
 *
 * 几件写清楚，免得被读成做漏了：
 *
 * - **回答走 Markdown**，HTML 只来自 `markdown/render.ts` 并经过 DOMPurify；
 *   引用编号只有能对上来源时才做成按钮。
 * - **阶段名与总数来自能力**，由 `socket.ts` 算好放在块里。阶段（能力声明的
 *   第几步）与 `progress`（阶段内部的多步进度）是两件事，画法也不同。
 * - **思考是推理，不是回答**：它是可折叠的次要内容，永远不混进回答正文。
 * - **工具结果按纯文本画**：外部数据源不决定页面结构。
 */

import type { Block, SourceItem } from '../../ws/blocks'
import { CHAT } from '../../copy/chat'
import MarkdownBody from '../common/MarkdownBody.vue'
import ToolCallCard from './ToolCallCard.vue'

defineProps<{
  blocks: readonly Block[]
  /** 能对上的引用编号。 */
  citations: ReadonlySet<number>
}>()

const emit = defineEmits<{
  (event: 'citation', index: number): void
  (event: 'open-source', source: SourceItem): void
  (event: 'retry-unconfirmed'): void
}>()

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
      <div v-if="block.kind === 'user'" class="bubble-wrap">
        <p class="bubble">{{ block.text }}</p>
        <p v-if="block.unconfirmed" class="unconfirmed">
          <span>{{ CHAT.retryUnconfirmedHint }}</span>
          <button type="button" class="unconfirmed__retry" @click="emit('retry-unconfirmed')">
            {{ CHAT.retrySend }}
          </button>
        </p>
      </div>

      <!-- AI 回答。`complete` 为假表示还在流式生成。 -->
      <div v-else-if="block.kind === 'answer'" class="answer">
        <MarkdownBody
          :text="block.text"
          :complete="block.complete"
          :citations="citations"
          @citation="emit('citation', $event)"
        />
        <!--
          生成中的标记：三个点。它**不是**不确定进度的替代品（§6.2 硬规则 1）
          —— 这里表达的是"还有字要来"。
        -->
        <span v-if="!block.complete" class="answer__typing" aria-label="正在生成">···</span>
      </div>

      <!-- 阶段指示。"第 3/5 步"里的两个数字都来自能力的阶段表。 -->
      <div v-else-if="block.kind === 'stage'" class="stage" :class="{ 'stage--done': block.done }">
        <span class="stage__mark" aria-hidden="true">{{ block.done ? '✓' : '●' }}</span>
        <span class="stage__name">{{ block.name }}</span>
        <span v-if="block.index > 0" class="stage__count">第 {{ block.index }}/{{ block.total }} 步</span>
      </div>

      <!-- 思考。可折叠，默认收起；它不参与回答正文。 -->
      <details v-else-if="block.kind === 'thinking'" class="thinking">
        <summary class="thinking__summary">{{ CHAT.thinking }}</summary>
        <p class="thinking__text">{{ block.text }}</p>
      </details>

      <!-- 观察：模型对材料/环境的一句小结。 -->
      <div v-else-if="block.kind === 'observation'" class="observation">
        <span class="observation__label">{{ CHAT.observation }}</span>
        <span class="observation__text">{{ block.text }}</span>
      </div>

      <!-- 阶段内部的多步进度。与阶段本身分开画。 -->
      <div v-else-if="block.kind === 'progress'" class="progress">
        <span class="progress__label">{{ block.label || CHAT.progress }}</span>
        <span v-if="block.total > 0" class="progress__count">{{ block.step }}/{{ block.total }}</span>
        <span v-if="block.total > 0" class="progress__track" aria-hidden="true">
          <span
            class="progress__fill"
            :style="{ width: `${Math.min(100, Math.round((block.step / block.total) * 100))}%` }"
          />
        </span>
      </div>

      <!-- 工具调用。可折叠，默认收起。 -->
      <ToolCallCard
        v-else-if="block.kind === 'tool'"
        :name="block.name"
        :args="block.args"
        :result="block.result"
        :ok="block.ok"
      />

      <!-- 来源。面板里有完整的一份，这里给一个能直接点开的短列表。 -->
      <details v-else-if="block.kind === 'sources'" class="sources">
        <summary class="sources__summary">{{ CHAT.sourceTitle }} · {{ block.sources.length }}</summary>
        <ul class="sources__list">
          <li v-for="source in block.sources" :key="source.index">
            <button type="button" class="sources__item" @click="emit('open-source', source)">
              <span class="sources__index">{{ source.index }}</span>
              <span class="sources__title">{{ source.title }}</span>
            </button>
          </li>
        </ul>
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
        <p v-if="block.quotaItem" class="error__quota">
          额度项：{{ block.quotaItem }}
          <template v-if="block.quotaResetsAt"> · {{ block.quotaResetsAt }} 恢复</template>
        </p>
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

.bubble-wrap {
  display: flex;
  flex-direction: column;
  gap: 4px;
  align-items: flex-end;
  max-width: 72%;
}

.bubble {
  max-width: 100%;
  padding: 9px 13px;
  border-radius: var(--radius) var(--radius) 2px var(--radius);
  background: var(--accent-soft);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.unconfirmed {
  display: flex;
  gap: 8px;
  align-items: center;
  color: var(--warning);
  font-size: 12px;
}

.unconfirmed__retry {
  padding: 2px 8px;
  border: 1px solid var(--warning);
  border-radius: 4px;
  background: transparent;
  color: var(--warning);
  font-size: 11px;
  cursor: pointer;
}

.answer {
  max-width: 100%;
  min-width: 0;
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

.thinking {
  padding: 6px 10px;
  border-left: 2px solid var(--border-strong);
  color: var(--text-muted);
  font-size: 12px;
}

.thinking__summary {
  color: var(--text-faint);
  cursor: pointer;
}

.thinking__text {
  margin: 6px 0 0;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  line-height: 1.6;
}

.observation {
  display: flex;
  gap: 8px;
  padding: 7px 10px;
  border: 1px dashed var(--border-strong);
  border-radius: var(--radius-sm);
  color: var(--text-muted);
  font-size: 12px;
}

.observation__label {
  flex: none;
  color: var(--text-faint);
}

.observation__text {
  overflow-wrap: anywhere;
}

.progress {
  display: flex;
  gap: 8px;
  align-items: center;
  color: var(--text-muted);
  font-size: 12px;
}

.progress__count {
  color: var(--text-faint);
  font-variant-numeric: tabular-nums;
}

.progress__track {
  flex: 1;
  max-width: 220px;
  height: 4px;
  border-radius: 2px;
  background: var(--border);
  overflow: hidden;
}

.progress__fill {
  display: block;
  height: 100%;
  background: var(--accent);
}

.sources {
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--surface);
  font-size: 12px;
}

.sources__summary {
  padding: 7px 10px;
  color: var(--text-muted);
  cursor: pointer;
}

.sources__list {
  display: flex;
  flex-direction: column;
  gap: 2px;
  margin: 0;
  padding: 0 8px 8px;
  list-style: none;
}

.sources__item {
  display: flex;
  gap: 7px;
  align-items: center;
  width: 100%;
  padding: 4px 6px;
  border: 0;
  border-radius: 4px;
  background: transparent;
  text-align: left;
  cursor: pointer;
}

.sources__item:hover {
  background: var(--surface-sunken);
}

.sources__index {
  flex: none;
  display: grid;
  place-items: center;
  width: 16px;
  height: 16px;
  border-radius: 8px;
  background: var(--accent-soft);
  color: var(--accent);
  font-family: var(--font-mono);
  font-size: 10px;
}

.sources__title {
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
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

.error__quota {
  margin-top: 4px;
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
