<script setup lang="ts">
/**
 * 输入区。`设计说明书.md` §3.4 说它是整个产品里被使用最多次的控件。
 *
 * stage 0 只有多行输入框与发送/停止。§3.4 表里的另外四样（附件与 chips、资源
 * 选择、模式选择、上传态）**都没有** —— 它们各自连着一整套后端契约（附件的
 * 四个状态、知识库选择、人格与技能），不是这个纵切面能顺带的。
 *
 * 照着文档做的两条：
 *
 * - **发送与停止在同一个位置**（§3.4）。不是一个按钮加一个按钮，是同一个按钮
 *   换形态 —— 生成中它就是"停止"。
 * - **高度随内容增长到上限后内部滚动**（§3.4）。上限是算出来的行高，不是
 *   `rows` 属性。
 */

import { nextTick, ref, watch } from 'vue'

const props = defineProps<{
  /** 正在生成。此时发送键变成停止键。 */
  running: boolean
  /** 服务端停下来等回答。此时发出去的是 `submit_reply`，不是新一轮。 */
  waiting: boolean
  /** 没有连上时不能用。 */
  disabled: boolean
  placeholder: string
}>()

const emit = defineEmits<{
  (event: 'send', text: string): void
  (event: 'stop'): void
}>()

/** 最多长到几行，超过就内部滚动。 */
const MAX_ROWS = 10
const LINE_HEIGHT = 22
const PADDING = 18

const text = ref('')
const field = ref<HTMLTextAreaElement | null>(null)

function resize(): void {
  const element = field.value
  if (!element) {
    return
  }
  element.style.height = 'auto'
  element.style.height = `${Math.min(element.scrollHeight, MAX_ROWS * LINE_HEIGHT + PADDING)}px`
}

// 发出去之后清空，高度要跟着收回来 —— 否则空输入框会停在一个撑开的高度上。
watch(text, () => {
  void nextTick(resize)
})

function send(): void {
  const value = text.value.trim()
  if (value === '' || props.running || props.disabled) {
    return
  }
  text.value = ''
  emit('send', value)
}

/** 回车发送，`Shift+Enter` 换行。这是输入框的通行约定，不是这里定的。 */
function onKeydown(event: KeyboardEvent): void {
  if (event.key === 'Enter' && !event.shiftKey && !event.isComposing) {
    event.preventDefault()
    send()
  }
}
</script>

<template>
  <div class="composer">
    <!--
      澄清追问的提示。§3.4 把它列为"内联提示，用于澄清追问" —— 所以这是一条
      状态说明，不是解释性小字，且它只在真的在追问时出现。
    -->
    <p v-if="props.waiting" class="composer__hint">正在等你的回答</p>

    <div class="composer__box">
      <textarea
        ref="field"
        v-model="text"
        class="composer__field"
        rows="1"
        :disabled="props.disabled"
        :placeholder="props.placeholder"
        :aria-label="props.waiting ? '回答上面那个问题' : '输入要问的话'"
        @input="resize"
        @keydown="onKeydown"
      />

      <!--
        同一个位置两种形态（§3.4）。停止用次样式而非主样式：它是一个打断，
        不是这一屏最该点的东西。
      -->
      <button
        v-if="props.running"
        type="button"
        class="composer__action composer__action--stop"
        @click="emit('stop')"
      >
        停止
      </button>
      <button
        v-else
        type="button"
        class="composer__action"
        :disabled="props.disabled || text.trim() === ''"
        @click="send"
      >
        {{ props.waiting ? '回答' : '发送' }}
      </button>
    </div>
  </div>
</template>

<style scoped>
.composer {
  padding: 0 24px 18px;
}

.composer__hint {
  padding: 0 2px 6px;
  color: var(--warning);
  font-size: 12px;
}

.composer__box {
  display: flex;
  gap: 10px;
  align-items: flex-end;
  padding: 8px 8px 8px 12px;
  border: 1px solid var(--border-strong);
  border-radius: var(--radius);
  background: var(--surface);
}

.composer__box:focus-within {
  border-color: var(--accent);
}

.composer__field {
  flex: 1;
  min-height: 22px;
  padding: 4px 0;
  border: 0;
  outline: none;
  background: none;
  line-height: 22px;
  resize: none;
}

.composer__field:disabled {
  color: var(--text-faint);
}

.composer__action {
  flex: none;
  padding: 7px 16px;
  border: 1px solid transparent;
  border-radius: var(--radius-sm);
  background: var(--accent);
  color: #fff;
  font-weight: 600;
  cursor: pointer;
}

.composer__action:disabled {
  background: #b9c6e6;
  cursor: default;
}

.composer__action--stop {
  border-color: var(--border-strong);
  background: var(--surface);
  color: var(--text);
}

.composer__action--stop:hover {
  border-color: var(--danger);
  color: var(--danger);
}
</style>
