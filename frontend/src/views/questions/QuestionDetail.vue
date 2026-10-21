<script setup lang="ts">
/**
 * 一道题的完整面孔。列表、复习与判分反馈都读到同一份 `QuestionEntry`，所以
 * 解析直接随题目给出，不在这里另拉一次详情。
 */

import type { QuestionEntry } from '../../api/questions'
import StatusBadge from '../../components/common/StatusBadge.vue'
import { QUESTIONS } from '../../copy/questions'
import { formatDateTime } from '../../shared/format'

import {
  isCorrectOption,
  resolvedLabel,
  resolvedTone,
  resultLabel,
  typeLabel,
} from './labels'

const props = defineProps<{ entry: QuestionEntry; busy: boolean }>()
const emit = defineEmits<{
  (event: 'bookmark'): void
  (event: 'resolve'): void
  (event: 'ask'): void
}>()
</script>

<template>
  <article class="detail">
    <header class="detail__head">
      <div class="detail__badges">
        <StatusBadge :label="typeLabel(props.entry.question.question_type)" />
        <StatusBadge
          :label="resolvedLabel(props.entry)"
          :tone="resolvedTone(props.entry)"
        />
        <StatusBadge
          v-if="props.entry.answer.bookmarked"
          :label="QUESTIONS.bookmarked"
          tone="accent"
        />
      </div>
      <div class="detail__actions">
        <button type="button" class="ghost" @click="emit('ask')">
          {{ QUESTIONS.askAbout }}
        </button>
        <button
          type="button"
          class="ghost"
          :disabled="props.busy"
          @click="emit('bookmark')"
        >
          {{ props.entry.answer.bookmarked ? QUESTIONS.bookmarked : QUESTIONS.bookmark }}
        </button>
        <button
          type="button"
          class="ghost"
          :disabled="props.busy"
          @click="emit('resolve')"
        >
          {{ props.entry.answer.resolved === true ? QUESTIONS.markOpen : QUESTIONS.markMastered }}
        </button>
      </div>
    </header>

    <p class="detail__question">{{ props.entry.question.text }}</p>

    <ol v-if="props.entry.question.options.length > 0" class="options">
      <li
        v-for="option in props.entry.question.options"
        :key="option.key"
        class="option"
        :class="{ 'option--correct': isCorrectOption(props.entry, option.key) }"
      >
        <span class="option__key">{{ option.key }}</span>
        <span class="option__text">{{ option.text }}</span>
        <span v-if="isCorrectOption(props.entry, option.key)" class="option__mark">
          {{ QUESTIONS.correctAnswer }}
        </span>
      </li>
    </ol>

    <section class="block">
      <h2 class="block__title">{{ QUESTIONS.answerDetail }}</h2>
      <p v-if="props.entry.answer.attempt_count === 0" class="muted">
        {{ QUESTIONS.neverAnswered }}
      </p>
      <template v-else>
        <p class="block__text">
          {{ props.entry.answer.user_answer || '（没有记录作答文本）' }}
        </p>
        <p class="block__meta">
          <span v-if="props.entry.answer.is_correct === true" class="verdict verdict--ok">
            {{ QUESTIONS.correct }}
          </span>
          <span
            v-else-if="props.entry.answer.is_correct === false"
            class="verdict verdict--bad"
          >
            {{ QUESTIONS.incorrect }}
          </span>
          <span v-else class="verdict">
            {{ resultLabel(props.entry.answer.result) || '未判定' }}
          </span>
          <span>{{ QUESTIONS.attempts(props.entry.answer.attempt_count) }}</span>
          <span>
            {{ QUESTIONS.lastAnswered }}：{{ formatDateTime(props.entry.answer.updated_at) }}
          </span>
        </p>
      </template>
      <p v-if="props.entry.answer.due_at" class="block__meta">
        {{ QUESTIONS.dueAt }}：{{ formatDateTime(props.entry.answer.due_at) }}
      </p>
    </section>

    <section class="block">
      <h2 class="block__title">{{ QUESTIONS.correctAnswer }}</h2>
      <p class="block__text">{{ props.entry.question.correct_answer || '—' }}</p>
    </section>

    <section class="block">
      <h2 class="block__title">{{ QUESTIONS.explanation }}</h2>
      <p class="block__text">
        {{ props.entry.question.explanation || QUESTIONS.noExplanation }}
      </p>
    </section>

    <dl class="meta">
      <template v-if="props.entry.question.difficulty">
        <dt>{{ QUESTIONS.difficulty }}</dt>
        <dd>{{ props.entry.question.difficulty }}</dd>
      </template>
      <template v-if="props.entry.question.source">
        <dt>{{ QUESTIONS.source }}</dt>
        <dd>{{ props.entry.question.source }}</dd>
      </template>
      <template v-if="props.entry.question.categories.length > 0">
        <dt>{{ QUESTIONS.categories }}</dt>
        <dd>{{ props.entry.question.categories.join('、') }}</dd>
      </template>
      <template v-if="(props.entry.question.knowledge_points ?? []).length > 0">
        <dt>{{ QUESTIONS.knowledgePoints }}</dt>
        <dd>{{ (props.entry.question.knowledge_points ?? []).join('、') }}</dd>
      </template>
    </dl>
  </article>
</template>

<style scoped src="../Questions.css"></style>
