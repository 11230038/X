<script setup lang="ts">
/**
 * 练习统计。统计失败是**部分失败**：上面的队列照常能做，这里单独给重试。
 */

import { onMounted, ref } from 'vue'

import type { ApiFailure } from '../../../api/client'
import { getPracticeStats, type PracticeStats } from '../../../api/practice'
import FailureState from '../../../components/common/FailureState.vue'
import SkeletonBlock from '../../../components/common/SkeletonBlock.vue'
import { PRACTICE } from '../../../copy/practice'
import { formatPercent } from '../../../shared/format'
import { toFailure } from '../../../shared/resource'

type LoadStatus = 'idle' | 'loading' | 'ready' | 'failed'

const stats = ref<PracticeStats | null>(null)
const statsStatus = ref<LoadStatus>('idle')
const statsFailure = ref<ApiFailure | null>(null)
const bucket = ref<'day' | 'week'>('day')

async function loadStats(): Promise<void> {
  statsStatus.value = 'loading'
  statsFailure.value = null
  try {
    stats.value = await getPracticeStats({ bucket: bucket.value })
    statsStatus.value = 'ready'
  } catch (cause) {
    statsFailure.value = toFailure(cause)
    statsStatus.value = 'failed'
  }
}

function switchBucket(next: 'day' | 'week'): void {
  if (bucket.value === next) return
  bucket.value = next
  void loadStats()
}

function bucketDate(label: string): string {
  return label.length >= 10 ? label.slice(0, 10) : label
}

onMounted(() => {
  void loadStats()
})
</script>

<template>
  <section class="stats">
    <header class="stats__head">
      <div>
        <h2 class="stats__title">{{ PRACTICE.statsTitle }}</h2>
        <p class="stats__description">{{ PRACTICE.statsDescription }}</p>
      </div>
      <div class="stats__switch">
        <button
          type="button"
          class="switch"
          :class="{ 'switch--on': bucket === 'day' }"
          @click="switchBucket('day')"
        >
          {{ PRACTICE.statsRangeDay }}
        </button>
        <button
          type="button"
          class="switch"
          :class="{ 'switch--on': bucket === 'week' }"
          @click="switchBucket('week')"
        >
          {{ PRACTICE.statsRangeWeek }}
        </button>
      </div>
    </header>

    <SkeletonBlock v-if="statsStatus === 'loading'" :lines="5" :line-height="34" />

    <FailureState
      v-else-if="statsStatus === 'failed' && statsFailure"
      :failure="statsFailure"
      :title="PRACTICE.statsFailed"
      @retry="loadStats"
    />

    <template v-else-if="stats">
      <p v-if="stats.series.length === 0" class="stats__empty">
        {{ PRACTICE.statsEmpty }} —— {{ PRACTICE.statsEmptyHint }}
      </p>

      <table v-else class="series">
        <thead>
          <tr>
            <th scope="col">日期</th>
            <th scope="col">{{ PRACTICE.reviews }}</th>
            <th scope="col">{{ PRACTICE.correctCount }}</th>
            <th scope="col">{{ PRACTICE.wrongCount }}</th>
            <th scope="col">{{ PRACTICE.accuracy }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="point in stats.series" :key="point.label">
            <td>{{ bucketDate(point.label) }}</td>
            <td>{{ point.reviews }}</td>
            <td>{{ point.correct }}</td>
            <td>{{ point.wrong }}</td>
            <td>{{ formatPercent(point.accuracy, 0) }}</td>
          </tr>
        </tbody>
      </table>

      <div class="distributions">
        <section class="distribution">
          <h3 class="distribution__title">{{ PRACTICE.sources }}</h3>
          <p v-if="stats.sources.length === 0" class="distribution__empty">
            {{ PRACTICE.noDistribution }}
          </p>
          <ul v-else class="distribution__list">
            <li v-for="row in stats.sources" :key="row.name">
              <span>{{ row.name || '未标注' }}</span>
              <span>{{ row.count }}</span>
            </li>
          </ul>
        </section>

        <section class="distribution">
          <h3 class="distribution__title">{{ PRACTICE.categories }}</h3>
          <p v-if="stats.categories.length === 0" class="distribution__empty">
            {{ PRACTICE.noDistribution }}
          </p>
          <ul v-else class="distribution__list">
            <li v-for="row in stats.categories" :key="row.name">
              <span>{{ row.name || '未标注' }}</span>
              <span>{{ row.count }}</span>
            </li>
          </ul>
        </section>
      </div>
    </template>
  </section>
</template>

<style scoped src="../Practice.css"></style>
