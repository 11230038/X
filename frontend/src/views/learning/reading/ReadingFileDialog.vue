<script setup lang="ts">
/**
 * 从文件库打开一份材料。
 *
 * 已上传但尚未解析的文件是正常状态：这里明确说"还没解析"，而不是让"打开"
 * 静默失败。真正的解析结果由服务端 document 字段给出。
 */

import { ref } from 'vue'

import type { ApiFailure } from '../../../api/client'
import type { FileSummary } from '../../../api/files'
import EmptyState from '../../../components/common/EmptyState.vue'
import FailureState from '../../../components/common/FailureState.vue'
import SkeletonBlock from '../../../components/common/SkeletonBlock.vue'
import { READING } from '../../../copy/reading'
import { formatDate } from '../../../shared/format'

defineProps<{
  status: 'idle' | 'loading' | 'ready' | 'failed'
  failure: ApiFailure | null
  files: FileSummary[]
  errors: Record<string, string>
  openingId: string
}>()

defineEmits<{
  retry: []
  choose: [file: FileSummary]
  goFiles: []
}>()

const dialog = ref<HTMLDialogElement | null>(null)

function show(): void {
  if (!dialog.value?.open) dialog.value?.showModal()
}

function hide(): void {
  if (dialog.value?.open) dialog.value.close()
}

defineExpose({ show, hide })
</script>

<template>
  <dialog ref="dialog" class="dialog dialog--wide" @cancel.prevent="hide">
    <h2 class="dialog__title">{{ READING.openFromFileTitle }}</h2>
    <p class="dialog__hint">{{ READING.openFromFileHint }}</p>

    <SkeletonBlock v-if="status === 'loading'" :lines="4" :line-height="30" />

    <FailureState
      v-else-if="status === 'failed' && failure"
      :failure="failure"
      :title="READING.openFromFileFailed"
      @retry="$emit('retry')"
    />

    <EmptyState
      v-else-if="status === 'ready' && files.length === 0"
      :title="READING.noParsedFiles"
      :action-label="READING.goFiles"
      @action="$emit('goFiles')"
    />

    <ul v-else-if="status === 'ready'" class="filelist">
      <li v-for="file in files" :key="file.id" class="filerow">
        <div class="filerow__body">
          <span class="filerow__name">{{ file.filename }}</span>
          <span class="filerow__meta">{{ formatDate(file.created_at) }}</span>
          <p v-if="errors[file.id]" class="inline-error">{{ errors[file.id] }}</p>
        </div>
        <button
          type="button"
          class="ghost"
          :disabled="openingId !== ''"
          @click="$emit('choose', file)"
        >
          {{ openingId === file.id ? READING.opening : READING.open }}
        </button>
      </li>
    </ul>

    <div class="dialog__actions">
      <button type="button" class="ghost" @click="hide">{{ READING.cancel }}</button>
    </div>
  </dialog>
</template>

<style scoped src="./controls.css"></style>
<style scoped src="./dialogs.css"></style>
