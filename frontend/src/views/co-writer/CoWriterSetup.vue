<script setup lang="ts">
/**
 * 写作首页：先被问清读者、目的和长度，再开始。
 *
 * 它不建会话、也不发消息 —— 两件事都由工作台做（`startSession` 负责建一条
 * `writing` 会话，并在连接就绪后把第一句发出去）。这里只把「这次写什么」拼成
 * 那句开场白。
 *
 * **不选人格**：写作页固定用「写作教练」，准备表对人格一个字都不提 —— 没有
 * 可挑的东西，就不必解释为什么没有选择器。
 *
 * 之所以还要这一页：空白输入框对写作不友好。写作的第一步通常不是「问」，而是
 * 先定下读者与目的。
 */

import { computed, ref } from 'vue'

import { CO_WRITER, type WritingStep } from '../../copy/collaboration'

const props = defineProps<{
  /** 建会话的请求还在飞。按钮要按住，防止连点建出两段写作。 */
  starting: boolean
  /** 建会话失败时那一句话；成功时为 null。 */
  startError: string | null
}>()

const emit = defineEmits<{
  (event: 'start', firstMessage: string): void
}>()

const step = ref<WritingStep>('free')
const content = ref('')

const steps = (Object.keys(CO_WRITER.steps) as WritingStep[]).map((key) => ({
  key,
  ...CO_WRITER.steps[key],
}))

const placeholder = computed(() =>
  step.value === 'revise' ? CO_WRITER.contentPlaceholderRevise : CO_WRITER.contentPlaceholder,
)

const canStart = computed(() => content.value.trim() !== '' && !props.starting)

/** 第一句话按步骤不同，但都先停下来问清读者与目的，而不是直接生成一整篇。 */
function firstMessage(): string {
  const text = content.value.trim()
  switch (step.value) {
    case 'outline':
      return `我想为这个主题搭一个写作提纲：${text}\n\n先问我读者是谁、写作目的和希望的长度，再给提纲。提纲里每一节写清它要回答的问题。`
    case 'draft':
      return `我想写这个主题的初稿：${text}\n\n先确认读者、目的和大致长度，然后一段一段写；每写完一段停下来，等我说继续或调整。`
    case 'revise':
      return `请帮我修改下面这段文字。先指出主要问题（结构、语气、冗余），再给修改版；改完说明你改了哪些地方，不要改变原意。\n\n${text}`
    default:
      return `我想写的是：${text}\n\n先别急着写成完整文章。先问我三个最要紧的问题，然后我们一段一段来。`
  }
}

function start(): void {
  if (!canStart.value) return
  emit('start', firstMessage())
}
</script>

<template>
  <div class="setup">
    <p class="setup__intro">{{ CO_WRITER.description }}</p>

    <section class="section">
      <h2 class="section__title">{{ CO_WRITER.stepTitle }}</h2>
      <div class="steps" role="group" :aria-label="CO_WRITER.stepTitle">
        <button
          v-for="entry in steps"
          :key="entry.key"
          type="button"
          class="step"
          :class="{ 'step--on': step === entry.key }"
          :aria-pressed="step === entry.key"
          @click="step = entry.key"
        >
          <span class="step__label">{{ entry.label }}</span>
          <span class="step__description">{{ entry.description }}</span>
        </button>
      </div>
    </section>

    <section class="section">
      <label class="section__title" for="co-writer-content">{{ CO_WRITER.contentLabel }}</label>
      <textarea
        id="co-writer-content"
        v-model="content"
        class="content"
        rows="6"
        :placeholder="placeholder"
      ></textarea>
      <p v-if="content.trim() === ''" class="hint">{{ CO_WRITER.emptyContent }}</p>
    </section>

    <div class="actions">
      <p v-if="startError" class="setup__error">{{ startError }}</p>
      <button type="button" class="primary" :disabled="!canStart" @click="start">
        {{ starting ? CO_WRITER.starting : CO_WRITER.start }}
      </button>
    </div>
  </div>
</template>

<style scoped>
.setup {
  display: flex;
  flex-direction: column;
  gap: 24px;
  max-width: 720px;
  margin: 0 auto;
  padding: 28px 32px 48px;
}

.setup__intro {
  margin: 0;
  color: var(--text-muted);
  font-size: 15px;
  line-height: 1.7;
}

.section {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.section__title {
  margin: 0;
  color: var(--text);
  font-size: 14px;
  font-weight: 650;
}

.steps {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(200px, 1fr));
  gap: 8px;
}

.step {
  display: flex;
  flex-direction: column;
  gap: 3px;
  padding: 14px 16px;
  border: 1px solid rgb(255 255 255 / 72%);
  border-radius: var(--radius);
  background: rgb(255 255 255 / 45%);
  color: var(--text);
  text-align: left;
  cursor: pointer;
  transition: all 0.2s ease;
}

.step:hover {
  background: rgb(255 255 255 / 72%);
}

.step--on {
  border-color: rgb(177 140 255 / 35%);
  background: #fff;
  box-shadow: 0 2px 10px rgb(177 140 255 / 12%);
}

.step__label {
  font-size: 15px;
  font-weight: 600;
}

.step__description {
  color: var(--text-muted);
  font-size: 14px;
  line-height: 1.5;
}

.content {
  width: 100%;
  padding: 14px 16px;
  border: 1px solid rgb(0 0 0 / 6%);
  border-radius: var(--radius);
  background: rgb(255 255 255 / 70%);
  color: var(--text);
  font: inherit;
  font-size: 15px;
  line-height: 1.7;
  outline: none;
  resize: vertical;
}

.content:focus {
  border-color: var(--color-purple);
  background: #fff;
  box-shadow: 0 0 0 4px rgb(177 140 255 / 15%);
}

.hint {
  margin: 0;
  color: var(--text-faint);
  font-size: 14px;
  line-height: 1.6;
}

.actions {
  display: flex;
  gap: 16px;
  align-items: center;
  justify-content: flex-end;
}

.setup__error {
  margin: 0;
  color: var(--danger);
  font-size: 14px;
}

.primary {
  padding: 7px 18px;
  border: 1px solid var(--accent);
  border-radius: var(--radius-sm);
  background: var(--accent);
  color: #fff;
  font-size: 15px;
  font-weight: 600;
  cursor: pointer;
}

.primary:disabled {
  background: #b9c6e6;
  border-color: #b9c6e6;
  cursor: default;
}
</style>
