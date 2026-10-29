<script setup lang="ts">
/** 左侧材料目录。只负责列出与选中，不加载正文。 */

import type { ReadingMaterial } from '../../../api/reading'
import SkeletonBlock from '../../../components/common/SkeletonBlock.vue'
import { READING } from '../../../copy/reading'
import { formatDate } from '../../../shared/format'
import AppIcon from '../../../components/common/AppIcon.vue'

defineProps<{
  materials: ReadingMaterial[]
  currentId: string
  loading: boolean
}>()

defineEmits<{
  select: [materialId: string]
  openFile: []
}>()
</script>

<template>
  <aside class="materials">
    <!-- 演示稿目录卡的第一块是「返回」，这里对应「从文件打开」。 -->
    <button type="button" class="toc-back" @click="$emit('openFile')">
      <AppIcon name="folder" />
      {{ READING.openFromFile }}
    </button>

    <h2 class="materials__title">
      <AppIcon name="reading" />
      {{ READING.materials }}
    </h2>

    <SkeletonBlock v-if="loading" :lines="6" :line-height="34" />

    <p v-else-if="materials.length === 0" class="materials__empty">
      {{ READING.materialsEmpty }}
    </p>

    <ul v-else class="materials__list">
      <li v-for="material in materials" :key="material.material_id">
        <button
          type="button"
          class="material"
          :class="{ 'material--on': material.material_id === currentId }"
          @click="$emit('select', material.material_id)"
        >
          <span class="material__name">{{ material.filename }}</span>
          <span class="material__meta">
            {{ material.source_type || '材料' }} · {{ formatDate(material.updated_at) }}
          </span>
        </button>
      </li>
    </ul>

    <p v-if="materials.length === 0" class="materials__hint">
      {{ READING.materialsEmptyHint }}
    </p>
  </aside>
</template>

<style scoped src="./controls.css"></style>
<style scoped src="./materials.css"></style>
