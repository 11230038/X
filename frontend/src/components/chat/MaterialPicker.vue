<script setup lang="ts">
/**
 * 发送前挑材料：附件（文件库）或知识库。
 *
 * 只负责"勾选"，不负责判断能不能读：附件是否被地面化、知识库是否检索到东西，
 * 由服务端在回合里说。前端在这里替它预判会把"文件已上传但未解析"画成不可选，
 * 而那个状态其实是正常的。
 *
 * 用 `dialog` 元素而不是自绘浮层：`Esc` 关闭、焦点进入与返回都由浏览器负责，
 * 与 `ConfirmDialog` 保持同一种交互约定。
 */

import { ref, watch } from 'vue'

import EmptyState from '../common/EmptyState.vue'
import FailureState from '../common/FailureState.vue'
import type { ApiFailure } from '../../api/client'
import { CHAT } from '../../copy/chat'
import AppIcon from '../common/AppIcon.vue'

export interface PickerItem {
  id: string
  label: string
  detail?: string
}

const props = defineProps<{
  open: boolean
  title: string
  hint: string
  items: readonly PickerItem[]
  selected: readonly string[]
  loading: boolean
  failure: ApiFailure | null
  emptyTitle: string
  emptyAction: string
}>()

const emit = defineEmits<{
  (event: 'close'): void
  (event: 'toggle', id: string): void
  (event: 'empty-action'): void
  (event: 'retry'): void
}>()

const dialog = ref<HTMLDialogElement | null>(null)

watch(
  () => props.open,
  (open) => {
    const element = dialog.value
    if (!element) return
    if (open && !element.open) element.showModal()
    if (!open && element.open) element.close()
  },
)

function close(): void {
  emit('close')
}
</script>

<template>
  <dialog ref="dialog" class="picker" @cancel.prevent="close" @close="close">
    <header class="picker__head">
      <h2 class="picker__title">{{ title }}</h2>
      <button type="button" class="picker__close" :aria-label="CHAT.closePicker" @click="close">
        <AppIcon name="close" />
      </button>
    </header>

    <p class="picker__hint">{{ hint }}</p>

    <div class="picker__body">
      <p v-if="loading" class="picker__loading">正在读取…</p>

      <FailureState
        v-else-if="failure"
        :failure="failure"
        title="列表没有读出来"
        @retry="emit('retry')"
      />

      <EmptyState
        v-else-if="items.length === 0"
        :title="emptyTitle"
        :action-label="emptyAction"
        @action="emit('empty-action')"
      />

      <ul v-else class="picker__list">
        <li v-for="item in items" :key="item.id">
          <label class="picker__item" :class="{ 'picker__item--on': selected.includes(item.id) }">
            <input
              type="checkbox"
              :checked="selected.includes(item.id)"
              @change="emit('toggle', item.id)"
            />
            <span class="picker__label">{{ item.label }}</span>
            <span v-if="item.detail" class="picker__detail">{{ item.detail }}</span>
          </label>
        </li>
      </ul>
    </div>

    <footer class="picker__foot">
      <span class="picker__count">{{ CHAT.selectedCount(selected.length) }}</span>
      <button type="button" class="picker__done" @click="close">完成</button>
    </footer>
  </dialog>
</template>

<style scoped>
.picker {
  width: min(480px, calc(100vw - 40px));
  max-height: min(560px, calc(100vh - 80px));
  padding: 0;
  border: 1px solid var(--glass-border);
  border-radius: var(--radius-card);
  background: rgb(255 255 255 / 92%);
  color: var(--text);
  box-shadow: 0 24px 64px -12px rgb(51 68 85 / 20%);
  backdrop-filter: blur(30px);
  -webkit-backdrop-filter: blur(30px);
}

.picker::backdrop {
  background: rgb(20 26 38 / 25%);
  backdrop-filter: blur(6px);
}

.picker__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 13px 16px 8px;
}

.picker__title {
  font-size: 15px;
  font-weight: 650;
}

.picker__close {
  padding: 3px 7px;
  border: 0;
  border-radius: 4px;
  background: transparent;
  color: var(--text-faint);
  cursor: pointer;
}

.picker__close:hover {
  background: var(--surface-sunken);
  color: var(--text);
}

.picker__hint {
  margin: 0;
  padding: 0 16px 10px;
  color: var(--text-muted);
  font-size: 13px;
  line-height: 1.6;
}

.picker__body {
  max-height: 360px;
  overflow-y: auto;
  padding: 0 10px;
}

.picker__loading {
  padding: 24px 6px;
  color: var(--text-muted);
  font-size: 14px;
}

.picker__list {
  display: flex;
  flex-direction: column;
  gap: 2px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.picker__item {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr);
  gap: 3px 8px;
  padding: 8px;
  border-radius: var(--radius-sm);
  cursor: pointer;
}

.picker__item:hover {
  background: var(--surface-sunken);
}

.picker__item--on {
  background: var(--accent-soft);
}

.picker__item input {
  margin-top: 3px;
}

.picker__label {
  overflow: hidden;
  font-size: 14px;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.picker__detail {
  grid-column: 2;
  color: var(--text-faint);
  font-size: 12px;
}

.picker__foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 10px 16px 14px;
}

.picker__count {
  color: var(--text-faint);
  font-size: 13px;
}

.picker__done {
  padding: 6px 16px;
  border: 1px solid var(--accent);
  border-radius: var(--radius-sm);
  background: var(--accent);
  color: #fff;
  font-size: 14px;
  cursor: pointer;
}
</style>
