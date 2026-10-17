<script setup lang="ts">
/**
 * 新建 / 编辑人格。
 *
 * 表单是本地的，校验也先在本地做一遍：名字与提示词都不能是空白，这一条
 * 后端也查，但让用户在看得到字段的地方当场知道，比提交后读一句横幅好。
 * 服务端的拒绝仍然照原样显示 —— 尤其是重名（409），本地没法可靠预判。
 *
 * 长度上限照 `backend/internal/persona/types.go`：名字 64、说明 500、提示词
 * 4000 个**字符**（不是 UTF-16 单元），所以计数用 `Array.from` 数码点。
 */

import { computed, nextTick, ref, watch } from 'vue'

import { isApiFailure } from '../../api/client'
import { describeFailure } from '../../api/errors'
import { createPersona, updatePersona, type Persona, type PersonaInput } from '../../api/personas'
import { PERSONAS } from '../../copy/personas'
import { notify } from '../../shared/toast'

const MAX_NAME = 64
const MAX_DESCRIPTION = 500
const MAX_CONTENT = 4000

const props = defineProps<{
  open: boolean
  /** null 表示新建。 */
  persona: Persona | null
}>()

const emit = defineEmits<{
  (event: 'close'): void
  (event: 'saved', persona: Persona): void
}>()

const dialog = ref<HTMLDialogElement | null>(null)
const name = ref('')
const description = ref('')
const content = ref('')
const error = ref<string | null>(null)
const busy = ref(false)

const title = computed(() => (props.persona ? PERSONAS.editTitle : PERSONAS.createTitle))

function runeCount(value: string): number {
  return Array.from(value).length
}

function reset(): void {
  name.value = props.persona?.name ?? ''
  description.value = props.persona?.description ?? ''
  content.value = props.persona?.content ?? ''
  error.value = null
  busy.value = false
}

watch(
  () => [props.open, props.persona] as const,
  ([open]) => {
    const element = dialog.value
    if (!element) return
    if (open) {
      reset()
      if (!element.open) element.showModal()
      void nextTick(() => {
        const first = element.querySelector<HTMLInputElement>('input')
        first?.focus()
      })
    } else if (element.open) {
      element.close()
    }
  },
)

function close(): void {
  if (busy.value) return
  emit('close')
}

function validate(): string | null {
  if (name.value.trim() === '' || runeCount(name.value.trim()) > MAX_NAME) {
    return PERSONAS.errorName
  }
  if (runeCount(description.value.trim()) > MAX_DESCRIPTION) {
    return PERSONAS.errorDescription
  }
  if (content.value.trim() === '' || runeCount(content.value.trim()) > MAX_CONTENT) {
    return PERSONAS.errorContent
  }
  return null
}

function messageFor(cause: unknown): string {
  if (!isApiFailure(cause)) return PERSONAS.errorSave
  switch (cause.failure.code) {
    case 'invalid_name':
      return PERSONAS.errorName
    case 'invalid_description':
      return PERSONAS.errorDescription
    case 'invalid_content':
      return PERSONAS.errorContent
    case 'name_taken':
      return PERSONAS.errorNameTaken
    case 'not_found':
      return PERSONAS.errorGone
    default:
      return describeFailure(cause.failure)
  }
}

