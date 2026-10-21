<script setup lang="ts">
/**
 * 题目问答的首页：从题库里挑一道题，再就这道题开始一段对话。
 *
 * 为什么是"选一道题"而不是给一个空输入框：这个页面不是通用对话，它的入口
 * 就是题库里的某一道题。选中的题会被写成第一句话的正文 —— `chat` 能力手里
 * 没有题库工具，模型能看到的东西只有这段文字，所以题干、选项、参考答案与
 * 解析要一次给全。
 *
 * 手动选完要点「开始提问」才发；但带着 `send=1` 进来（练习页与题库页的入口）
 * 时不停在这里：选中即建会话、把题发出去。发消息本身仍然走工作台给下来的
 * `startSession`，这样新建会话的失败、重入与路由跳转与别的页面是同一条路。
 */

import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import type { ApiFailure } from '../../api/client'
import { getQuestion, listQuestions, type QuestionEntry } from '../../api/questions'
import EmptyState from '../../components/common/EmptyState.vue'
import FailureState from '../../components/common/FailureState.vue'
import PartialBanner from '../../components/common/PartialBanner.vue'
import SkeletonBlock from '../../components/common/SkeletonBlock.vue'
import { QUESTIONS, QUESTION_CHAT } from '../../copy/questions'
import { toFailure } from '../../shared/resource'
import { truncate } from '../../shared/format'
import { typeLabel } from './labels'

const props = defineProps<{
  /** 建一段会话并把这句话发出去。工作台在会话建好、连接可用后发出。 */
  startSession: (text: string) => Promise<void>
  starting: boolean
  startError: string | null
}>()

const router = useRouter()
const route = useRoute()

const LIST_LIMIT = 30

const query = ref('')
const entries = ref<QuestionEntry[]>([])
const selected = ref<QuestionEntry | null>(null)
const ask = ref('')
const loading = ref(false)
const firstLoad = ref(true)
const failure = ref<ApiFailure | null>(null)
/** 带着题号进来但没选中（题没了、不是自己的）时说明一句，选择器照常可用。 */
const preselectFailed = ref(false)

/** 只让最后发出的那次搜索写回列表；先发后到的结果不能盖掉当前关键词。 */
let latest = 0
let debounce: ReturnType<typeof setTimeout> | null = null

const hasQuery = computed(() => query.value.trim() !== '')
const canStart = computed(
  () => selected.value !== null && ask.value.trim() !== '' && !props.starting,
)

async function load(): Promise<void> {
  const seq = ++latest
  loading.value = true
  try {
    const result = await listQuestions({
      q: hasQuery.value ? query.value.trim() : undefined,
      limit: LIST_LIMIT,
    })
    if (seq !== latest) return
    entries.value = result.questions
    failure.value = null
  } catch (cause) {
    if (seq !== latest) return
    failure.value = toFailure(cause)
  } finally {
    if (seq === latest) {
      loading.value = false
      firstLoad.value = false
    }
  }
}

watch(query, () => {
  if (debounce !== null) clearTimeout(debounce)
  debounce = setTimeout(() => void load(), 250)
})

function choose(entry: QuestionEntry): void {
  selected.value = entry
  if (ask.value.trim() === '') ask.value = QUESTION_CHAT.defaultAsk
}

/**
 * 练习页的「我不会，去提问」与题库页的「提问」都把题号放在查询串里
 * （`?question=qn_…`），并且带 `send=1`：不停在选题与输入上，选中之后直接建
 * 会话、把题目连同默认问题发给模型，地址随即变成
 * `/questions/chat/<session_id>`。
 *
 * 题号而不是题干：查询串会进历史记录，题干可能很长；而且这道题的内容要在
 * 点开时读到最新的一份。读不到就只留一句话——题可能被删了，或者不属于这个
 * 账号，选择器本身仍然能用。用户在读取途中自己选了一道，就不覆盖他的选择。
 *
 * 自动发送只做一次：先把 `send` 从地址里摘掉再发，建会话失败时留在这一页，
 * 用户可以按原来的方式重新开始，刷新也不会又建一段会话。
 */
