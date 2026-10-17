<script setup lang="ts">
/**
 * 全应用唯一的确认对话框。
 *
 * 页面通过 `confirmAction()` 打开它，拿到一个 Promise<boolean>。这里负责
 * 三件在原生 `confirm()` 里做不到的事：
 *
 * 1. 把**影响范围**完整写出来（软删能恢复、永久删不能）；
 * 2. 危险动作的确认键不用主色，默认焦点落在"取消"上；
 * 3. `Esc` 取消、焦点锁在对话框里 —— 键盘用户不会误触到背后的页面。
 */

import { nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { pendingConfirm, settleConfirm } from '../../shared/confirm'

const dialog = ref<HTMLElement | null>(null)

const FOCUSABLE =
  'button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'

watch(pendingConfirm, async (request) => {
  if (!request) return
  await nextTick()
  const cancel = dialog.value?.querySelector<HTMLElement>('[data-cancel]')
  cancel?.focus()
})

function onKeydown(event: KeyboardEvent): void {
  if (!pendingConfirm.value) return

  if (event.key === 'Escape') {
    event.preventDefault()
    settleConfirm(false)
    return
  }

  if (event.key !== 'Tab') return
  const root = dialog.value
  if (!root) return

  const items = Array.from(root.querySelectorAll<HTMLElement>(FOCUSABLE))
  if (items.length === 0) {
    event.preventDefault()
    root.focus()
    return
  }

  const first = items[0]
  const last = items[items.length - 1]
  const active = document.activeElement

  if (event.shiftKey) {
    if (active === first || !root.contains(active)) {
      event.preventDefault()
      last.focus()
    }
  } else if (active === last || !root.contains(active)) {
    event.preventDefault()
    first.focus()
  }
}

onMounted(() => window.addEventListener('keydown', onKeydown))
onBeforeUnmount(() => window.removeEventListener('keydown', onKeydown))
</script>

<template>
  <Teleport to="body">
    <div
      v-if="pendingConfirm"
      class="confirm-overlay"
      @click.self="settleConfirm(false)"
    >
      <div
        ref="dialog"
        class="confirm"
        role="dialog"
        aria-modal="true"
        aria-labelledby="confirm-title"
        tabindex="-1"
      >
        <h2 id="confirm-title" class="confirm__title">{{ pendingConfirm.title }}</h2>
        <p v-if="pendingConfirm.body" class="confirm__body">{{ pendingConfirm.body }}</p>
        <p v-if="pendingConfirm.note" class="confirm__note">{{ pendingConfirm.note }}</p>
        <div class="confirm__actions">
          <button
            data-cancel
            type="button"
            class="confirm__cancel"
            @click="settleConfirm(false)"
          >
            {{ pendingConfirm.cancelLabel || '取消' }}
          </button>
          <button
            type="button"
            class="confirm__submit"
            :class="{ 'confirm__submit--danger': pendingConfirm.danger }"
            @click="settleConfirm(true)"
          >
            {{ pendingConfirm.confirmLabel || '确认' }}
          </button>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<style scoped>
.confirm-overlay {
  position: fixed;
  inset: 0;
  z-index: 1000;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px;
  background: rgba(20, 24, 32, 0.38);
}

.confirm {
  width: min(440px, 100%);
  padding: 20px;
  border: 1px solid var(--border-strong);
  border-radius: var(--radius);
  background: var(--surface);
  box-shadow: 0 12px 40px rgba(20, 24, 32, 0.18);
}

.confirm__title {
  margin: 0 0 8px;
  color: var(--text);
  font-size: 15px;
  font-weight: 600;
}

.confirm__body {
  margin: 0;
  color: var(--text);
  font-size: 13px;
  line-height: 1.65;
}

.confirm__note {
  margin: 8px 0 0;
  color: var(--text-muted);
  font-size: 12px;
  line-height: 1.6;
}

.confirm__actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 18px;
}

.confirm__cancel,
.confirm__submit {
  padding: 6px 14px;
  border-radius: var(--radius-sm);
  font-size: 13px;
  cursor: pointer;
}

.confirm__cancel {
  border: 1px solid var(--border-strong);
  background: var(--surface);
  color: var(--text);
}

.confirm__cancel:hover {
  background: var(--surface-sunken);
}

.confirm__submit {
  border: 1px solid var(--accent);
  background: var(--accent);
  color: #fff;
}

.confirm__submit--danger {
  border-color: var(--danger);
  background: transparent;
  color: var(--danger);
}

.confirm__submit--danger:hover {
  background: var(--danger-soft);
}
</style>
