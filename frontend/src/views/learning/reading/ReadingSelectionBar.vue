<script setup lang="ts">
/** 选区工具条。出现的每个动作都直接用当前选区坐标。 */

import { READING } from '../../../copy/reading'
import { truncate } from '../../../shared/format'
import AppIcon from '../../../components/common/AppIcon.vue'

defineProps<{
  text: string
  saving: boolean
  /** 助手还在答上一问。 */
  explaining: boolean
}>()

defineEmits<{
  copy: []
  highlight: []
  favorite: []
  note: []
  explain: []
  ask: []
  clear: []
}>()
</script>

<template>
  <div class="selectionbar">
    <span class="selectionbar__meta">
      {{ READING.selectionChars(text.length) }}
      <span class="selectionbar__snippet">{{ truncate(text, 60) }}</span>
    </span>
    <div class="selectionbar__actions">
      <button type="button" class="ghost ghost--accent" :disabled="explaining" @click="$emit('explain')">
        <AppIcon name="sparkle" /> {{ READING.explainSelection }}
      </button>
      <button type="button" class="ghost" @click="$emit('copy')">
        {{ READING.copySelection }}
      </button>
      <button type="button" class="ghost" :disabled="saving" @click="$emit('highlight')">
        {{ READING.addHighlight }}
      </button>
      <button type="button" class="ghost" :disabled="saving" @click="$emit('favorite')">
        {{ READING.addFavorite }}
      </button>
      <button type="button" class="ghost" :disabled="saving" @click="$emit('note')">
        {{ READING.addNote }}
      </button>
      <button type="button" class="ghost" @click="$emit('ask')">
        {{ READING.askInChat }}
      </button>
      <button type="button" class="ghost" @click="$emit('clear')">
        {{ READING.clearSelection }}
      </button>
    </div>
  </div>
</template>

<style scoped src="./controls.css"></style>
<style scoped src="./selection-bar.css"></style>
