<script setup lang="ts">
/**
 * 状态标签。文字永远在，颜色只是辅助 —— `设计说明书.md` §6.2 的硬规则
 * 3：不能只靠颜色表达状态。
 */

withDefaults(
  defineProps<{
    label: string
    tone?: 'neutral' | 'accent' | 'success' | 'warning' | 'danger'
    /** 小一号，用于列表行内。 */
    small?: boolean
  }>(),
  { tone: 'neutral', small: false },
)
</script>

<template>
  <span class="badge" :class="[`badge--${tone}`, { 'badge--small': small }]">{{ label }}</span>
</template>

<style scoped>
.badge {
  display: inline-flex;
  align-items: center;
  padding: 1px 7px;
  border: 1px solid var(--border-strong);
  border-radius: 10px;
  background: var(--surface);
  color: var(--text-muted);
  font-size: 11px;
  line-height: 1.6;
  white-space: nowrap;
}

.badge--small {
  padding: 0 6px;
  font-size: 10px;
}

.badge--accent {
  border-color: var(--accent);
  background: var(--accent-soft);
  color: var(--accent);
}

.badge--success {
  border-color: var(--success);
  background: var(--success-soft);
  color: var(--success);
}

.badge--warning {
  border-color: var(--warning);
  background: var(--warning-soft);
  color: var(--text);
}

.badge--danger {
  border-color: var(--danger);
  background: var(--danger-soft);
  color: var(--danger);
}
</style>
