<script setup lang="ts">
/**
 * 掌握路径的出题弹窗。
 *
 * 模型用 `mastery_question` 出题时，题干、题型与选项都在那次调用的参数里。
 * 这一页把它们抬到眼前：选择题点选项，填空题与主观题写文字，提交就是**一条
 * 普通的用户消息**——下一轮模型拿这条答案去 `mastery_grade` 判卷。它不直接
 * 调判卷接口，因为判卷是能力里的一个工具，不是一条 REST 路径。
 *
 * 与澄清弹窗的区别：服务端**没有**停下来等这道题，关掉它不会卡住任何东西。
 * 所以「稍后作答」只是收起这一层，输入区仍然可以答。
 */

import { computed, nextTick, onMounted, ref, watch } from 'vue'

import { MASTERY } from '../../copy/mastery'
import type {
  MasteryQuestion,
  MasteryQuestionBank,
} from '../../views/chat/useMasteryQuestion'

const props = defineProps<{
  /** 当前待作答的题。空表示没有。 */
  question: MasteryQuestion | null
  /** 由页面控制的开合；页面按"回合结束且有未答的题"决定什么时候打开。 */
  open: boolean
  /** 连接没就绪时不能提交；与输入区的禁用条件一致。 */
  disabled: boolean
  /** 这道题在不在题库里：成功存进去的、核对到的、没核到的，说法不一样。 */
  bank: MasteryQuestionBank
}>()

const emit = defineEmits<{
  (event: 'submit', answer: string): void
  (event: 'dismiss'): void
}>()

const dialog = ref<HTMLDialogElement | null>(null)
const field = ref<HTMLInputElement | HTMLTextAreaElement | null>(null)
const text = ref('')
/** 已选的选项键，**按选项顺序**排——答案要写成 "A,C"。 */
const picked = ref<string[]>([])

const isChoice = computed(
  () => props.question?.type === 'choice' && (props.question?.options.length ?? 0) > 0,
)

/**
 * 题库的状态说明。`checking` 不出一行——查询通常在一个渲染帧里就回来了，
 * 先画一句"正在核对"只会闪一下；其余四种都说得出口。
 */
const bankNote = computed(() => {
  switch (props.bank) {
    case 'saved':
      return MASTERY.questionBankSaved
    case 'found':
      return MASTERY.questionBankFound
    case 'missing':
      return MASTERY.questionBankMissing
    case 'unknown':
      return MASTERY.questionBankUnknown
    default:
      return ''
  }
})

const answer = computed(() =>
  isChoice.value ? picked.value.join(',') : text.value.trim(),
)

const canSubmit = computed(() => answer.value !== '' && !props.disabled)

function reset(): void {
  text.value = ''
  picked.value = []
}

function show(): void {
  if (!props.question) return
  if (!dialog.value?.open) dialog.value?.showModal()
  void nextTick(() => field.value?.focus())
}

function hide(): void {
  if (dialog.value?.open) dialog.value.close()
}

function toggle(key: string): void {
  const chosen = new Set(picked.value)
  if (chosen.has(key)) chosen.delete(key)
  else chosen.add(key)
  // 点击顺序不算数：判卷认的是选项键，写成 "A,C" 而不是 "C,A"。
  picked.value = (props.question?.options ?? [])
    .filter((option) => chosen.has(option.key))
    .map((option) => option.key)
}

function submit(): void {
  if (!canSubmit.value) return
  emit('submit', answer.value)
}

watch(
  () => props.open,
  (open) => {
    if (open) show()
    else hide()
  },
)

watch(
  () => props.question?.callId,
  () => {
    reset()
    if (props.open) show()
  },
)

onMounted(() => {
  if (props.open) show()
})
</script>

