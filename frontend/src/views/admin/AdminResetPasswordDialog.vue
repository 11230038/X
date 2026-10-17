<script setup lang="ts">
/**
 * 重置一个账号的密码。
 *
 * 不需要旧密码 —— 这是管理员替别人换凭据，不是账号改自己的密码。代价是这
 * 个动作必须写进审计，并且成功之后**那个人的现有登录全部失效**：后端在写新
 * hash 的同一条语句里记下撤销时刻，不需要等 token 自然过期。
 *
 * 两次输入是界面的确认，不是协议的一部分：请求里只有一个 `password`。
 */

import { computed, ref } from 'vue'

import { resetUserPassword, type AdminUser } from '../../api/admin'
import type { ApiFailure } from '../../api/client'
import { ADMIN } from '../../copy/admin'
import { notify } from '../../shared/toast'
import { toFailure } from '../../shared/resource'

const props = defineProps<{ user: AdminUser }>()
const emit = defineEmits<{ (event: 'done'): void }>()

const dialog = ref<HTMLDialogElement | null>(null)

const password = ref('')
const confirmation = ref('')
const submitting = ref(false)
const failure = ref<ApiFailure | null>(null)

const MIN_PASSWORD_BYTES = 8
const MAX_PASSWORD_BYTES = 72

function passwordBytes(value: string): number {
  return new TextEncoder().encode(value).length
}

const passwordOk = computed(() => {
  const size = passwordBytes(password.value)
  return size >= MIN_PASSWORD_BYTES && size <= MAX_PASSWORD_BYTES
})
const mismatch = computed(
  () => confirmation.value !== '' && confirmation.value !== password.value,
)
const ready = computed(
  () => passwordOk.value && password.value === confirmation.value && !submitting.value,
)

function show(): void {
  password.value = ''
  confirmation.value = ''
  failure.value = null
  submitting.value = false
  if (!dialog.value?.open) dialog.value?.showModal()
}

function hide(): void {
  if (dialog.value?.open) dialog.value.close()
}

async function submit(): Promise<void> {
  if (!ready.value) return
  submitting.value = true
  failure.value = null
  try {
    await resetUserPassword(props.user.user_id, password.value)
    hide()
    notify(ADMIN.passwordResetDone, 'success')
    emit('done')
  } catch (cause) {
    failure.value = toFailure(cause)
  } finally {
    submitting.value = false
  }
}

defineExpose({ show, hide })
</script>

<template>
  <dialog ref="dialog" class="account-dialog" @cancel.prevent="hide">
    <h2 class="account-dialog__title">
      {{ ADMIN.resetPassword }} · {{ props.user.username }}
    </h2>
    <p class="account-dialog__hint">{{ ADMIN.resetPasswordScope }}</p>

    <div class="account-form">
      <label class="field">
        <span class="field__label">{{ ADMIN.newPassword }}</span>
        <input
          v-model="password"
          type="password"
          class="field__input"
          autocomplete="new-password"
          :disabled="submitting"
        />
        <span class="field__note">{{ ADMIN.passwordHint }}</span>
      </label>

      <label class="field">
        <span class="field__label">{{ ADMIN.confirmPassword }}</span>
        <input
          v-model="confirmation"
          type="password"
          class="field__input"
          autocomplete="new-password"
          :disabled="submitting"
          @keydown.enter.prevent="submit"
        />
        <span v-if="mismatch" class="field__note field__note--bad">
          {{ ADMIN.passwordMismatch }}
        </span>
      </label>
    </div>

    <p v-if="failure" class="error">
      {{ failure.message }}
      <code v-if="failure.requestId" class="account-dialog__req">{{ failure.requestId }}</code>
    </p>

    <div class="account-dialog__actions">
      <button type="button" class="ghost" :disabled="submitting" @click="hide">取消</button>
      <button type="button" class="primary" :disabled="!ready" @click="submit">
        {{ submitting ? '提交中…' : ADMIN.resetPassword }}
      </button>
    </div>
  </dialog>
</template>

<style scoped src="./AdminUsers.css"></style>
<style scoped>
.account-dialog {
  width: min(440px, calc(100vw - 32px));
  padding: 20px;
  border: 1px solid var(--border-strong);
  border-radius: var(--radius);
  background: var(--surface);
  color: var(--text);
}

.account-dialog::backdrop {
  background: rgb(0 0 0 / 35%);
}

.account-dialog__title {
  margin: 0 0 6px;
  font-size: 15px;
  font-weight: 650;
}

.account-dialog__hint {
  margin: 0 0 12px;
  color: var(--text-muted);
  font-size: 12px;
  line-height: 1.6;
}

.account-form {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.field__note {
  color: var(--text-faint);
  font-size: 10px;
}

.field__note--bad {
  color: var(--danger);
}

.account-dialog__actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 16px;
}

.account-dialog__req {
  color: var(--text-faint);
  font-size: 9px;
}
</style>
