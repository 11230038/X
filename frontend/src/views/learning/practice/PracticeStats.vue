<script setup lang="ts">
/**
 * 练习统计。挂在练习页右侧面板里，只有一张「你的学习记录」。
 *
 * 统计失败是**部分失败**：主队列照常能做，这里单独给重试。
 *
 * 卡里只放接口真给得出的三项：复习次数、正确 / 错误、准确率。演示稿还有
 * "平均用时"和"良好及以上"，但复习记录没有单题用时，自评界面也还没采集
 * 评分 —— 这两项宁可不画，也不填示意值。
 *
 * 取数按"累计"的口气要一段长窗口：`/practice/stats` 是画图用的窗口接口，
 * 默认只给 30 个桶、最多 400 个。这里按周取满约五年，比按天的默认窗口宽得
 * 多，也不至于用一天的桶铺到上限；更老的记录这个接口本来就数不到，真要全
 * 量得另开一个汇总接口。
 */

import { computed, onMounted, ref } from 'vue'

import type { ApiFailure } from '../../../api/client'
import { getPracticeStats, type PracticeStats } from '../../../api/practice'
import FailureState from '../../../components/common/FailureState.vue'
import SkeletonBlock from '../../../components/common/SkeletonBlock.vue'
import { PRACTICE } from '../../../copy/practice'
import { formatPercent } from '../../../shared/format'
import { toFailure } from '../../../shared/resource'
import AppIcon from '../../../components/common/AppIcon.vue'

type LoadStatus = 'idle' | 'loading' | 'ready' | 'failed'

/** 约五年。周桶铺满也不会碰到接口 400 个桶的上限。 */
const WINDOW_WEEKS = 255

const stats = ref<PracticeStats | null>(null)
const statsStatus = ref<LoadStatus>('idle')
const statsFailure = ref<ApiFailure | null>(null)

async function loadStats(): Promise<void> {
  statsStatus.value = 'loading'
  statsFailure.value = null
  try {
    const from = new Date(Date.now() - WINDOW_WEEKS * 7 * 24 * 60 * 60 * 1000)
    stats.value = await getPracticeStats({ from: from.toISOString(), bucket: 'week' })
    statsStatus.value = 'ready'
  } catch (cause) {
    statsFailure.value = toFailure(cause)
    statsStatus.value = 'failed'
  }
}

/** 数字全从序列加总，与接口同一口径：准确率只除判过的题（跳过不算）。 */
const totals = computed(() => {
  let reviews = 0
  let correct = 0
  let wrong = 0
  for (const point of stats.value?.series ?? []) {
    reviews += point.reviews
    correct += point.correct
    wrong += point.wrong
  }
  const judged = correct + wrong
  return {
    reviews,
    correct,
    wrong,
    accuracy: judged === 0 ? 0 : correct / judged,
  }
})

onMounted(() => {
  void loadStats()
})
</script>

<template>
  <section class="stats">
    <SkeletonBlock v-if="statsStatus === 'loading'" :lines="5" :line-height="34" />

    <FailureState
      v-else-if="statsStatus === 'failed' && statsFailure"
      :failure="statsFailure"
      :title="PRACTICE.statsFailed"
      @retry="loadStats"
    />

    <template v-else-if="stats">
      <section class="record">
        <h3 class="record__title">
          <AppIcon name="chart" />
          {{ PRACTICE.recordTitle }}
        </h3>
        <dl class="record__list">
          <div class="record__row">
            <dt><AppIcon name="calendar" /> {{ PRACTICE.recordReviews }}</dt>
            <dd>{{ PRACTICE.recordTimes(totals.reviews) }}</dd>
          </div>
          <div class="record__row">
            <dt><AppIcon name="check" /> {{ PRACTICE.recordResult }}</dt>
            <dd>{{ totals.correct }} / {{ totals.wrong }}</dd>
          </div>
          <div class="record__row">
            <dt><AppIcon name="target" /> {{ PRACTICE.recordAccuracy }}</dt>
            <dd>{{ formatPercent(totals.accuracy, 0) }}</dd>
          </div>
        </dl>
        <p class="record__quote">{{ PRACTICE.recordQuote }}</p>
      </section>

      <p v-if="totals.reviews === 0" class="stats__empty">{{ PRACTICE.statsEmpty }}</p>
    </template>
  </section>
</template>

<style scoped src="../Practice.css"></style>
