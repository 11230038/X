<script setup lang="ts">
/**
 * 扩展。
 *
 * 不可用的扩展和被杀掉的原因都留在列表里；结果按服务端声明的 schema 渲染，
 * 认不出的 schema 不猜。
 */

import type { ApiFailure } from '../../../api/client'
import { describeFailure } from '../../../api/errors'
import type { ExtensionOutcome } from '../../../api/reading'
import PartialBanner from '../../../components/common/PartialBanner.vue'
import SkeletonBlock from '../../../components/common/SkeletonBlock.vue'
import { READING } from '../../../copy/reading'
import type { ExtensionManifest } from './useReadingExtensions'
import { asCards, asMoments, asQuiz, audioUrl } from './useReadingExtensions'

defineProps<{
  extensions: ExtensionManifest[]
  status: 'idle' | 'loading' | 'ready' | 'failed'
  failure: ApiFailure | null
  results: ExtensionOutcome[]
  running: string
  actionFailure: ApiFailure | null
  hasSelection: boolean
}>()

defineEmits<{
  retry: []
  run: [name: string]
}>()
</script>

<template>
  <section class="panel__section">
    <h2 class="panel__title">{{ READING.extensionsTitle }}</h2>

    <SkeletonBlock v-if="status === 'loading'" :lines="3" :line-height="28" />

    <PartialBanner
      v-else-if="status === 'failed' && failure"
      :failure="failure"
      :text="READING.extensionsFailed"
      @retry="$emit('retry')"
    />

    <p v-else-if="extensions.length === 0" class="panel__empty">
      {{ READING.extensionsEmpty }}
    </p>

    <ul v-else class="extensions">
      <li v-for="extension in extensions" :key="extension.name" class="extension">
        <div class="extension__head">
          <span class="extension__name">{{ extension.name }}</span>
          <button
            v-if="extension.available"
            type="button"
            class="ghost"
            :disabled="running !== '' || !hasSelection"
            :title="hasSelection ? '' : READING.extensionNeedsSelection"
            @click="$emit('run', extension.name)"
          >
            {{ running === extension.name ? READING.extensionRunning : READING.extensionRun }}
          </button>
        </div>
        <p v-if="!extension.available" class="extension__reason">
          {{ READING.extensionUnavailable(extension.reason ?? '') }}
        </p>
        <p v-else-if="!hasSelection" class="extension__reason">
          {{ READING.extensionNeedsSelection }}
        </p>
      </li>
    </ul>

    <p v-if="actionFailure" class="inline-error">
      {{ READING.extensionFailed }}：{{ describeFailure(actionFailure) }}
    </p>

    <div v-if="results.length > 0" class="results">
      <h3 class="results__title">{{ READING.extensionResult }}</h3>
      <section v-for="(outcome, i) in results" :key="`${outcome.name}-${i}`" class="result">
        <h4 class="result__name">{{ outcome.name }}</h4>
        <p v-if="outcome.skipped" class="result__skipped">
          {{ READING.extensionSkipped(outcome.reason ?? '') }}
        </p>
        <template v-else-if="outcome.schema === 'text' && typeof outcome.result === 'string'">
          <p class="result__text">{{ outcome.result }}</p>
        </template>
        <template v-else-if="outcome.schema === 'cards'">
          <article v-for="(card, j) in asCards(outcome.result)" :key="j" class="card">
            <h5 class="card__title">{{ card.title }}</h5>
            <p class="card__body">{{ card.body }}</p>
          </article>
        </template>
        <template v-else-if="outcome.schema === 'timeline'">
          <ul class="timeline">
            <li v-for="(moment, j) in asMoments(outcome.result)" :key="j">
              <span class="timeline__label">{{ moment.label }}</span>
              <span class="timeline__detail">{{ moment.detail }}</span>
            </li>
          </ul>
        </template>
        <template v-else-if="outcome.schema === 'quiz'">
          <div v-for="(question, j) in asQuiz(outcome.result)" :key="j" class="quiz">
            <p class="quiz__question">{{ question.question }}</p>
            <ol class="quiz__choices">
              <li v-for="(choice, k) in question.choices" :key="k">{{ choice }}</li>
            </ol>
          </div>
        </template>
        <template v-else-if="outcome.schema === 'audio_ref'">
          <p v-if="audioUrl(outcome.result)" class="result__audio">{{ audioUrl(outcome.result) }}</p>
          <p v-else class="panel__empty">这条扩展没有给出音频地址。</p>
        </template>
      </section>
    </div>
  </section>
</template>

<style scoped src="./controls.css"></style>
<style scoped src="./panel-head.css"></style>
<style scoped src="./list-reset.css"></style>
<style scoped src="./extensions.css"></style>
