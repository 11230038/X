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
  padding: 2px 9px;
  border: 1px solid rgb(28 28 30 / 6%);
  border-radius: 999px;
  background: rgb(255 255 255 / 65%);
  color: var(--text-muted);
  font-size: 14px;
  font-weight: 600;
  line-height: 1.6;
  white-space: nowrap;
}

/* 小一号仍然要比默认小一档，否则 `small` 就只剩内边距的区别了。 */
.badge--small {
  padding: 0 7px;
  font-size: 13px;
}

.badge--accent {
  border-color: transparent;
  background: rgb(177 140 255 / 16%);
  color: var(--accent-ink);
}

.badge--success {
  border-color: transparent;
  background: rgb(167 243 208 / 45%);
  color: var(--success);
}

.badge--warning {
  border-color: transparent;
  background: rgb(254 221 118 / 40%);
  color: var(--warning);
}

.badge--danger {
  border-color: transparent;
  background: rgb(254 226 226 / 85%);
  color: var(--danger);
}
</style>