<template>
  <dialog
    ref="dialog"
    class="quiz"
    aria-labelledby="quiz-title"
    @cancel.prevent="emit('dismiss')"
  >
    <h2 id="quiz-title" class="quiz__title">{{ MASTERY.questionTitle }}</h2>
    <p v-if="question" class="quiz__stem">{{ question.stem }}</p>
    <p v-if="bankNote" class="quiz__bank">{{ bankNote }}</p>

    <div v-if="question && isChoice" class="quiz__options">
      <button
        v-for="option in question.options"
        :key="option.key"
        type="button"
        class="quiz__option"
        :class="{ 'quiz__option--picked': picked.includes(option.key) }"
        :aria-pressed="picked.includes(option.key)"
        @click="toggle(option.key)"
      >
        <span class="quiz__key">{{ option.key }}</span>
        <span class="quiz__text">{{ option.text }}</span>
      </button>
      <p class="quiz__hint">{{ MASTERY.questionChoiceHint }}</p>
    </div>

    <textarea
      v-else-if="question && question.type === 'short_answer'"
      ref="field"
      v-model="text"
      class="quiz__input quiz__input--long"
      rows="4"
      :placeholder="MASTERY.questionShortPlaceholder"
      :aria-label="MASTERY.questionShortPlaceholder"
    ></textarea>

    <input
      v-else-if="question"
      ref="field"
      v-model="text"
      type="text"
      class="quiz__input"
      :placeholder="MASTERY.questionInputPlaceholder"
      :aria-label="MASTERY.questionInputPlaceholder"
      @keydown.enter.exact.prevent="submit"
    />

    <p v-if="disabled" class="quiz__offline">{{ MASTERY.questionOffline }}</p>

    <div class="quiz__actions">
      <button type="button" class="quiz__submit" :disabled="!canSubmit" @click="submit">
        {{ MASTERY.questionSubmit }}
      </button>
      <button type="button" class="quiz__later" @click="emit('dismiss')">
        {{ MASTERY.questionLater }}
      </button>
    </div>
  </dialog>
</template>

<style scoped>
.quiz {
  width: min(560px, calc(100vw - 32px));
  max-height: calc(100vh - 64px);
  padding: 20px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--surface);
  color: var(--text);
  box-shadow: 0 18px 48px rgb(20 26 38 / 18%);
  overflow: auto;
}

.quiz::backdrop {
  background: rgb(20 26 38 / 32%);
}

.quiz__title {
  margin: 0 0 10px;
  font-size: 15px;
  font-weight: 650;
}

.quiz__stem {
  margin: 0 0 12px;
  font-size: 14px;
  line-height: 1.65;
  white-space: pre-wrap;
}

.quiz__bank {
  margin: -4px 0 12px;
  color: var(--text-muted);
  font-size: 12px;
}

.quiz__options {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.quiz__option {
  display: flex;
  gap: 10px;
  align-items: flex-start;
  width: 100%;
  padding: 8px 11px;
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-sm);
  background: var(--surface);
  color: var(--text);
  text-align: left;
  font-size: 14px;
  line-height: 1.55;
  cursor: pointer;
}

.quiz__option:hover {
  background: var(--surface-sunken);
}

.quiz__option--picked {
  border-color: var(--accent);
  background: var(--accent-soft);
}

.quiz__key {
  flex: none;
  min-width: 18px;
  color: var(--text-muted);
  font-family: var(--font-mono);
  font-size: 12px;
  font-weight: 650;
}

.quiz__text {
  overflow-wrap: anywhere;
}

.quiz__hint {
  margin: 2px 0 0;
  color: var(--text-faint);
  font-size: 12px;
}

.quiz__input {
  width: 100%;
  padding: 9px 11px;
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-sm);
  background: var(--surface);
  color: var(--text);
  font: inherit;
  font-size: 14px;
  line-height: 1.6;
}

.quiz__input--long {
  min-height: 96px;
  resize: vertical;
}

.quiz__input:focus {
  border-color: var(--accent);
  outline: 2px solid var(--accent-soft);
}

.quiz__offline {
  margin: 10px 0 0;
  color: var(--text-muted);
  font-size: 12px;
}

.quiz__actions {
  display: flex;
  gap: 8px;
  justify-content: flex-end;
  margin-top: 14px;
}

.quiz__submit {
  padding: 6px 14px;
  border: 1px solid var(--accent);
  border-radius: var(--radius-sm);
  background: var(--accent);
  color: #fff;
  font-size: 13px;
}

.quiz__submit:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.quiz__later {
  padding: 6px 12px;
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-sm);
  background: var(--surface);
  color: var(--text-muted);
  font-size: 13px;
}

.quiz__later:hover {
  background: var(--surface-sunken);
}
</style>
