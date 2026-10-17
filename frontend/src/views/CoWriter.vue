<script setup lang="ts">
/**
 * 协同写作：把一次写作的开场问准备好，然后交给对话。
 *
 * 这一页不复制输入区、也不保存文稿。它做的事只有两件：
 *
 * 1. 选一个伙伴（就是人格）和一种写作步骤；
 * 2. 把"这次写什么"拼成第一句话，连同人格 id 一起带进 `/chat`。
 *
 * 真正的来回在对话里，附件与知识库也在那里的输入区带上。之所以要这一页，
 * 是因为空白输入框对写作不友好 —— 写作的第一步通常不是"问",而是先被问清
 * 读者、目的和长度。
 */

import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'

import { canSurface } from '../auth/permissions'
import SurfaceGate from '../components/common/SurfaceGate.vue'
import AppShell from '../components/shell/AppShell.vue'
import PageHeader from '../components/shell/PageHeader.vue'
import { CO_WRITER, type WritingStep } from '../copy/collaboration'

import { usePersonaList } from './personas/usePersonaList'

const router = useRouter()
const { items, status, failure, load } = usePersonaList()

const step = ref<WritingStep>('free')
const personaId = ref('')
const content = ref('')

const steps = (Object.keys(CO_WRITER.steps) as WritingStep[]).map((key) => ({
  key,
  ...CO_WRITER.steps[key],
}))

const placeholder = computed(() =>
  step.value === 'revise' ? CO_WRITER.contentPlaceholderRevise : CO_WRITER.contentPlaceholder,
)

const canStart = computed(() => content.value.trim() !== '')

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
  const query: Record<string, string> = { draft: firstMessage() }
  if (personaId.value !== '') query.persona = personaId.value
  void router.push({ name: 'chat', query })
}

onMounted(() => {
  if (canSurface('chat') === false) return
  void load()
})
</script>

<template>
  <AppShell>
    <SurfaceGate surface="chat">
      <div class="page">
        <div class="page__inner">
          <PageHeader :title="CO_WRITER.title" :description="CO_WRITER.description">
            <template #actions>
              <button type="button" class="ghost" @click="router.push({ name: 'partners' })">
                {{ CO_WRITER.backToPartners }}
              </button>
              <button type="button" class="ghost" @click="router.push({ name: 'personas' })">
                {{ CO_WRITER.manage }}
              </button>
            </template>
          </PageHeader>

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
            <h2 class="section__title">{{ CO_WRITER.personaTitle }}</h2>

            <div class="persona-row">
              <select
                v-model="personaId"
                class="persona-select"
                :aria-label="CO_WRITER.personaTitle"
                :disabled="status === 'loading' && items.length === 0"
              >
                <option value="">
                  {{
                    status === 'loading' && items.length === 0
                      ? CO_WRITER.personaLoading
                      : CO_WRITER.noPersona
                  }}
                </option>
                <option
                  v-for="persona in items"
                  :key="persona.persona_id"
                  :value="persona.persona_id"
                >
                  {{ persona.name }}
                </option>
              </select>

              <button
                v-if="failure"
                type="button"
                class="link-button"
                @click="load"
              >
                {{ CO_WRITER.personaLoadFailed }}
              </button>
            </div>

            <p v-if="status === 'ready' && items.length === 0" class="hint">
              {{ CO_WRITER.personaEmpty }}
              <button type="button" class="link-button" @click="router.push({ name: 'personas' })">
                {{ CO_WRITER.manage }}
              </button>
            </p>
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
            <p v-if="!canStart" class="hint">{{ CO_WRITER.emptyContent }}</p>
          </section>

          <div class="actions">
            <button type="button" class="primary" :disabled="!canStart" @click="start">
              {{ CO_WRITER.start }}
            </button>
          </div>
        </div>
      </div>
    </SurfaceGate>
  </AppShell>
</template>

<style scoped>
.page__inner {
  display: flex;
  flex-direction: column;
  gap: 20px;
}

.ghost {
  padding: 6px 12px;
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-sm);
  background: var(--surface);
  color: var(--text-muted);
  font-size: 12px;
  cursor: pointer;
}

.ghost:hover {
  border-color: var(--accent);
  color: var(--accent);
}

.section {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.section__title {
  margin: 0;
  color: var(--text);
  font-size: 12px;
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
  padding: 10px 12px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--surface);
  color: var(--text);
  text-align: left;
  cursor: pointer;
}

.step:hover {
  border-color: var(--border-strong);
}

.step--on {
  border-color: var(--accent);
  background: var(--accent-soft);
}

.step__label {
  font-size: 13px;
  font-weight: 600;
}

.step__description {
  color: var(--text-muted);
  font-size: 11px;
  line-height: 1.5;
}

.persona-row {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  align-items: center;
}

.persona-select {
  min-width: 220px;
  padding: 6px 9px;
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-sm);
  background: var(--surface);
  color: var(--text);
  font: inherit;
  font-size: 12px;
}

.persona-select:focus {
  border-color: var(--accent);
  outline: none;
}

.content {
  width: 100%;
  padding: 9px 11px;
  border: 1px solid var(--border-strong);
  border-radius: var(--radius);
  background: var(--surface);
  color: var(--text);
  font: inherit;
  font-size: 13px;
  line-height: 1.7;
  resize: vertical;
}

.content:focus {
  border-color: var(--accent);
  outline: none;
}

.hint {
  margin: 0;
  color: var(--text-faint);
  font-size: 11px;
  line-height: 1.6;
}

.link-button {
  padding: 0;
  border: 0;
  background: none;
  color: var(--accent);
  font: inherit;
  font-size: 11px;
  cursor: pointer;
}

.actions {
  display: flex;
  justify-content: flex-end;
}

.primary {
  padding: 7px 18px;
  border: 1px solid var(--accent);
  border-radius: var(--radius-sm);
  background: var(--accent);
  color: #fff;
  font-size: 13px;
  font-weight: 600;
  cursor: pointer;
}

.primary:disabled {
  background: #b9c6e6;
  border-color: #b9c6e6;
  cursor: default;
}
</style>
