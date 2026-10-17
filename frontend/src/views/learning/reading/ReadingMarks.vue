<script setup lang="ts">
/** 批注列表。加载失败只影响这一栏，正文照常可读。 */

import type { ApiFailure } from '../../../api/client'
import type { ReadingMark } from '../../../api/reading'
import PartialBanner from '../../../components/common/PartialBanner.vue'
import SkeletonBlock from '../../../components/common/SkeletonBlock.vue'
import { READING } from '../../../copy/reading'
import { formatDateTime } from '../../../shared/format'

defineProps<{
  marks: ReadingMark[]
  status: 'idle' | 'loading' | 'ready' | 'failed'
  failure: ApiFailure | null
  saving: boolean
}>()

defineEmits<{
  retry: []
  bookmark: []
  jump: [mark: ReadingMark]
}>()

function kindLabel(mark: ReadingMark): string {
  if (mark.kind === 'note') return READING.markNote
  if (mark.kind === 'highlight') return READING.markHighlight
  return READING.markBookmark
}
</script>

<template>
  <section class="panel__section">
    <header class="panel__head">
      <h2 class="panel__title">{{ READING.marksTitle }}</h2>
      <button type="button" class="link" :disabled="saving" @click="$emit('bookmark')">
        加书签
      </button>
    </header>

    <SkeletonBlock v-if="status === 'loading'" :lines="3" :line-height="28" />

    <PartialBanner
      v-else-if="status === 'failed' && failure"
      :failure="failure"
      :text="READING.marksFailed"
      @retry="$emit('retry')"
    />

    <p v-else-if="marks.length === 0" class="panel__empty">{{ READING.marksEmpty }}</p>
    <p v-else class="panel__hint">{{ READING.marksHint }}</p>

    <ul v-if="marks.length > 0" class="marks">
      <li v-for="mark in marks" :key="mark.mark_id">
        <button type="button" class="mark" @click="$emit('jump', mark)">
          <span class="mark__kind">
            {{ kindLabel(mark) }} · {{ READING.markAt(mark.locator.ordinal) }}
          </span>
          <span v-if="mark.body" class="mark__body">{{ mark.body }}</span>
          <span class="mark__time">{{ formatDateTime(mark.created_at) }}</span>
        </button>
      </li>
    </ul>
  </section>
</template>

<style scoped src="./controls.css"></style>
<style scoped src="./panel-head.css"></style>
<style scoped src="./list-reset.css"></style>
<style scoped src="./marks.css"></style>
