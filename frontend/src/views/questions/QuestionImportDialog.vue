<script setup lang="ts">
/**
 * 题库导入。两步：先 `dry_run` 预览逐行结果，确认之后才写入。
 *
 * 预览之后改动了原文，旧预览立即作废 —— 否则确认写入的是另一段原文。这张
 * 对话框自己保存这段状态，导入成功后通知父级重读列表与分类。
 */

import { nextTick, ref, watch } from 'vue'

import { describeFailure } from '../../api/errors'
import { importQuestions, type ImportReport } from '../../api/questions'
import { QUESTIONS } from '../../copy/questions'
import { toFailure } from '../../shared/resource'
import { notify } from '../../shared/toast'

import { OUTCOME_LABELS } from './labels'

const emit = defineEmits<{ (event: 'imported'): void }>()

const dialog = ref<HTMLDialogElement | null>(null)
const text = ref('')
const busy = ref(false)
const error = ref<string | null>(null)
const report = ref<ImportReport | null>(null)
const committed = ref(false)

function describe(cause: unknown): string {
  return describeFailure(toFailure(cause))
}

// 原文改了，旧预览就不再是"这段原文的结果"。
watch(text, () => {
  report.value = null
  committed.value = false
  error.value = null
})

function reset(): void {
  text.value = ''
  report.value = null
  committed.value = false
  error.value = null
}

async function open(): Promise<void> {
  reset()
  await nextTick()
  if (dialog.value && !dialog.value.open) dialog.value.showModal()
}

function close(): void {
  if (busy.value) return
  if (dialog.value?.open) dialog.value.close()
}

async function preview(): Promise<void> {
  const content = text.value
  if (content.trim() === '') {
    error.value = QUESTIONS.importEmpty
    return
  }
  busy.value = true
  error.value = null
  try {
    report.value = await importQuestions(content, true)
    committed.value = false
  } catch (cause) {
    error.value = describe(cause)
  } finally {
    busy.value = false
  }
}

async function commit(): Promise<void> {
  const content = text.value
  const current = report.value
  if (content.trim() === '' || !current || current.writable === 0) return
  busy.value = true
  error.value = null
  try {
    const result = await importQuestions(content, false)
    report.value = result
    committed.value = true
    notify(QUESTIONS.importSucceeded(result.writable), 'success')
    emit('imported')
  } catch (cause) {
    error.value = describe(cause)
  } finally {
    busy.value = false
  }
}

defineExpose({ open })
</script>

<template>
  <dialog ref="dialog" class="import" @cancel.prevent="close" @close="close">
    <header class="import__head">
      <h2 class="import__title">
        {{ committed ? QUESTIONS.importResultTitle : QUESTIONS.importTitle }}
      </h2>
      <button type="button" class="import__close" aria-label="关闭" @click="close">✕</button>
    </header>

    <p class="import__hint">{{ QUESTIONS.importHint }}</p>

    <textarea
      v-if="!committed"
      v-model="text"
      class="import__text"
      rows="8"
      :placeholder="QUESTIONS.importPlaceholder"
      :disabled="busy"
      :aria-label="QUESTIONS.importHint"
    ></textarea>

    <p v-if="error" class="import__error">{{ error }}</p>
    <p v-else-if="busy" class="import__loading">正在处理…</p>

    <section v-if="report" class="import__report">
      <p class="import__summary">
        {{ QUESTIONS.importSummary(report.lines, report.writable) }}
      </p>
      <ul class="import__entries">
        <li
          v-for="entry in report.entries"
          :key="`${entry.line}-${entry.question_id}`"
          class="import__entry"
        >
          <span class="import__line">{{ QUESTIONS.lineNumber(entry.line) }}</span>
          <span
            class="import__outcome"
            :class="{ 'import__outcome--ok': entry.outcome === 'writable' }"
          >
            {{ OUTCOME_LABELS[entry.outcome] ?? entry.outcome }}
          </span>
          <span v-if="entry.question_id" class="import__id">{{ entry.question_id }}</span>
          <span v-if="entry.reason" class="import__reason">{{ entry.reason }}</span>
          <span v-if="entry.conflict && entry.conflicting_question_id" class="import__reason">
            {{ QUESTIONS.conflictWith(entry.conflicting_question_id, entry.conflicting_line) }}
          </span>
        </li>
      </ul>
    </section>

    <footer class="import__foot">
      <button type="button" class="ghost" :disabled="busy" @click="close">
        {{ committed ? QUESTIONS.importDone : QUESTIONS.importCancel }}
      </button>
      <template v-if="!committed">
        <button
          type="button"
          class="ghost"
          :disabled="busy || text.trim() === ''"
          @click="preview"
        >
          {{ report ? QUESTIONS.importAgain : QUESTIONS.importPreview }}
        </button>
        <button
          type="button"
          class="primary"
          :disabled="busy || !report || report.writable === 0"
          @click="commit"
        >
          {{ QUESTIONS.importCommit }}
        </button>
      </template>
    </footer>
  </dialog>
</template>

<style scoped src="../Questions.css"></style>
