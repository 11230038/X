<script setup lang="ts">
/**
 * 知识库文档的预览弹窗：显示服务端实际解析出的正文。
 *
 * 预览可能在向量还没就绪时打开——全文一直可读，`pending` 不是"不能预览"。
 * 长文档按窗口追加，不一次把几十万字塞进对话框。
 */

import { ref } from 'vue'

import type { ApiFailure } from '../../api/client'
import type { KbDocument, KbDocumentTextView } from '../../api/knowledgeBases'
import FailureState from '../../components/common/FailureState.vue'
import SkeletonBlock from '../../components/common/SkeletonBlock.vue'
import { KNOWLEDGE } from '../../copy/knowledge'
import { formatDateTime, formatNumber } from '../../shared/format'

defineProps<{
  document: KbDocument | null
  text: KbDocumentTextView | null
  status: 'idle' | 'loading' | 'ready' | 'failed'
  failure: ApiFailure | null
  loadingMore: boolean
}>()

const emit = defineEmits<{
  close: []
  retry: []
  loadMore: []
}>()

const dialog = ref<HTMLDialogElement | null>(null)

function show(): void {
  if (!dialog.value?.open) dialog.value?.showModal()
}

function hide(): void {
  if (dialog.value?.open) dialog.value.close()
}

function close(): void {
  hide()
  emit('close')
}

defineExpose({ show, hide })
</script>

<template>
  <dialog ref="dialog" class="dialog dialog--wide" @cancel.prevent="close">
    <header class="preview__head">
      <div class="preview__headtext">
        <h2 class="dialog__title">{{ document?.filename ?? KNOWLEDGE.previewTitle }}</h2>
        <p v-if="document" class="dialog__hint">
          {{ document.engine }} · {{ formatNumber(document.text_chars) }} 字 · 更新于
          {{ formatDateTime(document.updated_at) }}
        </p>
      </div>
      <button
        type="button"
        class="preview__close"
        :aria-label="KNOWLEDGE.previewClose"
        @click="close"
      >
        ×
      </button>
    </header>

    <p v-if="document?.truncated" class="preview__note">{{ KNOWLEDGE.previewTruncated }}</p>

    <SkeletonBlock v-if="status === 'loading'" :lines="8" :line-height="18" />
    <FailureState
      v-else-if="status === 'failed' && failure"
      :title="KNOWLEDGE.previewFailed"
      :failure="failure"
      @retry="$emit('retry')"
    />
    <template v-else-if="text">
      <p v-if="text.total_chars > 0" class="preview__range">
        {{ KNOWLEDGE.previewRange(text.char_start + 1, text.char_end, text.total_chars) }}
      </p>
      <pre class="preview__text">{{ text.text || KNOWLEDGE.previewEmpty }}</pre>
      <button
        v-if="text.char_end < text.total_chars"
        type="button"
        class="button preview__more"
        :disabled="loadingMore"
        @click="$emit('loadMore')"
      >
        {{ loadingMore ? KNOWLEDGE.previewLoadingMore : KNOWLEDGE.previewLoadMore }}
      </button>
    </template>

    <div class="dialog__actions">
      <button type="button" class="button" @click="close">{{ KNOWLEDGE.previewClose }}</button>
    </div>
  </dialog>
</template>

<style scoped src="./preview.css"></style>
