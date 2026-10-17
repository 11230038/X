<script setup lang="ts">
/**
 * 文件详情里的"文档"一段：已解析 → 文本抽屉；未解析 → 加入知识库。
 *
 * 已上传但未解析不是失败：这里明确说"还没解析"，并给出真正能改变它的动作。
 */

import type { ApiFailure } from '../../api/client'
import type { DocumentView, FileTextView } from '../../api/files'
import type { KnowledgeBase } from '../../api/knowledgeBases'
import FailureState from '../../components/common/FailureState.vue'
import SkeletonBlock from '../../components/common/SkeletonBlock.vue'
import StatusBadge from '../../components/common/StatusBadge.vue'
import { FILES, VECTOR_STATE_LABELS, VECTOR_STATE_TONES } from '../../copy/files'
import { formatDateTime, formatNumber } from '../../shared/format'

import type { FileMode } from './useFileList'

defineProps<{
  document: DocumentView | null
  mode: FileMode
  showText: boolean
  textStatus: 'idle' | 'loading' | 'ready' | 'failed'
  textFailure: ApiFailure | null
  text: FileTextView | null
  textLoadingMore: boolean
  kbPickerOpen: boolean
  kbOptions: KnowledgeBase[] | null
  kbOptionsFailure: string | null
  kbTargetId: string
  kbBusy: boolean
}>()

defineEmits<{
  toggleText: []
  loadText: [append: boolean]
  openKbPicker: []
  addToKnowledgeBase: []
  'update:kbTargetId': [value: string]
  'update:kbPickerOpen': [value: boolean]
}>()
</script>

<template>
  <section class="parsed">
    <h3 class="section-title">{{ FILES.document }}</h3>

    <template v-if="document">
      <div class="parsed__badges">
        <StatusBadge
          :label="VECTOR_STATE_LABELS[document.vector_state] ?? document.vector_state"
          :tone="VECTOR_STATE_TONES[document.vector_state] ?? 'neutral'"
        />
        <StatusBadge v-if="document.truncated" :label="FILES.truncated" tone="warning" />
      </div>
      <dl class="facts">
        <div class="fact">
          <dt>{{ FILES.engine }}</dt>
          <dd>{{ document.engine }}</dd>
        </div>
        <div class="fact">
          <dt>{{ FILES.textChars }}</dt>
          <dd>{{ formatNumber(document.text_chars) }}</dd>
        </div>
        <div class="fact">
          <dt>{{ FILES.chunkSize }}</dt>
          <dd>{{ formatNumber(document.chunk_size) }}</dd>
        </div>
        <div class="fact">
          <dt>{{ FILES.chunkOverlap }}</dt>
          <dd>{{ formatNumber(document.chunk_overlap) }}</dd>
        </div>
        <div class="fact">
          <dt>{{ FILES.parsedAt }}</dt>
          <dd>{{ formatDateTime(document.updated_at) }}</dd>
        </div>
      </dl>
      <p v-if="document.vector_error" class="parsed__error">{{ document.vector_error }}</p>

      <div class="text-block">
        <button type="button" class="button" @click="$emit('toggleText')">
          {{ showText ? '收起文本' : FILES.textTitle }}
        </button>
        <div v-if="showText" class="text-block__body">
          <SkeletonBlock v-if="textStatus === 'loading'" :lines="5" />
          <FailureState
            v-else-if="textStatus === 'failed' && textFailure"
            title="文本没能加载出来"
            :failure="textFailure"
            @retry="$emit('loadText', false)"
          />
          <template v-else-if="text">
            <p class="text-block__range">
              {{ FILES.textRange(text.char_start + 1, text.char_end, text.total_chars) }}
            </p>
            <pre class="text-block__text">{{ text.text }}</pre>
            <button
              v-if="text.char_end < text.total_chars"
              type="button"
              class="link-button"
              :disabled="textLoadingMore"
              @click="$emit('loadText', true)"
            >
              {{ textLoadingMore ? '正在加载…' : FILES.textLoadMore }}
            </button>
          </template>
        </div>
      </div>
    </template>

    <div v-else class="unparsed">
      <p class="unparsed__title">{{ FILES.unparsedTitle }}</p>
      <p class="unparsed__hint">{{ FILES.unparsedHint }}</p>

      <div v-if="mode === 'active'" class="unparsed__actions">
        <button type="button" class="button button--primary" @click="$emit('openKbPicker')">
          {{ FILES.addToKnowledgeBase }}
        </button>
        <RouterLink class="button" to="/knowledge-bases">
          {{ FILES.createKnowledgeBaseLink }}
        </RouterLink>
      </div>

      <div v-if="kbPickerOpen && mode === 'active'" class="kb-picker">
        <p v-if="kbOptionsFailure" class="pane-error" role="alert">{{ kbOptionsFailure }}</p>
        <template v-else-if="kbOptions === null">
          <SkeletonBlock :lines="2" />
        </template>
        <template v-else-if="kbOptions.length === 0">
          <p class="kb-picker__empty">{{ FILES.noKnowledgeBases }}</p>
          <RouterLink class="button" to="/knowledge-bases">
            {{ FILES.createKnowledgeBaseLink }}
          </RouterLink>
        </template>
        <template v-else>
          <label class="kb-picker__label" for="kb-picker-select">
            {{ FILES.chooseKnowledgeBase }}
          </label>
          <select
            id="kb-picker-select"
            class="kb-picker__select"
            :value="kbTargetId"
            @change="$emit('update:kbTargetId', ($event.target as HTMLSelectElement).value)"
          >
            <option v-for="kb in kbOptions" :key="kb.kb_id" :value="kb.kb_id">
              {{ kb.name }}
            </option>
          </select>
          <div class="kb-picker__actions">
            <button
              type="button"
              class="button button--primary"
              :disabled="kbBusy || !kbTargetId"
              @click="$emit('addToKnowledgeBase')"
            >
              {{ kbBusy ? '正在加入…' : FILES.addToKnowledgeBaseAction }}
            </button>
            <button type="button" class="button" @click="$emit('update:kbPickerOpen', false)">
              取消
            </button>
          </div>
        </template>
      </div>
    </div>
  </section>
</template>

<style scoped src="../Files.css"></style>
