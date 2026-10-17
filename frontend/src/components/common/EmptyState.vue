<script setup lang="ts">
/**
 * 空状态。`设计说明书.md` §7.1：**给动作，不给解释**。所以这里的默认形态
 * 是一个标题 + 一个真正能改变现状的按钮，而不是一段"暂无数据"的说明。
 */

withDefaults(
  defineProps<{
    title: string
    /** 可选的一句范围说明。不要写"暂无内容"这种同义反复。 */
    hint?: string
    actionLabel?: string
    secondaryLabel?: string
  }>(),
  { hint: '', actionLabel: '', secondaryLabel: '' },
)

defineEmits<{
  action: []
  secondary: []
}>()
</script>

<template>
  <div class="empty">
    <p class="empty__title">{{ title }}</p>
    <p v-if="hint" class="empty__hint">{{ hint }}</p>
    <div v-if="actionLabel || secondaryLabel || $slots.actions" class="empty__actions">
      <button v-if="actionLabel" type="button" class="empty__action" @click="$emit('action')">
        {{ actionLabel }}
      </button>
      <button
        v-if="secondaryLabel"
        type="button"
        class="empty__secondary"
        @click="$emit('secondary')"
      >
        {{ secondaryLabel }}
      </button>
      <slot name="actions"></slot>
    </div>
    <slot></slot>
  </div>
</template>

<style scoped>
.empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8px;
  padding: 36px 20px;
  text-align: center;
}

.empty__title {
  margin: 0;
  color: var(--text);
  font-size: 14px;
  font-weight: 600;
}

.empty__hint {
  max-width: 36em;
  margin: 0;
  color: var(--text-muted);
  font-size: 12px;
  line-height: 1.6;
}

.empty__actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 6px;
}

.empty__action {
  padding: 6px 14px;
  border: 1px solid var(--accent);
  border-radius: var(--radius-sm);
  background: var(--accent);
  color: #fff;
  font-size: 13px;
  cursor: pointer;
}

.empty__action:hover {
  filter: brightness(0.95);
}

.empty__secondary {
  padding: 6px 12px;
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--text-muted);
  font-size: 13px;
  cursor: pointer;
}

.empty__secondary:hover {
  background: var(--surface-sunken);
  color: var(--text);
}
</style>