async function preselect(): Promise<void> {
  const raw = route.query.question
  if (typeof raw !== 'string' || raw.trim() === '') return
  const questionId = raw.trim()
  const sendNow = route.query.send === '1'

  let entry: QuestionEntry
  try {
    entry = await getQuestion(questionId)
  } catch {
    preselectFailed.value = true
    return
  }
  if (selected.value !== null) return
  choose(entry)
  if (!sendNow) return

  await router.replace({ name: 'question-chat', query: { question: questionId } })
  await props.startSession(compose(entry, QUESTION_CHAT.defaultAsk))
}

/**
 * 第一句话。题目本身要说全：模型没有题库工具，看不到题干之外的东西。
 *
 * 参考答案带上选项原文，是因为选项键（`A`）离开选项表就没有意义；填空题的
 * 答案本身就是文字，不需要这一步。
 */
function compose(entry: QuestionEntry, text: string): string {
  const question = entry.question
  const lines = [QUESTION_CHAT.composeIntro, '']

  lines.push(`【${QUESTION_CHAT.composeQuestion}】${question.text}`)
  lines.push(`【${QUESTION_CHAT.composeType}】${typeLabel(question.question_type)}`)

  if (question.options.length > 0) {
    lines.push(`【${QUESTION_CHAT.composeOptions}】`)
    for (const option of question.options) {
      lines.push(`${option.key}. ${option.text}`)
    }
  }

  if (question.correct_answer) {
    const chosen = question.options.find((option) => option.key === question.correct_answer)
    lines.push(
      `【${QUESTION_CHAT.composeAnswer}】${
        chosen ? `${question.correct_answer}（${chosen.text}）` : question.correct_answer
      }`,
    )
  }

  if (question.explanation) {
    lines.push(`【${QUESTION_CHAT.composeExplanation}】${question.explanation}`)
  }

  lines.push('', `【${QUESTION_CHAT.composeAsk}】${text}`)
  return lines.join('\n')
}

async function submit(): Promise<void> {
  if (!canStart.value || selected.value === null) return
  await props.startSession(compose(selected.value, ask.value.trim()))
}

onMounted(() => {
  void load()
  void preselect()
})

onBeforeUnmount(() => {
  if (debounce !== null) clearTimeout(debounce)
})
</script>

<template>
  <div class="qc">
    <p class="qc__lead">{{ QUESTION_CHAT.lead }}</p>

    <input
      v-model="query"
      class="qc__search"
      type="search"
      :placeholder="QUESTION_CHAT.searchPlaceholder"
      :aria-label="QUESTION_CHAT.searchPlaceholder"
    />

    <p v-if="preselectFailed" class="qc__hint" role="status">
      {{ QUESTION_CHAT.preselectFailed }}
    </p>

    <SkeletonBlock v-if="firstLoad && loading" :lines="5" :line-height="42" />

    <FailureState
      v-else-if="failure && entries.length === 0"
      :failure="failure"
      :title="QUESTION_CHAT.loadFailed"
      @retry="load"
    />

    <EmptyState
      v-else-if="entries.length === 0 && hasQuery"
      :title="QUESTION_CHAT.noMatch"
      :hint="QUESTION_CHAT.noMatchHint"
    />

    <EmptyState
      v-else-if="entries.length === 0"
      :title="QUESTION_CHAT.empty"
      :hint="QUESTION_CHAT.emptyHint"
      :action-label="QUESTIONS.title"
      @action="router.push({ name: 'questions' })"
    />

    <template v-else>
      <PartialBanner
        v-if="failure"
        :failure="failure"
        :text="QUESTION_CHAT.loadFailed"
        @retry="load"
      />

      <ul class="qc__list">
        <li v-for="entry in entries" :key="entry.question.question_id">
          <button
            type="button"
            class="qrow"
            :class="{
              'qrow--on': selected?.question.question_id === entry.question.question_id,
            }"
            :aria-pressed="selected?.question.question_id === entry.question.question_id"
            @click="choose(entry)"
          >
            <span class="qrow__text">{{ truncate(entry.question.text, 96) }}</span>
            <span class="qrow__meta">
              {{ typeLabel(entry.question.question_type) }}
              <template v-if="entry.question.difficulty">
                · {{ entry.question.difficulty }}
              </template>
            </span>
          </button>
        </li>
      </ul>

      <p v-if="entries.length >= LIST_LIMIT" class="qc__hint">
        {{ QUESTION_CHAT.resultLimit(LIST_LIMIT) }}
      </p>
    </template>

    <form class="qc__ask" @submit.prevent="submit">
      <template v-if="selected">
        <p class="qc__picked">
          <span class="qc__picked-label">{{ QUESTION_CHAT.selectedLabel }}</span>
          <span class="qc__picked-text">{{ selected.question.text }}</span>
        </p>

        <label class="qc__ask-label" for="question-chat-ask">{{ QUESTION_CHAT.askLabel }}</label>
        <textarea
          id="question-chat-ask"
          v-model="ask"
          class="qc__ask-input"
          rows="3"
          :placeholder="QUESTION_CHAT.askPlaceholder"
        />
      </template>

      <p v-else class="qc__hint">{{ QUESTION_CHAT.pickPrompt }}</p>

      <p v-if="startError" class="qc__error" role="alert">{{ startError }}</p>

      <button type="submit" class="qc__start" :disabled="!canStart">
        {{ starting ? QUESTION_CHAT.starting : QUESTION_CHAT.start }}
      </button>
    </form>
  </div>
