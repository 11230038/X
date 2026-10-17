<script setup lang="ts">
/**
 * 轻提示的宿主，挂在根组件里一次。
 *
 * 只承接"动作已完成"的反馈；失败不进这里（失败要留在原地、带重试和
 * `request_id`）。
 */

import { dismissToast, toasts } from '../../shared/toast'
</script>

<template>
  <Teleport to="body">
    <div class="toast-host" aria-live="polite" aria-atomic="false">
      <TransitionGroup name="toast">
        <div
          v-for="toast in toasts"
          :key="toast.id"
          class="toast"
          :class="`toast--${toast.tone}`"
        >
          <span class="toast__text">{{ toast.text }}</span>
          <button
            type="button"
            class="toast__close"
            aria-label="关闭提示"
            @click="dismissToast(toast.id)"
          >
            ×
          </button>
        </div>
      </TransitionGroup>
    </div>
  </Teleport>
</template>

<style scoped>
.toast-host {
  position: fixed;
  bottom: 24px;
  left: 50%;
  z-index: 1100;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
  transform: translateX(-50%);
  pointer-events: none;
}

.toast {
  display: flex;
  align-items: center;
  gap: 10px;
  max-width: 480px;
  padding: 8px 12px;
  border: 1px solid var(--border-strong);
  border-radius: var(--radius);
  background: var(--surface-raised);
  color: var(--text);
  font-size: 12px;
  box-shadow: 0 6px 20px rgba(20, 24, 32, 0.12);
  pointer-events: auto;
}

.toast--success {
  border-color: var(--success);
  background: var(--success-soft);
}

.toast--error {
  border-color: var(--danger);
  background: var(--danger-soft);
}

.toast__close {
  border: 0;
  background: transparent;
  color: var(--text-muted);
  font-size: 15px;
  line-height: 1;
  cursor: pointer;
}

.toast-enter-active,
.toast-leave-active {
  transition: opacity 0.16s ease, transform 0.16s ease;
}

.toast-enter-from,
.toast-leave-to {
  opacity: 0;
  transform: translateY(6px);
}
</style>
