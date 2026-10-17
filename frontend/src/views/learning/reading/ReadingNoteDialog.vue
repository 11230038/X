<script setup lang="ts">
/** 写批注。对话框自己开关，父组件只负责决定何时打开。 */

import { ref } from 'vue'

import type { ApiFailure } from '../../../api/client'
import { describeFailure } from '../../../api/errors'
import { READING } from '../../../copy/reading'

defineProps<{
  body: string
  saving: boolean
  failure: ApiFailure | null
}>()

const emit = defineEmits<{
  'update:body': [value: string]
  submit: []
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
  <dialog ref="dialog" class="dialog" @cancel.prevent="hide">
    <h2 class="dialog__title">{{ READING.noteTitle }}</h2>
    <p class="dialog__hint">{{ READING.noteHint }}</p>
    <textarea
      class="dialog__textarea"
      rows="5"
      :value="body"
      :placeholder="READING.notePlaceholder"
      @input="$emit('update:body', ($event.target as HTMLTextAreaElement).value)"
    ></textarea>
    <p v-if="body.trim() === ''" class="dialog__hint">{{ READING.noteEmpty }}</p>
    <p v-if="failure" class="inline-error">{{ describeFailure(failure) }}</p>
    <div class="dialog__actions">
      <button type="button" class="ghost" @click="hide">{{ READING.cancel }}</button>
      <button
        type="button"
        class="primary"
        :disabled="body.trim() === '' || saving"
        @click="$emit('submit')"
      >
        {{ saving ? READING.noteSaving : READING.saveNote }}
      </button>
    </div>
  </dialog>
</template>

<style scoped src="./controls.css"></style>
<style scoped src="./dialogs.css"></style>
