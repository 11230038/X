<script setup lang="ts">
/**
 * 掌握路径一级页的路径卡片。
 *
 * 卡片尺度照知识库目录：白卡、28 内边距、52 图标、20px 名称，带进度条，先看
 * "学到哪"。学习地图的一级页用 `MapChooser`，风格不同，两张页面不混。
 */

import { RouterLink, type RouteLocationRaw } from 'vue-router'

import type { MasterySummary } from '../../api/mastery'
import { MASTERY } from '../../copy/mastery'
import { formatDateTime } from '../../shared/format'
import AppIcon, { iconForEmoji } from '../../components/common/AppIcon.vue'

const props = defineProps<{
  paths: MasterySummary[]
  to: (item: MasterySummary) => RouteLocationRaw
}>()

/** 卡片上的进度条：口径与总览页一致，已掌握 / 全部知识点。 */
function percentOf(item: MasterySummary): number {
  return item.knowledge_points === 0
    ? 0
    : Math.round((item.mastered / item.knowledge_points) * 100)
}
</script>

<template>
  <div class="path-grid">
    <RouterLink
      v-for="item in props.paths"
      :key="item.path_id"
      :to="props.to(item)"
      class="path-card"
    >
      <div class="path-card__top">
        <span class="path-card__icon" aria-hidden="true">
          <AppIcon :name="iconForEmoji(item.emoji, 'mastery')" />
        </span>
        <span class="path-card__name">{{ item.goal }}</span>
      </div>
      <p class="path-card__desc">{{ item.description || MASTERY.pathNoDescription }}</p>
      <p class="path-card__meta">
        {{ MASTERY.masteredCount(item.mastered, item.knowledge_points) }} ·
        {{ MASTERY.modules(item.modules) }}
      </p>
      <div
        class="path-card__bar"
        role="progressbar"
        aria-valuemin="0"
        aria-valuemax="100"
        :aria-valuenow="percentOf(item)"
      >
        <span :style="{ width: `${percentOf(item)}%` }"></span>
      </div>
      <p class="path-card__time">{{ MASTERY.updatedAt }} {{ formatDateTime(item.updated_at) }}</p>
    </RouterLink>
  </div>
</template>

<style scoped src="./PathChooser.css"></style>
