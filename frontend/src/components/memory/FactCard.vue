<script setup lang="ts">
/**
 * 一条 L2 事实的卡片。
 *
 * 四个状态原样画（生效中 / 已撤回 / 已被合并），因为撤回是"保留行、只改状态"：
 * 把撤回画成不存在，会让恢复这个动作无处可谈。`sensitive` 不是装饰，它告诉
 * 读者这条内容来自敏感上下文。
 */

import type { FactView } from '../../api/memory'
import { MEMORY } from '../../copy/memory'
import { formatDateTime, formatPercent } from '../../shared/format'

defineProps<{
  fact: FactView
  /** 详情页等场景不需要重复显示归属面。 */
  showSurface?: boolean
  busy?: boolean
}>()

defineEmits<{
  open: []
  edit: []
  retract: []
  restore: []
}>()

function statusLabel(status: string): string {
  return status === 'retracted'
    ? MEMORY.statusRetracted
    : status === 'superseded'
      ? MEMORY.statusSuperseded
      : MEMORY.statusActive
}

function surfaceLabel(surface: string): string {
  return MEMORY.surfaces[surface] ?? surface
}
</script>

<template>
  <article class="fact" :class="`fact--${fact.status}`">
    <header class="fact__head">
      <span class="chip" :class="`chip--${fact.status}`">{{ statusLabel(fact.status) }}</span>
      <span v-if="showSurface !== false" class="chip chip--surface">
        {{ surfaceLabel(fact.surface) }}
      </span>
      <span v-if="fact.scope" class="chip chip--scope">{{ MEMORY.scope }}：{{ fact.scope }}</span>
      <span v-if="fact.sensitive" class="chip chip--sensitive">{{ MEMORY.sensitive }}</span>
      <span class="chip chip--confirm">
        {{ fact.confirmed_by === 'user' ? MEMORY.confirmedUser : MEMORY.confirmedGate }}
      </span>
      <span class="fact__confidence">
        {{ MEMORY.confidence }} {{ formatPercent(fact.confidence, 0) }}
      </span>
    </header>

    <p class="fact__statement">{{ fact.statement }}</p>

    <footer class="fact__foot">
      <span class="fact__meta">
        {{ MEMORY.createdAt }} {{ formatDateTime(fact.created_at) }}
        <template v-if="fact.updated_at"> · {{ MEMORY.updatedAt }} {{ formatDateTime(fact.updated_at) }}</template>
        <template v-if="fact.retract_time"> · {{ MEMORY.retractTime }} {{ formatDateTime(fact.retract_time) }}</template>
        · {{ MEMORY.version }} {{ fact.version }}
      </span>
      <span class="fact__actions">
        <button type="button" class="link" @click="$emit('open')">{{ MEMORY.evidenceOpen }}</button>
        <button type="button" class="link" @click="$emit('edit')">{{ MEMORY.edit }}</button>
        <button
          v-if="fact.status === 'active'"
          type="button"
          class="link link--danger"
          :disabled="busy"
          @click="$emit('retract')"
        >
          {{ MEMORY.retract }}
        </button>
        <button
          v-else-if="fact.status === 'retracted'"
          type="button"
          class="link"
          :disabled="busy"
          @click="$emit('restore')"
        >
          {{ MEMORY.restore }}
        </button>
      </span>
    </footer>
  </article>
</template>

<style scoped>
.fact {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 11px 13px;
  border: 1px solid var(--border);
  border-left: 3px solid var(--border-strong);
  border-radius: var(--radius-sm);
  background: var(--surface);
}

.fact--retracted {
  border-left-color: var(--text-faint);
  background: var(--surface-sunken);
}

.fact--superseded {
  border-left-color: var(--warning);
  background: var(--surface-sunken);
}

.fact__head {
  display: flex;
  flex-wrap: wrap;
  gap: 5px;
  align-items: center;
}

.chip {
  padding: 0 6px;
  border-radius: 8px;
  font-size: 9px;
  line-height: 16px;
}

.chip--active {
  background: var(--success-soft, var(--surface-sunken));
  color: var(--success);
}

.chip--retracted {
  background: var(--surface-sunken);
  color: var(--text-faint);
}

.chip--superseded {
  background: var(--warning-soft);
  color: var(--warning);
}

.chip--surface,
.chip--scope,
.chip--confirm {
  border: 1px solid var(--border);
  color: var(--text-muted);
}

.chip--sensitive {
  background: var(--danger-soft);
  color: var(--danger);
}

.fact__confidence {
  margin-left: auto;
  color: var(--text-faint);
  font-size: 10px;
}

.fact__statement {
  margin: 0;
  font-size: 13px;
  line-height: 1.7;
  white-space: pre-wrap;
  word-break: break-word;
}

.fact--retracted .fact__statement {
  color: var(--text-muted);
  text-decoration: line-through;
  text-decoration-color: var(--text-faint);
}

.fact__foot {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  align-items: baseline;
  justify-content: space-between;
}

.fact__meta {
  color: var(--text-faint);
  font-size: 10px;
}

.fact__actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.link {
  padding: 0;
  border: 0;
  background: transparent;
  color: var(--accent);
  font-size: 11px;
  cursor: pointer;
}

.link:disabled {
  cursor: default;
  opacity: 0.55;
}

.link--danger {
  color: var(--danger);
}
</style>
