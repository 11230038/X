<script setup lang="ts">
/**
 * 选一道题库里的题。
 *
 * 题目问答页默认是一段空对话，输入区的「选择题目」打开这个弹窗；选中之后
 * 由页面把题目写成第一句话的正文放进输入框，用户能看见也能接着改。题目
 * 内容必须写全：`deep_solve` 读不到题库，模型能看到的只有这段文字。
 *
 * 用 `dialog` 元素而不是自绘浮层：`Esc` 关闭、焦点进入与返回都由浏览器负责，
 * 与材料选择器、确认框保持同一种交互约定。
 */

import { onBeforeUnmount, ref, watch } from 'vue'
import { useRouter } from 'vue-router'

import type { ApiFailure } from '../../api/client'
import { listQuestions, type QuestionEntry } from '../../api/questions'
import EmptyState from '../../components/common/EmptyState.vue'
import FailureState from '../../components/common/FailureState.vue'
import { QUESTIONS, QUESTION_CHAT } from '../../copy/questions'
import { truncate } from '../../shared/format'
import { toFailure } from '../../shared/resource'
import { typeLabel } from './labels'

const props = defineProps<{
  open: boolean
  /** 从练习页/题库页带着题号进来、但没读到那道题时，在顶部说明一句。 */
  notice?: boolean
}>()

const emit = defineEmits<{
  (event: 'close'): void
  (event: 'pick', entry: QuestionEntry): void
}>()

const router = useRouter()

const LIST_LIMIT = 30

const dialog = ref<HTMLDialogElement | null>(null)
const query = ref('')
const entries = ref<QuestionEntry[]>([])
const loading = ref(false)
const firstLoad = ref(true)
const failure = ref<ApiFailure | null>(null)

/** 只让最后发出的那次搜索写回列表；先发后到的结果不能盖掉当前关键词。 */
let latest = 0
let debounce: ReturnType<typeof setTimeout> | null = null

watch(
  () => props.open,
  (open) => {
    const element = dialog.value
    if (!element) return
    if (open && !element.open) {
      element.showModal()
      void load()
    }
    if (!open && element.open) element.close()
  },
)

watch(query, () => {
  if (debounce !== null) clearTimeout(debounce)
  debounce = setTimeout(() => void load(), 250)
})

async function load(): Promise<void> {
  const seq = ++latest
  loading.value = true
  try {
    const result = await listQuestions({
      q: query.value.trim() || undefined,
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

function close(): void {
  emit('close')
}

function goToQuestions(): void {
  close()
  void router.push({ name: 'questions' })
}

function pick(entry: QuestionEntry): void {
  emit('pick', entry)
}

onBeforeUnmount(() => {
  if (debounce !== null) clearTimeout(debounce)
})
</script>

<template>
  <dialog ref="dialog" class="picker" @cancel.prevent="close" @close="close">
    <header class="picker__head">
      <h2 class="picker__title">{{ QUESTION_CHAT.pickerTitle }}</h2>
      <button type="button" class="picker__close" :aria-label="QUESTION_CHAT.pickerClose" @click="close">
        ✕
      </button>
    </header>

    <p v-if="props.notice" class="picker__notice" role="status">
      {{ QUESTION_CHAT.preselectFailed }}
    </p>

    <div class="picker__search">
      <input
        v-model="query"
        class="picker__field"
        type="search"
        :placeholder="QUESTION_CHAT.searchPlaceholder"
        :aria-label="QUESTION_CHAT.searchPlaceholder"
      />
    </div>

    <div class="picker__body">
      <p v-if="firstLoad && loading" class="picker__loading">{{ QUESTION_CHAT.pickerLoading }}</p>

      <FailureState
        v-else-if="failure && entries.length === 0"
        :failure="failure"
        :title="QUESTION_CHAT.loadFailed"
        @retry="load"
      />

      <EmptyState
        v-else-if="entries.length === 0 && query.trim() !== ''"
        :title="QUESTION_CHAT.noMatch"
        :hint="QUESTION_CHAT.noMatchHint"
      />

      <EmptyState
        v-else-if="entries.length === 0"
        :title="QUESTION_CHAT.empty"
        :hint="QUESTION_CHAT.emptyHint"
        :action-label="QUESTIONS.title"
        @action="goToQuestions"
      />

      <ul v-else class="picker__list">
        <li v-for="entry in entries" :key="entry.question.question_id">
          <button type="button" class="picker__row" @click="pick(entry)">
            <span class="picker__text">{{ truncate(entry.question.text, 96) }}</span>
            <span class="picker__meta">
              {{ typeLabel(entry.question.question_type) }}
              <template v-if="entry.question.difficulty">
                · {{ entry.question.difficulty }}
              </template>
            </span>
          </button>
        </li>
      </ul>

      <p v-if="entries.length >= LIST_LIMIT" class="picker__more">
        {{ QUESTION_CHAT.resultLimit(LIST_LIMIT) }}
      </p>
    </div>
  </dialog>
</template>

<style scoped>
.picker {
  width: min(560px, calc(100vw - 40px));
  max-height: min(620px, calc(100vh - 80px));
  padding: 0;
  border: 1px solid var(--border-strong);
  border-radius: var(--radius);
  background: var(--surface);
  color: var(--text);
}

.picker::backdrop {
  background: rgb(20 26 38 / 35%);
}

.picker__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 13px 16px 8px;
}

.picker__title {
  font-size: 15px;
  font-weight: 650;
}

.picker__close {
  padding: 3px 7px;
  border: 0;
  border-radius: 4px;
  background: transparent;
  color: var(--text-faint);
  cursor: pointer;
}

.picker__close:hover {
  background: var(--surface-sunken);
  color: var(--text);
}

.picker__notice {
  margin: 0;
  padding: 0 16px 8px;
  color: var(--text-muted);
  font-size: 12px;
  line-height: 1.6;
}

.picker__search {
  padding: 0 16px 10px;
}

.picker__field {
  width: 100%;
  padding: 7px 10px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--surface);
  color: var(--text);
  font-size: 13px;
}

.picker__field:focus {
  border-color: var(--accent);
  outline: none;
}

.picker__body {
  max-height: 420px;
  overflow-y: auto;
  padding: 0 10px 12px;
}

.picker__loading {
  padding: 20px 6px;
  color: var(--text-muted);
  font-size: 13px;
}

.picker__list {
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.picker__row {
  display: flex;
  gap: 12px;
  align-items: flex-start;
  justify-content: space-between;
  width: 100%;
  padding: 9px 10px;
  border: 1px solid transparent;
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--text);
  text-align: left;
  cursor: pointer;
}

.picker__row:hover {
  border-color: var(--border);
  background: var(--surface-sunken);
}

.picker__text {
  display: -webkit-box;
  overflow: hidden;
  font-size: 13px;
  line-height: 1.55;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
}

.picker__meta {
  flex: none;
  color: var(--text-muted);
  font-size: 11px;
  white-space: nowrap;
}

.picker__more {
  margin: 8px 6px 0;
  color: var(--text-faint);
  font-size: 12px;
}
</style>
