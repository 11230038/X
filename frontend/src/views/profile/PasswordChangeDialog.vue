<script setup lang="ts">
/**
 * 改自己的密码。
 *
 * 与管理员重置别人的密码（`AdminResetPasswordDialog`）的区别：这里要旧密码，
 * 因为改的是自己账号的凭据，得先证明是本人；成功后后端吊销**包括当前 token
 * 在内**的全部凭据 —— 所以这里不发任何后续请求，"改完再登出"那一步交给页面
 * 处理（它 emits `done`）。
 *
 * 用原生 `<dialog>` + `showModal()`：焦点默认落进第一个输入框、`Esc` 关掉、
 * 背后页面不可点，这几件事浏览器已经做了，不重复实现。
 */

import { ref } from 'vue'

import { changePassword } from '../../api/auth'
import { describeFailure } from '../../api/errors'
import { ACTIONS } from '../../copy/states'
import { PROFILE } from '../../copy/profile'
import { SETTINGS } from '../../copy/settings'
import { toFailure } from '../../shared/resource'
import AppIcon from '../../components/common/AppIcon.vue'

const emit = defineEmits<{ (event: 'done'): void }>()

const dialog = ref<HTMLDialogElement | null>(null)

const oldPassword = ref('')
const newPassword = ref('')
const confirmPassword = ref('')
const submitting = ref(false)
const failure = ref('')

const MIN_BYTES = 8
const MAX_BYTES = 72

function passwordBytes(value: string): number {
  return new TextEncoder().encode(value).length
}

/** 打开前清空上一轮的输入与错误：关掉再打开不该看见上一次的残影。 */
function show(): void {
  oldPassword.value = ''
  newPassword.value = ''
  confirmPassword.value = ''
  failure.value = ''
  submitting.value = false
  if (!dialog.value?.open) dialog.value?.showModal()
}

function hide(): void {
  if (dialog.value?.open) dialog.value.close()
}

async function submit(): Promise<void> {
  if (submitting.value) return
  failure.value = ''
  if (oldPassword.value === '' || newPassword.value === '') {
    failure.value = PROFILE.passwordNeedBoth
    return
  }
  const bytes = passwordBytes(newPassword.value)
  if (bytes < MIN_BYTES || bytes > MAX_BYTES) {
    failure.value = PROFILE.passwordLength
    return
  }
  if (newPassword.value !== confirmPassword.value) {
    failure.value = PROFILE.passwordMismatch
    return
  }

  submitting.value = true
  try {
    await changePassword(oldPassword.value, newPassword.value)
    hide()
    emit('done')
  } catch (cause) {
    failure.value = describeFailure(toFailure(cause))
  } finally {
    submitting.value = false
  }
}

defineExpose({ show, hide })
</script>

<template>
  <dialog ref="dialog" class="pw-dialog" @cancel.prevent="hide">
    <div class="pw-dialog__head">
      <span class="pw-dialog__icon" aria-hidden="true"><AppIcon name="shield" /></span>
      <div class="pw-dialog__intro">
        <h2 class="pw-dialog__title">{{ SETTINGS.changePassword }}</h2>
        <p class="pw-dialog__note">{{ PROFILE.changePasswordNote }}</p>
      </div>
    </div>

    <form class="pw-form" @submit.prevent="submit">
      <label class="field">
        <span class="field__label">{{ SETTINGS.oldPassword }}</span>
        <input
          v-model="oldPassword"
          type="password"
          class="field__input"
          autocomplete="current-password"
          :disabled="submitting"
        />
      </label>
      <label class="field">
        <span class="field__label">{{ SETTINGS.newPassword }}</span>
        <input
          v-model="newPassword"
          type="password"
          class="field__input"
          autocomplete="new-password"
          :disabled="submitting"
        />
        <span class="field__rule">{{ PROFILE.passwordRule }}</span>
      </label>
      <label class="field">
        <span class="field__label">{{ PROFILE.confirmPassword }}</span>
        <input
          v-model="confirmPassword"
          type="password"
          class="field__input"
          autocomplete="new-password"
          :disabled="submitting"
        />
      </label>

      <p v-if="failure" class="error">{{ failure }}</p>

      <div class="pw-dialog__actions">
        <button type="button" class="ghost" :disabled="submitting" @click="hide">
          {{ ACTIONS.cancel }}
        </button>
        <button type="submit" class="primary" :disabled="submitting">
          {{ submitting ? PROFILE.passwordSubmitting : SETTINGS.changePassword }}
        </button>
      </div>
    </form>
  </dialog>