async function submit(): Promise<void> {
  const invalid = validate()
  if (invalid !== null) {
    error.value = invalid
    return
  }
  const input: PersonaInput = {
    name: name.value.trim(),
    description: description.value.trim(),
    content: content.value.trim(),
  }
  busy.value = true
  error.value = null
  try {
    const saved = props.persona
      ? await updatePersona(props.persona.persona_id, input)
      : await createPersona(input)
    notify(PERSONAS.saved, 'success')
    emit('saved', saved)
  } catch (cause) {
    error.value = messageFor(cause)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <dialog ref="dialog" class="editor" @cancel.prevent="close" @close="emit('close')">
    <form class="editor__form" @submit.prevent="submit">
      <header class="editor__head">
        <h2 class="editor__title">{{ title }}</h2>
        <button type="button" class="editor__close" :aria-label="PERSONAS.close" @click="close">
          ✕
        </button>
      </header>

      <label class="field">
        <span class="field__label">{{ PERSONAS.nameLabel }}</span>
        <input
          v-model="name"
          class="field__input"
          type="text"
          :placeholder="PERSONAS.namePlaceholder"
          :disabled="busy"
        />
        <span class="field__hint">{{ PERSONAS.nameHint }}</span>
      </label>

      <label class="field">
        <span class="field__label">{{ PERSONAS.descriptionLabel }}</span>
        <input
          v-model="description"
          class="field__input"
          type="text"
          :placeholder="PERSONAS.descriptionPlaceholder"
          :disabled="busy"
        />
        <span class="field__hint">{{ PERSONAS.descriptionHint }}</span>
      </label>

      <label class="field">
        <span class="field__label">{{ PERSONAS.contentLabel }}</span>
        <textarea
          v-model="content"
          class="field__text"
          rows="10"
          :placeholder="PERSONAS.contentPlaceholder"
          :disabled="busy"
        ></textarea>
        <span class="field__hint">{{ PERSONAS.contentHint }}</span>
      </label>

      <p v-if="error" class="editor__error" role="alert">{{ error }}</p>

      <footer class="editor__foot">
        <button type="button" class="editor__cancel" :disabled="busy" @click="close">
          {{ PERSONAS.cancel }}
        </button>
        <button type="submit" class="editor__save" :disabled="busy">
          {{ busy ? PERSONAS.saving : PERSONAS.save }}
        </button>
      </footer>
    </form>
  </dialog>
</template>

<style scoped>
.editor {
  width: min(620px, calc(100vw - 40px));
  max-height: min(760px, calc(100vh - 60px));
  padding: 0;
  border: 1px solid var(--border-strong);
  border-radius: var(--radius);
  background: var(--surface);
  color: var(--text);
}

.editor::backdrop {
  background: rgb(20 26 38 / 35%);
}

.editor__form {
  display: flex;
  flex-direction: column;
  gap: 14px;
  padding: 16px 18px 18px;
}

.editor__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.editor__title {
  margin: 0;
  font-size: 15px;
  font-weight: 650;
}

.editor__close {
  padding: 3px 7px;
  border: 0;
  border-radius: 4px;
  background: transparent;
  color: var(--text-faint);
  cursor: pointer;
}

.editor__close:hover {
  background: var(--surface-sunken);
  color: var(--text);
}

.field {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.field__label {
  color: var(--text);
  font-size: 12px;
  font-weight: 600;
}

.field__input,
.field__text {
  width: 100%;
  padding: 7px 9px;
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-sm);
  background: var(--surface);
  color: var(--text);
  font: inherit;
  font-size: 13px;
}

.field__text {
  line-height: 1.6;
  resize: vertical;
}

.field__input:focus,
.field__text:focus {
  border-color: var(--accent);
  outline: none;
}

.field__hint {
  color: var(--text-faint);
  font-size: 11px;
  line-height: 1.5;
}

.editor__error {
  margin: 0;
  padding: 7px 10px;
  border: 1px solid var(--danger);
  border-radius: var(--radius-sm);
  background: var(--danger-soft);
  color: var(--text);
  font-size: 12px;
}

.editor__foot {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}

.editor__cancel {
  padding: 6px 14px;
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--text-muted);
  font-size: 12px;
  cursor: pointer;
}

.editor__save {
  padding: 6px 16px;
  border: 1px solid var(--accent);
  border-radius: var(--radius-sm);
  background: var(--accent);
  color: #fff;
  font-size: 12px;
  font-weight: 600;
  cursor: pointer;
}

.editor__cancel:disabled,
.editor__save:disabled {
  cursor: default;
  opacity: 0.6;
}
</style>
