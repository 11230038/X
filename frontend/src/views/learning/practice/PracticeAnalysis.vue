<script setup lang="ts">
/**
 * 错题解析。解析的是队列里眼前这道题。
 *
 * 走一次性问答（`POST /chat/complete`）：不开会话、不写消息、不带历史，答案
 * 只活在这次响应里 —— 所以面板里看过的解析不会凭空长成一段对话。
 *
 * 三个决定：
 * 1. **点按钮才发**，切到这一栏不等于想花一次模型调用；解析里还会说出正确
 *    答案，所以**这道题没作答之前按钮是禁用的**，下面那句提示说明为什么。
 *    作答与否由主列写进 `currentQuestion.ts`（面板看不到作答用的 `phase`）。
 * 2. **题换了，解析就作废**：这段回答属于上一道题，搬不到下一道。
 * 3. 题目自带答案与解析时一并交给模型，让它有依据可讲，不必自己猜。
 */

import { computed, ref, watch } from 'vue'

import type { ApiFailure } from '../../../api/client'
import { completeQuestion } from '../../../api/directChat'
import type { QueueItem } from '../../../api/practice'
import type { QuestionType } from '../../../api/questions'
import FailureState from '../../../components/common/FailureState.vue'
import MarkdownBody from '../../../components/common/MarkdownBody.vue'
import SkeletonBlock from '../../../components/common/SkeletonBlock.vue'
import { PRACTICE } from '../../../copy/practice'
import { QUESTIONS } from '../../../copy/questions'
import { toFailure } from '../../../shared/resource'

import { practiceAnswered, practiceQuestion } from './currentQuestion'
import AppIcon from '../../../components/common/AppIcon.vue'

type Status = 'idle' | 'asking' | 'ready' | 'failed'

const TYPE_LABELS: Record<QuestionType, string> = {
  choice: QUESTIONS.typeChoice,
  fill_blank: QUESTIONS.typeFillBlank,
  short_answer: QUESTIONS.typeShortAnswer,
}

const status = ref<Status>('idle')
const answer = ref('')
const failure = ref<ApiFailure | null>(null)

const item = computed<QueueItem | null>(() => practiceQuestion.value)

/** 面板里放不下一整段题干，取开头一行作为"正在解析谁"。 */
const targetText = computed(() => {
  const text = item.value?.question.text.trim().replace(/\s+/g, ' ') ?? ''
  return text.length > 42 ? `${text.slice(0, 42)}…` : text
})

function promptFor(target: QueueItem): string {
  const question = target.question
  const lines = [
    '你是错题解析助手。请用中文解析下面这道题。',
    '',
    '要求：',
    '1. 先点明这道题考的是哪个知识点；',
    '2. 再分步骤讲清正确的解题思路；',
    '3. 指出最容易写错或误选的地方；',
    '4. 最后用一句话写出可以记住的要点。',
    '题目之外的事实不要编造；题目给了答案和解析，就以它们为准。',
    '',
    `题型：${TYPE_LABELS[question.question_type]}`,
    `题目：${question.text}`,
  ]
  if (question.options.length > 0) {
    lines.push('选项：', ...question.options.map((option) => `${option.key}. ${option.text}`))
  }
  if (question.correct_answer) lines.push(`正确答案：${question.correct_answer}`)
  if (question.explanation) lines.push(`题目原解析：${question.explanation}`)
  return lines.join('\n')
}

async function analyze(): Promise<void> {
  const target = item.value
  // 没作答就不解析：按钮已经禁用了，这里再挡一次（键盘回车、旧页面状态）。
  if (!target || !practiceAnswered.value) return
  const questionId = target.question.question_id
  status.value = 'asking'
  failure.value = null
  try {
    const { answer: text } = await completeQuestion(promptFor(target))
    // 回来时眼前已经是另一道题：这条回答不要了。
    if (item.value?.question.question_id !== questionId) return
    answer.value = text
    status.value = 'ready'
  } catch (cause) {
    if (item.value?.question.question_id !== questionId) return
    failure.value = toFailure(cause)
    status.value = 'failed'
  }
}

// 题换了，上一道题的解析跟着作废。
watch(
  () => item.value?.question.question_id ?? '',
  () => {
    status.value = 'idle'
    answer.value = ''
    failure.value = null
  },
)
</script>

<template>
  <section class="analysis">
    <template v-if="item">
      <h3 class="record__title">
        <AppIcon name="memory" />
        {{ PRACTICE.analysisTitle }}
      </h3>
      <p class="analysis__target">{{ PRACTICE.analysisTarget }}：{{ targetText }}</p>

      <FailureState
        v-if="status === 'failed' && failure"
        :failure="failure"
        :title="PRACTICE.analysisFailed"
        @retry="analyze"
      />

      <template v-else-if="status === 'asking'">
        <p class="analysis__loading">{{ PRACTICE.analysisLoading }}</p>
        <SkeletonBlock :lines="6" :line-height="26" />
      </template>

      <template v-else-if="status === 'ready'">
        <div class="analysis__answer">
          <MarkdownBody :text="answer" complete />
        </div>
        <button type="button" class="ghost analysis__again" @click="analyze">
          {{ PRACTICE.analysisAgain }}
        </button>
      </template>

      <div v-else class="analysis__entry">
        <button
          type="button"
          class="primary"
          :disabled="!practiceAnswered"
          @click="analyze"
        >
          {{ PRACTICE.analysisAsk }}
        </button>
        <p class="analysis__hint">
          {{ practiceAnswered ? PRACTICE.analysisHint : PRACTICE.analysisLocked }}
        </p>
      </div>
    </template>

    <p v-else class="stats__empty">{{ PRACTICE.analysisEmpty }}</p>
  </section>
</template>

<style scoped src="../Practice.css"></style>
