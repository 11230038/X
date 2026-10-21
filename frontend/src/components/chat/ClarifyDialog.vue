<script setup lang="ts">
/**
 * 澄清提问的弹窗。
 *
 * 服务端在一次回合中间停下来等回答时（`wait_for_input`），输入区会换成
 * 「回答」模式；但输入区在页面底部，长回答滚在上面时容易看不见。这里把
 * **同一个回答动作**抬到眼前：问题、输入框、提交都在一个 `<dialog>` 里。
 *
 * 它不自己发消息：`submit` 交给页面，走的是与输入区完全相同的那条
 * `submit_reply` 路径。关掉弹窗也不改变"这一轮还在等回答"——输入区仍然
 * 可以回答，页面上的「回答提问」可以再把它叫回来。
 */

import { computed, nextTick, ref, watch } from 'vue'

import { CHAT } from '../../copy/chat'

const props = defineProps<{
  /** 服务端正停在等回答。 */
  waiting: boolean
  /** 这一轮最新的那个问题。空串表示事件还没到。 */
  question: string
  /** 连接没就绪时不能回答；与输入区的禁用条件一致。 */
  disabled: boolean
}>()

const emit = defineEmits<{
  (event: 'submit', text: string): void
}>()

const dialog = ref<HTMLDialogElement | null>(null)
const field = ref<HTMLTextAreaElement | null>(null)
const text = ref('')

const canSubmit = computed(() => text.value.trim() !== '' && !props.disabled)

function show(): void {
  if (props.question.trim() === '') return
  if (!dialog.value?.open) dialog.value?.showModal()
  void nextTick(() => field.value?.focus())
}

function hide(): void {
  if (dialog.value?.open) dialog.value.close()
}

function dismiss(): void {
  hide()
}

function submit(): void {
  const value = text.value.trim()
  if (value === '' || props.disabled) return
  text.value = ''
  emit('submit', value)
  hide()
}

// 提问事件与 `waiting` 在同一次处理里落位；两个 watcher 都看，是因为
// `waiting` 可能先到一帧（订阅重放时尤其如此），此时问题还是空的。
watch(
  () => props.waiting,
  (waiting) => {
    if (waiting) {
      show()
      return
    }
    text.value = ''
    hide()
  },
)

watch(
  () => props.question,
  (question) => {
    if (props.waiting && question.trim() !== '') show()
  },
)

defineExpose({ show })
</script>

<template>
  <dialog
    ref="dialog"
    class="clarify"
    aria-labelledby="clarify-title"
    @cancel.prevent="dismiss"
  >
    <h2 id="clarify-title" class="clarify__title">{{ CHAT.questionTitle }}</h2>
    <p class="clarify__question">{{ question }}</p>
    <textarea
      ref="field"
      v-model="text"
      class="clarify__input"
      rows="3"
      :placeholder="CHAT.replyPlaceholder"
      :aria-label="CHAT.replyPlaceholder"
      @keydown.enter.exact.prevent="submit"
    ></textarea>
    <div class="clarify__actions">
      <button
        type="button"
        class="clarify__submit"
        :disabled="!canSubmit"
        @click="submit"
      >
        {{ CHAT.submitReply }}
      </button>
      <button type="button" class="clarify__later" @click="dismiss">
        {{ CHAT.questionLater }}
      </button>
    </div>
  </dialog>
</template>

<style scoped>
.clarify {
  width: min(520px, calc(100vw - 32px));
  padding: 20px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--surface);
  color: var(--text);
  box-shadow: 0 18px 48px rgb(20 26 38 / 18%);
}

.clarify::backdrop {
  background: rgb(20 26 38 / 32%);
}

.clarify__title {
  margin: 0 0 10px;
  font-size: 15px;
  font-weight: 650;
}

.clarify__question {
  margin: 0 0 12px;
  font-size: 14px;
  line-height: 1.65;
  white-space: pre-wrap;
}

.clarify__input {
  width: 100%;
  min-height: 84px;
  padding: 9px 11px;
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-sm);
  background: var(--surface);
  color: var(--text);
  font: inherit;
  font-size: 14px;
  line-height: 1.6;
  resize: vertical;
}

.clarify__input:focus {
  border-color: var(--accent);
  outline: 2px solid var(--accent-soft);
}

.clarify__actions {
  display: flex;
  gap: 8px;
  justify-content: flex-end;
  margin-top: 12px;
}

.clarify__submit {
  padding: 6px 14px;
  border: 1px solid var(--accent);
  border-radius: var(--radius-sm);
  background: var(--accent);
  color: #fff;
  font-size: 13px;
}

.clarify__submit:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.clarify__later {
  padding: 6px 12px;
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-sm);
  background: var(--surface);
  color: var(--text-muted);
  font-size: 13px;
}

.clarify__later:hover {
  background: var(--surface-sunken);
}
</style>