</template>

<style scoped>
/* 与账号页同一种玻璃：半透明白、大圆角、背后虚化。 */
.pw-dialog {
  width: min(440px, calc(100vw - 32px));
  padding: 28px 30px;
  border: 1px solid var(--glass-border);
  border-radius: 28px;
  background: rgb(255 255 255 / 92%);
  color: var(--text);
  box-shadow: 0 30px 60px rgb(0 0 0 / 15%), var(--inset-light);
  backdrop-filter: blur(30px);
  -webkit-backdrop-filter: blur(30px);
}

.pw-dialog::backdrop {
  background: rgb(20 26 38 / 25%);
  backdrop-filter: blur(6px);
  -webkit-backdrop-filter: blur(6px);
}

.pw-dialog__head {
  display: flex;
  gap: 14px;
  align-items: flex-start;
  margin-bottom: 22px;
}

.pw-dialog__icon {
  display: grid;
  flex: none;
  place-items: center;
  width: 40px;
  height: 40px;
  border-radius: 12px;
  background: linear-gradient(135deg, rgb(177 140 255 / 18%), rgb(102 210 245 / 18%));
  font-size: 20px;
  line-height: 1;
}

.pw-dialog__intro {
  min-width: 0;
}

.pw-dialog__title {
  margin: 0 0 4px;
  color: var(--text);
  font-size: 19px;
  font-weight: 800;
  letter-spacing: -0.02em;
}

.pw-dialog__note {
  margin: 0;
  color: var(--text-muted);
  font-size: 13px;
  line-height: 1.6;
}

.pw-form {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.field {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.field__label {
  color: var(--text-muted);
  font-size: 13px;
  font-weight: 600;
}

.field__input {
  padding: 11px 14px;
  border: 1px solid rgb(0 0 0 / 7%);
  border-radius: 12px;
  background: rgb(255 255 255 / 85%);
  color: var(--text);
  font-size: 15px;
  outline: none;
  transition: border-color 0.2s ease, box-shadow 0.2s ease, background 0.2s ease;
}

.field__input:hover {
  background: #fff;
}

.field__input:focus {
  border-color: var(--color-purple);
  background: #fff;
  box-shadow: 0 0 0 4px rgb(177 140 255 / 15%);
}

.field__rule {
  color: var(--text-faint);
  font-size: 12px;
}

.error {
  margin: 0;
  padding: 10px 14px;
  border-radius: 12px;
  background: var(--danger-soft);
  color: var(--danger);
  font-size: 13px;
  line-height: 1.5;
}

.pw-dialog__actions {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  margin-top: 6px;
}

/* 取消是描边按钮：让"修改密码"那个渐变按钮独占视线。 */
.ghost {
  padding: 10px 20px;
  border: 1px solid rgb(0 0 0 / 8%);
  border-radius: 12px;
  background: #fff;
  color: var(--text);
  font-size: 14px;
  font-weight: 600;
  cursor: pointer;
  transition: border-color 0.2s ease, background 0.2s ease;
}

.ghost:hover:enabled {
  border-color: rgb(0 0 0 / 20%);
}

.primary {
  padding: 10px 22px;
  border: 0;
  border-radius: 12px;
  background: var(--accent-gradient);
  box-shadow: 0 8px 18px rgb(177 140 255 / 28%);
  color: #fff;
  font-size: 14px;
  font-weight: 700;
  cursor: pointer;
  transition: transform 0.2s ease, box-shadow 0.2s ease, opacity 0.2s ease;
}

.primary:hover:enabled {
  box-shadow: 0 10px 22px rgb(177 140 255 / 38%);
  transform: translateY(-1px);
}

.ghost:disabled,
.primary:disabled {
  cursor: default;
  opacity: 0.55;
}
</style>
