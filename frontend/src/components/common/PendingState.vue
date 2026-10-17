<script setup lang="ts">
/**
 * 已知在进行、但不知道何时结束的操作。
 *
 * 与 `SkeletonBlock` 的分工：骨架代表"内容马上会来"的占位，`PendingState`
 * 代表"动作已经发出，服务端正在做" —— 后者需要一句动词、可能还需要取消，
 * 而且它**不能**看起来像一个空列表。
 */

withDefaults(
  defineProps<{
    label?: string
    detail?: string
    canCancel?: boolean
    cancelLabel?: string
  }>(),
  { label: '正在处理…', detail: '', canCancel: false, cancelLabel: '取消' },
)

defineEmits<{
  cancel: []
}>()
</script>

<template>
  <div class="pending" role="status" aria-live="polite">
    <span class="pending__spinner" aria-hidden="true"></span>
    <div class="pending__body">
      <p class="pending__label">{{ label }}</p>
      <p v-if="detail" class="pending__detail">{{ detail }}</p>
    </div>
    <button v-if="canCancel" type="button" class="pending__cancel" @click="$emit('cancel')">
      {{ cancelLabel }}
    </button>
  </div>
</template>

<style scoped>
.pending {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 12px 14px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--surface-sunken);
}

.pending__spinner {
  flex: none;
  width: 14px;
  height: 14px;
  border: 2px solid var(--border-strong);
  border-top-color: var(--accent);
  border-radius: 50%;
  animation: pending-spin 0.9s linear infinite;
}

.pending__body {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.pending__label {
  margin: 0;
  color: var(--text);
  font-size: 13px;
}

.pending__detail {
  margin: 0;
  color: var(--text-muted);
  font-size: 11px;
}

.pending__cancel {
  flex: none;
  padding: 3px 9px;
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-sm);
  background: var(--surface);
  color: var(--text);
  font-size: 11px;
  cursor: pointer;
}

@keyframes pending-spin {
  to {
    transform: rotate(360deg);
  }
}

@media (prefers-reduced-motion: reduce) {
  .pending__spinner {
    animation-duration: 2.4s;
  }
}
</style>
