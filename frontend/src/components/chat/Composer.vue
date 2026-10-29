<script setup lang="ts">
/**
 * 输入区。`设计说明书.md` §3.4 说它是整个产品里被使用最多次的控件。
 *
 * 照着文档做的几条：
 *
 * - **发送与停止在同一个位置**。不是一个按钮加一个按钮，是同一个按钮换形态
 *   —— 生成中它就是"停止"。
 * - **高度随内容增长到上限后内部滚动**。上限是算出来的行高，不是 `rows`。
 * - **附件、知识库、选区都是 chip**，在输入框上方；材料属于**这一轮**，不是
 *   这个会话的设置。
 *
 * 材料选择只负责带上去，不预判可读性：附件能不能读由服务端的地面化结果说。
 *
 * 人格不在这里：它是这一轮的说法，但演示稿把它放在顶栏的伙伴选择器里
 * （`.partner-selector`），所以输入区只剩下"这一轮额外带上了什么"的 chip。
 */

import { nextTick, ref, watch } from 'vue'

import { CHAT } from '../../copy/chat'
import AppIcon from '../common/AppIcon.vue'

export interface ComposerChip {
  id: string
  label: string
}

export interface PersonaChoice {
  id: string
  label: string
  detail?: string
}

const props = defineProps<{
  /** 正在生成。此时发送键变成停止键。 */
  running: boolean
  /** 服务端停下来等回答。此时发出去的是 `submit_reply`，不是新一轮。 */
  waiting: boolean
  /** 没有连上时不能用。 */
  disabled: boolean
  placeholder: string
  attachments: readonly ComposerChip[]
  knowledgeBases: readonly ComposerChip[]
  /** 阅读页带过来的选区；为空表示这一轮不带选区。 */
  selection: { label: string } | null
  /**
   * 进入页面时预填的第一句话。协同写作页把它拼好带过来；只做初值，不跟着
   * prop 变化覆盖用户已经敲进去的字。
   */
  draft?: string
}>()

const emit = defineEmits<{
  (event: 'send', text: string): void
  (event: 'stop'): void
  (event: 'open-attachments'): void
  (event: 'open-knowledge-bases'): void
  (event: 'remove-attachment', id: string): void
  (event: 'remove-knowledge-base', id: string): void
  (event: 'clear-selection'): void
}>()

/** 最多长到几行，超过就内部滚动。 */
const MAX_ROWS = 10
const LINE_HEIGHT = 22
const PADDING = 18

const text = ref(props.draft ?? '')
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

/**
 * 把一段文字放进输入框并聚焦。题目问答选完题之后用它：题目内容要看得见、
 * 能被用户接着改，而不是悄悄附在发送动作后面。
 */
function replace(value: string): void {
  text.value = value
  void nextTick(() => {
    resize()
    field.value?.focus()
  })
}

defineExpose({ replace, value: () => text.value })
</script>

<template>
  <div class="composer">
    <!--
      澄清追问的提示。§3.4 把它列为"内联提示，用于澄清追问" —— 所以这是一条
      状态说明，不是解释性小字，且它只在真的在追问时出现。
    -->
    <p v-if="props.waiting" class="composer__hint">正在等你的回答</p>

    <!-- 演示稿把工具排成输入框上方的一行 chip，输入框本身只剩文字与发送键。 -->
    <div v-if="!props.waiting" class="composer__tools">
      <slot name="tools" />
      <button type="button" class="composer__tool" @click="emit('open-attachments')">
        {{ CHAT.attachments }}
        <span v-if="attachments.length > 0" class="composer__badge">{{ attachments.length }}</span>
      </button>
      <button type="button" class="composer__tool" @click="emit('open-knowledge-bases')">
        {{ CHAT.knowledgeBases }}
        <span v-if="knowledgeBases.length > 0" class="composer__badge">
          {{ knowledgeBases.length }}
        </span>
      </button>
    </div>

    <div class="composer__box">
      <div class="composer__stack">
        <!-- 材料 chip。只在开新一轮时有意义：`submit_reply` 不带材料。 -->
        <div
          v-if="!props.waiting && (attachments.length > 0 || knowledgeBases.length > 0 || selection)"
          class="chips"
        >
          <span v-for="file in attachments" :key="`file-${file.id}`" class="chip">
            <span class="chip__kind">{{ CHAT.attachments }}</span>
            <span class="chip__label">{{ file.label }}</span>
            <button
              type="button"
              class="chip__remove"
              :aria-label="`移除附件 ${file.label}`"
              @click="emit('remove-attachment', file.id)"
            >
              <AppIcon name="close" />
            </button>
          </span>

          <span v-for="kb in knowledgeBases" :key="`kb-${kb.id}`" class="chip">
            <span class="chip__kind">{{ CHAT.knowledgeBases }}</span>
            <span class="chip__label">{{ kb.label }}</span>
            <button
              type="button"
              class="chip__remove"
              :aria-label="`移除知识库 ${kb.label}`"
              @click="emit('remove-knowledge-base', kb.id)"
            >
              <AppIcon name="close" />
            </button>
          </span>

          <span v-if="selection" class="chip chip--selection">
            <span class="chip__kind">{{ CHAT.selectionChip }}</span>
            <span class="chip__label">{{ selection.label }}</span>
            <button
              type="button"
              class="chip__remove"
              :aria-label="CHAT.clearSelection"
              @click="emit('clear-selection')"
            >
              <AppIcon name="close" />
            </button>
          </span>
        </div>

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

      </div>

      <!--
        同一个位置两种形态（§3.4）。停止用次样式而非主样式：它是一个打断，
        不是这一屏最该点的东西。
      -->
      <button
        v-if="props.running"
        type="button"
        class="composer__action composer__action--stop"
        :aria-label="CHAT.stop"
        @click="emit('stop')"
      >
        <span aria-hidden="true">■</span>
      </button>
      <button
        v-else
        type="button"
        class="composer__action"
        :disabled="props.disabled || text.trim() === ''"
        :aria-label="props.waiting ? CHAT.reply : CHAT.send"
        @click="send"
      >
        <span aria-hidden="true">↑</span>
      </button>
    </div>
  </div>
