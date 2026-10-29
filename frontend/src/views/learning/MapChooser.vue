<script setup lang="ts">
/**
 * 学习地图的一级页：选一条路径。
 *
 * 与掌握路径的一级页故意不同风格：那边是带进度条的卡片网格，先看"学到哪"；
 * 这里是横排条目，名字下面铺一排知识点小格，先看"地图里有什么"。
 *
 * 小格只用列表接口已经给出的 `mastered / knowledge_points`：实心是已掌握，
 * 空心是其余，超过 12 个折成 `+N`。它是地图的引子，读屏信息由右侧计数承担，
 * 所以整排小格 `aria-hidden`。
 */

import { RouterLink, type RouteLocationRaw } from 'vue-router'

import type { MasterySummary } from '../../api/mastery'
import { MASTERY } from '../../copy/mastery'
import { formatDateTime } from '../../shared/format'
import AppIcon from '../../components/common/AppIcon.vue'

const props = defineProps<{
  paths: MasterySummary[]
  to: (item: MasterySummary) => RouteLocationRaw
}>()

const MAX_DOTS = 12

function dotCount(item: MasterySummary): number {
  return Math.min(item.knowledge_points, MAX_DOTS)
}

/** 已掌握的点排在前面；具体哪几个掌握，列表接口没给，也不假装知道。 */
function isFilled(item: MasterySummary, index: number): boolean {
  return index < item.mastered
}

function overflowCount(item: MasterySummary): number {
  return Math.max(item.knowledge_points - MAX_DOTS, 0)
}
</script>

<template>
  <ul class="map-list">
    <li v-for="item in props.paths" :key="item.path_id">
      <RouterLink :to="props.to(item)" class="map-row">
        <!-- 图标固定用地图：掌握路径的卡片仍按每条路径自己的 emoji 换成对应
             图标（`iconForEmoji`），两张一级页从图标就能分开。路径的身份由
             名称、说明与右侧计数承担。 -->
        <span class="map-row__icon" aria-hidden="true"><AppIcon name="map" /></span>

        <div class="map-row__body">
          <h3 class="map-row__name">{{ item.goal }}</h3>
          <p class="map-row__desc">{{ item.description || MASTERY.pathNoDescription }}</p>
          <div class="map-row__dots" aria-hidden="true">
            <span
              v-for="n in dotCount(item)"
              :key="n"
              class="map-row__dot"
              :class="{ 'map-row__dot--on': isFilled(item, n - 1) }"
            ></span>
            <span v-if="overflowCount(item) > 0" class="map-row__more">
              +{{ overflowCount(item) }}
            </span>
          </div>
        </div>

        <!-- 三行各自成段：读屏按停顿念，不会连成"3 个模块更新于"。 -->
        <div class="map-row__meta">
          <p class="map-row__count">
            {{ MASTERY.masteredCount(item.mastered, item.knowledge_points) }}
          </p>
          <p class="map-row__modules">{{ MASTERY.modules(item.modules) }}</p>
          <p class="map-row__time">
            {{ MASTERY.updatedAt }} {{ formatDateTime(item.updated_at) }}
          </p>
        </div>

        <span class="map-row__go" aria-hidden="true">→</span>
      </RouterLink>
    </li>
  </ul>
</template>

<style scoped src="./MapChooser.css"></style>