</template>

<style scoped>
.qc {
  display: flex;
  flex-direction: column;
  gap: 14px;
  max-width: 720px;
  margin: 0 auto;
  padding: 28px 24px 32px;
}

.qc__lead {
  margin: 0;
  color: var(--text-muted);
  font-size: 13px;
  line-height: 1.7;
}

.qc__search {
  width: 100%;
  padding: 8px 12px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--surface);
  font-size: 13px;
}

.qc__search:focus {
  border-color: var(--accent);
  outline: none;
}

.qc__list {
  display: flex;
  flex-direction: column;
  gap: 6px;
  max-height: 34vh;
  margin: 0;
  padding: 0;
  overflow-y: auto;
  list-style: none;
}

.qrow {
  display: flex;
  gap: 16px;
  align-items: flex-start;
  justify-content: space-between;
  width: 100%;
  padding: 10px 12px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--surface);
  color: var(--text);
  text-align: left;
  cursor: pointer;
}

.qrow:hover {
  border-color: var(--border-strong);
}

.qrow--on {
  border-color: var(--accent);
  background: var(--accent-soft);
}

.qrow__text {
  display: -webkit-box;
  overflow: hidden;
  font-size: 13px;
  line-height: 1.55;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
}

.qrow__meta {
  flex: none;
  color: var(--text-muted);
  font-size: 11px;
  white-space: nowrap;
}

.qc__hint {
  margin: 0;
  color: var(--text-muted);
  font-size: 12px;
  line-height: 1.6;
}

.qc__ask {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin-top: 4px;
  padding: 14px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--surface);
}

.qc__picked {
  display: flex;
  gap: 8px;
  align-items: baseline;
  margin: 0;
  font-size: 12px;
  line-height: 1.6;
}

.qc__picked-label {
  flex: none;
  color: var(--text-muted);
}

.qc__picked-text {
  display: -webkit-box;
  overflow: hidden;
  font-weight: 600;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
}

.qc__ask-label {
  color: var(--text-muted);
  font-size: 12px;
}

.qc__ask-input {
  width: 100%;
  padding: 8px 10px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--surface);
  color: var(--text);
  font-family: inherit;
  font-size: 13px;
  line-height: 1.6;
  resize: vertical;
}

.qc__ask-input:focus {
  border-color: var(--accent);
  outline: none;
}

.qc__error {
  margin: 0;
  color: var(--danger);
  font-size: 12px;
}

.qc__start {
  align-self: flex-end;
  padding: 7px 16px;
  border: 1px solid var(--accent);
  border-radius: var(--radius-sm);
  background: var(--accent);
  color: #fff;
  font-size: 13px;
  cursor: pointer;
}

.qc__start:disabled {
  cursor: default;
  opacity: 0.5;
}
</style>