</template>

<style scoped>
/* 演示稿 `.chat-input-area`：页面留白 40 之后，输入条再往上收 16。
   主列底面还有 32 的内边距，所以这里合起来是 48。 */
.composer {
  padding: 0 40px 48px;
}

.composer__hint {
  padding: 0 4px 8px;
  color: var(--warning);
  font-size: 13px;
  font-weight: 600;
}

/* 演示稿的输入条：一整块液态玻璃，圆角很大，聚焦时描边变紫。 */
.composer__box {
  display: flex;
  gap: 16px;
  align-items: center;
  padding: 12px 12px 12px 24px;
  border: 1px solid var(--glass-border);
  border-radius: 999px;
  background: var(--glass-bg);
  box-shadow: var(--glass-shadow);
  backdrop-filter: blur(24px);
  -webkit-backdrop-filter: blur(24px);
  transition: box-shadow 0.3s ease, border-color 0.3s ease;
}

.composer__box:focus-within {
  border-color: rgb(177 140 255 / 55%);
  box-shadow: 0 0 0 4px rgb(177 140 255 / 12%), var(--glass-shadow);
}

.composer__stack {
  flex: 1;
  min-width: 0;
}

.composer__field {
  display: block;
  width: 100%;
  min-height: 22px;
  padding: 0;
  border: 0;
  outline: none;
  background: none;
  font-size: 16px;
  line-height: 22px;
  resize: none;
}

.composer__field:disabled {
  color: var(--text-faint);
}

/* 演示稿 `.chat-tools`：输入框上方的 chip 行。 */
.composer__tools {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  padding: 0 0 16px;
}

/* 演示稿 `.tool-chip`。 */
.composer__tool {
  display: inline-flex;
  gap: 6px;
  align-items: center;
  padding: 8px 14px;
  border: 1px solid rgb(0 0 0 / 6%);
  border-radius: 999px;
  background: #fff;
  color: var(--text);
  font-size: 16px;
  font-weight: 600;
  cursor: pointer;
  transition: all 0.3s cubic-bezier(0.25, 0.8, 0.25, 1);
}

.composer__tool:hover {
  border-color: var(--accent);
  color: var(--accent-ink);
  transform: translateY(-1px);
}

.composer__badge {
  display: grid;
  place-items: center;
  min-width: 15px;
  height: 15px;
  padding: 0 3px;
  border-radius: 8px;
  background: var(--accent);
  color: #fff;
  font-size: 12px;
}

.chips {
  display: flex;
  flex-wrap: wrap;
  gap: 5px;
  padding: 2px 0 6px;
}

.chip {
  display: inline-flex;
  gap: 5px;
  align-items: center;
  max-width: 100%;
  padding: 2px 6px;
  border: 1px solid var(--border);
  border-radius: 999px;
  background: var(--surface-sunken);
  font-size: 13px;
}

.chip--selection {
  border-color: #f0dca8;
  background: var(--warning-soft);
}

.chip__kind {
  color: var(--text-faint);
}

.chip__label {
  max-width: 220px;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.chip__remove {
  padding: 0 3px;
  border: 0;
  background: transparent;
  color: var(--text-faint);
  font-size: 13px;
  cursor: pointer;
}

.chip__remove:hover {
  color: var(--danger);
}

/* 演示稿 `.send-btn`：44×44 的圆形渐变键。 */
.composer__action {
  display: grid;
  flex: none;
  place-items: center;
  width: 44px;
  height: 44px;
  padding: 0;
  border: 1px solid transparent;
  border-radius: 50%;
  background: var(--warm-gradient);
  color: #fff;
  font-size: 18px;
  font-weight: 700;
  line-height: 1;
  cursor: pointer;
  box-shadow: 0 8px 24px rgb(177 140 255 / 40%);
  transition: all 0.25s cubic-bezier(0.34, 1.56, 0.64, 1);
}

.composer__action:hover:not(:disabled) {
  transform: translateY(-1px);
  box-shadow: 0 12px 30px rgb(177 140 255 / 50%);
}

.composer__action:active:not(:disabled) {
  transform: scale(0.97);
}

.composer__action:disabled {
  background: #d9cdf1;
  box-shadow: none;
  cursor: default;
}

.composer__action--stop {
  border-color: rgb(0 0 0 / 8%);
  background: #fff;
  color: var(--text);
  box-shadow: none;
}

.composer__action--stop:hover {
  border-color: var(--danger);
  color: var(--danger);
}
</style>
