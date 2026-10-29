<script setup lang="ts">
/**
 * 上传文件的弹窗：把文件拖进来，或者点一下打开文件选择器。
 *
 * 选中之后立刻开始上传 —— 单文件、没有别的字段要填，再多一步"确认"只是多一次
 * 点击。上传中关掉弹窗（叉、遮罩、Esc）等同于取消，请求会真的中止，不会留在
 * 后台传完。
 */

import { nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'

import { FILES } from '../../copy/files'

const props = defineProps<{
  open: boolean
  uploading: boolean
  name: string
  percent: number | null
  progressLabel: string
  error: string | null
}>()

const emit = defineEmits<{
  close: []
  pick: [file: File]
  cancel: []
}>()

const dialog = ref<HTMLElement | null>(null)
const input = ref<HTMLInputElement | null>(null)
const dragging = ref(false)

/** 拖过子元素也会触发 `dragleave`，用进出计数抵消，免得边框一闪一闪。 */
let dragDepth = 0

function onDragEnter(event: DragEvent): void {
  event.preventDefault()
  if (props.uploading) return
  dragDepth += 1
  dragging.value = true
}

function onDragOver(event: DragEvent): void {
  event.preventDefault()
  if (event.dataTransfer) event.dataTransfer.dropEffect = 'copy'
}

function onDragLeave(): void {
  dragDepth = Math.max(0, dragDepth - 1)
  if (dragDepth === 0) dragging.value = false
}

function onDrop(event: DragEvent): void {
  event.preventDefault()
  dragDepth = 0
  dragging.value = false
  if (props.uploading) return
  // 一次只收一个：后端也是这样要求的，多选的第一个就是用户真正想传的那份。
  const file = event.dataTransfer?.files?.[0]
  if (file) emit('pick', file)
}

function openPicker(): void {
  if (props.uploading) return
  input.value?.click()
}

function onPicked(event: Event): void {
  const picked = (event.target as HTMLInputElement).files?.[0]
  ;(event.target as HTMLInputElement).value = ''
  if (picked) emit('pick', picked)
}

watch(
  () => props.open,
  async (open) => {
    if (!open) {
      dragging.value = false
      dragDepth = 0
      return
    }
    await nextTick()
    dialog.value?.querySelector<HTMLElement>('[data-zone]')?.focus()
  },
)

const FOCUSABLE =
  'button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'

function onKeydown(event: KeyboardEvent): void {
  if (!props.open) return

  if (event.key === 'Escape') {
    event.preventDefault()
    emit('close')
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
    <div v-if="open" class="upload-overlay" @click.self="emit('close')">
      <div
        ref="dialog"
        class="upload-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby="upload-title"
        tabindex="-1"
      >
        <header class="upload-dialog__head">
          <h2 id="upload-title" class="upload-dialog__title">{{ FILES.uploadTitle }}</h2>
          <button
            type="button"
            class="upload-dialog__close"
            :aria-label="FILES.close"
            @click="emit('close')"
          >
            ×
          </button>
        </header>

        <input
          ref="input"
          class="upload-dialog__file"
          type="file"
          tabindex="-1"
          aria-hidden="true"
          @change="onPicked"
        />

        <button
          type="button"
          data-zone
          class="dropzone"
          :class="{ 'dropzone--over': dragging, 'dropzone--busy': uploading }"
          :aria-disabled="uploading"
          @click="openPicker"
          @dragenter="onDragEnter"
          @dragover="onDragOver"
          @dragleave="onDragLeave"
          @drop="onDrop"
        >
          <template v-if="uploading">
            <span class="dropzone__name" :title="name">{{ name }}</span>
            <progress
              class="dropzone__bar"
              :value="percent ?? undefined"
              max="100"
            ></progress>
            <span class="dropzone__percent">{{ progressLabel }}</span>
          </template>
          <template v-else>
            <span class="dropzone__icon" aria-hidden="true">＋</span>
            <span class="dropzone__title">{{ FILES.dropTitle }}</span>
            <span class="dropzone__hint">{{ FILES.dropHint }}</span>
          </template>
        </button>

        <p v-if="error" class="upload-dialog__error" role="alert">{{ error }}</p>
        <p class="upload-dialog__note">{{ FILES.uploadOneAtATime }}</p>

        <div class="upload-dialog__actions">
          <button
            v-if="uploading"
            type="button"
            class="upload-dialog__cancel"
            @click="emit('cancel')"
          >
            {{ FILES.cancelUpload }}
          </button>
          <button v-else type="button" class="upload-dialog__cancel" @click="emit('close')">
            {{ FILES.close }}
          </button>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<style scoped>
.upload-overlay {
  position: fixed;
  inset: 0;
  z-index: 1000;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px;
  background: rgb(20 24 32 / 30%);
  backdrop-filter: blur(8px);
  -webkit-backdrop-filter: blur(8px);
}

.upload-dialog {
  width: min(520px, 100%);
  padding: 28px;
  border: 1px solid var(--glass-border);
  border-radius: 28px;
  background: rgb(255 255 255 / 90%);
  box-shadow: 0 30px 60px rgb(0 0 0 / 15%), var(--inset-light);
  backdrop-filter: blur(24px);
  -webkit-backdrop-filter: blur(24px);
}

.upload-dialog__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 18px;
}

.upload-dialog__title {
  margin: 0;
  color: var(--text);
  font-size: 19px;
  font-weight: 800;
  letter-spacing: -0.02em;
}

.upload-dialog__close {
  display: grid;
  place-items: center;
  width: 32px;
  height: 32px;
  border: 0;
  border-radius: 50%;
  background: rgb(0 0 0 / 4%);
  color: var(--text-muted);
  font-size: 20px;
  line-height: 1;
  cursor: pointer;
  transition: all 0.2s ease;
}

.upload-dialog__close:hover {
  background: rgb(0 0 0 / 8%);
  color: var(--text);
}

.upload-dialog__file {
  display: none;
}

/* 投递区：平时是虚线框，拖进来或悬停时换成主色。 */
.dropzone {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8px;
  width: 100%;
  min-height: 200px;
  padding: 32px 24px;
  border: 2px dashed rgb(0 0 0 / 12%);
  border-radius: 20px;
  background: rgb(255 255 255 / 55%);
  color: var(--text);
  cursor: pointer;
  transition: all 0.2s ease;
}

.dropzone:hover,
.dropzone:focus-visible,
.dropzone--over {
  border-color: var(--color-purple);
  background: var(--accent-soft);
  outline: none;
}

.dropzone--busy {
  cursor: default;
}

.dropzone__icon {
  display: grid;
  place-items: center;
  width: 52px;
  height: 52px;
  margin-bottom: 4px;
  border-radius: 50%;
  background: var(--accent-gradient);
  color: #fff;
  font-size: 26px;
  font-weight: 300;
  line-height: 1;
  box-shadow: 0 6px 16px rgb(177 140 255 / 30%);
}

.dropzone__title {
  font-size: 16px;
  font-weight: 700;
}

.dropzone__hint {
  color: var(--text-faint);
  font-size: 14px;
}

.dropzone__name {
  max-width: 100%;
  overflow: hidden;
  font-size: 15px;
  font-weight: 700;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.dropzone__bar {
  width: min(320px, 100%);
  height: 6px;
  accent-color: var(--accent);
}

.dropzone__percent {
  color: var(--text-muted);
  font-size: 14px;
}

.upload-dialog__error {
  margin: 14px 0 0;
  padding: 10px 14px;
  border: 1px solid var(--danger);
  border-radius: var(--radius-sm);
  background: var(--danger-soft);
  color: var(--text);
  font-size: 14px;
  line-height: 1.5;
}

.upload-dialog__note {
  margin: 12px 0 0;
  color: var(--text-faint);
  font-size: 14px;
}

.upload-dialog__actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 16px;
}

.upload-dialog__cancel {
  padding: 9px 20px;
  border: 1px solid rgb(0 0 0 / 8%);
  border-radius: var(--radius-sm);
  background: #fff;
  color: var(--text);
  font-size: 14px;
  font-weight: 700;
  cursor: pointer;
}

.upload-dialog__cancel:hover {
  border-color: var(--text);
}
</style>
