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
 * 人格也是这一轮的选择，但它显示成下拉而不是 chip：chip 是"这一轮额外带上
 * 了什么"，人格是"这一轮用哪种说法"，而且选一次通常会连着用几轮。
 */

import { nextTick, ref, watch } from 'vue'

import { CHAT } from '../../copy/chat'
import { PERSONAS } from '../../copy/personas'

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
  /** 可选的、保存在服务端的人格。 */
  personas: readonly PersonaChoice[]
  /** 当前选中的人格 id；空串表示不指定。 */
  persona: string
  personaLoading: boolean
  /** 列表没读出来。与"没有选中"分开画，否则失败会悄悄变成"不指定人格"。 */
  personaFailure: boolean
  /**
   * 这一页锁死了人格（题目问答）。锁定时不画下拉：下拉会暗示可以换，
   * 而每一轮发出去的都是同一个人格。
   */
  personaLocked?: boolean
  /** 锁定时显示的固定人格名。 */
  personaLockLabel?: string
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
  (event: 'select-persona', id: string): void
  (event: 'retry-personas'): void
  (event: 'manage-personas'): void
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

function onPersonaChange(event: Event): void {
  emit('select-persona', (event.target as HTMLSelectElement).value)
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
              ✕
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
              ✕
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
              ✕
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

        <div v-if="!props.waiting" class="composer__tools">
          <span v-if="props.personaLocked" class="composer__tool composer__persona">
            {{ PERSONAS.composerLabel }}
            <strong class="composer__locked">
              {{ props.personaLoading ? PERSONAS.personaLoading : props.personaLockLabel }}
            </strong>
          </span>
          <label v-else class="composer__tool composer__persona">
            {{ PERSONAS.composerLabel }}
            <select
              class="composer__select"
              :value="props.persona"
              :disabled="props.personaLoading"
              :aria-label="PERSONAS.composerLabel"
              @change="onPersonaChange"
            >
              <option value="">
                {{ props.personaLoading ? PERSONAS.personaLoading : PERSONAS.noPersona }}
              </option>
              <option v-for="item in props.personas" :key="item.id" :value="item.id">
                {{ item.label }}
              </option>
            </select>
          </label>
          <button type="button" class="composer__tool" @click="emit('manage-personas')">
            {{ PERSONAS.manage }}
          </button>
          <!-- 页面自己的工具（题目问答的「选择题目」）。 -->
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
          <button
            v-if="props.personaFailure"
            type="button"
            class="composer__tool composer__tool--danger"
            @click="emit('retry-personas')"
          >
            {{ PERSONAS.personaLoadFailed }}
          </button>
        </div>
      </div>

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
        {{ CHAT.stop }}
      </button>
      <button
        v-else
        type="button"
        class="composer__action"
        :disabled="props.disabled || text.trim() === ''"
        @click="send"
      >
        {{ props.waiting ? CHAT.reply : CHAT.send }}
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

.composer__stack {
  flex: 1;
  min-width: 0;
}

.composer__field {
  display: block;
  width: 100%;
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

.composer__tools {
  display: flex;
  gap: 6px;
  padding-top: 4px;
}

.composer__tool {
  display: inline-flex;
  gap: 5px;
  align-items: center;
  padding: 2px 7px;
  border: 1px solid var(--border);
  border-radius: 999px;
  background: var(--surface-sunken);
  color: var(--text-muted);
  font-size: 11px;
  cursor: pointer;
}

.composer__tool:hover {
  border-color: var(--accent);
  color: var(--accent);
}

.composer__tool--danger {
  border-color: var(--danger);
  color: var(--danger);
}

.composer__select {
  max-width: 11em;
  border: 0;
  background: transparent;
  color: inherit;
  font: inherit;
  font-size: 11px;
  cursor: pointer;
}

.composer__select:disabled {
  color: var(--text-faint);
  cursor: default;
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
  font-size: 10px;
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
  font-size: 11px;
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
  font-size: 10px;
  cursor: pointer;
}

.chip__remove:hover {
  color: var(--danger);
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
