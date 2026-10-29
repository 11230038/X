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
  bottom: 40px;
  left: 50%;
  z-index: 1100;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 14px;
  transform: translateX(-50%);
  pointer-events: none;
}

/* 演示稿的提示是一条深色胶囊，浮在页面底部中央。 */
.toast {
  display: flex;
  align-items: center;
  gap: 16px;
  max-width: 520px;
  padding: 14px 26px;
  border: 1px solid rgb(255 255 255 / 12%);
  border-radius: 999px;
  background: rgb(28 28 30 / 92%);
  color: #fff;
  font-size: 15px;
  font-weight: 500;
  box-shadow: 0 20px 40px rgb(0 0 0 / 20%);
  backdrop-filter: blur(16px);
  -webkit-backdrop-filter: blur(16px);
  pointer-events: auto;
}

.toast--success {
  background: rgb(31 122 77 / 95%);
}

.toast--error {
  background: rgb(165 45 35 / 95%);
}

.toast__close {
  border: 0;
  background: transparent;
  color: rgb(255 255 255 / 65%);
  font-size: 17px;
  line-height: 1;
  cursor: pointer;
}

.toast__close:hover {
  color: #fff;
}

.toast-enter-active,
.toast-leave-active {
  transition: opacity 0.25s ease, transform 0.25s ease;
}

.toast-enter-from,
.toast-leave-to {
  opacity: 0;
  transform: translateY(14px) scale(0.94);
}
</style>
